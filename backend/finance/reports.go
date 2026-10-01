package finance

import (
	"context"
	"math"
	"sort"
	"time"
)

// Reports are always computed from the ledger at query time: a correction or
// a deletion is reflected immediately and no total is ever stored.
//
// Rules: spending = EXPENSE - REFUND; TRANSFER never counts as spending or
// income; PENDING and deleted rows are excluded.

// ReportFilter narrows a report. CategoryID includes subcategories.
type ReportFilter struct {
	CategoryID *int64
	MemberID   *int64
}

type CategoryTotal struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Icon            string   `json:"icon"`
	Essentiality    string   `json:"essentiality"`
	AmountCents     int64    `json:"amount_cents"`
	PreviousCents   int64    `json:"previous_cents"`
	SharePct        float64  `json:"share_pct"`
	ChangePct       *float64 `json:"change_pct"`
	BudgetCents     *int64   `json:"budget_cents"`
	TransactionsCnt int      `json:"transactions"`
}

type MemberTotal struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	ExpensesCents int64  `json:"expenses_cents"`
	IncomeCents   int64  `json:"income_cents"`
}

// minProjectionDays: extrapolating fewer days of a month is noise (one big
// purchase on the 1st would "project" a month thirty times larger).
const minProjectionDays = 7

type Summary struct {
	Period            Period          `json:"period"`
	Previous          Period          `json:"previous"`
	ExpensesCents     int64           `json:"expenses_cents"`
	IncomeCents       int64           `json:"income_cents"`
	BalanceCents      int64           `json:"balance_cents"`
	TransfersCents    int64           `json:"transfers_cents"`
	PrevExpensesCents int64           `json:"previous_expenses_cents"`
	PrevIncomeCents   int64           `json:"previous_income_cents"`
	ExpenseChangePct  *float64        `json:"expense_change_pct"`
	IncomeChangePct   *float64        `json:"income_change_pct"`
	DailyAverageCents int64           `json:"daily_average_cents"`
	ProjectionCents   *int64          `json:"projection_cents"`
	Transactions      int             `json:"transactions"`
	Pending           int             `json:"pending"`
	Categories        []CategoryTotal `json:"categories"`
	IncomeCategories  []CategoryTotal `json:"income_categories"`
	// ByEssentiality splits net spending by the essentiality of the exact
	// category used (Delivery is discretionary even under Alimentação).
	ByEssentiality map[string]int64 `json:"by_essentiality"`
	Members        []MemberTotal    `json:"members"`
}

type aggRow struct {
	typ      string
	leaf     *int64
	parent   *int64
	payer    *int64
	cents    int64
	count    int
	previous bool
}

