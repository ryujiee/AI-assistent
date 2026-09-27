package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"secretary/db/dbtest"
	"secretary/finance"
	"secretary/timeutil"
)

// financeHarness serves the finance API over a migrated test schema with a
// logged-in session.
type financeHarness struct {
	h      http.Handler
	cookie *http.Cookie
	svc    *finance.Service
}

func newFinanceHarness(t *testing.T, enabled bool) *financeHarness {
	t.Helper()
	svc := finance.NewService(dbtest.New(t))
	svc.Now = func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, timeutil.Location()) }
	a := NewAuthenticator(testPassword, strings.Repeat("s", 32), nil)
	api := &FinanceAPI{Enabled: enabled, Svc: svc}
	h := NewHandler(Options{Auth: a, ProtectedRoutes: []func(*http.ServeMux){api.Register}})
	return &financeHarness{h: h, cookie: login(t, h), svc: svc}
}

func (f *financeHarness) call(method, target, body string) (int, map[string]any) {
	rec := do(f.h, method, target, body, sameOrigin, f.cookie)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestFinanceDisabledAnswers503(t *testing.T) {
	f := newFinanceHarness(t, false)
	code, body := f.call(http.MethodGet, "/api/finance/transactions", "")
	if code != http.StatusServiceUnavailable || body["error"] != "finance_disabled" {
		t.Fatalf("disabled module: %d %v", code, body)
	}
	if code, body := f.call(http.MethodGet, "/api/finance/status", ""); code != 200 || body["enabled"] != false {
		t.Fatalf("status: %d %v", code, body)
	}
}

func TestFinanceRequiresSession(t *testing.T) {
	f := newFinanceHarness(t, true)
	if rec := do(f.h, http.MethodGet, "/api/finance/transactions", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

func TestFinanceTransactionCRUD(t *testing.T) {
	f := newFinanceHarness(t, true)
	ctx := context.Background()
	ws, err := f.svc.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mercado, _, _ := f.svc.ResolveCategory(ctx, ws.ID, "Mercado", finance.KindExpense)

	code, created := f.call(http.MethodPost, "/api/finance/transactions",
		`{"type":"EXPENSE","amount_cents":5000,"description":"Compra","category_id":`+strconv.FormatInt(mercado.ID, 10)+`,"transaction_date":"2026-09-27"}`)
	if code != http.StatusCreated || created["category_name"] != "Mercado" || created["status"] != "CONFIRMED" {
		t.Fatalf("create: %d %v", code, created)
	}
	id := strconv.FormatInt(int64(created["id"].(float64)), 10)

	code, list := f.call(http.MethodGet, "/api/finance/transactions?period=this_month", "")
	if code != 200 || list["total"].(float64) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}

	code, detail := f.call(http.MethodPatch, "/api/finance/transactions/"+id, `{"amount_cents":6000,"confirm":true}`)
	if code != 200 || detail["amount_cents"].(float64) != 6000 || len(detail["events"].([]any)) != 2 {
		t.Fatalf("patch: %d %v", code, detail)
	}

	if code, _ := f.call(http.MethodDelete, "/api/finance/transactions/"+id, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := f.call(http.MethodGet, "/api/finance/transactions/"+id, ""); code != 404 {
		t.Fatalf("deleted still readable: %d", code)
	}
	if code, _ := f.call(http.MethodPost, "/api/finance/transactions/"+id+"/restore", ""); code != 200 {
		t.Fatalf("restore: %d", code)
	}
}

func TestFinanceRejectsClientWorkspaceAndForeignIDs(t *testing.T) {
	f := newFinanceHarness(t, true)
	ctx := context.Background()
	if _, err := f.svc.DefaultWorkspace(ctx); err != nil {
		t.Fatal(err)
	}
	other, _ := f.svc.CreateWorkspace(ctx, "Outro casal")
	cat, _, _ := f.svc.ResolveCategory(ctx, other.ID, "Mercado", finance.KindExpense)
	txs, err := f.svc.CreateTransactions(ctx, other.ID, finance.Actor{Channel: finance.ChannelWeb},
		[]finance.TxInput{{Type: finance.TypeExpense, AmountCents: 999, CategoryID: &cat.ID, Date: "2026-09-27", Source: finance.SourceWeb}})
	if err != nil {
		t.Fatal(err)
	}
	foreign := strconv.FormatInt(txs[0].ID, 10)

	for _, c := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/finance/transactions/" + foreign, ""},
		{http.MethodPatch, "/api/finance/transactions/" + foreign, `{"amount_cents":1}`},
		{http.MethodDelete, "/api/finance/transactions/" + foreign, ""},
		{http.MethodPatch, "/api/finance/categories/" + strconv.FormatInt(cat.ID, 10), `{"name":"x"}`},
	} {
		if code, _ := f.call(c.method, c.target, c.body); code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.target, code)
		}
	}
	if code, _ := f.call(http.MethodPost, "/api/finance/transactions",
		`{"type":"EXPENSE","amount_cents":100,"transaction_date":"2026-09-27","workspace_id":`+strconv.FormatInt(other.ID, 10)+`}`); code != http.StatusBadRequest {
		t.Errorf("workspace_id in body accepted: %d", code)
	}
	if code, _ := f.call(http.MethodPost, "/api/finance/transactions",
		`{"type":"EXPENSE","amount_cents":100,"transaction_date":"2026-09-27","category_id":`+strconv.FormatInt(cat.ID, 10)+`}`); code != http.StatusBadRequest {
		t.Errorf("foreign category accepted: %d", code)
	}
	if code, list := f.call(http.MethodGet, "/api/finance/transactions?period=all", ""); code != 200 || list["total"].(float64) != 0 {
		t.Errorf("list leaked other workspace: %v", list)
	}
	for _, bad := range []string{"abc", "-1", "0", "1%2F..%2F2"} {
		if code, _ := f.call(http.MethodGet, "/api/finance/transactions/"+bad, ""); code != http.StatusNotFound {
			t.Errorf("id %q = %d", bad, code)
		}
	}
}

