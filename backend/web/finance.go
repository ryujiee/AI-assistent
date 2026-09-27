package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"

	"secretary/db"
	"secretary/finance"
	"secretary/whatsapp"
)

// FinanceAPI exposes the finance module to the panel. The workspace always
// comes from the server (the admin session maps to the panel workspace);
// ids in URLs are only looked up inside that workspace.
type FinanceAPI struct {
	Enabled  bool
	Svc      *finance.Service
	Ingestor *finance.Ingestor
	// Fake is set only with WHATSAPP_FAKE; it enables the /api/dev routes.
	Fake *whatsapp.FakeGateway

	migrated atomic.Bool
}

// FinanceMigration is the migration that creates the finance tables.
const FinanceMigration = "002"

func (api *FinanceAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/finance/status", api.handleStatus)

	mux.HandleFunc("GET /api/finance/settings", api.ws(api.getSettings))
	mux.HandleFunc("PATCH /api/finance/settings", api.ws(api.patchSettings))

	mux.HandleFunc("GET /api/finance/categories", api.ws(api.listCategories))
	mux.HandleFunc("POST /api/finance/categories", api.ws(api.createCategory))
	mux.HandleFunc("PATCH /api/finance/categories/{id}", api.ws(api.patchCategory))

	mux.HandleFunc("GET /api/finance/members", api.ws(api.listMembers))
	mux.HandleFunc("PATCH /api/finance/members/{id}", api.ws(api.patchMember))

	mux.HandleFunc("GET /api/finance/transactions", api.ws(api.listTransactions))
	mux.HandleFunc("POST /api/finance/transactions", api.ws(api.createTransaction))
	mux.HandleFunc("GET /api/finance/transactions/{id}", api.ws(api.getTransaction))
	mux.HandleFunc("PATCH /api/finance/transactions/{id}", api.ws(api.patchTransaction))
	mux.HandleFunc("DELETE /api/finance/transactions/{id}", api.ws(api.deleteTransaction))
	mux.HandleFunc("POST /api/finance/transactions/{id}/restore", api.ws(api.restoreTransaction))

	mux.HandleFunc("GET /api/finance/attachments/{id}", api.ws(api.getAttachment))
	mux.HandleFunc("GET /api/finance/overview", api.ws(api.overview))
	mux.HandleFunc("GET /api/finance/budgets", api.ws(api.budgets))

	if api.Ingestor != nil {
		api.registerWhatsApp(mux)
	}
}

// ready reports whether the finance tables exist. Checked lazily so running
// "secretary migrate apply" does not require a restart.
func (api *FinanceAPI) ready(ctx context.Context) bool {
	if api.migrated.Load() {
		return true
	}
	ok, err := db.IsApplied(ctx, api.Svc.DB, FinanceMigration)
	if err == nil && ok {
		api.migrated.Store(true)
	}
	return ok
}

func (api *FinanceAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	res := map[string]any{"enabled": api.Enabled, "migrated": false}
	if api.Enabled && api.Svc != nil && api.ready(r.Context()) {
		res["migrated"] = true
		if ws, err := api.Svc.DefaultWorkspace(r.Context()); err == nil {
			res["workspace"] = ws
		}
	}
	writeJSON(w, http.StatusOK, res)
}

type wsHandler func(w http.ResponseWriter, r *http.Request, ws *finance.Workspace)

// ws resolves the workspace of the session and guards the module state.
func (api *FinanceAPI) ws(h wsHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !api.Enabled || api.Svc == nil {
			writeError(w, http.StatusServiceUnavailable, "finance_disabled", "O módulo financeiro está desativado neste servidor.")
			return
		}
		if !api.ready(r.Context()) {
			writeError(w, http.StatusServiceUnavailable, "finance_not_migrated", "As tabelas do financeiro ainda não foram criadas (secretary migrate apply).")
			return
		}
		ws, err := api.Svc.DefaultWorkspace(r.Context())
		if err != nil {
			financeError(w, err)
			return
		}
		h(w, r, ws)
	}
}

// financeError maps service errors to responses without leaking internals.
func financeError(w http.ResponseWriter, err error) {
	var v *finance.ValidationError
	switch {
	case errors.As(err, &v):
		writeError(w, http.StatusBadRequest, "validation", v.Msg)
	case errors.Is(err, finance.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Registro não encontrado.")
	case errors.Is(err, finance.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "A operação conflita com outro registro.")
	case errors.Is(err, finance.ErrUndoStale):
		writeError(w, http.StatusConflict, "stale", "O registro mudou depois dessa ação.")
	case errors.Is(err, finance.ErrInvalidPeriod):
		writeError(w, http.StatusBadRequest, "invalid_period", "Período inválido.")
	default:
		slog.Error("finance.http_error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Não foi possível concluir a operação.")
	}
}

// decodeBody reads a small JSON body and rejects unknown fields, so a client
// cannot slip in fields such as workspace_id.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Dados inválidos na requisição.")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Registro não encontrado.")
		return 0, false
	}
	return id, true
}

