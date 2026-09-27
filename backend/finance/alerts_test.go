package finance

import (
	"context"
	"strings"
	"testing"
	"time"

	"secretary/timeutil"
	"secretary/whatsapp"
)

func (d *dataset) spend(t *testing.T, reais int64, cat, date string) *Transaction {
	t.Helper()
	return mustCreate(t, d.s, d.ws.ID, Actor{Channel: ChannelWeb}, TxInput{Type: TypeExpense, AmountCents: reais * 100,
		CategoryID: &d.cat(cat).ID, Date: date, Source: SourceWeb, Description: cat, PayerMemberID: &d.ana.ID})
}

func (d *dataset) alerts(t *testing.T, tx *Transaction) []string {
	t.Helper()
	return d.s.AlertsAfterChange(context.Background(), d.ws, []int64{tx.ID})
}

func emptyDataset(t *testing.T) *dataset {
	s := newTestService(t)
	ws, _ := s.DefaultWorkspace(context.Background())
	d := &dataset{s: s, ws: ws}
	d.ana = mustMember(t, s, ws.ID, "5511900000001@s.whatsapp.net", "Ana")
	d.cat = func(name string) *Category { return mustCategory(t, s, ws.ID, name, KindExpense) }
	return d
}

func TestBudgetAlertsFireOncePerThreshold(t *testing.T) {
	d := emptyDataset(t)
	ctx := context.Background()
	d.s.SetBudget(ctx, d.ws.ID, d.cat("Restaurantes").ID, ptr(int64(60000)))

	if got := d.alerts(t, d.spend(t, 300, "Restaurantes", "2026-09-10")); len(got) != 0 {
		t.Fatalf("50%%: %v", got)
	}
	got := d.alerts(t, d.spend(t, 130, "Restaurantes", "2026-09-12")) // 71.7%
	if len(got) != 1 || !strings.Contains(got[0], "chegou a 71% do orçamento") {
		t.Fatalf("70%%: %v", got)
	}
	got = d.alerts(t, d.spend(t, 90, "Restaurantes", "2026-09-15")) // 86.7%
	if len(got) != 1 || got[0] != "⚠️ Vocês gastaram R$ 520 dos R$ 600 planejados com restaurantes este mês. Restam R$ 80." {
		t.Fatalf("80%%: %v", got)
	}
	if got := d.alerts(t, d.spend(t, 10, "Restaurantes", "2026-09-16")); len(got) != 0 {
		t.Fatalf("repeated 80%%: %v", got)
	}
	got = d.alerts(t, d.spend(t, 100, "Restaurantes", "2026-09-20")) // 105%
	if len(got) != 1 || !strings.Contains(got[0], "passou do orçamento do mês: R$ 630 de R$ 600") {
		t.Fatalf("100%%: %v", got)
	}
	// Last month's spending never alerts about this month's budget.
	if got := d.alerts(t, d.spend(t, 500, "Restaurantes", "2026-08-20")); len(got) != 0 {
		t.Fatalf("backdated: %v", got)
	}
}

func TestBudgetJumpSendsOnlyTheHighestLevel(t *testing.T) {
	d := emptyDataset(t)
	d.s.SetBudget(context.Background(), d.ws.ID, d.cat("Lazer").ID, ptr(int64(40000)))
	got := d.alerts(t, d.spend(t, 450, "Lazer", "2026-09-19"))
	if len(got) != 1 || !strings.Contains(got[0], "passou do orçamento") {
		t.Fatalf("jump: %v", got)
	}
	if got := d.alerts(t, d.spend(t, 10, "Lazer", "2026-09-20")); len(got) != 0 {
		t.Fatalf("lower thresholds announced after 100%%: %v", got)
	}
}

func TestParentBudgetCountsSubcategories(t *testing.T) {
	d := emptyDataset(t)
	d.s.SetBudget(context.Background(), d.ws.ID, d.cat("Alimentação").ID, ptr(int64(100000)))
	d.spend(t, 600, "Mercado", "2026-09-05")
	got := d.alerts(t, d.spend(t, 250, "Delivery", "2026-09-06"))
	if len(got) != 1 || !strings.Contains(got[0], "alimentação") {
		t.Fatalf("parent budget: %v", got)
	}
}

func TestOutlierAlertNeedsEightSamples(t *testing.T) {
	d := emptyDataset(t)
	for _, day := range []string{"06-05", "06-15", "06-25", "07-05", "07-15", "07-25", "08-05"} {
		d.spend(t, 200, "Mercado", "2026-"+day)
	}
	big := d.spend(t, 900, "Mercado", "2026-09-10")
	if got := d.alerts(t, big); len(got) != 0 {
		t.Fatalf("outlier with only 7 past expenses: %v", got)
	}
	d.s.DeleteTransaction(context.Background(), d.ws.ID, big.ID, Actor{Channel: ChannelWeb})
	d.spend(t, 210, "Mercado", "2026-08-15")
	got := d.alerts(t, d.spend(t, 950, "Mercado", "2026-09-12"))
	if len(got) != 1 || !strings.Contains(got[0], "acima do habitual em Mercado") {
		t.Fatalf("outlier: %v", got)
	}
}

