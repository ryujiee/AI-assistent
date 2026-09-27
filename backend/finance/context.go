package finance

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Compact context for the agent: a few recent transactions, the transaction a
// quoted reply points at, and the last messages of the group. Never the whole
// ledger.

const (
	contextTransactions = 5
	historyMessages     = 8
	// ownRecentWindow is how recent the sender's last transaction must be to
	// count as "this one" for a correction without a quote.
	ownRecentWindow = 30 * time.Minute
)

// RecentForContext lists pending transactions first, then the most recent
// ones, favoring the sender's own.
func (s *Service) RecentForContext(ctx context.Context, wsID int64, memberID *int64) ([]TransactionView, error) {
	since := Today(s.now()).AddDate(0, 0, -14).Format(DateLayout)
	rows, err := s.DB.Query(ctx, `
		SELECT t.id FROM finance_transactions t
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND (t.status = 'PENDING' OR t.transaction_date >= $2::date OR t.created_at > now() - interval '2 days')
		ORDER BY (t.status = 'PENDING') DESC, (t.created_by_member_id IS NOT DISTINCT FROM $3) DESC, t.created_at DESC
		LIMIT $4`, wsID, since, memberID, contextTransactions)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, rows.Err()
	}
	views, _, err := s.ListTransactions(ctx, wsID, TxFilter{OnlyIDs: ids, Limit: len(ids)})
	return views, err
}

// LastOwnTransaction is the sender's most recent live transaction created in
// the last ownRecentWindow, the fallback target of an unquoted correction.
func (s *Service) LastOwnTransaction(ctx context.Context, wsID int64, memberID *int64) (int64, bool) {
	if memberID == nil {
		return 0, false
	}
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM finance_transactions
		WHERE workspace_id = $1 AND created_by_member_id = $2 AND deleted_at IS NULL AND created_at > $3
		ORDER BY created_at DESC LIMIT 1`, wsID, *memberID, time.Now().Add(-ownRecentWindow)).Scan(&id)
	return id, err == nil
}

// QuotedTransactions resolves a quoted message (a bot confirmation or the
// user's own earlier message) to the transactions that message touched. It
// is the strongest reference for "na verdade foi 60".
func (s *Service) QuotedTransactions(ctx context.Context, wsID int64, quotedID string) ([]int64, error) {
	if quotedID == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(ctx, `
		WITH src AS (
			SELECT id FROM finance_inbox WHERE workspace_id = $1 AND (reply_message_id = $2 OR wa_message_id = $2)
		)
		SELECT DISTINCT t.id FROM finance_transactions t
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND (
			t.source_inbox_id IN (SELECT id FROM src)
			OR t.reply_message_id = $2
			OR t.id IN (SELECT e.transaction_id FROM finance_transaction_events e WHERE e.workspace_id = $1 AND e.source_inbox_id IN (SELECT id FROM src))
		)
		ORDER BY t.id`, wsID, quotedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ChatLine is one message of the group conversation kept for context.
type ChatLine struct {
	Role    string
	Content string
}

// RecentChat returns the last messages of the group, oldest first. It uses
// the chat_history table shared with the Secretária, keyed by the group JID.
func (s *Service) RecentChat(ctx context.Context, chatJID string) ([]ChatLine, error) {
	rows, err := s.DB.Query(ctx, `SELECT role, content FROM chat_history WHERE jid = $1 ORDER BY id DESC LIMIT $2`, chatJID, historyMessages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatLine
	for rows.Next() {
		var l ChatLine
		if err := rows.Scan(&l.Role, &l.Content); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (s *Service) SaveChat(ctx context.Context, chatJID, role, content string) error {
	_, err := s.DB.Exec(ctx, "INSERT INTO chat_history (jid, role, content) VALUES ($1, $2, $3)", chatJID, role, truncate(content, 2000))
	return err
}

// ResolvePayer maps what the user said about who paid ("eu", "minha esposa",
// "Ana") to a member. In a two-person workspace "ela/ele/minha esposa/meu
// marido" means the other person.
func (s *Service) ResolvePayer(ctx context.Context, wsID int64, sender *int64, said string) (*int64, error) {
	n := normalize(said)
	if n == "" || n == "eu" || n == "mim" || n == "sender" {
		return sender, nil
	}
	members, err := s.ListMembers(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if normalize(m.DisplayName) == n || strings.HasPrefix(normalize(m.DisplayName), n+" ") {
			return &m.ID, nil
		}
	}
	other := map[string]bool{"ela": true, "ele": true, "esposa": true, "minha esposa": true, "marido": true, "meu marido": true,
		"minha mulher": true, "meu esposo": true, "esposo": true, "namorada": true, "minha namorada": true, "namorado": true,
		"meu namorado": true, "a outra pessoa": true, "outro": true, "outra": true, "parceiro": true, "parceira": true}
	if other[n] && sender != nil && len(members) == 2 {
		for _, m := range members {
			if m.ID != *sender {
				return &m.ID, nil
			}
		}
	}
	return nil, invalid(fmt.Sprintf("Não sei quem é %q. Membros: %s.", said, memberNames(members)))
}

func memberNames(ms []Member) string {
	var names []string
	for _, m := range ms {
		names = append(names, m.DisplayName)
	}
	return strings.Join(names, ", ")
}