func queryInt64(r *http.Request, key string) *int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

var webActor = finance.Actor{Channel: finance.ChannelWeb}

// ---- settings ----

func (api *FinanceAPI) getSettings(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	writeJSON(w, http.StatusOK, ws)
}

func (api *FinanceAPI) patchSettings(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	var p finance.SettingsPatch
	if !decodeBody(w, r, &p) {
		return
	}
	updated, err := api.Svc.UpdateSettings(r.Context(), ws.ID, p)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---- categories ----

// listCategories returns the tree plus this month's spending per category
// (parents include their subcategories), for the budgets screen.
func (api *FinanceAPI) listCategories(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	cats, err := api.Svc.ListCategories(r.Context(), ws.ID, r.URL.Query().Get("archived") == "1")
	if err != nil {
		financeError(w, err)
		return
	}
	if cats == nil {
		cats = []finance.Category{}
	}
	month, _ := finance.ResolvePeriod("this_month", "", "", "", api.Svc.Now())
	leaf, err := api.Svc.CategorySpending(r.Context(), ws.ID, month)
	if err != nil {
		financeError(w, err)
		return
	}
	spent := map[string]int64{}
	for id, v := range finance.RollUp(cats, leaf) {
		spent[strconv.FormatInt(id, 10)] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats, "month": month, "spent": spent})
}

func (api *FinanceAPI) createCategory(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	var in finance.CategoryInput
	if !decodeBody(w, r, &in) {
		return
	}
	c, err := api.Svc.CreateCategory(r.Context(), ws.ID, in)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (api *FinanceAPI) patchCategory(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in finance.CategoryInput
	if !decodeBody(w, r, &in) {
		return
	}
	c, err := api.Svc.UpdateCategory(r.Context(), ws.ID, id, in)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---- members ----

type memberView struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
}

func toMemberViews(ms []finance.Member) []memberView {
	out := make([]memberView, 0, len(ms))
	for _, m := range ms {
		v := memberView{ID: m.ID, DisplayName: m.DisplayName}
		if m.PhoneNumber != nil {
			v.Phone = *m.PhoneNumber
		}
		out = append(out, v)
	}
	return out
}

func (api *FinanceAPI) listMembers(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	ms, err := api.Svc.ListMembers(r.Context(), ws.ID)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": toMemberViews(ms)})
}

func (api *FinanceAPI) patchMember(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	m, err := api.Svc.RenameMember(r.Context(), ws.ID, id, body.DisplayName)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMemberViews([]finance.Member{*m})[0])
}

// ---- transactions ----

// periodFromQuery resolves ?period=&month=&start=&end= (default: this month).
func (api *FinanceAPI) periodFromQuery(r *http.Request) (finance.Period, error) {
	q := r.URL.Query()
	return finance.ResolvePeriod(q.Get("period"), q.Get("month"), q.Get("start"), q.Get("end"), api.Svc.Now())
}

