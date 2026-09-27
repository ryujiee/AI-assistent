package finance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type dataset struct {
	s          *Service
	ws         *Workspace
	ana, bruno *Member
	cat        func(string) *Category
}

// seedSeptember builds a small, fully known ledger (today is 27/09/2026):
//
//	Aug 5  Mercado      400  Ana       (previous period)
//	Aug 10 Restaurantes 300  Bruno     (previous period)
//	Aug 20 Delivery     200  Bruno     (previous period)
//	Aug 29 Mercado      999  Ana       (outside the month-to-date baseline)
//	Sep 3  Mercado      500  Ana
//	Sep 5  Aluguel     1500  Ana
//	Sep 5  Delivery     300  Bruno
//	Sep 5  Salário     6000  Ana (income)
//	Sep 10 transfer    1000  Ana (never counted)
//	Sep 12 Restaurantes 520  Bruno
//	Sep 15 Mercado      343  Bruno
//	Sep 16 refund Mercado 43 Ana
//	Sep 20 Delivery     310  Bruno
//	Sep 21 pending       80  (not counted), Sep 22 deleted 999 (not counted)
func seedSeptember(t *testing.T) *dataset {
	t.Helper()
	s := newTestService(t)
	ctx := context.Background()
	ws, _ := s.DefaultWorkspace(ctx)
	d := &dataset{s: s, ws: ws}
	d.ana = mustMember(t, s, ws.ID, "5511900000001@s.whatsapp.net", "Ana")
	d.bruno = mustMember(t, s, ws.ID, "5511900000002@s.whatsapp.net", "Bruno")
	d.cat = func(name string) *Category {
		kind := KindExpense
		if name == "Salário" {
			kind = KindIncome
		}
		return mustCategory(t, s, ws.ID, name, kind)
	}
	add := func(typ string, reais int64, cat, date string, payer *Member) *Transaction {
		in := TxInput{Type: typ, AmountCents: reais * 100, Date: date, Source: SourceWeb, PayerMemberID: &payer.ID}
		if cat != "" {
			in.CategoryID = &d.cat(cat).ID
		}
		return mustCreate(t, s, ws.ID, Actor{Channel: ChannelWeb}, in)
	}
	add(TypeExpense, 400, "Mercado", "2026-08-05", d.ana)
	add(TypeExpense, 300, "Restaurantes", "2026-08-10", d.bruno)
	add(TypeExpense, 200, "Delivery", "2026-08-20", d.bruno)
	add(TypeExpense, 999, "Mercado", "2026-08-29", d.ana)
	add(TypeExpense, 500, "Mercado", "2026-09-03", d.ana)
	add(TypeExpense, 1500, "Aluguel", "2026-09-05", d.ana)
	add(TypeExpense, 300, "Delivery", "2026-09-05", d.bruno)
	add(TypeIncome, 6000, "Salário", "2026-09-05", d.ana)
	add(TypeTransfer, 1000, "", "2026-09-10", d.ana)
	add(TypeExpense, 520, "Restaurantes", "2026-09-12", d.bruno)
	add(TypeExpense, 343, "Mercado", "2026-09-15", d.bruno)
	add(TypeRefund, 43, "Mercado", "2026-09-16", d.ana)
	add(TypeExpense, 310, "Delivery", "2026-09-20", d.bruno)
	add(TypeExpense, 80, "", "2026-09-21", d.ana) // pending: no category
	del := add(TypeExpense, 999, "Lazer", "2026-09-22", d.ana)
	if _, err := s.DeleteTransaction(ctx, ws.ID, del.ID, Actor{Channel: ChannelWeb}); err != nil {
		t.Fatal(err)
	}
	return d
}

