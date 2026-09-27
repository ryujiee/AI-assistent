package finance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"secretary/whatsapp"

	"github.com/jackc/pgx/v5"
)

// Gateway is what the finance module needs from WhatsApp. The live
// implementation is whatsapp.RealGateway; development and tests use
// whatsapp.FakeGateway.
type Gateway interface {
	Connected() bool
	JoinedGroups(ctx context.Context) ([]whatsapp.Group, error)
	GroupInfo(ctx context.Context, jid string) (*whatsapp.Group, error)
	SendText(ctx context.Context, chatJID, text string, quote *whatsapp.Quote) (string, error)
	Download(ctx context.Context, kind string, ref []byte) ([]byte, error)
	SelfJIDs() []string
}

// InboxItem is one accepted message being processed.
type InboxItem struct {
	ID              int64
	WorkspaceID     int64
	ChatJID         string
	SenderJID       string
	WAMessageID     string
	MemberID        *int64
	Kind            string
	Text            string
	QuotedMessageID string
	MediaRef        []byte
	MediaMime       string
	MediaSize       int64
	AttachmentID    *int64
	MessageAt       time.Time
	Attempts        int
}

// HandlerResult is what processing a message produced.
type HandlerResult struct {
	Reply          string
	TransactionIDs []int64
	// PendingActionID is the question this reply asks; the reply id is
	// stored on it so a quoted answer finds it.
	PendingActionID *int64
}

// Handler interprets one message (the financial agent).
type Handler func(ctx context.Context, item *InboxItem) (*HandlerResult, error)

// PermanentError ends processing without retry and answers the user.
type PermanentError struct {
	Code  string
	Reply string
}

func (e *PermanentError) Error() string { return e.Code }

const (
	// MaxAttempts bounds retries of a failing message.
	MaxAttempts = 3
	// StaleAfter is how long a PROCESSING row may stay untouched before it is
	// considered abandoned by a crashed process.
	StaleAfter      = 3 * time.Minute
	recoverInterval = 30 * time.Second
	maxTextLength   = 4000
)

var retryBackoff = []time.Duration{5 * time.Second, 30 * time.Second}

// Ingestor turns messages of linked groups into inbox rows and processes them
// in order, one worker per workspace: "gastei 50" is always handled before
// "na verdade foi 60", and a busy workspace never blocks another.
type Ingestor struct {
	Svc            *Service
	GW             Gateway
	Handle         Handler
	ProcessTimeout time.Duration

	mu      sync.RWMutex
	groups  map[string]LinkedGroup
	wmu     sync.Mutex
	workers map[int64]chan struct{}
	started bool
}

func NewIngestor(svc *Service, gw Gateway, handle Handler) *Ingestor {
	return &Ingestor{Svc: svc, GW: gw, Handle: handle, ProcessTimeout: 2 * time.Minute,
		groups: map[string]LinkedGroup{}, workers: map[int64]chan struct{}{}}
}

// Start loads the linked groups, recovers abandoned rows and resumes pending
// work. Safe to call once the finance tables exist.
func (ing *Ingestor) Start(ctx context.Context) error {
	if err := ing.RefreshGroups(ctx); err != nil {
		return err
	}
	ing.wmu.Lock()
	ing.started = true
	ing.wmu.Unlock()
	if _, err := ing.Recover(ctx); err != nil {
		slog.Error("finance.recover_failed", "error", err)
	}
	ing.wakePending(ctx)
	go func() {
		t := time.NewTicker(recoverInterval)
		defer t.Stop()
		for range t.C {
			if _, err := ing.Recover(context.Background()); err != nil {
				slog.Error("finance.recover_failed", "error", err)
			}
		}
	}()
	return nil
}

// Started reports whether the ingestor is running (finance tables exist).
func (ing *Ingestor) Started() bool {
	ing.wmu.Lock()
	defer ing.wmu.Unlock()
	return ing.started
}

// RefreshGroups reloads the group -> workspace map used by the router.
func (ing *Ingestor) RefreshGroups(ctx context.Context) error {
	groups, err := ing.Svc.LinkedGroups(ctx)
	if err != nil {
		return err
	}
	ing.mu.Lock()
	ing.groups = groups
	ing.mu.Unlock()
	return nil
}

// IsLinked is the router's finance group check (an in-memory lookup).
func (ing *Ingestor) IsLinked(chatJID string) bool {
	ing.mu.RLock()
	defer ing.mu.RUnlock()
	_, ok := ing.groups[chatJID]
	return ok
}

