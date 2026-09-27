package finance

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Alerts are short, neutral lines appended to the bot's reply after a change
// in the group. Every alert has a key in finance_alerts_sent, so the same
// fact is announced once (per month, per day or per transaction), and at
// most maxAlertLines go out with a reply: useful, never spam.

const maxAlertLines = 2

var budgetThresholds = []int{100, 80, 70}

type alert struct {
	key      string
	priority int // lower is more important
	text     string
	// alsoMark are lower-level keys consumed together (crossing 100% at once
	// also settles 70% and 80%).
	alsoMark []string
}

// markSent records an alert key; false means it was already sent.
func (s *Service) markSent(ctx context.Context, wsID int64, key string) (bool, error) {
	tag, err := s.DB.Exec(ctx, `INSERT INTO finance_alerts_sent (workspace_id, alert_key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, wsID, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Service) wasSent(ctx context.Context, wsID int64, key string) bool {
	var ok bool
	s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM finance_alerts_sent WHERE workspace_id = $1 AND alert_key = $2)`, wsID, key).Scan(&ok)
	return ok
}

// AlertsAfterChange evaluates the alerts triggered by the given transactions
// and returns the lines to append to the reply.
func (s *Service) AlertsAfterChange(ctx context.Context, ws *Workspace, touched []int64) []string {
	if len(touched) == 0 {
		return nil
	}
	items, _, err := s.ListTransactions(ctx, ws.ID, TxFilter{OnlyIDs: touched, Limit: len(touched)})
	if err != nil {
		slog.Error("finance.alerts_failed", "workspace", ws.ID, "error", err)
		return nil
	}
	month, _ := ResolvePeriod("this_month", "", "", "", s.now())
	var candidates []alert
	seen := map[string]bool{}
	add := func(a []alert) {
		for _, x := range a {
			if !seen[x.key] {
				seen[x.key] = true
				candidates = append(candidates, x)
			}
		}
	}
	for _, t := range items {
		if t.Status != StatusConfirmed || t.Type != TypeExpense || t.CategoryID == nil {
			continue
		}
		inMonth := t.TransactionDate >= month.Start && t.TransactionDate <= month.End
		if inMonth {
			add(s.budgetAlerts(ctx, ws.ID, t, month))
			add(s.spikeAlerts(ctx, ws.ID, t, month))
		}
		add(s.outlierAlert(ctx, ws.ID, t))
		add(s.sequenceAlert(ctx, ws.ID, t))
		add(s.subscriptionAlert(ctx, ws.ID, t))
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority < candidates[j].priority })

	var lines []string
	for _, c := range candidates {
		if len(lines) == maxAlertLines {
			break
		}
		ok, err := s.markSent(ctx, ws.ID, c.key)
		if err != nil || !ok {
			continue
		}
		for _, k := range c.alsoMark {
			s.markSent(ctx, ws.ID, k)
		}
		lines = append(lines, c.text)
		slog.Info("finance.alert", "action", "finance.alert.sent", "workspace", ws.ID, "kind", strings.SplitN(c.key, ":", 2)[0])
	}
	return lines
}

// budgetAlerts checks the transaction's category and its parent. Only the
// highest threshold reached is announced.
func (s *Service) budgetAlerts(ctx context.Context, wsID int64, t TransactionView, month Period) []alert {
	cats, err := s.ListCategories(ctx, wsID, false)
	if err != nil {
		return nil
	}
	byCat := CategoryIndex(cats)
	ids := []int64{*t.CategoryID}
	if t.ParentCategoryID != nil {
		ids = append(ids, *t.ParentCategoryID)
	}
	var spent map[int64]int64
	var out []alert
	for _, id := range ids {
		c := byCat[id]
		if c == nil || c.MonthlyBudgetCents == nil {
			continue
		}
		if spent == nil {
			leaf, err := s.CategorySpending(ctx, wsID, month)
			if err != nil {
				return nil
			}
			spent = RollUp(cats, leaf)
		}
		budget, used := *c.MonthlyBudgetCents, spent[id]
		pctUsed := float64(used) / float64(budget) * 100
		ym := month.Start[:7]
		for i, th := range budgetThresholds {
			if pctUsed < float64(th) {
				continue
			}
			key := fmt.Sprintf("budget:%d:%s:%d", id, ym, th)
			if s.wasSent(ctx, wsID, key) {
				break // this or a higher level was already announced
			}
			var lower []string
			for _, l := range budgetThresholds[i+1:] {
				lower = append(lower, fmt.Sprintf("budget:%d:%s:%d", id, ym, l))
			}
			name := strings.ToLower(c.Name)
			var text string
			switch th {
			case 100:
				text = fmt.Sprintf("⚠️ %s passou do orçamento do mês: %s de %s.", c.Name, FormatBRLShort(used), FormatBRLShort(budget))
			case 80:
				text = fmt.Sprintf("⚠️ Vocês gastaram %s dos %s planejados com %s este mês. Restam %s.", FormatBRLShort(used), FormatBRLShort(budget), name, FormatBRLShort(budget-used))
			default:
				text = fmt.Sprintf("🎯 %s chegou a %s do orçamento (%s de %s).", c.Name, pct(float64(int(pctUsed))), FormatBRLShort(used), FormatBRLShort(budget))
			}
			out = append(out, alert{key: key, priority: 100 - th, text: text, alsoMark: lower})
			break
		}
	}
	return out
}