func (s *Service) aggregate(ctx context.Context, wsID int64, cur, prev Period, f ReportFilter) ([]aggRow, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT t.transaction_date >= $2::date AS current, t.type, t.category_id, c.parent_id, t.payer_member_id,
			sum(t.amount_cents)::bigint, count(*)
		FROM finance_transactions t
		LEFT JOIN finance_categories c ON c.id = t.category_id
		WHERE t.workspace_id = $1 AND t.status = 'CONFIRMED' AND t.deleted_at IS NULL
		  AND ((t.transaction_date BETWEEN $2::date AND $3::date) OR (t.transaction_date BETWEEN $4::date AND $5::date))
		  AND ($6::bigint IS NULL OR t.category_id = $6 OR c.parent_id = $6)
		  AND ($7::bigint IS NULL OR t.payer_member_id = $7)
		GROUP BY 1, 2, 3, 4, 5`, wsID, cur.Start, cur.End, prev.Start, prev.End, f.CategoryID, f.MemberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggRow
	for rows.Next() {
		var r aggRow
		var current bool
		if err := rows.Scan(&current, &r.typ, &r.leaf, &r.parent, &r.payer, &r.cents, &r.count); err != nil {
			return nil, err
		}
		// A row can only be in one window when the periods do not overlap;
		// they never do (the previous period ends before the current starts).
		r.previous = !current
		out = append(out, r)
	}
	return out, rows.Err()
}

func changePct(cur, prev int64) *float64 {
	if prev <= 0 {
		return nil
	}
	v := math.Round(float64(cur-prev)/float64(prev)*1000) / 10
	return &v
}

// signedSpending turns EXPENSE into +amount and REFUND into -amount.
func signedSpending(typ string, cents int64) (int64, bool) {
	switch typ {
	case TypeExpense:
		return cents, true
	case TypeRefund:
		return -cents, true
	}
	return 0, false
}

// Summary computes the totals of a period against its comparison baseline.
func (s *Service) Summary(ctx context.Context, wsID int64, p Period, f ReportFilter) (*Summary, error) {
	prev := PreviousPeriod(p, s.now())
	rows, err := s.aggregate(ctx, wsID, p, prev, f)
	if err != nil {
		return nil, err
	}
	cats, err := s.ListCategories(ctx, wsID, true)
	if err != nil {
		return nil, err
	}
	byCat := CategoryIndex(cats)
	members, err := s.ListMembers(ctx, wsID)
	if err != nil {
		return nil, err
	}

	sum := &Summary{Period: p, Previous: prev, Categories: []CategoryTotal{}, IncomeCategories: []CategoryTotal{}, Members: []MemberTotal{},
		ByEssentiality: map[string]int64{Essential: 0, Important: 0, Discretionary: 0}}
	catTotals := map[int64]*CategoryTotal{}
	incomeTotals := map[int64]*CategoryTotal{}
	memberTotals := map[int64]*MemberTotal{}
	for _, m := range members {
		memberTotals[m.ID] = &MemberTotal{ID: m.ID, Name: m.DisplayName}
	}
	// With a category filter the breakdown goes one level down (its
	// subcategories); otherwise it rolls up to the top-level categories.
	bucket := func(r aggRow) *int64 {
		if r.leaf == nil {
			return nil
		}
		if f.CategoryID == nil && r.parent != nil {
			return r.parent
		}
		return r.leaf
	}

	for _, r := range rows {
		if r.previous {
			switch r.typ {
			case TypeIncome:
				sum.PrevIncomeCents += r.cents
				if id := bucket(r); id != nil {
					catTotal(incomeTotals, byCat, *id).PreviousCents += r.cents
				}
			default:
				if v, ok := signedSpending(r.typ, r.cents); ok {
					sum.PrevExpensesCents += v
					if id := bucket(r); id != nil {
						ct := catTotal(catTotals, byCat, *id)
						ct.PreviousCents += v
					}
				}
			}
			continue
		}
		sum.Transactions += r.count
		switch r.typ {
		case TypeIncome:
			sum.IncomeCents += r.cents
			if id := bucket(r); id != nil {
				ct := catTotal(incomeTotals, byCat, *id)
				ct.AmountCents += r.cents
				ct.TransactionsCnt += r.count
			}
			if r.payer != nil && memberTotals[*r.payer] != nil {
				memberTotals[*r.payer].IncomeCents += r.cents
			}
		case TypeTransfer:
			sum.TransfersCents += r.cents
		default:
			v, _ := signedSpending(r.typ, r.cents)
			sum.ExpensesCents += v
			if id := bucket(r); id != nil {
				ct := catTotal(catTotals, byCat, *id)
				ct.AmountCents += v
				ct.TransactionsCnt += r.count
			}
			if r.leaf != nil {
				if c, ok := byCat[*r.leaf]; ok {
					sum.ByEssentiality[c.Essentiality] += v
				}
			}
			if r.payer != nil && memberTotals[*r.payer] != nil {
				memberTotals[*r.payer].ExpensesCents += v
			}
		}
	}

	sum.BalanceCents = sum.IncomeCents - sum.ExpensesCents
	sum.ExpenseChangePct = changePct(sum.ExpensesCents, sum.PrevExpensesCents)
	sum.IncomeChangePct = changePct(sum.IncomeCents, sum.PrevIncomeCents)
	elapsed := p.ElapsedDays(s.now())
	sum.DailyAverageCents = sum.ExpensesCents / int64(elapsed)
	if p.IsCurrentMonth(s.now()) {
		start := p.startTime()
		if month := daysIn(start.Year(), start.Month()); elapsed < month && elapsed >= minProjectionDays {
			proj := sum.ExpensesCents * int64(month) / int64(elapsed)
			sum.ProjectionCents = &proj
		}
	}

	sum.Categories = rankCategories(catTotals, sum.ExpensesCents)
	sum.IncomeCategories = rankCategories(incomeTotals, sum.IncomeCents)
	for _, m := range members {
		sum.Members = append(sum.Members, *memberTotals[m.ID])
	}

	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM finance_transactions WHERE workspace_id = $1 AND status = 'PENDING' AND deleted_at IS NULL`, wsID).Scan(&sum.Pending); err != nil {
		return nil, err
	}
	return sum, nil
}

