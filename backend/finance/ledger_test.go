package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"secretary/db/dbtest"
)

// newTestService returns a service on an isolated schema with a clock fixed at
// fixedNow (Sunday 27/09/2026 10:00 in Brasília).
func newTestService(t *testing.T) *Service {
	t.Helper()
	s := NewService(dbtest.New(t))
	s.Now = func() time.Time { return fixedNow }
	return s
}

func mustWorkspace(t *testing.T, s *Service, name string) *Workspace {
	t.Helper()
	w, err := s.CreateWorkspace(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func mustCategory(t *testing.T, s *Service, wsID int64, name, kind string) *Category {
	t.Helper()
	c, _, err := s.ResolveCategory(context.Background(), wsID, name, kind)
	if err != nil || c == nil {
		t.Fatalf("category %q not found: %v", name, err)
	}
	return c
}

func mustMember(t *testing.T, s *Service, wsID int64, jid, name string) *Member {
	t.Helper()
	m, err := s.UpsertMember(context.Background(), wsID, MemberInput{JID: jid, DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func expense(cents int64, cat *Category, date string) TxInput {
	in := TxInput{Type: TypeExpense, AmountCents: cents, Date: date, Source: SourceWeb, Description: "teste"}
	if cat != nil {
		in.CategoryID = &cat.ID
	}
	return in
}

func mustCreate(t *testing.T, s *Service, wsID int64, actor Actor, in TxInput) *Transaction {
	t.Helper()
	txs, err := s.CreateTransactions(context.Background(), wsID, actor, []TxInput{in})
	if err != nil {
		t.Fatal(err)
	}
	return &txs[0]
}

func countEvents(t *testing.T, s *Service, txID int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM finance_transaction_events WHERE transaction_id = $1", txID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDefaultWorkspaceSeedsCategoriesOnce(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w1, err := s.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := s.DefaultWorkspace(ctx)
	if err != nil || w2.ID != w1.ID {
		t.Fatalf("second call created another workspace: %v %v", w2, err)
	}

	cats, _ := s.ListCategories(ctx, w1.ID, false)
	before := len(cats)
	byID := CategoryIndex(cats)
	mercado := mustCategory(t, s, w1.ID, "Mercado", KindExpense)
	if CategoryPath(mercado, byID) != "Alimentação › Mercado" {
		t.Errorf("Mercado path = %q", CategoryPath(mercado, byID))
	}
	if mercado.Essentiality != Essential || mustCategory(t, s, w1.ID, "Delivery", KindExpense).Essentiality != Discretionary {
		t.Error("seed essentiality wrong")
	}

	if err := SeedDefaultCategories(ctx, s.DB, w1.ID); err != nil {
		t.Fatal(err)
	}
	cats, _ = s.ListCategories(ctx, w1.ID, false)
	if len(cats) != before {
		t.Fatalf("seed not idempotent: %d -> %d", before, len(cats))
	}
}

func TestCreateTransactionRules(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w := mustWorkspace(t, s, "A")
	mercado := mustCategory(t, s, w.ID, "Mercado", KindExpense)
	salario := mustCategory(t, s, w.ID, "Salário", KindIncome)
	web := Actor{Channel: ChannelWeb}

	tx := mustCreate(t, s, w.ID, web, expense(5000, mercado, "2026-09-27"))
	if tx.Status != StatusConfirmed || tx.AmountCents != 5000 || tx.Currency != "BRL" {
		t.Errorf("confirmed expense = %+v", tx)
	}
	if countEvents(t, s, tx.ID) != 1 {
		t.Error("CREATE audit event missing")
	}

	pending := mustCreate(t, s, w.ID, web, expense(8000, nil, "2026-09-27"))
	if pending.Status != StatusPending || pending.PendingReason == nil || *pending.PendingReason != ReasonCategoryMissing {
		t.Errorf("uncategorized expense = %+v", pending)
	}

	transfer := expense(50000, mercado, "2026-09-27")
	transfer.Type = TypeTransfer
	tr := mustCreate(t, s, w.ID, web, transfer)
	if tr.CategoryID != nil || tr.Status != StatusConfirmed {
		t.Errorf("transfer = %+v", tr)
	}

	bad := []TxInput{
		{Type: TypeIncome, AmountCents: 100, CategoryID: &mercado.ID, Date: "2026-09-27", Source: SourceWeb},
		{Type: TypeExpense, AmountCents: 100, CategoryID: &mercado.ID, Date: "2026-09-28", Source: SourceWeb},
		{Type: TypeExpense, AmountCents: 0, CategoryID: &mercado.ID, Date: "2026-09-27", Source: SourceWeb},
		{Type: "GIFT", AmountCents: 100, Date: "2026-09-27", Source: SourceWeb},
		{Type: TypeExpense, AmountCents: 100, CategoryID: &salario.ID, Date: "2026-09-27", Source: SourceWeb},
	}
	for i, in := range bad {
		if _, err := s.CreateTransactions(ctx, w.ID, web, []TxInput{in}); !errors.Is(err, ErrValidation) {
			t.Errorf("bad input %d accepted: %v", i, err)
		}
	}
}

func TestSanitizesFreeTextBeforeStoring(t *testing.T) {
	s := newTestService(t)
	w := mustWorkspace(t, s, "A")
	in := expense(1000, mustCategory(t, s, w.ID, "Outros", KindExpense), "2026-09-27")
	in.Description = "PIX para CPF 123.456.789-09"
	in.ExternalRef = "123.456.789-09"
	tx := mustCreate(t, s, w.ID, Actor{Channel: ChannelWeb}, in)
	if tx.Description == "PIX para CPF 123.456.789-09" || tx.ExternalRef != nil {
		t.Errorf("PII stored: %q ref %v", tx.Description, tx.ExternalRef)
	}
}

func TestSameMessageNeverCreatesTwice(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w := mustWorkspace(t, s, "A")
	m := mustMember(t, s, w.ID, "5511999990001@s.whatsapp.net", "Ana")
	var inboxID int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO finance_inbox (workspace_id, chat_jid, sender_jid, wa_message_id, kind, message_at)
		VALUES ($1, 'g@g.us', $2, 'MSG1', 'TEXT', now()) RETURNING id`, w.ID, m.JID).Scan(&inboxID); err != nil {
		t.Fatal(err)
	}
	actor := Actor{MemberID: &m.ID, Channel: ChannelWhatsApp, InboxID: &inboxID}
	in := expense(5000, mustCategory(t, s, w.ID, "Mercado", KindExpense), "2026-09-27")
	in.Source = SourceWhatsAppText

	if _, err := s.CreateTransactions(ctx, w.ID, actor, []TxInput{in}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTransactions(ctx, w.ID, actor, []TxInput{in}); !errors.Is(err, ErrAlreadyProcessed) {
		t.Fatalf("replay err = %v, want ErrAlreadyProcessed", err)
	}
	var n int
	s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_transactions WHERE source_inbox_id = $1", inboxID).Scan(&n)
	if n != 1 {
		t.Fatalf("transactions for message = %d", n)
	}
}

func TestUpdateAuditsAndConfirms(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w := mustWorkspace(t, s, "A")
	web := Actor{Channel: ChannelWeb}
	mercado := mustCategory(t, s, w.ID, "Mercado", KindExpense)

	tx := mustCreate(t, s, w.ID, web, expense(5000, mercado, "2026-09-27"))
	got, changed, err := s.UpdateTransaction(ctx, w.ID, tx.ID, web, TxPatch{AmountCents: ptr(int64(6000))})
	if err != nil || !changed || got.AmountCents != 6000 {
		t.Fatalf("update = %+v changed=%v err=%v", got, changed, err)
	}
	if countEvents(t, s, tx.ID) != 2 {
		t.Error("UPDATE audit event missing")
	}
	if _, changed, _ := s.UpdateTransaction(ctx, w.ID, tx.ID, web, TxPatch{AmountCents: ptr(int64(6000))}); changed {
		t.Error("no-op patch reported a change")
	}

	d, err := s.GetTransactionDetail(ctx, w.ID, tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := d.Events[len(d.Events)-1]
	if last.Action != "UPDATE" || len(last.Changes) != 1 || last.Changes[0].Before != "R$ 50,00" || last.Changes[0].After != "R$ 60,00" {
		t.Errorf("history = %+v", last)
	}

	pending := mustCreate(t, s, w.ID, web, expense(8000, nil, "2026-09-27"))
	got, _, err = s.UpdateTransaction(ctx, w.ID, pending.ID, web, TxPatch{CategoryID: &mercado.ID, Confirm: true})
	if err != nil || got.Status != StatusConfirmed || got.PendingReason != nil {
		t.Fatalf("confirm pending = %+v err %v", got, err)
	}
}

func TestDeleteRestoreAndUndo(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w := mustWorkspace(t, s, "A")
	ana := mustMember(t, s, w.ID, "5511999990001@s.whatsapp.net", "Ana")
	bia := mustMember(t, s, w.ID, "5511999990002@s.whatsapp.net", "Bia")
	asAna := Actor{MemberID: &ana.ID, Channel: ChannelWhatsApp}
	asBia := Actor{MemberID: &bia.ID, Channel: ChannelWhatsApp}
	mercado := mustCategory(t, s, w.ID, "Mercado", KindExpense)

	tx := mustCreate(t, s, w.ID, asAna, expense(5000, mercado, "2026-09-27"))
	if _, err := s.DeleteTransaction(ctx, w.ID, tx.ID, Actor{Channel: ChannelWeb}); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := s.ListTransactions(ctx, w.ID, TxFilter{}); len(list) != 0 {
		t.Fatal("deleted transaction still listed")
	}
	if _, err := s.RestoreTransaction(ctx, w.ID, tx.ID, Actor{Channel: ChannelWeb}); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := s.ListTransactions(ctx, w.ID, TxFilter{}); len(list) != 1 {
		t.Fatal("restored transaction not listed")
	}

	// Ana: create + update, then undo twice.
	tx2 := mustCreate(t, s, w.ID, asAna, expense(7000, mercado, "2026-09-26"))
	if _, _, err := s.UpdateTransaction(ctx, w.ID, tx2.ID, asAna, TxPatch{AmountCents: ptr(int64(9700))}); err != nil {
		t.Fatal(err)
	}
	r, err := s.UndoLast(ctx, w.ID, asAna)
	if err != nil || r.Reverted != "UPDATE" || r.Transaction.AmountCents != 7000 {
		t.Fatalf("undo update = %+v err %v", r, err)
	}
	r, err = s.UndoLast(ctx, w.ID, asAna)
	if err != nil || r.Reverted != "CREATE" || r.Transaction.DeletedAt == nil {
		t.Fatalf("undo create = %+v err %v", r, err)
	}

	// Bia changed Ana's transaction afterwards: Ana cannot silently revert.
	tx3 := mustCreate(t, s, w.ID, asAna, expense(1000, mercado, "2026-09-26"))
	if _, _, err := s.UpdateTransaction(ctx, w.ID, tx3.ID, asBia, TxPatch{AmountCents: ptr(int64(1200))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoLast(ctx, w.ID, asAna); !errors.Is(err, ErrUndoStale) {
		t.Fatalf("stale undo err = %v", err)
	}
	if _, err := s.UndoLast(ctx, w.ID, Actor{Channel: ChannelWeb}); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("web undo err = %v", err)
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mustWorkspace(t, s, "A")
	b := mustWorkspace(t, s, "B")
	web := Actor{Channel: ChannelWeb}
	catB := mustCategory(t, s, b.ID, "Mercado", KindExpense)
	txB := mustCreate(t, s, b.ID, web, expense(5000, catB, "2026-09-27"))
	memberB := mustMember(t, s, b.ID, "5511999990009@s.whatsapp.net", "Outra")

	if _, err := s.GetTransaction(ctx, a.ID, txB.ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("read across workspaces: %v", err)
	}
	if _, _, err := s.UpdateTransaction(ctx, a.ID, txB.ID, web, TxPatch{AmountCents: ptr(int64(1))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update across workspaces: %v", err)
	}
	if _, err := s.DeleteTransaction(ctx, a.ID, txB.ID, web); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete across workspaces: %v", err)
	}
	if list, _, _ := s.ListTransactions(ctx, a.ID, TxFilter{}); len(list) != 0 {
		t.Errorf("list leaked %d transactions", len(list))
	}
	in := expense(100, catB, "2026-09-27")
	if _, err := s.CreateTransactions(ctx, a.ID, web, []TxInput{in}); !errors.Is(err, ErrValidation) {
		t.Errorf("foreign category accepted: %v", err)
	}
	in = expense(100, mustCategory(t, s, a.ID, "Mercado", KindExpense), "2026-09-27")
	in.PayerMemberID = &memberB.ID
	if _, err := s.CreateTransactions(ctx, a.ID, web, []TxInput{in}); !errors.Is(err, ErrValidation) {
		t.Errorf("foreign member accepted: %v", err)
	}
	if _, err := s.GetCategory(ctx, a.ID, catB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign category readable: %v", err)
	}
}

func TestCategoryRules(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	w := mustWorkspace(t, s, "A")
	mercado := mustCategory(t, s, w.ID, "Mercado", KindExpense)
	alimentacao := mustCategory(t, s, w.ID, "Alimentação", KindExpense)

	if _, err := s.CreateCategory(ctx, w.ID, CategoryInput{Name: ptr("Hortifruti"), ParentID: &mercado.ID}); !errors.Is(err, ErrValidation) {
		t.Errorf("second level accepted: %v", err)
	}
	if _, err := s.CreateCategory(ctx, w.ID, CategoryInput{Name: ptr("mercado"), ParentID: &alimentacao.ID}); !errors.Is(err, ErrValidation) {
		t.Errorf("duplicate name accepted: %v", err)
	}
	c, err := s.CreateCategory(ctx, w.ID, CategoryInput{Name: ptr("Padaria"), ParentID: &alimentacao.ID, Essentiality: ptr(Important)})
	if err != nil || c.Kind != KindExpense {
		t.Fatalf("create subcategory = %+v, %v", c, err)
	}
	updated, err := s.SetBudget(ctx, w.ID, c.ID, ptr(int64(60000)))
	if err != nil || updated.MonthlyBudgetCents == nil || *updated.MonthlyBudgetCents != 60000 {
		t.Fatalf("set budget = %+v, %v", updated, err)
	}
	cleared, err := s.SetBudget(ctx, w.ID, c.ID, nil)
	if err != nil || cleared.MonthlyBudgetCents != nil {
		t.Fatalf("clear budget = %+v, %v", cleared, err)
	}

	for name, want := range map[string]string{
		"mercado": "Mercado", "MERCADO ": "Mercado", "restaurante": "Restaurantes", "Alimentação > Delivery": "Delivery",
		"Impostos/Taxas": "Impostos/Taxas", "combustivel": "Combustível", "farmacia": "Farmácia",
	} {
		if got := mustCategory(t, s, w.ID, name, KindExpense); got.Name != want {
			t.Errorf("resolve %q = %q, want %q", name, got.Name, want)
		}
	}
	got, options, _ := s.ResolveCategory(ctx, w.ID, "criptomoedas", KindExpense)
	if got != nil || len(options) == 0 {
		t.Errorf("unknown category resolved to %+v", got)
	}
	if got, _, _ := s.ResolveCategory(ctx, w.ID, "mercado", KindIncome); got != nil {
		t.Error("expense category resolved for income")
	}
}
