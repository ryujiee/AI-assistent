package finance

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"
)

// Scheduled summaries sent to the group: monthly (last day of the month or
// day 1 of the next one, off by default) and weekly (off by default). Each is
// sent once (finance_alerts_sent) and never for an empty period.

// MonthlySummaryText renders the closing of a month.
func (s *Service) MonthlySummaryText(ctx context.Context, wsID int64, month Period) (string, bool, error) {
	sum, err := s.Summary(ctx, wsID, month, ReportFilter{})
	if err != nil {
		return "", false, err
	}
	if sum.Transactions == 0 {
		return "", false, nil
	}
	name := strings.ToLower(monthNames[month.startTime().Month()-1])
	var b strings.Builder
	fmt.Fprintf(&b, "📊 *Fechamento de %s*\n\n", name)
	fmt.Fprintf(&b, "Receitas: %s\nDespesas: %s\nSaldo: %s\n", FormatBRLShort(sum.IncomeCents), FormatBRLShort(sum.ExpensesCents), FormatBRLShort(sum.BalanceCents))
	if len(sum.Categories) > 0 {
		b.WriteString("\nMaiores categorias:\n")
		for i, c := range sum.Categories {
			if i == 3 || c.AmountCents <= 0 {
				break
			}
			fmt.Fprintf(&b, "• %s %s — %s\n", c.Icon, c.Name, FormatBRLShort(c.AmountCents))
		}
	}
	prevName := strings.ToLower(monthNames[sum.Previous.startTime().Month()-1])
	if a := arrow(sum.ExpenseChangePct); a != "" {
		fmt.Fprintf(&b, "\nComparação com %s: %s nas despesas (%s × %s).\n", prevName, a, FormatBRLShort(sum.ExpensesCents), FormatBRLShort(sum.PrevExpensesCents))
	}
	if line := s.budgetClosingLine(ctx, wsID, month); line != "" {
		b.WriteString(line + "\n")
	}
	if sum.Pending > 0 {
		fmt.Fprintf(&b, "_%d registro(s) pendente(s) não entraram na conta._\n", sum.Pending)
	}
	return strings.TrimSpace(b.String()), true, nil
}

func (s *Service) budgetClosingLine(ctx context.Context, wsID int64, month Period) string {
	cats, err := s.ListCategories(ctx, wsID, false)
	if err != nil {
		return ""
	}
	leaf, err := s.CategorySpending(ctx, wsID, month)
	if err != nil {
		return ""
	}
	spent := RollUp(cats, leaf)
	total, within := 0, 0
	var over []string
	for _, c := range cats {
		if c.MonthlyBudgetCents == nil {
			continue
		}
		total++
		if spent[c.ID] <= *c.MonthlyBudgetCents {
			within++
		} else if len(over) < 2 {
			over = append(over, fmt.Sprintf("%s (%s de %s)", c.Name, FormatBRLShort(spent[c.ID]), FormatBRLShort(*c.MonthlyBudgetCents)))
		}
	}
	if total == 0 {
		return ""
	}
	line := fmt.Sprintf("Orçamentos: %d de %d dentro do limite", within, total)
	if len(over) > 0 {
		line += "; acima: " + strings.Join(over, ", ")
	}
	return line + "."
}

