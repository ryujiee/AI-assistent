package finance

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClassifyReply(t *testing.T) {
	strong := []string{"sim", "sim.", "Sim!", "sim, tá certo", "sim, ta certo", "isso", "isso mesmo", "pode", "pode registrar",
		"correto", "confirmo", "confirma", "está certo", "exato", "s"}
	for _, in := range strong {
		if got := classifyReply(in); got != replyAffirmStrong {
			t.Errorf("classifyReply(%q) = %v, want strong affirmative", in, got)
		}
	}
	for _, in := range []string{"👍", "ok", "beleza", "✅"} {
		if got := classifyReply(in); got != replyAffirmWeak {
			t.Errorf("classifyReply(%q) = %v, want weak affirmative", in, got)
		}
	}
	for _, in := range []string{"não", "nao", "não tá certo", "cancela", "deixa", "deixa pra lá", "esquece", "👎"} {
		if got := classifyReply(in); got != replyNegative {
			t.Errorf("classifyReply(%q) = %v, want negative", in, got)
		}
	}
	for _, in := range []string{"Cookies", "mercado", "sim mas foi 40", "na verdade foi 35", "gastei 50 no mercado", "quanto gastamos?"} {
		if got := classifyReply(in); got != replyOther {
			t.Errorf("classifyReply(%q) = %v, want other", in, got)
		}
	}
	for in, want := range map[string]int64{"na verdade foi 35": 3500, "foi 38": 3800, "era 40,50": 4050, "35": 3500, "R$ 35": 3500} {
		if got, ok := amountCorrection(in, AwaitCategory); !ok || got != want {
			t.Errorf("amountCorrection(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	// While a confirmation is open, a new expense is not a correction of the draft.
	if _, ok := amountCorrection("paguei 80 de luz", AwaitConfirm); ok {
		t.Error("a new expense must not be read as an amount correction")
	}
	if got, ok := amountCorrection("80", AwaitConfirm); !ok || got != 8000 {
		t.Errorf("bare amount while confirming = %d %v, want 8000", got, ok)
	}
	if _, ok := amountCorrection("gastei 50 no mercado", AwaitCategory); ok {
		t.Error("a new expense read as a correction")
	}
}

// cookieCreate is what the production model did with the screenshot message:
// category left empty, confidence 0.9.
func cookieCreate(category string) step {
	cat := "null"
	if category != "" {
		cat = `"` + category + `"`
	}
	return step{tool: "create_transaction", args: `{"items":[{"type":"EXPENSE","amount_cents":3300,"description":"Compra de cookie","category":` + cat +
		`,"merchant":"Cookies'N Blues","date":null,"payer":null,"payment_method":null,"confidence":0.9}]}`}
}

func noValueQuestion(t *testing.T, replies ...string) {
	t.Helper()
	for _, r := range replies {
		if strings.Contains(strings.ToLower(r), "valor") {
			t.Fatalf("asked about the amount the user wrote: %q", r)
		}
	}
}

// Regression for the production conversation of 27/09/2026.
func TestScreenshotConversationNoLongerLoops(t *testing.T) {
	h := newAgentHarness(t)
	r1 := h.send(t, from(h.ana, "Gastamos 33 reais em cookie, da Cookies'N Blues"), cookieCreate(""), echo())
	if r1 != "💬 R$ 33,00 anotado.\nFoi com o quê?" {
		t.Fatalf("1: %q", r1)
	}
	tx := h.lastTx(t)

	// The model restates the amount while filling the category, exactly as it
	// did in production. Unchanged amounts no longer need evidence.
	restate := `{"transaction_id":` + itoa(int(tx.ID)) + `,"amount_cents":3300,"category":"Alimentação","date":null,"payer":null,"description":null,"merchant":null,"type":null,"confirm":true}`
	r2 := h.send(t, from(h.ana, "Cookies"), upd(restate), echo())
	if r2 != "✅ R$ 33,00 · Alimentação · Cookies'N Blues · hoje" {
		t.Fatalf("2: %q", r2)
	}

	calls := len(h.llm.requests)
	r3 := h.send(t, from(h.ana, "sim"))
	r4 := h.send(t, from(h.ana, "sim, ta certo"))
	if len(h.llm.requests) != calls {
		t.Fatal("a bare confirmation went to the model")
	}
	if r3 != "Já está registrado: R$ 33,00 · Alimentação · Cookies'N Blues · hoje" || r4 != r3 {
		t.Fatalf("3: %q 4: %q", r3, r4)
	}
	noValueQuestion(t, r1, r2, r3, r4)

	var n int
	h.s.DB.QueryRow(context.Background(), "SELECT count(*) FROM finance_transactions WHERE deleted_at IS NULL").Scan(&n)
	final := h.lastTx(t)
	if n != 1 || final.Status != StatusConfirmed || final.AmountCents != 3300 {
		t.Fatalf("transactions=%d final=%+v", n, final)
	}
}

func TestScreenshotConversationWithCompletePending(t *testing.T) {
	h := newAgentHarness(t)
	h.send(t, from(h.ana, "Gastamos 33 reais em cookie, da Cookies'N Blues"), cookieCreate(""), echo())
	r2 := h.send(t, from(h.ana, "Cookies"), step{tool: "complete_pending", args: `{"category":"Alimentação","amount_cents":null,"date":null,"description":null}`}, echo())
	if r2 != "✅ R$ 33,00 · Alimentação · Cookies'N Blues · hoje" {
		t.Fatalf("2: %q", r2)
	}
	// The model got the draft from the database, not from the chat.
	ctxMsg := h.llm.requests[len(h.llm.requests)-2].Messages[1].Content
	if !strings.Contains(ctxMsg, "PENDÊNCIA ABERTA") || !strings.Contains(ctxMsg, "aguardando a CATEGORIA") {
		t.Fatalf("pending draft missing from context:\n%s", ctxMsg)
	}
}

func TestExplicitMessageRegistersAtOnce(t *testing.T) {
	h := newAgentHarness(t)
	r := h.send(t, from(h.ana, "Gastamos 33 reais em cookie, da Cookies'N Blues"), cookieCreate("Alimentação"), echo())
	if r != "✅ R$ 33,00 · Alimentação · Cookies'N Blues · hoje" {
		t.Fatalf("reply = %q", r)
	}
	if tx := h.lastTx(t); tx.Status != StatusConfirmed || !tx.Shared {
		t.Fatalf("tx = %+v", tx)
	}
}

func TestMissingCategoryIsAnsweredWithoutTheModel(t *testing.T) {
	h := newAgentHarness(t)
	r1 := h.send(t, from(h.ana, "Gastei 50"), create(item("EXPENSE", 5000, "", "", 0.9)), echo())
	if r1 != "💬 R$ 50,00 anotado.\nFoi com o quê?" {
		t.Fatalf("1: %q", r1)
	}
	calls := len(h.llm.requests)
	r2 := h.send(t, from(h.ana, "mercado"))
	if len(h.llm.requests) != calls {
		t.Fatal("a known category went to the model")
	}
	tx := h.lastTx(t)
	if r2 != "✅ R$ 50,00 · Mercado · hoje" || tx.Status != StatusConfirmed || tx.AmountCents != 5000 {
		t.Fatalf("2: %q tx %+v", r2, tx)
	}
	var n int
	h.s.DB.QueryRow(context.Background(), "SELECT count(*) FROM finance_transactions").Scan(&n)
	if n != 1 {
		t.Fatalf("transactions = %d", n)
	}
}

// openConfirm creates an entry whose amount the model got wrong, so the bot
// asks for confirmation (the one case where confirming makes sense).
func openConfirm(t *testing.T, h *agentHarness) *Transaction {
	t.Helper()
	r := h.send(t, from(h.ana, "gastei trinta e três no mercado"), create(item("EXPENSE", 3300, "Mercado", "", 0.9)), echo())
	if !strings.Contains(r, "O valor está certo?") {
		t.Fatalf("confirmation question = %q", r)
	}
	return h.lastTx(t)
}

func TestConfirmationVariants(t *testing.T) {
	for _, yes := range []string{"sim", "sim.", "sim, tá certo", "isso", "isso mesmo", "pode", "pode registrar", "correto", "confirmo", "👍"} {
		t.Run(yes, func(t *testing.T) {
			h := newAgentHarness(t)
			tx := openConfirm(t, h)
			calls := len(h.llm.requests)
			r := h.send(t, from(h.ana, yes))
			got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, false)
			if len(h.llm.requests) != calls || got.Status != StatusConfirmed || r != "✅ R$ 33,00 · Mercado · hoje" {
				t.Fatalf("reply %q status %s llm calls %d", r, got.Status, len(h.llm.requests)-calls)
			}
		})
	}
}

func TestRefusalCancels(t *testing.T) {
	for _, no := range []string{"não", "não tá certo", "cancela", "deixa"} {
		t.Run(no, func(t *testing.T) {
			h := newAgentHarness(t)
			tx := openConfirm(t, h)
			r := h.send(t, from(h.ana, no))
			got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, true)
			if r != "Beleza, não registrei." || got.DeletedAt == nil {
				t.Fatalf("reply %q deleted=%v", r, got.DeletedAt != nil)
			}
			if after := h.send(t, from(h.ana, "sim")); after != "Não encontrei uma confirmação pendente. Me diga novamente o gasto." {
				t.Fatalf("sim after cancel = %q", after)
			}
		})
	}
}

func TestCorrectionWhileConfirmingIsApplied(t *testing.T) {
	h := newAgentHarness(t)
	tx := openConfirm(t, h)
	r := h.send(t, from(h.ana, "na verdade foi 35"))
	got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, false)
	if r != "✅ R$ 35,00 · Mercado · hoje" || got.AmountCents != 3500 || got.Status != StatusConfirmed {
		t.Fatalf("reply %q tx %+v", r, got)
	}
}

