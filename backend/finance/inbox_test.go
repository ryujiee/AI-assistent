package finance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"secretary/whatsapp"
)

const testGroup = "120363000000000001@g.us"

type inboxHarness struct {
	s   *Service
	ing *Ingestor
	gw  *whatsapp.FakeGateway
	ws  *Workspace

	mu    sync.Mutex
	calls []string
	fn    func(ctx context.Context, it *InboxItem) (*HandlerResult, error)
}

func newInboxHarness(t *testing.T) *inboxHarness {
	t.Helper()
	h := &inboxHarness{s: newTestService(t), gw: whatsapp.NewFakeGateway()}
	h.ing = NewIngestor(h.s, h.gw, func(ctx context.Context, it *InboxItem) (*HandlerResult, error) {
		h.mu.Lock()
		h.calls = append(h.calls, it.Text)
		h.mu.Unlock()
		if h.fn != nil {
			return h.fn(ctx, it)
		}
		return &HandlerResult{}, nil
	})
	var err error
	h.ws, err = h.s.DefaultWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.ing.LinkGroup(context.Background(), h.ws.ID, testGroup); err != nil {
		t.Fatal(err)
	}
	return h
}

func msg(id, text string) whatsapp.Message {
	return whatsapp.Message{
		ChatJID: testGroup, SenderJID: "5511900000001@s.whatsapp.net", SenderPN: "5511900000001@s.whatsapp.net",
		MessageID: id, Timestamp: time.Now().Add(time.Second), PushName: "Ana", IsGroup: true, Kind: whatsapp.KindText, Text: text,
	}
}