// WeeklySummaryText renders a Monday-to-Sunday week.
func (s *Service) WeeklySummaryText(ctx context.Context, wsID int64, week Period) (string, bool, error) {
	sum, err := s.Summary(ctx, wsID, week, ReportFilter{})
	if err != nil {
		return "", false, err
	}
	if sum.Transactions == 0 {
		return "", false, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🗓️ *Resumo da semana (%s)*\n\nGastos: %s", rangeLabel(week.startTime(), week.endTime()), FormatBRLShort(sum.ExpensesCents))
	if a := arrow(sum.ExpenseChangePct); a != "" {
		fmt.Fprintf(&b, " (%s vs semana anterior)", a)
	}
	b.WriteString("\n")

	// Above the recent average? Four previous weeks as the baseline.
	start := week.startTime()
	four, _ := ResolvePeriod("custom", "", start.AddDate(0, 0, -28).Format(DateLayout), start.AddDate(0, 0, -1).Format(DateLayout), s.now())
	if base, err := s.Summary(ctx, wsID, four, ReportFilter{}); err == nil && base.ExpensesCents > 0 {
		avg := base.ExpensesCents / 4
		if c := changePct(sum.ExpensesCents, avg); c != nil && *c >= 20 {
			fmt.Fprintf(&b, "Acima da média das últimas 4 semanas (%s).\n", FormatBRLShort(avg))
		}
	}
	for i, c := range sum.Categories {
		if i == 3 || c.AmountCents <= 0 {
			break
		}
		if i == 0 {
			b.WriteString("Maiores: ")
		} else {
			b.WriteString(" · ")
		}
		fmt.Fprintf(&b, "%s %s", c.Name, FormatBRLShort(c.AmountCents))
	}
	b.WriteString("\n")
	if budgets, err := s.Budgets(ctx, wsID, nil); err == nil {
		var warn []string
		for _, bs := range budgets {
			if bs.State != "ok" && len(warn) < 2 {
				warn = append(warn, fmt.Sprintf("%s %s", bs.Name, pct(bs.Pct)))
			}
		}
		if len(warn) > 0 {
			fmt.Fprintf(&b, "Orçamentos em atenção: %s.\n", strings.Join(warn, ", "))
		}
	}
	return strings.TrimSpace(b.String()), true, nil
}

// Summaries sends the scheduled messages through the WhatsApp gateway.
type Summaries struct {
	Svc   *Service
	GW    Gateway
	Ready func() bool
	// Jitter delays each send by a random amount, like the Secretária's
	// morning summary: identical send times every day look automated.
	Jitter time.Duration
	sleep  func(time.Duration)
}

func (m *Summaries) wait() {
	if m.Jitter <= 0 {
		return
	}
	d := time.Duration(rand.Int63n(int64(m.Jitter)))
	if m.sleep != nil {
		m.sleep(d)
		return
	}
	time.Sleep(d)
}

func (m *Summaries) linkedWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := m.Svc.DB.Query(ctx, "SELECT "+workspaceCols+" FROM finance_workspaces WHERE group_jid IS NOT NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// RunMonthly is called daily for one of the two settings: "last_day" sends
// the closing of the running month on its last day (evening run),
// "first_day" the previous month on day 1 (morning run).
func (m *Summaries) RunMonthly(ctx context.Context, setting string) {
	if m.Ready != nil && !m.Ready() {
		return
	}
	today := Today(m.Svc.now())
	lastDay := today.AddDate(0, 0, 1).Day() == 1
	wss, err := m.linkedWorkspaces(ctx)
	if err != nil {
		slog.Error("finance.summary_failed", "error", err)
		return
	}
	for _, w := range wss {
		var month Period
		if w.MonthlySummary != setting {
			continue
		}
		switch {
		case setting == "last_day" && lastDay:
			month, _ = ResolvePeriod("month", today.Format("2006-01"), "", "", m.Svc.now())
		case setting == "first_day" && today.Day() == 1:
			month, _ = ResolvePeriod("last_month", "", "", "", m.Svc.now())
		default:
			continue
		}
		m.send(ctx, &w, "summary:monthly:"+month.Start[:7], func() (string, bool, error) {
			return m.Svc.MonthlySummaryText(ctx, w.ID, month)
		})
	}
}

// RunWeekly is called on Mondays and summarizes the previous week.
func (m *Summaries) RunWeekly(ctx context.Context) {
	if m.Ready != nil && !m.Ready() {
		return
	}
	week, _ := ResolvePeriod("last_week", "", "", "", m.Svc.now())
	wss, err := m.linkedWorkspaces(ctx)
	if err != nil {
		slog.Error("finance.summary_failed", "error", err)
		return
	}
	for _, w := range wss {
		if !w.WeeklySummary {
			continue
		}
		m.send(ctx, &w, "summary:weekly:"+week.Start, func() (string, bool, error) {
			return m.Svc.WeeklySummaryText(ctx, w.ID, week)
		})
	}
}

func (m *Summaries) send(ctx context.Context, w *Workspace, key string, render func() (string, bool, error)) {
	if m.Svc.wasSent(ctx, w.ID, key) || !m.GW.Connected() {
		return
	}
	text, ok, err := render()
	if err != nil || !ok {
		if err != nil {
			slog.Error("finance.summary_failed", "workspace", w.ID, "error", err)
		}
		return
	}
	m.wait()
	// Claim the key first: two overlapping runs cannot both send.
	if first, err := m.Svc.markSent(ctx, w.ID, key); err != nil || !first {
		return
	}
	if _, err := m.GW.SendText(ctx, *w.GroupJID, text, nil); err != nil {
		slog.Error("finance.summary_send_failed", "workspace", w.ID, "error", err)
		m.Svc.DB.Exec(ctx, "DELETE FROM finance_alerts_sent WHERE workspace_id = $1 AND alert_key = $2", w.ID, key)
		return
	}
	slog.Info("finance.summary", "action", "finance.summary.sent", "workspace", w.ID, "kind", strings.Split(key, ":")[1])
}