// spikeAlerts: a discretionary category well above the same days of the
// previous month (>= 30% and >= R$ 100 more, with a real baseline).
func (s *Service) spikeAlerts(ctx context.Context, wsID int64, t TransactionView, month Period) []alert {
	cat, err := s.GetCategory(ctx, wsID, *t.CategoryID)
	if err != nil || cat.Essentiality != Discretionary {
		return nil
	}
	sum, err := s.Summary(ctx, wsID, month, ReportFilter{CategoryID: &cat.ID})
	if err != nil || sum.PrevExpensesCents < 10000 {
		return nil
	}
	diff := sum.ExpensesCents - sum.PrevExpensesCents
	change := changePct(sum.ExpensesCents, sum.PrevExpensesCents)
	if change == nil || *change < 30 || diff < 10000 {
		return nil
	}
	return []alert{{key: fmt.Sprintf("spike:%d:%s", cat.ID, month.Start[:7]), priority: 40,
		text: fmt.Sprintf("📈 %s está %s acima do mesmo período do mês passado (%s × %s).", cat.Name, pct(roundPct(*change)), FormatBRLShort(sum.ExpensesCents), FormatBRLShort(sum.PrevExpensesCents))}}
}

func (s *Service) outlierAlert(ctx context.Context, wsID int64, t TransactionView) []alert {
	var mean, sd float64
	var n int
	err := s.DB.QueryRow(ctx, `
		SELECT COALESCE(avg(amount_cents), 0)::float8, COALESCE(stddev_samp(amount_cents), 0)::float8, count(*)
		FROM finance_transactions
		WHERE workspace_id = $1 AND category_id = $2 AND id <> $3 AND status = 'CONFIRMED' AND deleted_at IS NULL
		  AND type = 'EXPENSE' AND transaction_date BETWEEN $4::date - 120 AND $4::date`, wsID, *t.CategoryID, t.ID, t.TransactionDate).Scan(&mean, &sd, &n)
	if err != nil || n < outlierMinSamples {
		return nil
	}
	amount := float64(t.AmountCents)
	if amount <= mean+3*sd || amount <= 2*mean {
		return nil
	}
	return []alert{{key: fmt.Sprintf("outlier:%d", t.ID), priority: 30,
		text: fmt.Sprintf("🔎 Esse valor está bem acima do habitual em %s (costuma ficar perto de %s).", t.CategoryName, FormatBRLShort(roundTo(int64(mean), 100)))}}
}

// sequenceAlert: the third or more entry of the same discretionary category
// dated since yesterday; once per category per day.
func (s *Service) sequenceAlert(ctx context.Context, wsID int64, t TransactionView) []alert {
	since := Today(s.now()).AddDate(0, 0, -1).Format(DateLayout)
	if t.TransactionDate < since {
		return nil
	}
	cat, err := s.GetCategory(ctx, wsID, *t.CategoryID)
	if err != nil || cat.Essentiality != Discretionary {
		return nil
	}
	var n int
	var total int64
	err = s.DB.QueryRow(ctx, `SELECT count(*), COALESCE(sum(amount_cents), 0)::bigint FROM finance_transactions
		WHERE workspace_id = $1 AND category_id = $2 AND status = 'CONFIRMED' AND deleted_at IS NULL AND type = 'EXPENSE'
		  AND transaction_date >= $3::date`, wsID, cat.ID, since).Scan(&n, &total)
	if err != nil || n < 3 {
		return nil
	}
	return []alert{{key: fmt.Sprintf("sequence:%d:%s", cat.ID, Today(s.now()).Format(DateLayout)), priority: 50,
		text: fmt.Sprintf("🔁 %dº registro de %s desde ontem (%s no total).", n, cat.Name, FormatBRLShort(total))}}
}

// subscriptionAlert: a subscription charged more than the same one last month.
func (s *Service) subscriptionAlert(ctx context.Context, wsID int64, t TransactionView) []alert {
	if normalize(t.CategoryName) != "assinaturas" && normalize(t.ParentCategoryName) != "assinaturas" {
		return nil
	}
	name := t.Description
	if t.Merchant != nil && *t.Merchant != "" {
		name = *t.Merchant
	}
	if len(normalize(name)) < 3 {
		return nil
	}
	d, err := time.Parse(DateLayout, t.TransactionDate)
	if err != nil {
		return nil
	}
	rows, err := s.DB.Query(ctx, `SELECT amount_cents, COALESCE(merchant, description) FROM finance_transactions
		WHERE workspace_id = $1 AND category_id = $2 AND id <> $3 AND status = 'CONFIRMED' AND deleted_at IS NULL
		  AND transaction_date BETWEEN $4::date AND $5::date ORDER BY transaction_date DESC`,
		wsID, *t.CategoryID, t.ID, d.AddDate(0, -1, -10).Format(DateLayout), d.AddDate(0, 0, -20).Format(DateLayout))
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var prev int64
		var prevName string
		if rows.Scan(&prev, &prevName) != nil || !similarNames(prevName, name) {
			continue
		}
		if t.AmountCents > prev {
			return []alert{{key: fmt.Sprintf("subscription:%d:%s:%s", *t.CategoryID, normalize(name), t.TransactionDate[:7]), priority: 45,
				text: fmt.Sprintf("💡 %s subiu de %s para %s em relação ao mês passado.", name, FormatBRL(prev), FormatBRL(t.AmountCents))}}
		}
		return nil
	}
	return nil
}