func TestSuggestedCategoryAcceptedOrRejected(t *testing.T) {
	h := newAgentHarness(t)
	h.send(t, from(h.ana, "uns 40 no mercado"), create(item("EXPENSE", 4000, "Mercado", "", 0.4)), echo())
	if r := h.send(t, from(h.ana, "sim")); r != "✅ R$ 40,00 · Mercado · hoje" {
		t.Fatalf("accepted suggestion: %q", r)
	}
	h.send(t, from(h.bruno, "uns 20 no uber"), create(item("EXPENSE", 2000, "Aplicativo", "", 0.4)), echo())
	if r := h.send(t, from(h.bruno, "não")); r != "Tudo bem. Foi com o quê?" {
		t.Fatalf("rejected suggestion: %q", r)
	}
	if r := h.send(t, from(h.bruno, "combustível")); r != "✅ R$ 20,00 · Combustível · hoje" {
		t.Fatalf("category after rejection: %q", r)
	}
}

func TestUnknownCategoryNeverBecomesAValueQuestion(t *testing.T) {
	h := newAgentHarness(t)
	h.send(t, from(h.ana, "Gastei 50"), create(item("EXPENSE", 5000, "", "", 0.9)), echo())
	r := h.send(t, from(h.ana, "Cookies"), step{tool: "complete_pending", args: `{"category":"Doces","amount_cents":null,"date":null,"description":null}`}, echo())
	if r != "Não encontrei essa categoria. Foi com o quê?" {
		t.Fatalf("reply = %q", r)
	}
	noValueQuestion(t, r)
}