func (api *FinanceAPI) listTransactions(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	q := r.URL.Query()
	f := finance.TxFilter{
		CategoryID: queryInt64(r, "category_id"),
		MemberID:   queryInt64(r, "member_id"),
		Type:       q.Get("type"),
		Status:     q.Get("status"),
		Search:     q.Get("q"),
		MinCents:   queryInt64(r, "min_cents"),
		MaxCents:   queryInt64(r, "max_cents"),
		OrderBy:    q.Get("order"),
	}
	if f.Type != "" && !contains(finance.TransactionTypes, f.Type) {
		writeError(w, http.StatusBadRequest, "validation", "Tipo inválido.")
		return
	}
	if v := queryInt64(r, "limit"); v != nil {
		f.Limit = int(*v)
	}
	if v := queryInt64(r, "offset"); v != nil {
		f.Offset = int(*v)
	}
	if q.Get("period") != "all" {
		p, err := api.periodFromQuery(r)
		if err != nil {
			financeError(w, err)
			return
		}
		f.Start, f.End = p.Start, p.End
	}
	items, total, err := api.Svc.ListTransactions(r.Context(), ws.ID, f)
	if err != nil {
		financeError(w, err)
		return
	}
	if items == nil {
		items = []finance.TransactionView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type createTxBody struct {
	Type            string `json:"type"`
	AmountCents     int64  `json:"amount_cents"`
	Description     string `json:"description"`
	Merchant        string `json:"merchant"`
	CategoryID      *int64 `json:"category_id"`
	TransactionDate string `json:"transaction_date"`
	PayerMemberID   *int64 `json:"payer_member_id"`
	Shared          *bool  `json:"shared"`
	PaymentMethod   string `json:"payment_method"`
	Notes           string `json:"notes"`
}

func (api *FinanceAPI) createTransaction(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	var b createTxBody
	if !decodeBody(w, r, &b) {
		return
	}
	txs, err := api.Svc.CreateTransactions(r.Context(), ws.ID, webActor, []finance.TxInput{{
		Type: b.Type, AmountCents: b.AmountCents, Description: b.Description, Merchant: b.Merchant,
		CategoryID: b.CategoryID, Date: b.TransactionDate, PayerMemberID: b.PayerMemberID, Shared: b.Shared,
		PaymentMethod: b.PaymentMethod, Notes: b.Notes, Source: finance.SourceWeb,
	}})
	if err != nil {
		financeError(w, err)
		return
	}
	view, err := api.Svc.GetTransactionView(r.Context(), ws.ID, txs[0].ID)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (api *FinanceAPI) getTransaction(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := api.Svc.GetTransactionDetail(r.Context(), ws.ID, id)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (api *FinanceAPI) patchTransaction(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p finance.TxPatch
	if !decodeBody(w, r, &p) {
		return
	}
	if _, _, err := api.Svc.UpdateTransaction(r.Context(), ws.ID, id, webActor, p); err != nil {
		financeError(w, err)
		return
	}
	d, err := api.Svc.GetTransactionDetail(r.Context(), ws.ID, id)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (api *FinanceAPI) deleteTransaction(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := api.Svc.DeleteTransaction(r.Context(), ws.ID, id, webActor); err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (api *FinanceAPI) restoreTransaction(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := api.Svc.RestoreTransaction(r.Context(), ws.ID, id, webActor); err != nil {
		financeError(w, err)
		return
	}
	d, err := api.Svc.GetTransactionDetail(r.Context(), ws.ID, id)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// getAttachment streams a receipt of the session's workspace. There is no
// public URL: every read goes through the session, and nothing is cached.
func (api *FinanceAPI) getAttachment(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := api.Svc.GetAttachment(r.Context(), ws.ID, id)
	if err != nil {
		financeError(w, err)
		return
	}
	ext, ok := finance.AllowedMimes[a.Mime]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Registro não encontrado.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.Mime)
	h.Set("Content-Length", strconv.Itoa(len(a.Data)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `inline; filename="comprovante.`+ext+`"`)
	if a.Mime != "application/pdf" {
		// Opened directly, an image renders in a document that can run nothing.
		// PDFs keep the browser's viewer working and stay same-origin only.
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	}
	w.WriteHeader(http.StatusOK)
	w.Write(a.Data)
}

// overview feeds the dashboard: totals, comparison, series, largest
// expenses, insights and budgets, all computed from the ledger now.
func (api *FinanceAPI) overview(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	p, err := api.periodFromQuery(r)
	if err != nil {
		financeError(w, err)
		return
	}
	f := finance.ReportFilter{CategoryID: queryInt64(r, "category_id"), MemberID: queryInt64(r, "member_id")}
	ctx := r.Context()
	sum, err := api.Svc.Summary(ctx, ws.ID, p, f)
	if err != nil {
		financeError(w, err)
		return
	}
	series, err := api.Svc.SpendingSeries(ctx, ws.ID, p, f)
	if err != nil {
		financeError(w, err)
		return
	}
	largest, _, err := api.Svc.ListTransactions(ctx, ws.ID, finance.TxFilter{Start: p.Start, End: p.End, CategoryID: f.CategoryID,
		MemberID: f.MemberID, Type: finance.TypeExpense, Status: finance.StatusConfirmed, OrderBy: "amount", Limit: 5})
	if err != nil {
		financeError(w, err)
		return
	}
	insights, err := api.Svc.Insights(ctx, ws.ID, p)
	if err != nil {
		financeError(w, err)
		return
	}
	budgets, err := api.Svc.Budgets(ctx, ws.ID, nil)
	if err != nil {
		financeError(w, err)
		return
	}
	if largest == nil {
		largest = []finance.TransactionView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": sum, "series": series, "largest": largest, "insights": insights, "budgets": budgets})
}

func (api *FinanceAPI) budgets(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	b, err := api.Svc.Budgets(r.Context(), ws.ID, nil)
	if err != nil {
		financeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"budgets": b})
}
