package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Pending reasons stored in pending_reason (comma separated).
const (
	ReasonCategoryMissing = "category_missing"
	ReasonLowConfidence   = "low_confidence"
	ReasonAmountUnchecked = "amount_not_in_message"
	ReasonDuplicate       = "possible_duplicate"
	ReasonDateUnclear     = "date_unclear"
)

// TxInput is a transaction to create. Workspace, author and source message
// come from the Actor and the caller's trusted context, never from here.
type TxInput struct {
	Type                string
	AmountCents         int64
	Currency            string
	Description         string
	Merchant            string
	CategoryID          *int64
	Date                string
	PayerMemberID       *int64
	Shared              *bool
	PaymentMethod       string
	ExternalRef         string
	Source              string
	SourceItem          int
	AttachmentID        *int64
	AIConfidence        *float64
	PendingReasons      []string
	PossibleDuplicateOf *int64
	Notes               string
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// normalizeInput validates and cleans an input in place and decides whether
// the transaction can be CONFIRMED. Ownership of every referenced row is
// checked against the workspace.
func (s *Service) normalizeInput(ctx context.Context, q querier, wsID int64, in *TxInput) (status string, err error) {
	if !contains(TransactionTypes, in.Type) {
		return "", invalid("Tipo de transação inválido.")
	}
	if in.AmountCents <= 0 || in.AmountCents > MaxAmountCents {
		return "", invalid("Valor inválido.")
	}
	if in.Currency == "" {
		in.Currency = "BRL"
	}
	in.Currency = strings.ToUpper(in.Currency)
	if !currencyRe.MatchString(in.Currency) {
		return "", invalid("Moeda inválida.")
	}
	d, err := time.ParseInLocation(DateLayout, in.Date, Today(s.now()).Location())
	if err != nil {
		return "", invalid("Data inválida.")
	}
	if d.After(Today(s.now())) {
		return "", invalid("A data não pode estar no futuro.")
	}
	if d.Year() < 2000 {
		return "", invalid("Data inválida.")
	}
	in.Description = truncate(SanitizeText(in.Description), 200)
	in.Merchant = truncate(SanitizeText(in.Merchant), 120)
	in.Notes = truncate(SanitizeText(in.Notes), 500)
	if in.PaymentMethod != "" && !contains(PaymentMethods, in.PaymentMethod) {
		in.PaymentMethod = "OTHER"
	}
	in.ExternalRef = strings.TrimSpace(in.ExternalRef)
	if in.ExternalRef != "" && CleanExternalRef(in.ExternalRef) == nil {
		in.ExternalRef = ""
	}
	if !contains([]string{SourceWhatsAppText, SourceWhatsAppAudio, SourceWhatsAppReceipt, SourceWeb}, in.Source) {
		return "", invalid("Origem inválida.")
	}
	if in.AIConfidence != nil {
		c := *in.AIConfidence
		if c < 0 {
			c = 0
		}
		if c > 1 {
			c = 1
		}
		in.AIConfidence = &c
	}

	if in.Type == TypeTransfer {
		in.CategoryID = nil
	}
	if in.CategoryID != nil {
		c, err := getCategory(ctx, q, wsID, *in.CategoryID)
		if errors.Is(err, ErrNotFound) {
			return "", invalid("Categoria não encontrada.")
		}
		if err != nil {
			return "", err
		}
		if c.ArchivedAt != nil {
			return "", invalid("Categoria arquivada.")
		}
		wantKind := KindExpense
		if in.Type == TypeIncome {
			wantKind = KindIncome
		}
		if c.Kind != wantKind {
			return "", invalid("A categoria não combina com o tipo da transação.")
		}
	}
	if err := checkOwned(ctx, q, "finance_members", wsID, in.PayerMemberID, "Pessoa não encontrada."); err != nil {
		return "", err
	}
	if err := checkOwned(ctx, q, "finance_attachments", wsID, in.AttachmentID, "Comprovante não encontrado."); err != nil {
		return "", err
	}
	if err := checkOwned(ctx, q, "finance_transactions", wsID, in.PossibleDuplicateOf, "Transação não encontrada."); err != nil {
		return "", err
	}

	if in.CategoryID == nil && in.Type != TypeTransfer && !contains(in.PendingReasons, ReasonCategoryMissing) {
		in.PendingReasons = append(in.PendingReasons, ReasonCategoryMissing)
	}
	if len(in.PendingReasons) > 0 {
		return StatusPending, nil
	}
	return StatusConfirmed, nil
}

// checkOwned verifies that an optional reference belongs to the workspace.
// table is always a constant from this package.
func checkOwned(ctx context.Context, q querier, table string, wsID int64, id *int64, msg string) error {
	if id == nil {
		return nil
	}
	var ok bool
	if err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" WHERE workspace_id = $1 AND id = $2)", wsID, *id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid(msg)
	}
	return nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func pendingReason(reasons []string) *string {
	if len(reasons) == 0 {
		return nil
	}
	return ptr(strings.Join(reasons, ","))
}

// ErrAlreadyProcessed means the source message already produced this item.
var ErrAlreadyProcessed = errors.New("mensagem já processada")

// CreateTransactions inserts a batch atomically, each with its CREATE audit
// event. A batch coming from a WhatsApp message is idempotent: replaying the
// same message fails with ErrAlreadyProcessed instead of duplicating.
func (s *Service) CreateTransactions(ctx context.Context, wsID int64, actor Actor, items []TxInput) ([]Transaction, error) {
	if len(items) == 0 {
		return nil, invalid("Nada para registrar.")
	}
	var out []Transaction
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		for i := range items {
			in := &items[i]
			status, err := s.normalizeInput(ctx, tx, wsID, in)
			if err != nil {
				return err
			}
			shared := true
			if in.Shared != nil {
				shared = *in.Shared
			}
			payer := in.PayerMemberID
			if payer == nil {
				payer = actor.MemberID
			}
			var raw []byte
			err = tx.QueryRow(ctx, `
				INSERT INTO finance_transactions (workspace_id, type, status, amount_cents, currency, description, merchant,
					category_id, transaction_date, payer_member_id, created_by_member_id, shared, payment_method, external_ref,
					source, source_inbox_id, source_item, attachment_id, ai_confidence, pending_reason, possible_duplicate_of, notes)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::date, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
				RETURNING to_jsonb(finance_transactions.*)`,
				wsID, in.Type, status, in.AmountCents, in.Currency, in.Description, nullIfEmpty(in.Merchant),
				in.CategoryID, in.Date, payer, actor.MemberID, shared, nullIfEmpty(in.PaymentMethod), nullIfEmpty(in.ExternalRef),
				in.Source, actor.InboxID, in.SourceItem, in.AttachmentID, in.AIConfidence, pendingReason(in.PendingReasons),
				in.PossibleDuplicateOf, nullIfEmpty(in.Notes)).Scan(&raw)
			if err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.ConstraintName == "finance_transactions_source_uq" {
					return ErrAlreadyProcessed
				}
				return err
			}
			t, err := decodeJSON[Transaction](raw)
			if err != nil {
				return err
			}
			if _, err := insertEvent(ctx, tx, wsID, t.ID, "CREATE", nil, raw, actor); err != nil {
				return err
			}
			out = append(out, *t)
		}
		return nil
	})
	return out, err
}