func TestSpikeSequenceSubscriptionAndCap(t *testing.T) {
	d := emptyDataset(t)
	d.spend(t, 200, "Delivery", "2026-08-10")
	d.spend(t, 190, "Delivery", "2026-09-26")
	d.spend(t, 60, "Delivery", "2026-09-26")
	got := d.alerts(t, d.spend(t, 70, "Delivery", "2026-09-27"))
	joined := strings.Join(got, "\n")
	if len(got) != 2 || !strings.Contains(joined, "Delivery está 60% acima") || !strings.Contains(joined, "3º registro de Delivery desde ontem (R$ 320 no total)") {
		t.Fatalf("spike + sequence: %v", got)
	}
	if got := d.alerts(t, d.spend(t, 20, "Delivery", "2026-09-27")); len(got) != 0 {
		t.Fatalf("spike/sequence repeated: %v", got)
	}

	sub := func(reais int64, date string) *Transaction {
		return mustCreate(t, d.s, d.ws.ID, Actor{Channel: ChannelWeb}, TxInput{Type: TypeExpense, AmountCents: reais * 100,
			CategoryID: &d.cat("Assinaturas").ID, Date: date, Source: SourceWeb, Merchant: "Streaming Flix", Description: "assinatura"})
	}
	sub(55, "2026-08-27")
	got = d.alerts(t, sub(60, "2026-09-27"))
	if len(got) != 1 || !strings.Contains(got[0], "Streaming Flix subiu de R$ 55,00 para R$ 60,00") {
		t.Fatalf("subscription: %v", got)
	}
}

func TestAlertsRideOnTheReply(t *testing.T) {
	h := newAgentHarness(t)
	h.agent.AfterChange = h.s.AlertsAfterChange
	h.s.SetBudget(context.Background(), h.ws.ID, mustCategory(t, h.s, h.ws.ID, "Restaurantes", KindExpense).ID, ptr(int64(10000)))
	reply := h.send(t, from(h.ana, "gastei 90 no restaurante"), create(item("EXPENSE", 9000, "Restaurantes", "", 0.95)), echo())
	if reply != "✅ R$ 90,00 · Restaurantes · hoje\n⚠️ Vocês gastaram R$ 90 dos R$ 100 planejados com restaurantes este mês. Restam R$ 10." {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSummariesAreSentOnceAndNeverEmpty(t *testing.T) {
	d := seedSeptember(t)
	ctx := context.Background()
	gw := whatsapp.NewFakeGateway()
	whatsapp.InitFake("connected")
	d.s.LinkGroup(ctx, d.ws.ID, "120363000000000001@g.us", "Financeiro", time.Now())
	sm := &Summaries{Svc: d.s, GW: gw}

	text, ok, err := d.s.MonthlySummaryText(ctx, d.ws.ID, mustPeriod(t, "month", "2026-08"))
	if err != nil || !ok || !strings.Contains(text, "📊 *Fechamento de agosto*") || !strings.Contains(text, "Despesas: R$ 1.899") {
		t.Fatalf("monthly text (%v %v):\n%s", ok, err, text)
	}

	// Default setting is off: nothing goes out.
	d.s.Now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, timeutil.Location()) }
	sm.RunMonthly(ctx, "first_day")
	if len(gw.Outbox()) != 0 {
		t.Fatal("summary sent while disabled")
	}
	d.s.UpdateSettings(ctx, d.ws.ID, SettingsPatch{MonthlySummary: ptr("first_day"), WeeklySummary: ptr(true)})
	sm.RunMonthly(ctx, "last_day") // wrong run for this setting
	sm.RunMonthly(ctx, "first_day")
	sm.RunMonthly(ctx, "first_day") // duplicate cron fire
	out := gw.Outbox()
	if len(out) != 1 || !strings.Contains(out[0].Text, "Fechamento de setembro") || !strings.Contains(out[0].Text, "Comparação com agosto") {
		t.Fatalf("monthly outbox = %+v", out)
	}

	d.s.Now = func() time.Time { return time.Date(2026, 9, 28, 9, 0, 0, 0, timeutil.Location()) } // Monday
	d.spend(t, 120, "Mercado", "2026-09-23")
	sm.RunWeekly(ctx)
	sm.RunWeekly(ctx)
	out = gw.Outbox()
	if len(out) != 2 || !strings.Contains(out[1].Text, "Resumo da semana (21/09 a 27/09)") {
		t.Fatalf("weekly outbox = %+v", out)
	}

	// An empty week sends nothing.
	d.s.Now = func() time.Time { return time.Date(2026, 11, 16, 9, 0, 0, 0, timeutil.Location()) }
	sm.RunWeekly(ctx)
	if len(gw.Outbox()) != 2 {
		t.Fatal("empty weekly summary sent")
	}
}

func mustPeriod(t *testing.T, key, month string) Period {
	t.Helper()
	p, err := ResolvePeriod(key, month, "", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
