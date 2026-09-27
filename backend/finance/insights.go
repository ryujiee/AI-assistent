package finance

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Insights are facts computed from the ledger and phrased neutrally: numbers,
// comparisons and projections, never judgments. Essential categories (rent,
// health) are not presented as something to cut.

type Insight struct {
	Kind       string `json:"kind"`     // change, budget, projection, largest, outlier, share
	Severity   string `json:"severity"` // info or attention
	Text       string `json:"text"`
	CategoryID *int64 `json:"category_id,omitempty"`
}

const (
	insightMinChangePct   = 25.0
	insightMinChangeCents = 5000 // R$ 50
	outlierMinSamples     = 8
)

func (s *Service) Insights(ctx context.Context, wsID int64, p Period) ([]Insight, error) {
	sum, err := s.Summary(ctx, wsID, p, ReportFilter{})
	if err != nil {
		return nil, err
	}
	out := []Insight{}
	isMonth := p.startTime().Day() == 1
	baseline := "do período anterior"
	if isMonth {
		baseline = "do mesmo período do mês passado"
		if !p.IsCurrentMonth(s.now()) {
			baseline = "do mês anterior"
		}
	}

	// Budgets first: they are the user's own targets.
	if p.IsCurrentMonth(s.now()) {
		budgets, err := s.Budgets(ctx, wsID, nil)
		if err != nil {
			return nil, err
		}
		for _, b := range budgets {
			id := b.CategoryID
			switch {
			case b.Pct >= 100:
				out = append(out, Insight{Kind: "budget", Severity: "attention", CategoryID: &id,
					Text: fmt.Sprintf("%s passou do orçamento: %s de %s (%s).", b.Name, FormatBRLShort(b.SpentCents), FormatBRLShort(b.BudgetCents), pct(b.Pct))})
			case b.Pct >= 70:
				out = append(out, Insight{Kind: "budget", Severity: "attention", CategoryID: &id,
					Text: fmt.Sprintf("%s: %s de %s (%s). Restam %s.", b.Name, FormatBRLShort(b.SpentCents), FormatBRLShort(b.BudgetCents), pct(b.Pct), FormatBRLShort(b.RemainingCents))})
			case b.ProjectionCents > b.BudgetCents && b.SpentCents > 0:
				out = append(out, Insight{Kind: "budget", Severity: "info", CategoryID: &id,
					Text: fmt.Sprintf("No ritmo atual, %s fecharia o mês em cerca de %s, acima do orçamento de %s (projeção).", b.Name, FormatBRLShort(b.ProjectionCents), FormatBRLShort(b.BudgetCents))})
			}
		}
	}

	// Category changes, discretionary categories first.
	var changes []Insight
	for _, c := range sum.Categories {
		if c.ChangePct == nil || *c.ChangePct < insightMinChangePct || c.AmountCents-c.PreviousCents < insightMinChangeCents {
			continue
		}
		id := c.ID
		sev := "info"
		if c.Essentiality == Discretionary {
			sev = "attention"
		}
		changes = append(changes, Insight{Kind: "change", Severity: sev, CategoryID: &id,
			Text: fmt.Sprintf("%s está %s acima %s (+%s).", c.Name, pct(*c.ChangePct), baseline, FormatBRLShort(c.AmountCents-c.PreviousCents))})
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Severity == "attention" && changes[j].Severity != "attention" })
	if len(changes) > 3 {
		changes = changes[:3]
	}
	out = append(out, changes...)

	if sum.ProjectionCents != nil && sum.ExpensesCents > 0 {
		text := fmt.Sprintf("Se mantiverem o ritmo atual, a projeção do mês é de cerca de %s (projeção, não certeza).", FormatBRLShort(roundTo(*sum.ProjectionCents, 1000)))
		out = append(out, Insight{Kind: "projection", Severity: "info", Text: text})
	}

	largest, _, err := s.ListTransactions(ctx, wsID, TxFilter{Start: p.Start, End: p.End, Type: TypeExpense, Status: StatusConfirmed, OrderBy: "amount", Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(largest) == 1 {
		l := largest[0]
		out = append(out, Insight{Kind: "largest", Severity: "info", CategoryID: l.CategoryID,
			Text: fmt.Sprintf("Maior gasto: %s · %s (%s).", FormatBRLShort(l.AmountCents), viewCategory(l), formatShortCivil(l.TransactionDate))})
	}

	outliers, err := s.Outliers(ctx, wsID, p)
	if err != nil {
		return nil, err
	}
	out = append(out, outliers...)

	var discretionary int64
	for _, c := range sum.Categories {
		if c.Essentiality == Discretionary {
			discretionary += c.AmountCents
		}
	}
	if sum.ExpensesCents > 0 && discretionary > 0 {
		share := float64(discretionary) / float64(sum.ExpensesCents) * 100
		out = append(out, Insight{Kind: "share", Severity: "info",
			Text: fmt.Sprintf("Gastos discricionários (lazer, delivery, restaurantes...) somam %s do total.", pct(math.Round(share)))})
	}
	return out, nil
}

// Outliers flags expenses far above the category's usual amount, only when
// the category has enough history to say what "usual" is.
func (s *Service) Outliers(ctx context.Context, wsID int64, p Period) ([]Insight, error) {
	rows, err := s.DB.Query(ctx, `
		WITH hist AS (
			SELECT category_id, avg(amount_cents) AS mean, stddev_samp(amount_cents) AS sd, count(*) AS n
			FROM finance_transactions
			WHERE workspace_id = $1 AND status = 'CONFIRMED' AND deleted_at IS NULL AND type = 'EXPENSE'
			  AND category_id IS NOT NULL AND transaction_date BETWEEN $2::date - 120 AND $2::date - 1
			GROUP BY category_id
		)
		SELECT t.id, t.amount_cents, t.transaction_date::text, c.name, h.mean::bigint
		FROM finance_transactions t
		JOIN hist h ON h.category_id = t.category_id
		JOIN finance_categories c ON c.id = t.category_id
		WHERE t.workspace_id = $1 AND t.status = 'CONFIRMED' AND t.deleted_at IS NULL AND t.type = 'EXPENSE'
		  AND t.transaction_date BETWEEN $2::date AND $3::date
		  AND h.n >= $4 AND t.amount_cents > h.mean + 3 * COALESCE(h.sd, 0) AND t.amount_cents > 2 * h.mean
		ORDER BY t.amount_cents DESC LIMIT 2`, wsID, p.Start, p.End, outlierMinSamples)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Insight
	for rows.Next() {
		var id, cents, mean int64
		var date, name string
		if err := rows.Scan(&id, &cents, &date, &name, &mean); err != nil {
			return nil, err
		}
		out = append(out, Insight{Kind: "outlier", Severity: "info",
			Text: fmt.Sprintf("Gasto fora do padrão: %s em %s (%s); o habitual é perto de %s.", FormatBRLShort(cents), name, formatShortCivil(date), FormatBRLShort(roundTo(mean, 100)))})
	}
	return out, rows.Err()
}

func pct(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f%%", v)
	}
	return strings.Replace(fmt.Sprintf("%.1f%%", v), ".", ",", 1)
}

func roundTo(cents, step int64) int64 {
	if step <= 0 {
		return cents
	}
	return (cents + step/2) / step * step
}

func formatShortCivil(date string) string {
	if len(date) == 10 {
		return date[8:10] + "/" + date[5:7]
	}
	return date
}

func viewCategory(v TransactionView) string {
	switch {
	case v.Type == TypeTransfer:
		return "transferência"
	case v.CategoryName == "":
		return "sem categoria"
	}
	return v.CategoryName
}