func TestAttachmentEndpointIsPrivate(t *testing.T) {
	f := newFinanceHarness(t, true)
	ctx := context.Background()
	ws, _ := f.svc.DefaultWorkspace(ctx)
	other, _ := f.svc.CreateWorkspace(ctx, "Outro")
	jpeg := append([]byte("\xFF\xD8\xFF\xE0\x00\x10JFIF\x00"), []byte(strings.Repeat("x", 32))...)
	mine, _, err := f.svc.StoreAttachment(ctx, ws.ID, jpeg)
	if err != nil {
		t.Fatal(err)
	}
	theirs, _, _ := f.svc.StoreAttachment(ctx, other.ID, append(jpeg, 'z'))

	rec := do(f.h, http.MethodGet, "/api/finance/attachments/"+strconv.FormatInt(mine.ID, 10), "", nil, f.cookie)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("own attachment: %d %v", rec.Code, rec.Header())
	}
	if rec := do(f.h, http.MethodGet, "/api/finance/attachments/"+strconv.FormatInt(theirs.ID, 10), "", nil, f.cookie); rec.Code != 404 {
		t.Fatalf("other workspace attachment: %d", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/finance/attachments/"+strconv.FormatInt(mine.ID, 10), "", nil); rec.Code != 401 {
		t.Fatalf("attachment without session: %d", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/finance/attachments/..%2F..%2Fetc%2Fpasswd", "", nil, f.cookie); rec.Code != 404 {
		t.Fatalf("traversal id: %d", rec.Code)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	f := newFinanceHarness(t, true)
	ctx := context.Background()
	ws, _ := f.svc.DefaultWorkspace(ctx)
	cat, _, _ := f.svc.ResolveCategory(ctx, ws.ID, "Mercado", finance.KindExpense)
	f.svc.CreateTransactions(ctx, ws.ID, finance.Actor{Channel: finance.ChannelWeb}, []finance.TxInput{
		{Type: finance.TypeExpense, AmountCents: 5000, CategoryID: &cat.ID, Date: "2026-09-20", Source: finance.SourceWeb},
	})
	code, body := f.call(http.MethodGet, "/api/finance/overview?period=this_month", "")
	if code != 200 {
		t.Fatalf("overview: %d %v", code, body)
	}
	sum := body["summary"].(map[string]any)
	if sum["expenses_cents"].(float64) != 5000 || len(body["series"].(map[string]any)["points"].([]any)) != 27 || len(body["largest"].([]any)) != 1 {
		t.Fatalf("overview body: %v", body)
	}
	if code, _ := f.call(http.MethodGet, "/api/finance/overview?period=custom&start=2026-09-20&end=2026-09-01", ""); code != 400 {
		t.Fatalf("invalid period: %d", code)
	}
	code, cats := f.call(http.MethodGet, "/api/finance/categories", "")
	if code != 200 || cats["spent"].(map[string]any)[strconv.FormatInt(cat.ID, 10)].(float64) != 5000 {
		t.Fatalf("categories spent: %v", cats["spent"])
	}
}