func insertEvent(ctx context.Context, q querier, wsID, txID int64, action string, before, after []byte, actor Actor) (int64, error) {
	channel := actor.Channel
	if channel == "" {
		channel = ChannelSystem
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO finance_transaction_events (workspace_id, transaction_id, action, before, after, actor_member_id, channel, source_inbox_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		wsID, txID, action, before, after, actor.MemberID, channel, actor.InboxID).Scan(&id)
	return id, err
}

// lockTransaction reads a live transaction for update.
func lockTransaction(ctx context.Context, q querier, wsID, id int64, includeDeleted bool) (*Transaction, []byte, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT to_jsonb(t) FROM finance_transactions t
		WHERE t.workspace_id = $1 AND t.id = $2 AND ($3 OR t.deleted_at IS NULL) FOR UPDATE`, wsID, id, includeDeleted).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	t, err := decodeJSON[Transaction](raw)
	return t, raw, err
}

// writeRow persists every mutable column of t and returns the new snapshot.
func writeRow(ctx context.Context, q querier, t *Transaction) ([]byte, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		UPDATE finance_transactions SET type = $3, status = $4, amount_cents = $5, currency = $6, description = $7,
			merchant = $8, category_id = $9, transaction_date = $10::date, payer_member_id = $11, shared = $12,
			payment_method = $13, notes = $14, pending_reason = $15, possible_duplicate_of = $16, deleted_at = $17,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING to_jsonb(finance_transactions.*)`,
		t.WorkspaceID, t.ID, t.Type, t.Status, t.AmountCents, t.Currency, t.Description, t.Merchant, t.CategoryID,
		t.TransactionDate, t.PayerMemberID, t.Shared, t.PaymentMethod, t.Notes, t.PendingReason, t.PossibleDuplicateOf,
		t.DeletedAt).Scan(&raw)
	return raw, err
}