func thisMonth(t *testing.T) Period {
	p, err := ResolvePeriod("this_month", "", "", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSummaryTotals(t *testing.T) {
	d := seedSeptember(t)
	sum, err := d.s.Summary(context.Background(), d.ws.ID, thisMonth(t), ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.ExpensesCents != 343000 || sum.IncomeCents != 600000 || sum.BalanceCents != 257000 || sum.TransfersCents != 100000 {
		t.Fatalf("totals: expenses %d income %d balance %d transfers %d", sum.ExpensesCents, sum.IncomeCents, sum.BalanceCents, sum.TransfersCents)
	}
	if sum.PrevExpensesCents != 90000 || sum.ExpenseChangePct == nil || *sum.ExpenseChangePct != 281.1 {
		t.Fatalf("baseline: prev %d change %v", sum.PrevExpensesCents, sum.ExpenseChangePct)
	}
	if sum.DailyAverageCents != 343000/27 || sum.ProjectionCents == nil || *sum.ProjectionCents != 343000*30/27 {
		t.Fatalf("average %d projection %v", sum.DailyAverageCents, sum.ProjectionCents)
	}
	if sum.Pending != 1 {
		t.Fatalf("pending = %d", sum.Pending)
	}
	if len(sum.Categories) < 2 || sum.Categories[0].Name != "Alimentação" || sum.Categories[0].AmountCents != 193000 || sum.Categories[1].Name != "Moradia" {
		t.Fatalf("categories = %+v", sum.Categories)
	}
	members := map[string]int64{}
	for _, m := range sum.Members {
		members[m.Name] = m.ExpensesCents
	}
	if members["Ana"] != 195700 || members["Bruno"] != 147300 {
		t.Fatalf("members = %v", members)
	}
}

func TestSummaryFilters(t *testing.T) {
	d := seedSeptember(t)
	ctx := context.Background()
	mercado := d.cat("Mercado")
	sum, _ := d.s.Summary(ctx, d.ws.ID, thisMonth(t), ReportFilter{CategoryID: &mercado.ID})
	if sum.ExpensesCents != 80000 {
		t.Fatalf("Mercado net = %d, want 80000 (500 + 343 - 43)", sum.ExpensesCents)
	}
	alim := d.cat("Alimentação")
	sum, _ = d.s.Summary(ctx, d.ws.ID, thisMonth(t), ReportFilter{CategoryID: &alim.ID})
	if sum.ExpensesCents != 193000 || len(sum.Categories) != 3 {
		t.Fatalf("Alimentação = %d with %d subcategories", sum.ExpensesCents, len(sum.Categories))
	}
	sum, _ = d.s.Summary(ctx, d.ws.ID, thisMonth(t), ReportFilter{MemberID: &d.bruno.ID})
	if sum.ExpensesCents != 147300 {
		t.Fatalf("Bruno = %d", sum.ExpensesCents)
	}
	custom, _ := ResolvePeriod("custom", "", "2026-09-10", "2026-09-20", fixedNow)
	sum, _ = d.s.Summary(ctx, d.ws.ID, custom, ReportFilter{})
	if sum.ExpensesCents != 52000+34300-4300+31000 {
		t.Fatalf("10..20 = %d", sum.ExpensesCents)
	}
}

func TestCorrectionsReflectImmediately(t *testing.T) {
	d := seedSeptember(t)
	ctx := context.Background()
	list, _, _ := d.s.ListTransactions(ctx, d.ws.ID, TxFilter{Start: "2026-09-05", End: "2026-09-05", CategoryID: &d.cat("Aluguel").ID})
	if _, _, err := d.s.UpdateTransaction(ctx, d.ws.ID, list[0].ID, Actor{Channel: ChannelWeb}, TxPatch{AmountCents: ptr(int64(160000))}); err != nil {
		t.Fatal(err)
	}
	sum, _ := d.s.Summary(ctx, d.ws.ID, thisMonth(t), ReportFilter{})
	if sum.ExpensesCents != 353000 {
		t.Fatalf("after correction = %d", sum.ExpensesCents)
	}
}

func TestSeriesAndBudgets(t *testing.T) {
	d := seedSeptember(t)
	ctx := context.Background()
	series, err := d.s.SpendingSeries(ctx, d.ws.ID, thisMonth(t), ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if series.Granularity != "day" || len(series.Points) != 27 || series.Points[4].CurrentCents != 180000 || series.Points[4].PreviousCents != 40000 {
		t.Fatalf("series day 5 = %+v (len %d)", series.Points[4], len(series.Points))
	}
	var total int64
	for _, p := range series.Points {
		total += p.CurrentCents
	}
	if total != 343000 {
		t.Fatalf("series total = %d", total)
	}

	d.s.SetBudget(ctx, d.ws.ID, d.cat("Restaurantes").ID, ptr(int64(60000)))
	d.s.SetBudget(ctx, d.ws.ID, d.cat("Delivery").ID, ptr(int64(50000)))
	d.s.SetBudget(ctx, d.ws.ID, d.cat("Alimentação").ID, ptr(int64(300000)))
	budgets, err := d.s.Budgets(ctx, d.ws.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]BudgetStatus{}
	for _, b := range budgets {
		got[b.Name] = b
	}
	if b := got["Delivery"]; b.State != "over" || b.SpentCents != 61000 || b.RemainingCents != -11000 {
		t.Fatalf("Delivery = %+v", b)
	}
	if b := got["Restaurantes"]; b.State != "warning" || b.Pct != 86.7 || b.RemainingCents != 8000 {
		t.Fatalf("Restaurantes = %+v", b)
	}
	if b := got["Alimentação"]; b.SpentCents != 193000 || b.State != "ok" {
		t.Fatalf("parent budget includes subcategories: %+v", b)
	}
}

func TestInsightsAreFactsNotJudgments(t *testing.T) {
	d := seedSeptember(t)
	ctx := context.Background()
	d.s.SetBudget(ctx, d.ws.ID, d.cat("Restaurantes").ID, ptr(int64(60000)))
	list, err := d.s.Insights(ctx, d.ws.ID, thisMonth(t))
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, in := range list {
		all = append(all, in.Text)
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{"Restaurantes: R$ 520 de R$ 600 (86,7%). Restam R$ 80.", "Maior gasto: R$ 1.500 · Aluguel (05/09).", "projeção",
		"Alimentação está 114,4% acima do mesmo período do mês passado (+R$ 1.030)."} {
		if !strings.Contains(text, want) {
			t.Errorf("missing insight %q in:\n%s", want, text)
		}
	}
	for _, banned := range []string{"demais", "irresponsável", "parem", "exager"} {
		if strings.Contains(strings.ToLower(text), banned) {
			t.Errorf("judgmental wording %q in:\n%s", banned, text)
		}
	}
}

func TestOutlierNeedsHistory(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	ws, _ := s.DefaultWorkspace(ctx)
	mercado := mustCategory(t, s, ws.ID, "Mercado", KindExpense)
	web := Actor{Channel: ChannelWeb}
	mustCreate(t, s, ws.ID, web, expense(89000, mercado, "2026-09-20"))
	p := thisMonth(t)
	if out, _ := s.Outliers(ctx, ws.ID, p); len(out) != 0 {
		t.Fatalf("outlier without history: %+v", out)
	}
	for i, day := range []string{"06-10", "06-20", "07-01", "07-10", "07-20", "08-01", "08-10", "08-20"} {
		mustCreate(t, s, ws.ID, web, expense(int64(20000+i*500), mercado, "2026-"+day))
	}
	out, _ := s.Outliers(ctx, ws.ID, p)
	if len(out) != 1 || !strings.Contains(out[0].Text, "R$ 890 em Mercado") {
		t.Fatalf("outliers = %+v", out)
	}
}

func runTool(t *testing.T, d *dataset, sender *Member, name, args string) map[string]any {
	t.Helper()
	cats, _ := d.s.ListCategories(context.Background(), d.ws.ID, false)
	tr := &turn{svc: d.s, ws: d.ws, sender: sender, actor: Actor{MemberID: &sender.ID, Channel: ChannelWhatsApp},
		allowed: map[int64]bool{}, cats: cats, byCat: CategoryIndex(cats)}
	var out map[string]any
	if err := json.Unmarshal([]byte(tr.execute(context.Background(), name, args)), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("%s failed: %v", name, out)
	}
	return out
}

func TestReportTools(t *testing.T) {
	d := seedSeptember(t)
	period := `"period":"this_month","month":null,"start":null,"end":null`

	report := runTool(t, d, d.ana, "financial_summary", `{`+period+`,"category":null,"member":null,"type":null}`)["report"].(string)
	for _, want := range []string{"📊 *Setembro de 2026*", "Total gasto: *R$ 3.430*", "Alimentação", "R$ 1.930", "↑ 281% em relação a agosto de 2026 (até dia 27)", "Quem pagou: Ana R$ 1.957 · Bruno R$ 1.473", "1 registro(s) pendente(s)"} {
		if !strings.Contains(report, want) {
			t.Errorf("summary report missing %q:\n%s", want, report)
		}
	}

	report = runTool(t, d, d.ana, "financial_summary", `{`+period+`,"category":"mercado","member":null,"type":null}`)["report"].(string)
	if !strings.Contains(report, "Mercado · Setembro de 2026") || !strings.Contains(report, "R$ 800") {
		t.Errorf("category report:\n%s", report)
	}

	report = runTool(t, d, d.ana, "financial_summary", `{`+period+`,"category":null,"member":"minha esposa","type":null}`)["report"].(string)
	if !strings.Contains(report, "Bruno") || !strings.Contains(report, "R$ 1.473") {
		t.Errorf("member report:\n%s", report)
	}

	report = runTool(t, d, d.ana, "financial_summary", `{"period":"custom","month":null,"start":"dia 10","end":"20","category":null,"member":null,"type":null}`)["report"].(string)
	if !strings.Contains(report, "10/09 a 20/09") || !strings.Contains(report, "R$ 1.130") {
		t.Errorf("custom period report:\n%s", report)
	}

	report = runTool(t, d, d.ana, "list_transactions", `{`+period+`,"category":null,"member":null,"type":"EXPENSE","order":"amount","limit":3}`)["report"].(string)
	if !strings.HasPrefix(strings.Split(report, "\n")[1], "1. R$ 1.500 · Aluguel") {
		t.Errorf("largest report:\n%s", report)
	}

	report = runTool(t, d, d.ana, "compare_periods", `{"period_a":"this_month","month_a":null,"start_a":null,"end_a":null,"period_b":"last_month","month_b":null,"start_b":null,"end_b":null,"category":null}`)["report"].(string)
	if !strings.Contains(report, "R$ 3.430 × R$ 900") || !strings.Contains(report, "Moradia") {
		t.Errorf("compare report:\n%s", report)
	}
	report = runTool(t, d, d.ana, "budget_status", `{"category":null}`)["report"].(string)
	if !strings.Contains(report, "Nenhum orçamento") {
		t.Errorf("empty budget report:\n%s", report)
	}
}
