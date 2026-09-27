package finance

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type seedCategory struct {
	name, icon, kind, essentiality string
	children                       []seedCategory
}

func exp(name, icon, ess string, children ...seedCategory) seedCategory {
	return seedCategory{name, icon, KindExpense, ess, children}
}

// defaultCategories is the starting tree. It lives in the database and is
// editable in the panel; the model only sees the current names.
var defaultCategories = []seedCategory{
	exp("Moradia", "🏠", Essential,
		exp("Aluguel", "🔑", Essential), exp("Contas da casa", "💡", Essential), exp("Internet", "📶", Essential)),
	exp("Alimentação", "🍽️", Essential,
		exp("Mercado", "🛒", Essential), exp("Restaurantes", "🍝", Discretionary), exp("Delivery", "🛵", Discretionary)),
	exp("Transporte", "🚗", Important,
		exp("Combustível", "⛽", Important), exp("Manutenção", "🔧", Important), exp("Aplicativo", "🚕", Important)),
	exp("Saúde", "🩺", Essential,
		exp("Farmácia", "💊", Essential), exp("Consultas", "🏥", Essential)),
	exp("Lazer", "🎮", Discretionary),
	exp("Assinaturas", "📺", Discretionary),
	exp("Educação", "📚", Important),
	exp("Pets", "🐾", Important),
	exp("Roupas", "👕", Discretionary),
	exp("Casa", "🛋️", Important),
	exp("Viagens", "✈️", Discretionary),
	exp("Presentes", "🎁", Discretionary),
	exp("Impostos/Taxas", "🧾", Essential),
	exp("Dívidas", "💳", Essential),
	exp("Outros", "📦", Important),
	{"Salário", "💼", KindIncome, Important, nil},
	{"Renda extra", "💰", KindIncome, Important, nil},
	{"Outras receitas", "📥", KindIncome, Important, nil},
}

// SeedDefaultCategories inserts the default tree. Idempotent: existing names
// are left untouched.
func SeedDefaultCategories(ctx context.Context, q querier, wsID int64) error {
	const upsert = `
		INSERT INTO finance_categories (workspace_id, parent_id, name, icon, kind, essentiality, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, (COALESCE(parent_id, 0)), (lower(name))) DO NOTHING`
	const lookup = `SELECT id FROM finance_categories WHERE workspace_id = $1 AND COALESCE(parent_id, 0) = COALESCE($2, 0) AND lower(name) = lower($3)`

	for i, c := range defaultCategories {
		if _, err := q.Exec(ctx, upsert, wsID, nil, c.name, c.icon, c.kind, c.essentiality, i); err != nil {
			return err
		}
		if len(c.children) == 0 {
			continue
		}
		var parentID int64
		if err := q.QueryRow(ctx, lookup, wsID, nil, c.name).Scan(&parentID); err != nil {
			return err
		}
		for j, ch := range c.children {
			if _, err := q.Exec(ctx, upsert, wsID, parentID, ch.name, ch.icon, ch.kind, ch.essentiality, j); err != nil {
				return err
			}
		}
	}
	return nil
}

const categoryCols = `id, workspace_id, parent_id, name, icon, kind, essentiality, monthly_budget_cents,
	sort_order, archived_at, created_at, updated_at`

func scanCategories(rows pgx.Rows) ([]Category, error) {
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.ParentID, &c.Name, &c.Icon, &c.Kind, &c.Essentiality,
			&c.MonthlyBudgetCents, &c.SortOrder, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCategories returns every category of the workspace, parents first.
func (s *Service) ListCategories(ctx context.Context, wsID int64, includeArchived bool) ([]Category, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+categoryCols+` FROM finance_categories
		WHERE workspace_id = $1 AND ($2 OR archived_at IS NULL)
		ORDER BY kind DESC, COALESCE(parent_id, id), parent_id NULLS FIRST, sort_order, name`, wsID, includeArchived)
	if err != nil {
		return nil, err
	}
	return scanCategories(rows)
}

func (s *Service) GetCategory(ctx context.Context, wsID, id int64) (*Category, error) {
	return getCategory(ctx, s.DB, wsID, id)
}

func getCategory(ctx context.Context, q querier, wsID, id int64) (*Category, error) {
	rows, err := q.Query(ctx, "SELECT "+categoryCols+" FROM finance_categories WHERE workspace_id = $1 AND id = $2", wsID, id)
	if err != nil {
		return nil, err
	}
	cs, err := scanCategories(rows)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, ErrNotFound
	}
	return &cs[0], nil
}