func TestPendingConversationGivesUpInsteadOfLooping(t *testing.T) {
	h := newAgentHarness(t)
	tx := func() *Transaction {
		h.send(t, from(h.ana, "Gastei 50"), create(item("EXPENSE", 5000, "", "", 0.9)), echo())
		return h.lastTx(t)
	}()
	var replies []string
	for i := 0; i < 3; i++ {
		replies = append(replies, h.send(t, from(h.ana, "sim")))
	}
	if replies[0] != "Preciso só de mais uma informação: Foi com o quê?" || !strings.Contains(replies[2], "deixei o lançamento de R$ 50,00 como pendente no painel") {
		t.Fatalf("replies = %q", replies)
	}
	if got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, false); got.Status != StatusPending {
		t.Fatal("given-up entry was confirmed")
	}
	if r := h.send(t, from(h.ana, "sim")); r != "Não encontrei uma confirmação pendente. Me diga novamente o gasto." {
		t.Fatalf("after giving up: %q", r)
	}
}

func TestExpiredPendingIsNotConfirmedLater(t *testing.T) {
	h := newAgentHarness(t)
	tx := openConfirm(t, h)
	h.s.DB.Exec(context.Background(), "UPDATE finance_pending_actions SET expires_at = now() - interval '1 minute'")
	if r := h.send(t, from(h.ana, "sim")); r != "Não encontrei uma confirmação pendente. Me diga novamente o gasto." {
		t.Fatalf("reply = %q", r)
	}
	if got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, false); got.Status != StatusPending {
		t.Fatal("expired draft was confirmed")
	}
}

func TestQuotedReplyTargetsItsOwnQuestion(t *testing.T) {
	h := newAgentHarness(t)
	h.send(t, from(h.ana, "gastei trinta no mercado"), create(item("EXPENSE", 3000, "Mercado", "", 0.9)), echo())
	anaQuestion := h.gw.Outbox()[len(h.gw.Outbox())-1].ID
	anaTx := h.lastTx(t)
	h.send(t, from(h.bruno, "Gastei 70"), create(item("EXPENSE", 7000, "", "", 0.9)), echo())

	// Bruno answers Ana's question by quoting it.
	m := from(h.bruno, "sim")
	m.QuotedMessageID = anaQuestion
	h.llm.steps = nil
	h.ing.Accept(m)
	h.ing.Drain(context.Background(), h.ws.ID)
	if got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, anaTx.ID, false); got.Status != StatusConfirmed {
		t.Fatal("quoted confirmation did not reach the quoted question")
	}
	if r := h.send(t, from(h.bruno, "mercado")); r != "✅ R$ 70,00 · Mercado · hoje" {
		t.Fatalf("Bruno's own question: %q", r)
	}
}