func catTotal(m map[int64]*CategoryTotal, byCat map[int64]*Category, id int64) *CategoryTotal {
	if ct, ok := m[id]; ok {
		return ct
	}
	ct := &CategoryTotal{ID: id}
	if c, ok := byCat[id]; ok {
		ct.Name, ct.Icon, ct.Essentiality, ct.BudgetCents = c.Name, c.Icon, c.Essentiality, c.MonthlyBudgetCents
	}
	m[id] = ct
	return ct
}

type SeriesPoint struct {
	Label         string `json:"label"`
	Start         string `json:"start"`
	CurrentCents  int64  `json:"current_cents"`
	PreviousCents int64  `json:"previous_cents"`
}

type Series struct {
	Granularity string        `json:"granularity"` // day, week or month
	Points      []SeriesPoint `json:"points"`
}

// SpendingSeries buckets net spending over the period (days up to two
// months, then weeks, then months) aligned with the comparison period.
func (s *Service) SpendingSeries(ctx context.Context, wsID int64, p Period, f ReportFilter) (*Series, error) {
	return s.series(ctx, wsID, p, f, false)
}

// IncomeSeries buckets income the same way.
func (s *Service) IncomeSeries(ctx context.Context, wsID int64, p Period, f ReportFilter) (*Series, error) {
	return s.series(ctx, wsID, p, f, true)
}