// TxPatch changes some fields of a transaction. Nil means unchanged; an empty
// string clears merchant or notes. Confirm marks the user's explicit
// confirmation ("sim, registra", saving the edit form), which clears
// low-confidence and duplicate flags when the data is complete.
type TxPatch struct {
	Type          *string `json:"type"`
	AmountCents   *int64  `json:"amount_cents"`
	Description   *string `json:"description"`
	Merchant      *string `json:"merchant"`
	CategoryID    *int64  `json:"category_id"`
	Date          *string `json:"transaction_date"`
	PayerMemberID *int64  `json:"payer_member_id"`
	Shared        *bool   `json:"shared"`
	PaymentMethod *string `json:"payment_method"`
	Notes         *string `json:"notes"`
	Confirm       bool    `json:"confirm"`
}

// UpdateTransaction applies a patch with an UPDATE audit event (before and
// after snapshots in the same SQL transaction). It returns changed=false when
// the patch did not change anything.
func (s *Service) UpdateTransaction(ctx context.Context, wsID, id int64, actor Actor, p TxPatch) (*Transaction, bool, error) {
	var result *Transaction
	changed := false
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		t, before, err := lockTransaction(ctx, tx, wsID, id, false)
		if err != nil {
			return err
		}

		in := TxInput{
			Type: t.Type, AmountCents: t.AmountCents, Currency: t.Currency, Description: t.Description,
			CategoryID: t.CategoryID, Date: t.TransactionDate, PayerMemberID: t.PayerMemberID, Shared: &t.Shared,
			Source: t.Source, PossibleDuplicateOf: t.PossibleDuplicateOf,
		}
		if t.Merchant != nil {
			in.Merchant = *t.Merchant
		}
		if t.Notes != nil {
			in.Notes = *t.Notes
		}
		if t.PaymentMethod != nil {
			in.PaymentMethod = *t.PaymentMethod
		}
		var reasons []string
		if t.PendingReason != nil {
			for _, r := range strings.Split(*t.PendingReason, ",") {
				if r != "" && r != ReasonCategoryMissing {
					reasons = append(reasons, r)
				}
			}
		}

		if p.Type != nil {
			in.Type = *p.Type
		}
		if p.AmountCents != nil {
			in.AmountCents = *p.AmountCents
			reasons = without(reasons, ReasonAmountUnchecked)
		}
		if p.Description != nil {
			in.Description = *p.Description
		}
		if p.Merchant != nil {
			in.Merchant = *p.Merchant
		}
		if p.CategoryID != nil {
			in.CategoryID = p.CategoryID
		}
		if p.Date != nil {
			in.Date = *p.Date
			reasons = without(reasons, ReasonDateUnclear)
		}
		if p.PayerMemberID != nil {
			in.PayerMemberID = p.PayerMemberID
		}
		if p.Shared != nil {
			in.Shared = p.Shared
		}
		if p.PaymentMethod != nil {
			in.PaymentMethod = *p.PaymentMethod
		}
		if p.Notes != nil {
			in.Notes = *p.Notes
		}
		// Switching between expense and income leaves a category of the wrong
		// kind behind; drop it so the transaction asks for a new one.
		if in.CategoryID != nil && p.Type != nil && p.CategoryID == nil && *p.Type != t.Type {
			if c, err := getCategory(ctx, tx, wsID, *in.CategoryID); err == nil {
				want := KindExpense
				if in.Type == TypeIncome {
					want = KindIncome
				}
				if c.Kind != want {
					in.CategoryID = nil
				}
			}
		}
		if p.Confirm {
			reasons = nil
			in.PossibleDuplicateOf = nil
		}
		in.PendingReasons = reasons

		status, err := s.normalizeInput(ctx, tx, wsID, &in)
		if err != nil {
			return err
		}

		next := *t
		next.Type, next.Status, next.AmountCents, next.Currency = in.Type, status, in.AmountCents, in.Currency
		next.Description, next.Merchant, next.CategoryID = in.Description, nullIfEmpty(in.Merchant), in.CategoryID
		next.TransactionDate, next.PayerMemberID, next.Shared = in.Date, in.PayerMemberID, *in.Shared
		next.PaymentMethod, next.Notes = nullIfEmpty(in.PaymentMethod), nullIfEmpty(in.Notes)
		next.PendingReason, next.PossibleDuplicateOf = pendingReason(in.PendingReasons), in.PossibleDuplicateOf

		if sameContent(t, &next) {
			result = t
			return nil
		}
		after, err := writeRow(ctx, tx, &next)
		if err != nil {
			return err
		}
		if _, err := insertEvent(ctx, tx, wsID, id, "UPDATE", before, after, actor); err != nil {
			return err
		}
		changed = true
		result, err = decodeJSON[Transaction](after)
		return err
	})
	return result, changed, err
}