type CategoryInput struct {
	ParentID           *int64  `json:"parent_id"`
	Name               *string `json:"name"`
	Icon               *string `json:"icon"`
	Kind               *string `json:"kind"`
	Essentiality       *string `json:"essentiality"`
	MonthlyBudgetCents *int64  `json:"monthly_budget_cents"`
	// ClearBudget removes the budget (a null budget in a PATCH is ambiguous).
	ClearBudget bool  `json:"clear_budget"`
	Archived    *bool `json:"archived"`
}

func (s *Service) validateCategory(ctx context.Context, wsID int64, selfID int64, in *CategoryInput) error {
	if in.Name != nil {
		n := truncate(*in.Name, 40)
		if n == "" {
			return invalid("Informe o nome da categoria.")
		}
		in.Name = &n
	}
	if in.Icon != nil {
		ic := truncate(*in.Icon, 8)
		in.Icon = &ic
	}
	if in.Kind != nil && *in.Kind != KindExpense && *in.Kind != KindIncome {
		return invalid("Tipo de categoria inválido.")
	}
	if in.Essentiality != nil && !contains(Essentialities, *in.Essentiality) {
		return invalid("Essencialidade inválida.")
	}
	if in.MonthlyBudgetCents != nil && (*in.MonthlyBudgetCents <= 0 || *in.MonthlyBudgetCents > MaxAmountCents) {
		return invalid("Orçamento deve ser maior que zero.")
	}
	if in.ParentID != nil {
		if *in.ParentID == selfID {
			return invalid("Uma categoria não pode ser subcategoria dela mesma.")
		}
		parent, err := s.GetCategory(ctx, wsID, *in.ParentID)
		if errors.Is(err, ErrNotFound) {
			return invalid("Categoria pai não encontrada.")
		}
		if err != nil {
			return err
		}
		if parent.ParentID != nil {
			return invalid("Subcategorias têm apenas um nível.")
		}
		if selfID != 0 {
			var children int
			if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM finance_categories WHERE workspace_id = $1 AND parent_id = $2", wsID, selfID).Scan(&children); err != nil {
				return err
			}
			if children > 0 {
				return invalid("Esta categoria tem subcategorias e não pode virar subcategoria.")
			}
		}
		// A subcategory always has the kind of its parent.
		in.Kind = &parent.Kind
	}
	return nil
}

func (s *Service) CreateCategory(ctx context.Context, wsID int64, in CategoryInput) (*Category, error) {
	if in.Name == nil {
		return nil, invalid("Informe o nome da categoria.")
	}
	if err := s.validateCategory(ctx, wsID, 0, &in); err != nil {
		return nil, err
	}
	kind, ess, icon := KindExpense, Important, ""
	if in.Kind != nil {
		kind = *in.Kind
	}
	if in.Essentiality != nil {
		ess = *in.Essentiality
	}
	if in.Icon != nil {
		icon = *in.Icon
	}
	rows, err := s.DB.Query(ctx, `
		INSERT INTO finance_categories (workspace_id, parent_id, name, icon, kind, essentiality, monthly_budget_cents, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1000)
		RETURNING `+categoryCols, wsID, in.ParentID, *in.Name, icon, kind, ess, in.MonthlyBudgetCents)
	if err != nil {
		return nil, err
	}
	cs, err := scanCategories(rows)
	if err != nil {
		if strings.Contains(err.Error(), "finance_categories_name_uq") {
			return nil, invalid("Já existe uma categoria com esse nome.")
		}
		return nil, err
	}
	return &cs[0], nil
}

