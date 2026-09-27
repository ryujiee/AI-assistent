package finance

import (
	"context"
	"errors"
	"time"

	"secretary/timeutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Transaction types. TRANSFER moves money between own accounts (including
// paying the credit card bill) and never counts as spending; REFUND reduces
// the spending of its category.
const (
	TypeExpense  = "EXPENSE"
	TypeIncome   = "INCOME"
	TypeTransfer = "TRANSFER"
	TypeRefund   = "REFUND"

	StatusConfirmed = "CONFIRMED"
	StatusPending   = "PENDING"

	SourceWhatsAppText    = "WHATSAPP_TEXT"
	SourceWhatsAppAudio   = "WHATSAPP_AUDIO"
	SourceWhatsAppReceipt = "WHATSAPP_RECEIPT"
	SourceWeb             = "WEB"

	ChannelWhatsApp = "WHATSAPP"
	ChannelWeb      = "WEB"
	ChannelSystem   = "SYSTEM"

	KindExpense = "EXPENSE"
	KindIncome  = "INCOME"

	Essential     = "ESSENTIAL"
	Important     = "IMPORTANT"
	Discretionary = "DISCRETIONARY"
)

var (
	TransactionTypes = []string{TypeExpense, TypeIncome, TypeTransfer, TypeRefund}
	PaymentMethods   = []string{"PIX", "CREDIT_CARD", "DEBIT_CARD", "CASH", "BOLETO", "TRANSFER", "OTHER"}
	Essentialities   = []string{Essential, Important, Discretionary}
)

var (
	ErrNotFound   = errors.New("não encontrado")
	ErrValidation = errors.New("dados inválidos")
	ErrConflict   = errors.New("conflito")
)

// ValidationError carries a message safe to show to the user or to the model.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }
func (e *ValidationError) Unwrap() error { return ErrValidation }

func invalid(msg string) error { return &ValidationError{Msg: msg} }

type Workspace struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	GroupJID            *string    `json:"group_jid"`
	GroupName           *string    `json:"group_name"`
	GroupLinkedAt       *time.Time `json:"group_linked_at"`
	ConfidenceThreshold float64    `json:"confidence_threshold"`
	MonthlySummary      string     `json:"monthly_summary"`
	WeeklySummary       bool       `json:"weekly_summary"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type Member struct {
	ID          int64     `json:"id"`
	WorkspaceID int64     `json:"workspace_id"`
	JID         string    `json:"jid"`
	LID         *string   `json:"lid"`
	PhoneNumber *string   `json:"phone_number"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Category struct {
	ID                 int64      `json:"id"`
	WorkspaceID        int64      `json:"workspace_id"`
	ParentID           *int64     `json:"parent_id"`
	Name               string     `json:"name"`
	Icon               string     `json:"icon"`
	Kind               string     `json:"kind"`
	Essentiality       string     `json:"essentiality"`
	MonthlyBudgetCents *int64     `json:"monthly_budget_cents"`
	SortOrder          int        `json:"sort_order"`
	ArchivedAt         *time.Time `json:"archived_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// Transaction mirrors a finance_transactions row. JSON tags are the column
// names so rows decode straight from to_jsonb(), which is also the format of
// the audit snapshots.
type Transaction struct {
	ID                  int64      `json:"id"`
	WorkspaceID         int64      `json:"workspace_id"`
	Type                string     `json:"type"`
	Status              string     `json:"status"`
	AmountCents         int64      `json:"amount_cents"`
	Currency            string     `json:"currency"`
	Description         string     `json:"description"`
	Merchant            *string    `json:"merchant"`
	CategoryID          *int64     `json:"category_id"`
	TransactionDate     string     `json:"transaction_date"`
	PayerMemberID       *int64     `json:"payer_member_id"`
	CreatedByMemberID   *int64     `json:"created_by_member_id"`
	Shared              bool       `json:"shared"`
	PaymentMethod       *string    `json:"payment_method"`
	ExternalRef         *string    `json:"external_ref"`
	Source              string     `json:"source"`
	SourceInboxID       *int64     `json:"source_inbox_id"`
	SourceItem          int        `json:"source_item"`
	AttachmentID        *int64     `json:"attachment_id"`
	ReplyMessageID      *string    `json:"reply_message_id"`
	AIConfidence        *float64   `json:"ai_confidence"`
	PendingReason       *string    `json:"pending_reason"`
	PossibleDuplicateOf *int64     `json:"possible_duplicate_of"`
	Notes               *string    `json:"notes"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at"`
}

// Actor identifies who changed the ledger, for the audit trail.
type Actor struct {
	MemberID *int64
	Channel  string
	InboxID  *int64
}

// querier is satisfied by both the pool and a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Service is the deterministic finance core: every read and write of the
// ledger goes through it, scoped to a workspace id resolved by the caller
// from trusted context (the session or the WhatsApp group), never from input.
type Service struct {
	DB  *pgxpool.Pool
	Now func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{DB: pool, Now: timeutil.Now}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return timeutil.Now()
}

func ptr[T any](v T) *T { return &v }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
