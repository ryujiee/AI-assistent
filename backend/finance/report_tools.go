package finance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

// Query tools for the agent. They return a ready-to-send "report" computed
// by the backend plus the raw numbers; the model only picks the tool and may
// shorten the text. It never adds up anything itself.

func reportTools() []sashabaranov_openai.Tool {
	nullType := sEnum("EXPENSE para gastos (padrão), INCOME para receitas", []string{TypeExpense, TypeIncome}, true)
	return []sashabaranov_openai.Tool{
		strictTool("financial_summary", "Totais do período: gastos, receitas, saldo, por categoria e por pessoa, com comparação ao período anterior.",
			periodProps(map[string]any{
				"category": sNullString("Filtra por categoria (inclui subcategorias)"),
				"member":   sNullString("Filtra por quem pagou: nome, 'eu' ou 'minha esposa/meu marido'"),
				"type":     nullType,
			})),
		strictTool("list_transactions", "Lista transações (ex.: maiores gastos, últimos registros).",
			periodProps(map[string]any{
				"category": sNullString("Categoria"),
				"member":   sNullString("Quem pagou"),
				"type":     sEnum("Tipo", TransactionTypes, true),
				"order":    sEnum("date = mais recentes; amount = maiores valores", []string{"date", "amount"}, false),
				"limit":    sInt("Quantidade, de 1 a 20"),
			})),
		strictTool("compare_periods", "Compara dois períodos (total e maiores variações por categoria).", map[string]any{
			"period_a": sEnum("Primeiro período", PeriodKeys, false), "month_a": sNullString("AAAA-MM"), "start_a": sNullString("AAAA-MM-DD"), "end_a": sNullString("AAAA-MM-DD"),
			"period_b": sEnum("Segundo período", PeriodKeys, false), "month_b": sNullString("AAAA-MM"), "start_b": sNullString("AAAA-MM-DD"), "end_b": sNullString("AAAA-MM-DD"),
			"category": sNullString("Categoria (opcional)"),
		}),
		strictTool("budget_status", "Situação dos orçamentos mensais (gasto, restante, projeção).", map[string]any{
			"category": sNullString("Uma categoria; null = todas com orçamento"),
		}),
		strictTool("insights", "Destaques do período baseados nos dados: variações, orçamentos, projeção, gastos fora do padrão.", periodProps(nil)),
	}
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// resolveToolPeriod accepts ISO dates or what the user said ("dia 10", "01/09").
func (t *turn) resolveToolPeriod(key string, month, start, end *string) (Period, error) {
	st, en := strOr(start, ""), strOr(end, "")
	if key == "custom" {
		if d, err := ResolveDate(st, t.svc.now()); err == nil {
			st = d
		}
		if d, err := ResolveDate(en, t.svc.now()); err == nil {
			en = d
		}
	}
	return ResolvePeriod(key, strOr(month, ""), st, en, t.svc.now())
}

func (t *turn) reportFilter(ctx context.Context, category, member *string) (ReportFilter, string, error) {
	var f ReportFilter
	var labels []string
	if category != nil && strings.TrimSpace(*category) != "" {
		c, options := resolveCategory(t.cats, *category, KindExpense)
		if c == nil {
			c, _ = resolveCategory(t.cats, *category, KindIncome)
		}
		if c == nil {
			return f, "", fmt.Errorf("categoria desconhecida; opções: %s", strings.Join(options, ", "))
		}
		f.CategoryID = &c.ID
		labels = append(labels, strings.TrimSpace(c.Icon+" "+c.Name))
	}
	if member != nil && strings.TrimSpace(*member) != "" {
		id, err := t.svc.ResolvePayer(ctx, t.ws.ID, t.actor.MemberID, *member)
		if err != nil || id == nil {
			return f, "", fmt.Errorf("não sei quem é %q", *member)
		}
		f.MemberID = id
		if m, err := t.svc.GetMember(ctx, t.ws.ID, *id); err == nil {
			labels = append(labels, "👤 "+m.DisplayName)
		}
	}
	return f, strings.Join(labels, " · "), nil
}

func (t *turn) runReportTool(ctx context.Context, name, args string) (string, bool) {
	switch name {
	case "financial_summary":
		return t.financialSummary(ctx, args), true
	case "list_transactions":
		return t.listTransactions(ctx, args), true
	case "compare_periods":
		return t.comparePeriods(ctx, args), true
	case "budget_status":
		return t.budgetStatus(ctx, args), true
	case "insights":
		return t.insights(ctx, args), true
	}
	return "", false
}

type reportResult struct {
	OK      bool   `json:"ok"`
	Report  string `json:"report"`
	Details any    `json:"details,omitempty"`
}

func (r reportResult) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func (t *turn) financialSummary(ctx context.Context, args string) string {
	var a struct {
		Period   string  `json:"period"`
		Month    *string `json:"month"`
		Start    *string `json:"start"`
		End      *string `json:"end"`
		Category *string `json:"category"`
		Member   *string `json:"member"`
		Type     *string `json:"type"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	p, err := t.resolveToolPeriod(a.Period, a.Month, a.Start, a.End)
	if err != nil {
		return fail(err.Error())
	}
	f, label, err := t.reportFilter(ctx, a.Category, a.Member)
	if err != nil {
		return fail(err.Error())
	}
	sum, err := t.svc.Summary(ctx, t.ws.ID, p, f)
	if err != nil {
		return fail("não foi possível consultar")
	}
	report := FormatSummary(sum, label, strOr(a.Type, TypeExpense) == TypeIncome)
	return reportResult{OK: true, Report: report, Details: map[string]any{
		"periodo": p.Label, "de": p.Start, "ate": p.End, "gastos_centavos": sum.ExpensesCents, "receitas_centavos": sum.IncomeCents,
		"saldo_centavos": sum.BalanceCents, "variacao_gastos_pct": sum.ExpenseChangePct, "periodo_anterior": sum.Previous.Label,
		"gastos_anterior_centavos": sum.PrevExpensesCents, "por_pessoa": sum.Members, "pendentes": sum.Pending,
	}}.String()
}

func dots(name string, width int) string {
	n := width - utf8.RuneCountInString(name)
	if n < 2 {
		n = 2
	}
	return " " + strings.Repeat(".", n) + " "
}

func arrow(pctChange *float64) string {
	if pctChange == nil {
		return ""
	}
	switch {
	case *pctChange > 0:
		return "↑ " + pct(roundPct(*pctChange))
	case *pctChange < 0:
		return "↓ " + pct(roundPct(-*pctChange))
	}
	return "= estável"
}

func roundPct(v float64) float64 {
	if v >= 10 {
		return float64(int64(v + 0.5))
	}
	return float64(int64(v*10+0.5)) / 10
}

// FormatSummary renders the compact WhatsApp report.
func FormatSummary(sum *Summary, filterLabel string, incomeFocus bool) string {
	var b strings.Builder
	title := sum.Period.Label
	if filterLabel != "" {
		title = filterLabel + " · " + title
	}
	fmt.Fprintf(&b, "📊 *%s*\n\n", title)

	if incomeFocus {
		fmt.Fprintf(&b, "Receitas: *%s*", FormatBRLShort(sum.IncomeCents))
		if a := arrow(sum.IncomeChangePct); a != "" {
			fmt.Fprintf(&b, " (%s vs %s)", a, sum.Previous.Label)
		}
		b.WriteString("\n")
		return strings.TrimSpace(b.String())
	}

	if sum.Transactions == 0 && sum.PrevExpensesCents == 0 {
		b.WriteString("Nenhum gasto registrado nesse período.")
		return b.String()
	}
	fmt.Fprintf(&b, "Total gasto: *%s*\n", FormatBRLShort(sum.ExpensesCents))

	if len(sum.Categories) > 1 {
		shown, rest := sum.Categories, int64(0)
		if len(shown) > 6 {
			for _, c := range shown[6:] {
				rest += c.AmountCents
			}
			shown = shown[:6]
		}
		for _, c := range shown {
			if c.AmountCents == 0 {
				continue
			}
			name := strings.TrimSpace(c.Icon + " " + c.Name)
			fmt.Fprintf(&b, "%s%s%s\n", name, dots(c.Name, 14), FormatBRLShort(c.AmountCents))
		}
		if rest > 0 {
			fmt.Fprintf(&b, "Demais%s%s\n", dots("Demais", 16), FormatBRLShort(rest))
		}
	}

	if a := arrow(sum.ExpenseChangePct); a != "" {
		fmt.Fprintf(&b, "\n%s em relação a %s.\n", a, strings.ToLower(sum.Previous.Label[:1])+sum.Previous.Label[1:])
	}
	if filterLabel == "" && (sum.IncomeCents > 0) {
		fmt.Fprintf(&b, "Receitas: %s · Saldo: %s\n", FormatBRLShort(sum.IncomeCents), FormatBRLShort(sum.BalanceCents))
	}
	if !strings.Contains(filterLabel, "👤") {
		var parts []string
		for _, m := range sum.Members {
			if m.ExpensesCents != 0 {
				parts = append(parts, fmt.Sprintf("%s %s", m.Name, FormatBRLShort(m.ExpensesCents)))
			}
		}
		if len(parts) > 1 {
			fmt.Fprintf(&b, "Quem pagou: %s\n", strings.Join(parts, " · "))
		}
	}
	if sum.ProjectionCents != nil && filterLabel == "" {
		fmt.Fprintf(&b, "Projeção do mês no ritmo atual: ~%s\n", FormatBRLShort(roundTo(*sum.ProjectionCents, 1000)))
	}
	if sum.Pending > 0 {
		fmt.Fprintf(&b, "_%d registro(s) pendente(s) fora da conta._\n", sum.Pending)
	}
	return strings.TrimSpace(b.String())
}

func (t *turn) listTransactions(ctx context.Context, args string) string {
	var a struct {
		Period   string  `json:"period"`
		Month    *string `json:"month"`
		Start    *string `json:"start"`
		End      *string `json:"end"`
		Category *string `json:"category"`
		Member   *string `json:"member"`
		Type     *string `json:"type"`
		Order    string  `json:"order"`
		Limit    int     `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	p, err := t.resolveToolPeriod(a.Period, a.Month, a.Start, a.End)
	if err != nil {
		return fail(err.Error())
	}
	f, label, err := t.reportFilter(ctx, a.Category, a.Member)
	if err != nil {
		return fail(err.Error())
	}
	if a.Limit < 1 || a.Limit > 20 {
		a.Limit = 10
	}
	typ := strOr(a.Type, "")
	if typ != "" && !contains(TransactionTypes, typ) {
		return fail("tipo inválido")
	}
	items, total, err := t.svc.ListTransactions(ctx, t.ws.ID, TxFilter{Start: p.Start, End: p.End, CategoryID: f.CategoryID,
		MemberID: f.MemberID, Type: typ, Status: StatusConfirmed, OrderBy: a.Order, Limit: a.Limit})
	if err != nil {
		return fail("não foi possível consultar")
	}
	var b strings.Builder
	head := "🧾 Últimos registros"
	if a.Order == "amount" {
		head = "🧾 Maiores valores"
	}
	fmt.Fprintf(&b, "%s · %s", head, p.Label)
	if label != "" {
		b.WriteString(" · " + label)
	}
	b.WriteString("\n")
	var details []map[string]any
	for i, v := range items {
		t.allow(v.ID)
		line := fmt.Sprintf("%d. %s · %s · %s", i+1, FormatBRLShort(v.AmountCents), viewCategory(v), formatShortCivil(v.TransactionDate))
		if v.PayerName != "" {
			line += " · " + v.PayerName
		}
		b.WriteString(line + "\n")
		details = append(details, map[string]any{"id": v.ID, "valor_centavos": v.AmountCents, "categoria": viewCategory(v), "data": v.TransactionDate, "descricao": v.Description})
	}
	if len(items) == 0 {
		b.WriteString("Nenhum registro nesse período.")
	} else if total > len(items) {
		fmt.Fprintf(&b, "_+%d outros no período._", total-len(items))
	}
	return reportResult{OK: true, Report: strings.TrimSpace(b.String()), Details: details}.String()
}

func (t *turn) comparePeriods(ctx context.Context, args string) string {
	var a struct {
		PeriodA  string  `json:"period_a"`
		MonthA   *string `json:"month_a"`
		StartA   *string `json:"start_a"`
		EndA     *string `json:"end_a"`
		PeriodB  string  `json:"period_b"`
		MonthB   *string `json:"month_b"`
		StartB   *string `json:"start_b"`
		EndB     *string `json:"end_b"`
		Category *string `json:"category"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	pa, err := t.resolveToolPeriod(a.PeriodA, a.MonthA, a.StartA, a.EndA)
	if err != nil {
		return fail(err.Error())
	}
	pb, err := t.resolveToolPeriod(a.PeriodB, a.MonthB, a.StartB, a.EndB)
	if err != nil {
		return fail(err.Error())
	}
	// Comparing the running month with a whole month would be unfair: use the
	// same number of days of the other month.
	if pa.IsCurrentMonth(t.svc.now()) && pb.startTime().Day() == 1 && pb.Days() > pa.Days() {
		pb = mkPeriod(pb.Key, pb.startTime(), pb.startTime().AddDate(0, 0, pa.Days()-1), pb.Label+" (até dia "+fmt.Sprint(pa.Days())+")")
	}
	f, label, err := t.reportFilter(ctx, a.Category, nil)
	if err != nil {
		return fail(err.Error())
	}
	sa, err := t.svc.Summary(ctx, t.ws.ID, pa, f)
	if err != nil {
		return fail("não foi possível consultar")
	}
	sb, err := t.svc.Summary(ctx, t.ws.ID, pb, f)
	if err != nil {
		return fail("não foi possível consultar")
	}
	var b strings.Builder
	title := pa.Label + " × " + pb.Label
	if label != "" {
		title = label + " · " + title
	}
	fmt.Fprintf(&b, "📊 *%s*\n\nTotal: %s × %s", title, FormatBRLShort(sa.ExpensesCents), FormatBRLShort(sb.ExpensesCents))
	if c := changePct(sa.ExpensesCents, sb.ExpensesCents); c != nil {
		fmt.Fprintf(&b, " (%s)", arrow(c))
	}
	b.WriteString("\n")

	prev := map[int64]int64{}
	names := map[int64]string{}
	for _, c := range sb.Categories {
		prev[c.ID] = c.AmountCents
		names[c.ID] = c.Name
	}
	type delta struct {
		name     string
		cur, old int64
	}
	var deltas []delta
	seen := map[int64]bool{}
	for _, c := range sa.Categories {
		seen[c.ID] = true
		deltas = append(deltas, delta{c.Name, c.AmountCents, prev[c.ID]})
	}
	for id, v := range prev {
		if !seen[id] {
			deltas = append(deltas, delta{names[id], 0, v})
		}
	}
	sortByAbsDelta(deltas, func(d delta) int64 { return d.cur - d.old })
	if len(deltas) > 0 && label == "" {
		b.WriteString("Maiores variações:\n")
		for i, d := range deltas {
			if i == 4 || d.cur == d.old {
				break
			}
			line := fmt.Sprintf("• %s: %s × %s", d.name, FormatBRLShort(d.cur), FormatBRLShort(d.old))
			if c := changePct(d.cur, d.old); c != nil {
				line += " (" + arrow(c) + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return reportResult{OK: true, Report: strings.TrimSpace(b.String()), Details: map[string]any{
		"a": map[string]any{"periodo": pa.Label, "gastos_centavos": sa.ExpensesCents},
		"b": map[string]any{"periodo": pb.Label, "gastos_centavos": sb.ExpensesCents},
	}}.String()
}

func sortByAbsDelta[T any](list []T, delta func(T) int64) {
	abs := func(v int64) int64 {
		if v < 0 {
			return -v
		}
		return v
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && abs(delta(list[j])) > abs(delta(list[j-1])); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func (t *turn) budgetStatus(ctx context.Context, args string) string {
	var a struct {
		Category *string `json:"category"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	var catID *int64
	if a.Category != nil && strings.TrimSpace(*a.Category) != "" {
		c, options := resolveCategory(t.cats, *a.Category, KindExpense)
		if c == nil {
			return fail("categoria desconhecida; opções: " + strings.Join(options, ", "))
		}
		catID = &c.ID
	}
	budgets, err := t.svc.Budgets(ctx, t.ws.ID, catID)
	if err != nil {
		return fail("não foi possível consultar")
	}
	month, _ := ResolvePeriod("this_month", "", "", "", t.svc.now())
	if len(budgets) == 0 {
		return reportResult{OK: true, Report: "🎯 Nenhum orçamento definido" + map[bool]string{true: " para essa categoria", false: ""}[catID != nil] +
			". Ex.: “limite de 600 para restaurantes”."}.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🎯 *Orçamentos · %s*\n", month.Label)
	for _, bs := range budgets {
		line := fmt.Sprintf("• %s: %s de %s (%s)", bs.Name, FormatBRLShort(bs.SpentCents), FormatBRLShort(bs.BudgetCents), pct(bs.Pct))
		if bs.RemainingCents > 0 {
			line += " · restam " + FormatBRLShort(bs.RemainingCents)
		} else {
			line += " · " + FormatBRLShort(-bs.RemainingCents) + " acima"
		}
		if bs.ProjectionCents > bs.BudgetCents && bs.RemainingCents > 0 {
			line += " · projeção ~" + FormatBRLShort(roundTo(bs.ProjectionCents, 1000))
		}
		b.WriteString(line + "\n")
	}
	return reportResult{OK: true, Report: strings.TrimSpace(b.String()), Details: budgets}.String()
}

func (t *turn) insights(ctx context.Context, args string) string {
	var a struct {
		Period string  `json:"period"`
		Month  *string `json:"month"`
		Start  *string `json:"start"`
		End    *string `json:"end"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	p, err := t.resolveToolPeriod(a.Period, a.Month, a.Start, a.End)
	if err != nil {
		return fail(err.Error())
	}
	list, err := t.svc.Insights(ctx, t.ws.ID, p)
	if err != nil {
		return fail("não foi possível consultar")
	}
	if len(list) == 0 {
		return reportResult{OK: true, Report: "💡 Nada fora do comum em " + strings.ToLower(p.Label) + " até agora."}.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "💡 *Destaques · %s*\n", p.Label)
	for i, in := range list {
		if i == 6 {
			break
		}
		b.WriteString("• " + in.Text + "\n")
	}
	return reportResult{OK: true, Report: strings.TrimSpace(b.String()), Details: list}.String()
}
