package finance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TransactionView is what the panel and the model see: names instead of ids
// and no WhatsApp identifiers.
type TransactionView struct {
	ID                  int64     `json:"id"`
	Type                string    `json:"type"`
	Status              string    `json:"status"`
	AmountCents         int64     `json:"amount_cents"`
	Currency            string    `json:"currency"`
	Description         string    `json:"description"`
	Merchant            *string   `json:"merchant"`
	TransactionDate     string    `json:"transaction_date"`
	CategoryID          *int64    `json:"category_id"`
	CategoryName        string    `json:"category_name"`
	CategoryIcon        string    `json:"category_icon"`
	ParentCategoryID    *int64    `json:"parent_category_id"`
	ParentCategoryName  string    `json:"parent_category_name"`
	PayerMemberID       *int64    `json:"payer_member_id"`
	PayerName           string    `json:"payer_name"`
	Shared              bool      `json:"shared"`
	PaymentMethod       *string   `json:"payment_method"`
	Source              string    `json:"source"`
	AttachmentID        *int64    `json:"attachment_id"`
	AIConfidence        *float64  `json:"ai_confidence"`
	PendingReasons      []string  `json:"pending_reasons"`
	PossibleDuplicateOf *int64    `json:"possible_duplicate_of"`
	Notes               *string   `json:"notes"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// TxFilter narrows a transaction listing. Zero values mean "any".
type TxFilter struct {
	Start, End   string
	CategoryID   *int64 // matches the category and its subcategories
	MemberID     *int64 // payer
	Type         string
	Status       string
	Search       string
	MinCents     *int64
	MaxCents     *int64
	OrderBy      string // "date" (default) or "amount"
	Limit        int
	Offset       int
	OnlyIDs      []int64
	ExcludeTypes []string
}

const viewSelect = `
	SELECT to_jsonb(t), COALESCE(c.name, ''), COALESCE(c.icon, ''), c.parent_id, COALESCE(p.name, ''),
		COALESCE(m.display_name, ''), count(*) OVER ()
	FROM finance_transactions t
	LEFT JOIN finance_categories c ON c.id = t.category_id
	LEFT JOIN finance_categories p ON p.id = c.parent_id
	LEFT JOIN finance_members m ON m.id = t.payer_member_id`

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListTransactions returns live transactions of the workspace and the total
// count matching the filter.
func (s *Service) ListTransactions(ctx context.Context, wsID int64, f TxFilter) ([]TransactionView, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	order := "t.transaction_date DESC, t.id DESC"
	if f.OrderBy == "amount" {
		order = "t.amount_cents DESC, t.transaction_date DESC, t.id DESC"
	}
	var search *string
	if q := strings.TrimSpace(f.Search); q != "" {
		search = ptr("%" + escapeLike(q) + "%")
	}
	var ids []int64
	if f.OnlyIDs != nil {
		ids = f.OnlyIDs
	}
	rows, err := s.DB.Query(ctx, viewSelect+`
		WHERE t.workspace_id = $1 AND t.deleted_at IS NULL
		  AND ($2::date IS NULL OR t.transaction_date >= $2::date)
		  AND ($3::date IS NULL OR t.transaction_date <= $3::date)
		  AND ($4::bigint IS NULL OR t.category_id = $4 OR c.parent_id = $4)
		  AND ($5::bigint IS NULL OR t.payer_member_id = $5)
		  AND ($6::text IS NULL OR t.type = $6)
		  AND ($7::text IS NULL OR t.status = $7)
		  AND ($8::text IS NULL OR t.description ILIKE $8 OR t.merchant ILIKE $8 OR c.name ILIKE $8 OR p.name ILIKE $8)
		  AND ($9::bigint IS NULL OR t.amount_cents >= $9)
		  AND ($10::bigint IS NULL OR t.amount_cents <= $10)
		  AND ($11::bigint[] IS NULL OR t.id = ANY($11))
		  AND NOT (t.type = ANY($12::text[]))
		ORDER BY `+order+` LIMIT $13 OFFSET $14`,
		wsID, nullIfEmpty(f.Start), nullIfEmpty(f.End), f.CategoryID, f.MemberID, nullIfEmpty(f.Type), nullIfEmpty(f.Status),
		search, f.MinCents, f.MaxCents, ids, append([]string{}, f.ExcludeTypes...), f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []TransactionView
	total := 0
	for rows.Next() {
		var raw []byte
		var v TransactionView
		if err := rows.Scan(&raw, &v.CategoryName, &v.CategoryIcon, &v.ParentCategoryID, &v.ParentCategoryName, &v.PayerName, &total); err != nil {
			return nil, 0, err
		}
		t, err := decodeJSON[Transaction](raw)
		if err != nil {
			return nil, 0, err
		}
		fillView(&v, t)
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func fillView(v *TransactionView, t *Transaction) {
	v.ID, v.Type, v.Status, v.AmountCents, v.Currency = t.ID, t.Type, t.Status, t.AmountCents, t.Currency
	v.Description, v.Merchant, v.TransactionDate, v.CategoryID = t.Description, t.Merchant, t.TransactionDate, t.CategoryID
	v.PayerMemberID, v.Shared, v.PaymentMethod, v.Source = t.PayerMemberID, t.Shared, t.PaymentMethod, t.Source
	v.AttachmentID, v.AIConfidence, v.PossibleDuplicateOf, v.Notes = t.AttachmentID, t.AIConfidence, t.PossibleDuplicateOf, t.Notes
	v.CreatedAt, v.UpdatedAt = t.CreatedAt, t.UpdatedAt
	v.PendingReasons = []string{}
	if t.PendingReason != nil {
		for _, r := range strings.Split(*t.PendingReason, ",") {
			if r != "" {
				v.PendingReasons = append(v.PendingReasons, r)
			}
		}
	}
}

// GetTransactionView reads one live transaction as a view.
func (s *Service) GetTransactionView(ctx context.Context, wsID, id int64) (*TransactionView, error) {
	views, _, err := s.ListTransactions(ctx, wsID, TxFilter{OnlyIDs: []int64{id}, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, ErrNotFound
	}
	return &views[0], nil
}

// FieldChange is one line of the audit history, already in user terms.
type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type EventView struct {
	ID        int64         `json:"id"`
	Action    string        `json:"action"`
	Channel   string        `json:"channel"`
	ActorName string        `json:"actor_name"`
	Undone    bool          `json:"undone"`
	CreatedAt time.Time     `json:"created_at"`
	Changes   []FieldChange `json:"changes"`
}

// Origin describes where a transaction came from, without technical ids.
type Origin struct {
	Channel   string     `json:"channel"`
	Kind      string     `json:"kind,omitempty"`
	Text      string     `json:"text,omitempty"`
	MessageAt *time.Time `json:"message_at,omitempty"`
	Sender    string     `json:"sender,omitempty"`
}

type TransactionDetail struct {
	TransactionView
	Events []EventView `json:"events"`
	Origin Origin      `json:"origin"`
}

var typeLabels = map[string]string{TypeExpense: "Despesa", TypeIncome: "Receita", TypeTransfer: "Transferência", TypeRefund: "Reembolso"}

func (s *Service) GetTransactionDetail(ctx context.Context, wsID, id int64) (*TransactionDetail, error) {
	view, err := s.GetTransactionView(ctx, wsID, id)
	if err != nil {
		return nil, err
	}
	d := &TransactionDetail{TransactionView: *view, Events: []EventView{}}

	cats, err := s.ListCategories(ctx, wsID, true)
	if err != nil {
		return nil, err
	}
	byCat := CategoryIndex(cats)
	members, err := s.ListMembers(ctx, wsID)
	if err != nil {
		return nil, err
	}
	memberName := map[int64]string{}
	for _, m := range members {
		memberName[m.ID] = m.DisplayName
	}

	rows, err := s.DB.Query(ctx, `SELECT id, action, channel, actor_member_id, undone_by_event_id IS NOT NULL, created_at,
			COALESCE(before, 'null'::jsonb), COALESCE(after, 'null'::jsonb)
		FROM finance_transaction_events WHERE workspace_id = $1 AND transaction_id = $2 ORDER BY id`, wsID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EventView
		var actor *int64
		var before, after []byte
		if err := rows.Scan(&e.ID, &e.Action, &e.Channel, &actor, &e.Undone, &e.CreatedAt, &before, &after); err != nil {
			return nil, err
		}
		if actor != nil {
			e.ActorName = memberName[*actor]
		}
		e.Changes = diffSnapshots(before, after, byCat, memberName)
		d.Events = append(d.Events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.Origin = Origin{Channel: ChannelWeb}
	if view.Source != SourceWeb {
		d.Origin.Channel = ChannelWhatsApp
		var kind string
		var text *string
		var at time.Time
		var sender *int64
		err := s.DB.QueryRow(ctx, `SELECT i.kind, i.text, i.message_at, i.member_id FROM finance_inbox i
			JOIN finance_transactions t ON t.source_inbox_id = i.id
			WHERE t.workspace_id = $1 AND t.id = $2`, wsID, id).Scan(&kind, &text, &at, &sender)
		if err == nil {
			d.Origin.Kind = kind
			if text != nil {
				d.Origin.Text = *text
			}
			d.Origin.MessageAt = &at
			if sender != nil {
				d.Origin.Sender = memberName[*sender]
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return d, nil
}

func diffSnapshots(before, after []byte, cats map[int64]*Category, members map[int64]string) []FieldChange {
	var b, a Transaction
	hasBefore := json.Unmarshal(before, &b) == nil && string(before) != "null"
	if json.Unmarshal(after, &a) != nil || string(after) == "null" {
		return nil
	}
	catName := func(id *int64) string {
		if id == nil {
			return "—"
		}
		if c, ok := cats[*id]; ok {
			return CategoryPath(c, cats)
		}
		return "—"
	}
	memberOf := func(id *int64) string {
		if id == nil {
			return "—"
		}
		if n, ok := members[*id]; ok {
			return n
		}
		return "—"
	}
	str := func(p *string) string {
		if p == nil || *p == "" {
			return "—"
		}
		return *p
	}
	statusLabel := map[string]string{StatusConfirmed: "Confirmada", StatusPending: "Pendente"}
	deleted := func(t *Transaction) string {
		if t.DeletedAt != nil {
			return "Excluída"
		}
		return "Ativa"
	}

	fields := []struct {
		name string
		get  func(t *Transaction) string
	}{
		{"Valor", func(t *Transaction) string { return FormatBRL(t.AmountCents) }},
		{"Tipo", func(t *Transaction) string { return typeLabels[t.Type] }},
		{"Categoria", func(t *Transaction) string { return catName(t.CategoryID) }},
		{"Data", func(t *Transaction) string { return formatCivil(t.TransactionDate) }},
		{"Descrição", func(t *Transaction) string { return str(&t.Description) }},
		{"Estabelecimento", func(t *Transaction) string { return str(t.Merchant) }},
		{"Pago por", func(t *Transaction) string { return memberOf(t.PayerMemberID) }},
		{"Situação", func(t *Transaction) string { return statusLabel[t.Status] }},
		{"Registro", deleted},
		{"Observações", func(t *Transaction) string { return str(t.Notes) }},
	}
	var out []FieldChange
	for _, f := range fields {
		av := f.get(&a)
		if !hasBefore {
			if f.name == "Registro" || av == "—" {
				continue
			}
			out = append(out, FieldChange{Field: f.name, After: av})
			continue
		}
		if bv := f.get(&b); bv != av {
			out = append(out, FieldChange{Field: f.name, Before: bv, After: av})
		}
	}
	return out
}

func formatCivil(date string) string {
	d, err := time.Parse(DateLayout, date)
	if err != nil {
		return date
	}
	return d.Format("02/01/2006")
}