func (s *Service) series(ctx context.Context, wsID int64, p Period, f ReportFilter, income bool) (*Series, error) {
	types := []string{TypeExpense, TypeRefund}
	if income {
		types = []string{TypeIncome}
	}
	prev := PreviousPeriod(p, s.now())
	days := p.Days()
	gran := "day"
	switch {
	case days > 400:
		gran = "month"
	case days > 62:
		gran = "week"
	}
	rows, err := s.DB.Query(ctx, `
		SELECT t.transaction_date::text, sum(CASE WHEN t.type = 'EXPENSE' THEN t.amount_cents ELSE -t.amount_cents END)::bigint
		FROM finance_transactions t LEFT JOIN finance_categories c ON c.id = t.category_id
		WHERE t.workspace_id = $1 AND t.status = 'CONFIRMED' AND t.deleted_at IS NULL AND t.type = ANY($8)
		  AND ((t.transaction_date BETWEEN $2::date AND $3::date) OR (t.transaction_date BETWEEN $4::date AND $5::date))
		  AND ($6::bigint IS NULL OR t.category_id = $6 OR c.parent_id = $6)
		  AND ($7::bigint IS NULL OR t.payer_member_id = $7)
		GROUP BY 1`, wsID, p.Start, p.End, prev.Start, prev.End, f.CategoryID, f.MemberID, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]int64{}
	for rows.Next() {
		var d string
		var v int64
		if err := rows.Scan(&d, &v); err != nil {
			return nil, err
		}
		byDay[d] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	index := func(start time.Time, d time.Time) int {
		switch gran {
		case "month":
			return (d.Year()-start.Year())*12 + int(d.Month()-start.Month())
		case "week":
			return int(d.Sub(start).Hours()/24) / 7
		}
		return int(d.Sub(start).Hours()/24 + 0.5)
	}
	cs, ps := p.startTime(), prev.startTime()
	n := index(cs, p.endTime()) + 1
	series := &Series{Granularity: gran, Points: make([]SeriesPoint, n)}
	for i := range series.Points {
		var start time.Time
		switch gran {
		case "month":
			start = cs.AddDate(0, i, 0)
			series.Points[i].Label = monthNames[start.Month()-1][:3] + "/" + start.Format("06")
		case "week":
			start = cs.AddDate(0, 0, 7*i)
			series.Points[i].Label = start.Format("02/01")
		default:
			start = cs.AddDate(0, 0, i)
			series.Points[i].Label = start.Format("02")
		}
		series.Points[i].Start = start.Format(DateLayout)
	}
	for day, v := range byDay {
		d, _ := time.ParseInLocation(DateLayout, day, cs.Location())
		if !d.Before(cs) {
			if i := index(cs, d); i >= 0 && i < n {
				series.Points[i].CurrentCents += v
			}
		} else if i := index(ps, d); i >= 0 && i < n {
			series.Points[i].PreviousCents += v
		}
	}
	return series, nil
}

type BudgetStatus struct {
	CategoryID      int64   `json:"category_id"`
	Name            string  `json:"name"`
	Icon            string  `json:"icon"`
	Essentiality    string  `json:"essentiality"`
	BudgetCents     int64   `json:"budget_cents"`
	SpentCents      int64   `json:"spent_cents"`
	RemainingCents  int64   `json:"remaining_cents"`
	Pct             float64 `json:"pct"`
	ProjectionCents int64   `json:"projection_cents"`
	State           string  `json:"state"` // ok, warning (>= 80%), over (>= 100%)
}

// CategorySpending returns net spending per category id (leaf) in a period.
func (s *Service) CategorySpending(ctx context.Context, wsID int64, p Period) (map[int64]int64, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT category_id, sum(CASE WHEN type = 'EXPENSE' THEN amount_cents ELSE -amount_cents END)::bigint
		FROM finance_transactions
		WHERE workspace_id = $1 AND status = 'CONFIRMED' AND deleted_at IS NULL AND type IN ('EXPENSE', 'REFUND')
		  AND category_id IS NOT NULL AND transaction_date BETWEEN $2::date AND $3::date
		GROUP BY 1`, wsID, p.Start, p.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, v int64
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// RollUp adds subcategory spending into the parents: spent[parent] is the
// whole tree, spent[leaf] only the leaf.
func RollUp(cats []Category, leaf map[int64]int64) map[int64]int64 {
	out := map[int64]int64{}
	for id, v := range leaf {
		out[id] += v
	}
	for _, c := range cats {
		if c.ParentID != nil {
			out[*c.ParentID] += leaf[c.ID]
		}
	}
	return out
}

// Budgets reports every category with a monthly budget for the month
// containing p's end (budgets are monthly).
func (s *Service) Budgets(ctx context.Context, wsID int64, categoryID *int64) ([]BudgetStatus, error) {
	month, _ := ResolvePeriod("this_month", "", "", "", s.now())
	cats, err := s.ListCategories(ctx, wsID, false)
	if err != nil {
		return nil, err
	}
	leaf, err := s.CategorySpending(ctx, wsID, month)
	if err != nil {
		return nil, err
	}
	spent := RollUp(cats, leaf)
	elapsed := month.ElapsedDays(s.now())
	days := daysIn(month.startTime().Year(), month.startTime().Month())
	out := []BudgetStatus{}
	for _, c := range cats {
		if c.MonthlyBudgetCents == nil || (categoryID != nil && c.ID != *categoryID) {
			continue
		}
		b := BudgetStatus{CategoryID: c.ID, Name: c.Name, Icon: c.Icon, Essentiality: c.Essentiality,
			BudgetCents: *c.MonthlyBudgetCents, SpentCents: spent[c.ID]}
		b.RemainingCents = b.BudgetCents - b.SpentCents
		b.Pct = math.Round(float64(b.SpentCents)/float64(b.BudgetCents)*1000) / 10
		b.ProjectionCents = b.SpentCents
		if elapsed >= minProjectionDays {
			b.ProjectionCents = b.SpentCents * int64(days) / int64(elapsed)
		}
		switch {
		case b.Pct >= 100:
			b.State = "over"
		case b.Pct >= 80:
			b.State = "warning"
		default:
			b.State = "ok"
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pct > out[j].Pct })
	return out, nil
}

func rankCategories(totals map[int64]*CategoryTotal, total int64) []CategoryTotal {
	out := []CategoryTotal{}
	for _, ct := range totals {
		if ct.AmountCents == 0 && ct.PreviousCents == 0 {
			continue
		}
		if total > 0 {
			ct.SharePct = math.Round(float64(ct.AmountCents)/float64(total)*1000) / 10
		}
		ct.ChangePct = changePct(ct.AmountCents, ct.PreviousCents)
		out = append(out, *ct)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AmountCents != out[j].AmountCents {
			return out[i].AmountCents > out[j].AmountCents
		}
		return out[i].Name < out[j].Name
	})
	return out
}