func (s *Service) UpdateCategory(ctx context.Context, wsID, id int64, in CategoryInput) (*Category, error) {
	if _, err := s.GetCategory(ctx, wsID, id); err != nil {
		return nil, err
	}
	if err := s.validateCategory(ctx, wsID, id, &in); err != nil {
		return nil, err
	}
	var archive *bool = in.Archived
	rows, err := s.DB.Query(ctx, `
		UPDATE finance_categories SET
			parent_id = CASE WHEN $3::bigint IS NULL THEN parent_id ELSE $3 END,
			name = COALESCE($4, name),
			icon = COALESCE($5, icon),
			kind = COALESCE($6, kind),
			essentiality = COALESCE($7, essentiality),
			monthly_budget_cents = CASE WHEN $9 THEN NULL ELSE COALESCE($8, monthly_budget_cents) END,
			archived_at = CASE WHEN $10::boolean IS NULL THEN archived_at WHEN $10 THEN COALESCE(archived_at, now()) ELSE NULL END,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+categoryCols, wsID, id, in.ParentID, in.Name, in.Icon, in.Kind, in.Essentiality, in.MonthlyBudgetCents, in.ClearBudget, archive)
	if err != nil {
		return nil, err
	}
	cs, err := scanCategories(rows)
	if err != nil {
		if strings.Contains(err.Error(), "finance_categories_name_uq") {
			return nil, invalid("Já existe uma categoria com esse nome.")
		}
		return nil, err
	}
	if len(cs) == 0 {
		return nil, ErrNotFound
	}
	// Subcategories follow their parent's kind.
	if in.Kind != nil && cs[0].ParentID == nil {
		if _, err := s.DB.Exec(ctx, "UPDATE finance_categories SET kind = $3 WHERE workspace_id = $1 AND parent_id = $2", wsID, id, *in.Kind); err != nil {
			return nil, err
		}
	}
	return &cs[0], nil
}

// SetBudget sets or clears (cents == nil) the monthly budget of a category.
func (s *Service) SetBudget(ctx context.Context, wsID, id int64, cents *int64) (*Category, error) {
	in := CategoryInput{MonthlyBudgetCents: cents, ClearBudget: cents == nil}
	return s.UpdateCategory(ctx, wsID, id, in)
}

// CategoryPath renders "Alimentação › Mercado".
func CategoryPath(c *Category, byID map[int64]*Category) string {
	if c == nil {
		return ""
	}
	if c.ParentID != nil {
		if p, ok := byID[*c.ParentID]; ok {
			return p.Name + " › " + c.Name
		}
	}
	return c.Name
}

// CategoryIndex indexes categories by id.
func CategoryIndex(cs []Category) map[int64]*Category {
	m := make(map[int64]*Category, len(cs))
	for i := range cs {
		m[cs[i].ID] = &cs[i]
	}
	return m
}

// ResolveCategory maps a name said by the user or the model ("mercado",
// "Alimentação > Mercado", "restaurante") to an active category of the
// requested kind. It only matches names that exist in the database; when
// nothing matches it returns the valid names instead of guessing.
func (s *Service) ResolveCategory(ctx context.Context, wsID int64, name, kind string) (*Category, []string, error) {
	cs, err := s.ListCategories(ctx, wsID, false)
	if err != nil {
		return nil, nil, err
	}
	c, options := resolveCategory(cs, name, kind)
	return c, options, nil
}

func singular(s string) string {
	if len(s) > 3 && strings.HasSuffix(s, "s") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

func resolveCategory(cs []Category, name, kind string) (*Category, []string) {
	byID := CategoryIndex(cs)
	var options []string
	var active []*Category
	for i := range cs {
		if cs[i].Kind == kind {
			active = append(active, &cs[i])
			options = append(options, CategoryPath(&cs[i], byID))
		}
	}
	sort.Strings(options)

	// Try the full name first ("Impostos/Taxas"), then the last segment of a
	// path ("Alimentação > Mercado").
	full := normalize(name)
	candidates := []string{full}
	for _, sep := range []string{"›", ">", "/"} {
		if i := strings.LastIndex(full, sep); i >= 0 {
			candidates = append(candidates, strings.TrimSpace(full[i+len(sep):]))
		}
	}
	pick := func(list []*Category) *Category {
		// Prefer a leaf ("Mercado" under Alimentação) over a parent of the same name.
		for _, c := range list {
			if c.ParentID != nil {
				return c
			}
		}
		return list[0]
	}
	for _, want := range candidates {
		if want == "" {
			continue
		}
		var exact, loose []*Category
		for _, c := range active {
			n := normalize(c.Name)
			switch {
			case n == want:
				exact = append(exact, c)
			case singular(n) == singular(want):
				loose = append(loose, c)
			}
		}
		if len(exact) > 0 {
			return pick(exact), options
		}
		if len(loose) > 0 {
			return pick(loose), options
		}
	}
	return nil, options
}