func without(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func sameContent(a, b *Transaction) bool {
	x, y := *a, *b
	x.UpdatedAt, y.UpdatedAt = time.Time{}, time.Time{}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

// DeleteTransaction soft-deletes (reports ignore deleted rows) with a DELETE
// audit event.
func (s *Service) DeleteTransaction(ctx context.Context, wsID, id int64, actor Actor) (*Transaction, error) {
	var result *Transaction
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		t, before, err := lockTransaction(ctx, tx, wsID, id, false)
		if err != nil {
			return err
		}
		now := time.Now()
		t.DeletedAt = &now
		after, err := writeRow(ctx, tx, t)
		if err != nil {
			return err
		}
		if _, err := insertEvent(ctx, tx, wsID, id, "DELETE", before, after, actor); err != nil {
			return err
		}
		result, err = decodeJSON[Transaction](after)
		return err
	})
	return result, err
}

// ErrNothingToUndo is returned when the actor has no recent action to revert.
var ErrNothingToUndo = errors.New("nada para desfazer")

// ErrUndoStale is returned when the transaction changed after the action the
// actor wants to undo; reverting it would silently drop the later change.
var ErrUndoStale = errors.New("a transação mudou depois dessa ação")

// UndoWindow limits "desfaz" to recent actions.
const UndoWindow = 24 * time.Hour

type UndoResult struct {
	Transaction *Transaction
	// Reverted is the action that was undone: CREATE, UPDATE or DELETE.
	Reverted string
}

// UndoLast reverts the most recent action of a WhatsApp member.
func (s *Service) UndoLast(ctx context.Context, wsID int64, actor Actor) (*UndoResult, error) {
	if actor.MemberID == nil {
		return nil, ErrNothingToUndo
	}
	var eventID int64
	err := s.DB.QueryRow(ctx, `
		SELECT id FROM finance_transaction_events
		WHERE workspace_id = $1 AND actor_member_id = $2 AND undone_by_event_id IS NULL
		  AND action IN ('CREATE', 'UPDATE', 'DELETE') AND created_at > $3
		ORDER BY id DESC LIMIT 1`, wsID, *actor.MemberID, s.now().Add(-UndoWindow)).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNothingToUndo
	}
	if err != nil {
		return nil, err
	}
	return s.revertEvent(ctx, wsID, eventID, actor)
}