func (ing *Ingestor) group(chatJID string) (LinkedGroup, bool) {
	ing.mu.RLock()
	defer ing.mu.RUnlock()
	g, ok := ing.groups[chatJID]
	return g, ok
}

// Accept stores a finance group message. It runs synchronously inside the
// WhatsApp event handler, so it only does quick database work: no download,
// no AI. The unique key turns redelivered events into no-ops.
func (ing *Ingestor) Accept(m whatsapp.Message) {
	lg, ok := ing.group(m.ChatJID)
	if !ok {
		return
	}
	logger := slog.With("action", "finance.inbox.accept", "workspace", lg.WorkspaceID, "kind", m.Kind)
	if m.Timestamp.Before(lg.LinkedAt) {
		logger.Info("finance.inbox.accept", "result", "before_link")
		return
	}
	if m.IsEdit {
		// ponytail: edits are ignored; the user corrects with a new message.
		logger.Info("finance.inbox.accept", "result", "edit_ignored")
		return
	}
	for _, self := range ing.GW.SelfJIDs() {
		if self == m.SenderJID || self == m.SenderLID || self == m.SenderPN {
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	member, err := ing.Svc.UpsertMember(ctx, lg.WorkspaceID, MemberInput{
		JID: m.SenderJID, LID: m.SenderLID, PhoneNumber: m.SenderPN, DisplayName: m.PushName,
	})
	if err != nil {
		logger.Error("finance.inbox.accept", "result", "member_error", "error", err)
		return
	}
	id, inserted, err := ing.Svc.insertInbox(ctx, lg.WorkspaceID, member.ID, m)
	if err != nil {
		logger.Error("finance.inbox.accept", "result", "insert_error", "error", err)
		return
	}
	if !inserted {
		logger.Info("finance.inbox.accept", "result", "duplicate")
		return
	}
	logger.Info("finance.inbox.accept", "result", "queued", "inbox", id, "member", member.ID)
	ing.wake(lg.WorkspaceID)
}

func (s *Service) insertInbox(ctx context.Context, wsID, memberID int64, m whatsapp.Message) (int64, bool, error) {
	var id int64
	text := truncate(SanitizeText(m.Text), maxTextLength)
	err := s.DB.QueryRow(ctx, `
		INSERT INTO finance_inbox (workspace_id, chat_jid, sender_jid, wa_message_id, member_id, kind, text,
			quoted_message_id, media_ref, media_mime, media_size, message_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, NULLIF($10, ''), $11, $12)
		ON CONFLICT (chat_jid, sender_jid, wa_message_id) DO NOTHING
		RETURNING id`,
		wsID, m.ChatJID, m.SenderJID, m.MessageID, memberID, m.Kind, text, m.QuotedMessageID,
		m.MediaRef, m.MediaMime, m.MediaSize, m.Timestamp).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func (ing *Ingestor) wake(wsID int64) {
	ing.wmu.Lock()
	defer ing.wmu.Unlock()
	if !ing.started {
		return
	}
	ch, ok := ing.workers[wsID]
	if !ok {
		ch = make(chan struct{}, 1)
		ing.workers[wsID] = ch
		go ing.runWorker(wsID, ch)
	}
	select {
	case ch <- struct{}{}:
	default: // a wake-up is already pending
	}
}

func (ing *Ingestor) wakePending(ctx context.Context) {
	rows, err := ing.Svc.DB.Query(ctx, "SELECT DISTINCT workspace_id FROM finance_inbox WHERE status = 'PENDING'")
	if err != nil {
		slog.Error("finance.wake_pending_failed", "error", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		ing.wake(id)
	}
}

func (ing *Ingestor) runWorker(wsID int64, wake <-chan struct{}) {
	for {
		wait := ing.Drain(context.Background(), wsID)
		if wait <= 0 {
			<-wake
			continue
		}
		select {
		case <-wake:
		case <-time.After(wait):
		}
	}
}

// Drain processes the workspace queue in order until it is empty (returns 0)
// or its head has to wait (returns how long).
func (ing *Ingestor) Drain(ctx context.Context, wsID int64) time.Duration {
	for {
		var id int64
		var status string
		var nextAt *time.Time
		err := ing.Svc.DB.QueryRow(ctx, `SELECT id, status, next_attempt_at FROM finance_inbox
			WHERE workspace_id = $1 AND status IN ('PENDING', 'PROCESSING') ORDER BY id LIMIT 1`, wsID).Scan(&id, &status, &nextAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0
		}
		if err != nil {
			slog.Error("finance.inbox.head_failed", "workspace", wsID, "error", err)
			return recoverInterval
		}
		if status == "PROCESSING" {
			// Left behind by a crashed process; Recover releases it.
			return recoverInterval
		}
		if nextAt != nil {
			if wait := time.Until(*nextAt); wait > 0 {
				return wait
			}
		}
		item, err := ing.Svc.claimInbox(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			slog.Error("finance.inbox.claim_failed", "workspace", wsID, "error", err)
			return recoverInterval
		}
		ing.process(item)
	}
}

func (s *Service) claimInbox(ctx context.Context, id int64) (*InboxItem, error) {
	var it InboxItem
	var text, quoted, mime *string
	err := s.DB.QueryRow(ctx, `
		UPDATE finance_inbox SET status = 'PROCESSING', attempts = attempts + 1, processing_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'PENDING'
		RETURNING id, workspace_id, chat_jid, sender_jid, wa_message_id, member_id, kind, text, quoted_message_id,
			media_ref, media_mime, COALESCE(media_size, 0), attachment_id, message_at, attempts`, id).
		Scan(&it.ID, &it.WorkspaceID, &it.ChatJID, &it.SenderJID, &it.WAMessageID, &it.MemberID, &it.Kind, &text, &quoted,
			&it.MediaRef, &mime, &it.MediaSize, &it.AttachmentID, &it.MessageAt, &it.Attempts)
	if err != nil {
		return nil, err
	}
	if text != nil {
		it.Text = *text
	}
	if quoted != nil {
		it.QuotedMessageID = *quoted
	}
	if mime != nil {
		it.MediaMime = *mime
	}
	return &it, nil
}

// alreadyApplied reports whether a message already changed the ledger: a
// replay after a crash must not apply it twice.
func (s *Service) alreadyApplied(ctx context.Context, inboxID int64) (bool, error) {
	var done bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM finance_transactions WHERE source_inbox_id = $1)
		OR EXISTS (SELECT 1 FROM finance_transaction_events WHERE source_inbox_id = $1)`, inboxID).Scan(&done)
	return done, err
}

func (ing *Ingestor) process(item *InboxItem) {
	start := time.Now()
	logger := slog.With("action", "finance.inbox.process", "workspace", item.WorkspaceID, "inbox", item.ID,
		"kind", item.Kind, "attempt", item.Attempts)
	ctx, cancel := context.WithTimeout(context.Background(), ing.ProcessTimeout)
	defer cancel()

	var res *HandlerResult
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		done, err := ing.Svc.alreadyApplied(ctx, item.ID)
		if err != nil {
			return err
		}
		if done {
			res = &HandlerResult{}
			logger.Info("finance.inbox.process", "result", "already_applied")
			return nil
		}
		if ing.Handle == nil {
			res = &HandlerResult{}
			return nil
		}
		res, err = ing.Handle(ctx, item)
		return err
	}()
	duration := time.Since(start).Milliseconds()

	var perm *PermanentError
	switch {
	case err == nil:
		ing.reply(ctx, item, res)
		ing.Svc.finishInbox(context.Background(), item.ID, "DONE", "", nil)
		logger.Info("finance.inbox.process", "result", "done", "durationMs", duration, "transactions", len(res.TransactionIDs))
	case errors.As(err, &perm):
		ing.reply(ctx, item, &HandlerResult{Reply: perm.Reply})
		ing.Svc.finishInbox(context.Background(), item.ID, "FAILED", perm.Code, nil)
		logger.Warn("finance.inbox.process", "result", "failed", "code", perm.Code, "durationMs", duration)
	case item.Attempts >= MaxAttempts:
		ing.reply(ctx, item, &HandlerResult{Reply: "⚠️ Não consegui processar essa mensagem agora. Pode enviar de novo em instantes?"})
		ing.Svc.finishInbox(context.Background(), item.ID, "FAILED", errorCode(err), nil)
		logger.Error("finance.inbox.process", "result", "gave_up", "code", errorCode(err), "durationMs", duration, "error", err)
	default:
		next := time.Now().Add(retryBackoff[min(item.Attempts-1, len(retryBackoff)-1)])
		ing.Svc.finishInbox(context.Background(), item.ID, "PENDING", errorCode(err), &next)
		logger.Warn("finance.inbox.process", "result", "retry", "code", errorCode(err), "durationMs", duration, "error", err)
	}
}

// errorCode keeps logs and the inbox free of message content.
func errorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case strings.HasPrefix(err.Error(), "panic"):
		return "panic"
	case strings.Contains(err.Error(), "openai"):
		return "ai_error"
	}
	return "error"
}

func (ing *Ingestor) reply(ctx context.Context, item *InboxItem, res *HandlerResult) {
	if res == nil || strings.TrimSpace(res.Reply) == "" {
		return
	}
	quote := &whatsapp.Quote{MessageID: item.WAMessageID, SenderJID: item.SenderJID, Text: truncate(item.Text, 80)}
	replyID, err := ing.GW.SendText(ctx, item.ChatJID, res.Reply, quote)
	if err != nil {
		slog.Error("finance.reply_failed", "workspace", item.WorkspaceID, "inbox", item.ID, "error", err)
		return
	}
	if _, err := ing.Svc.DB.Exec(ctx, "UPDATE finance_inbox SET reply_message_id = $2 WHERE id = $1", item.ID, replyID); err != nil {
		slog.Error("finance.reply_link_failed", "inbox", item.ID, "error", err)
	}
	if err := ing.Svc.SetReplyMessage(ctx, item.WorkspaceID, res.TransactionIDs, replyID); err != nil {
		slog.Error("finance.reply_link_failed", "inbox", item.ID, "error", err)
	}
	if res.PendingActionID != nil {
		if err := ing.Svc.SetPendingQuestion(ctx, *res.PendingActionID, replyID); err != nil {
			slog.Error("finance.reply_link_failed", "inbox", item.ID, "error", err)
		}
	}
}

// finishInbox closes a processing attempt. Media references are dropped once
// the message is DONE or FAILED.
func (s *Service) finishInbox(ctx context.Context, id int64, status, code string, nextAt *time.Time) {
	_, err := s.DB.Exec(ctx, `UPDATE finance_inbox SET status = $2, last_error_code = NULLIF($3, ''),
			next_attempt_at = $4, processing_at = NULL, updated_at = now(),
			media_ref = CASE WHEN $2 IN ('DONE', 'FAILED') THEN NULL ELSE media_ref END
		WHERE id = $1`, id, status, code, nextAt)
	if err != nil {
		slog.Error("finance.inbox.finish_failed", "inbox", id, "error", err)
	}
}

// Recover releases rows abandoned in PROCESSING by a crashed process: rows
// whose effects are already in the ledger become DONE (never re-applied),
// the rest go back to PENDING until MaxAttempts is reached.
func (ing *Ingestor) Recover(ctx context.Context) (int, error) {
	stale := time.Now().Add(-StaleAfter)
	tag, err := ing.Svc.DB.Exec(ctx, `
		UPDATE finance_inbox i SET status = 'DONE', processing_at = NULL, media_ref = NULL, updated_at = now()
		WHERE i.status = 'PROCESSING' AND i.processing_at < $1
		  AND (EXISTS (SELECT 1 FROM finance_transactions t WHERE t.source_inbox_id = i.id)
		    OR EXISTS (SELECT 1 FROM finance_transaction_events e WHERE e.source_inbox_id = i.id))`, stale)
	if err != nil {
		return 0, err
	}
	rows, err := ing.Svc.DB.Query(ctx, `
		UPDATE finance_inbox SET
			status = CASE WHEN attempts >= $2 THEN 'FAILED' ELSE 'PENDING' END,
			last_error_code = CASE WHEN attempts >= $2 THEN 'stale_processing' ELSE last_error_code END,
			media_ref = CASE WHEN attempts >= $2 THEN NULL ELSE media_ref END,
			processing_at = NULL, next_attempt_at = NULL, updated_at = now()
		WHERE status = 'PROCESSING' AND processing_at < $1
		RETURNING workspace_id`, stale, MaxAttempts)
	if err != nil {
		return 0, err
	}
	var wake []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			wake = append(wake, id)
		}
	}
	rows.Close()
	n := int(tag.RowsAffected()) + len(wake)
	if n > 0 {
		slog.Warn("finance.inbox.recovered", "rows", n)
	}
	for _, id := range wake {
		ing.wake(id)
	}
	return n, rows.Err()
}