func (h *inboxHarness) count(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := h.s.DB.QueryRow(context.Background(), "SELECT count(*) FROM finance_inbox WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLinkGroupRegistersHumansOnly(t *testing.T) {
	h := newInboxHarness(t)
	members, _ := h.s.ListMembers(context.Background(), h.ws.ID)
	if len(members) != 2 {
		t.Fatalf("members = %+v, want Ana and Bruno (bot excluded)", members)
	}
	if !h.ing.IsLinked(testGroup) || h.ing.IsLinked("120363000000000002@g.us") {
		t.Fatal("router cache not updated")
	}
}

func TestRedeliveredMessageIsStoredOnce(t *testing.T) {
	h := newInboxHarness(t)
	m := msg("WAMSG1", "gastei 50 no mercado")
	h.ing.Accept(m)
	h.ing.Accept(m) // reconnect redelivery
	h.ing.Accept(m)
	if n := h.count(t, "wa_message_id = 'WAMSG1'"); n != 1 {
		t.Fatalf("inbox rows = %d", n)
	}
	h.ing.Drain(context.Background(), h.ws.ID)
	if len(h.calls) != 1 {
		t.Fatalf("handler calls = %d", len(h.calls))
	}
}

func TestMessagesBeforeLinkAndOtherGroupsAreIgnored(t *testing.T) {
	h := newInboxHarness(t)
	old := msg("OLD", "gastei 10")
	old.Timestamp = time.Now().Add(-time.Hour)
	h.ing.Accept(old)
	other := msg("OTHER", "gastei 10")
	other.ChatJID = "120363000000000002@g.us"
	h.ing.Accept(other)
	edit := msg("EDIT", "gastei 99")
	edit.IsEdit = true
	h.ing.Accept(edit)
	bot := msg("BOT", "✅ R$ 10,00")
	bot.SenderJID, bot.SenderPN = whatsapp.FakeSelfJID, whatsapp.FakeSelfJID
	h.ing.Accept(bot)
	if n := h.count(t, "true"); n != 0 {
		t.Fatalf("inbox rows = %d, want 0", n)
	}
}

func TestProcessingKeepsOrderAndReplies(t *testing.T) {
	h := newInboxHarness(t)
	h.fn = func(ctx context.Context, it *InboxItem) (*HandlerResult, error) {
		return &HandlerResult{Reply: "ok: " + it.Text}, nil
	}
	h.ing.Accept(msg("A1", "gastei 50"))
	h.ing.Accept(msg("A2", "na verdade foi 60"))
	h.ing.Drain(context.Background(), h.ws.ID)

	if len(h.calls) != 2 || h.calls[0] != "gastei 50" || h.calls[1] != "na verdade foi 60" {
		t.Fatalf("order = %v", h.calls)
	}
	out := h.gw.Outbox()
	if len(out) != 2 || out[0].ChatJID != testGroup || out[0].QuotedID != "A1" {
		t.Fatalf("replies = %+v", out)
	}
	if n := h.count(t, "status = 'DONE' AND reply_message_id IS NOT NULL"); n != 2 {
		t.Fatalf("done rows = %d", n)
	}
}

func TestTransientErrorsRetryThenFail(t *testing.T) {
	h := newInboxHarness(t)
	retryBackoff = []time.Duration{0, 0}
	defer func() { retryBackoff = []time.Duration{5 * time.Second, 30 * time.Second} }()
	h.fn = func(context.Context, *InboxItem) (*HandlerResult, error) { return nil, errors.New("openai: 503") }
	h.ing.Accept(msg("R1", "gastei 50"))
	for i := 0; i < 5; i++ {
		h.ing.Drain(context.Background(), h.ws.ID)
	}
	if len(h.calls) != MaxAttempts {
		t.Fatalf("attempts = %d, want %d", len(h.calls), MaxAttempts)
	}
	if n := h.count(t, "status = 'FAILED' AND last_error_code = 'ai_error' AND media_ref IS NULL"); n != 1 {
		t.Fatal("message not FAILED after max attempts")
	}
	if out := h.gw.Outbox(); len(out) != 1 {
		t.Fatalf("user not told about the failure: %+v", out)
	}
}

func TestPermanentErrorAnswersWithoutRetry(t *testing.T) {
	h := newInboxHarness(t)
	h.fn = func(context.Context, *InboxItem) (*HandlerResult, error) {
		return nil, &PermanentError{Code: "mime_not_allowed", Reply: "Envie JPG, PNG, WEBP ou PDF."}
	}
	h.ing.Accept(msg("P1", ""))
	h.ing.Drain(context.Background(), h.ws.ID)
	if len(h.calls) != 1 || h.count(t, "status = 'FAILED'") != 1 {
		t.Fatalf("calls %d", len(h.calls))
	}
	if out := h.gw.Outbox(); len(out) != 1 || out[0].Text != "Envie JPG, PNG, WEBP ou PDF." {
		t.Fatalf("reply = %+v", out)
	}
}

func TestCrashAfterTransactionIsNotReplayed(t *testing.T) {
	h := newInboxHarness(t)
	ctx := context.Background()
	h.ing.Accept(msg("C1", "gastei 50 no mercado"))
	item, err := h.s.claimInbox(ctx, h.firstInboxID(t))
	if err != nil {
		t.Fatal(err)
	}
	// The process created the transaction and died before marking the row DONE.
	mercado := mustCategory(t, h.s, h.ws.ID, "Mercado", KindExpense)
	in := expense(5000, mercado, "2026-09-27")
	in.Source = SourceWhatsAppText
	if _, err := h.s.CreateTransactions(ctx, h.ws.ID, Actor{Channel: ChannelWhatsApp, InboxID: &item.ID, MemberID: item.MemberID}, []TxInput{in}); err != nil {
		t.Fatal(err)
	}
	h.s.DB.Exec(ctx, "UPDATE finance_inbox SET processing_at = now() - interval '10 minutes' WHERE id = $1", item.ID)

	if _, err := h.ing.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	h.ing.Drain(ctx, h.ws.ID)
	if len(h.calls) != 0 {
		t.Fatalf("handler replayed a message whose transaction exists: %v", h.calls)
	}
	if h.count(t, "status = 'DONE'") != 1 {
		t.Fatal("row not closed as DONE")
	}
	var n int
	h.s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_transactions").Scan(&n)
	if n != 1 {
		t.Fatalf("transactions = %d", n)
	}
}

func TestStaleProcessingIsResumed(t *testing.T) {
	h := newInboxHarness(t)
	ctx := context.Background()
	h.ing.Accept(msg("S1", "gastei 50"))
	id := h.firstInboxID(t)
	if _, err := h.s.claimInbox(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Fresh PROCESSING rows are left alone...
	if n, _ := h.ing.Recover(ctx); n != 0 {
		t.Fatalf("recovered a live row")
	}
	// ...abandoned ones go back to the queue and are processed once.
	h.s.DB.Exec(ctx, "UPDATE finance_inbox SET processing_at = now() - interval '10 minutes' WHERE id = $1", id)
	if n, _ := h.ing.Recover(ctx); n != 1 {
		t.Fatal("stale row not recovered")
	}
	h.ing.Drain(ctx, h.ws.ID)
	if len(h.calls) != 1 || h.count(t, "status = 'DONE' AND attempts = 2") != 1 {
		t.Fatalf("calls %v", h.calls)
	}
}

func (h *inboxHarness) firstInboxID(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := h.s.DB.QueryRow(context.Background(), "SELECT id FROM finance_inbox ORDER BY id LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