// RestoreTransaction brings back a deleted transaction (panel "desfazer
// exclusão"), reverting its DELETE event.
func (s *Service) RestoreTransaction(ctx context.Context, wsID, id int64, actor Actor) (*Transaction, error) {
	var eventID int64
	err := s.DB.QueryRow(ctx, `
		SELECT id FROM finance_transaction_events
		WHERE workspace_id = $1 AND transaction_id = $2 AND action = 'DELETE' AND undone_by_event_id IS NULL
		ORDER BY id DESC LIMIT 1`, wsID, id).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r, err := s.revertEvent(ctx, wsID, eventID, actor)
	if err != nil {
		return nil, err
	}
	return r.Transaction, nil
}

func (s *Service) revertEvent(ctx context.Context, wsID, eventID int64, actor Actor) (*UndoResult, error) {
	var result UndoResult
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var txID int64
		var action string
		var beforeSnap []byte
		if err := tx.QueryRow(ctx, `SELECT transaction_id, action, before FROM finance_transaction_events
			WHERE workspace_id = $1 AND id = $2 AND undone_by_event_id IS NULL FOR UPDATE`, wsID, eventID).
			Scan(&txID, &action, &beforeSnap); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNothingToUndo
			}
			return err
		}
		var later bool
		// A later change that is still in effect (not itself undone) blocks the
		// revert; UNDO events only reverted later changes and do not count.
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM finance_transaction_events
			WHERE workspace_id = $1 AND transaction_id = $2 AND id > $3 AND action <> 'UNDO' AND undone_by_event_id IS NULL)`,
			wsID, txID, eventID).Scan(&later); err != nil {
			return err
		}
		if later {
			return ErrUndoStale
		}

		t, current, err := lockTransaction(ctx, tx, wsID, txID, true)
		if err != nil {
			return err
		}
		next := *t
		switch action {
		case "CREATE":
			now := time.Now()
			next.DeletedAt = &now
		case "DELETE":
			next.DeletedAt = nil
		case "UPDATE":
			prev, err := decodeJSON[Transaction](beforeSnap)
			if err != nil {
				return err
			}
			next = *prev
			next.ReplyMessageID = t.ReplyMessageID
		default:
			return fmt.Errorf("cannot undo %s", action)
		}
		after, err := writeRow(ctx, tx, &next)
		if err != nil {
			return err
		}
		undoID, err := insertEvent(ctx, tx, wsID, txID, "UNDO", current, after, actor)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE finance_transaction_events SET undone_by_event_id = $3 WHERE workspace_id = $1 AND id = $2", wsID, eventID, undoID); err != nil {
			return err
		}
		result.Reverted = action
		result.Transaction, err = decodeJSON[Transaction](after)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTransaction reads a transaction of the workspace.
func (s *Service) GetTransaction(ctx context.Context, wsID, id int64, includeDeleted bool) (*Transaction, error) {
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT to_jsonb(t) FROM finance_transactions t
		WHERE t.workspace_id = $1 AND t.id = $2 AND ($3 OR t.deleted_at IS NULL)`, wsID, id, includeDeleted).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeJSON[Transaction](raw)
}

// SetReplyMessage remembers the bot reply that confirmed these transactions.
func (s *Service) SetReplyMessage(ctx context.Context, wsID int64, ids []int64, replyID string) error {
	if len(ids) == 0 || replyID == "" {
		return nil
	}
	_, err := s.DB.Exec(ctx, "UPDATE finance_transactions SET reply_message_id = $3 WHERE workspace_id = $1 AND id = ANY($2)", wsID, ids, replyID)
	return err
}