func TestMerchantHistoryFillsTheCategory(t *testing.T) {
	h := newAgentHarness(t)
	h.send(t, from(h.ana, "Gastamos 33 reais em cookie, da Cookies'N Blues"), cookieCreate("Alimentação"), echo())
	r := h.send(t, from(h.bruno, "mais 20 na Cookies'N Blues"), step{tool: "create_transaction", args: `{"items":[{"type":"EXPENSE","amount_cents":2000,"description":"Cookie","category":null,"merchant":"cookies'n blues","date":null,"payer":null,"payment_method":null,"confidence":0.5}]}`}, echo())
	if !strings.HasPrefix(r, "✅ R$ 20,00 · Alimentação") {
		t.Fatalf("history not used: %q", r)
	}
}

func TestDatabaseFailureAnswersOnceAndKeepsTheDraft(t *testing.T) {
	h := newAgentHarness(t)
	retryBackoff = []time.Duration{0, 0}
	defer func() { retryBackoff = []time.Duration{5 * time.Second, 30 * time.Second} }()
	ctx := context.Background()
	tx := openConfirm(t, h)

	// The audit table is unreachable: the confirmation cannot be written.
	h.s.DB.Exec(ctx, "ALTER TABLE finance_transaction_events RENAME TO finance_events_down")
	before := len(h.gw.Outbox())
	h.ing.Accept(from(h.ana, "sim"))
	for i := 0; i < 5; i++ {
		h.ing.Drain(ctx, h.ws.ID)
	}
	out := h.gw.Outbox()[before:]
	if len(out) != 1 || !strings.Contains(out[0].Text, "Não consegui processar essa mensagem agora") {
		t.Fatalf("replies during the outage = %+v", out)
	}
	h.s.DB.Exec(ctx, "ALTER TABLE finance_events_down RENAME TO finance_transaction_events")

	// The draft and its question survived: a new "sim" finishes it.
	if r := h.send(t, from(h.ana, "sim")); r != "✅ R$ 33,00 · Mercado · hoje" {
		t.Fatalf("after the outage: %q", r)
	}
	if got, _ := h.s.GetTransaction(ctx, h.ws.ID, tx.ID, false); got.Status != StatusConfirmed {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestRedeliveredConfirmationDoesNotDuplicate(t *testing.T) {
	h := newAgentHarness(t)
	ctx := context.Background()
	openConfirm(t, h)
	yes := from(h.ana, "sim")
	h.ing.Accept(yes)
	h.ing.Drain(ctx, h.ws.ID)
	sent := len(h.gw.Outbox())
	h.ing.Accept(yes) // WhatsApp redelivers after a reconnect
	h.ing.Drain(ctx, h.ws.ID)
	var txs, updates int
	h.s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_transactions").Scan(&txs)
	h.s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_transaction_events WHERE action = 'UPDATE'").Scan(&updates)
	if txs != 1 || updates != 1 || len(h.gw.Outbox()) != sent {
		t.Fatalf("transactions=%d updates=%d replies +%d", txs, updates, len(h.gw.Outbox())-sent)
	}
}

func TestPanelConfirmationClosesTheQuestion(t *testing.T) {
	h := newAgentHarness(t)
	ctx := context.Background()
	tx := openConfirm(t, h)
	if _, _, err := h.s.UpdateTransaction(ctx, h.ws.ID, tx.ID, Actor{Channel: ChannelWeb}, TxPatch{Confirm: true}); err != nil {
		t.Fatal(err)
	}
	// A later "ok" is plain chat, not a second confirmation.
	if r := h.send(t, from(h.ana, "ok")); r != "" {
		t.Fatalf("reply = %q", r)
	}
	var open int
	h.s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_pending_actions WHERE status = 'OPEN'").Scan(&open)
	if open != 0 {
		t.Fatalf("open questions = %d", open)
	}
}

// An unquoted message long after the question is not an answer to it: a
// refusal in ordinary chat must not delete the draft an hour later.
func TestStaleQuestionIsNotAnsweredWithoutQuote(t *testing.T) {
	h := newAgentHarness(t)
	tx := openConfirm(t, h)
	h.s.DB.Exec(context.Background(), "UPDATE finance_pending_actions SET updated_at = now() - interval '1 hour'")
	h.send(t, from(h.ana, "não"))
	got, _ := h.s.GetTransaction(context.Background(), h.ws.ID, tx.ID, true)
	if got.DeletedAt != nil {
		t.Fatal("a stale unquoted 'não' deleted the pending draft")
	}
}
