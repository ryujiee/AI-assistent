package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct-horse-battery"

func newTestAuth(t *testing.T) (*Authenticator, http.Handler) {
	t.Helper()
	a := NewAuthenticator(testPassword, strings.Repeat("s", 32), []string{"http://localhost:5173"})
	h := NewHandler(Options{
		Auth: a,
		ProtectedRoutes: []func(*http.ServeMux){func(mux *http.ServeMux) {
			mux.HandleFunc("/api/probe", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			})
		}},
	})
	return a, h
}

func do(h http.Handler, method, target, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "panel.test"
	req.RemoteAddr = "203.0.113.7:4444"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var sameOrigin = map[string]string{"Origin": "https://panel.test", "Content-Type": "application/json"}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := do(h, http.MethodPost, "/api/login", `{"password":"`+testPassword+`"}`, sameOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func TestProtectedEndpointWithoutSessionIs401(t *testing.T) {
	_, h := newTestAuth(t)
	for _, target := range []string{"/api/probe", "/api/status", "/api/config", "/api/finance/anything"} {
		if rec := do(h, http.MethodGet, target, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", target, rec.Code)
		}
	}
}

func TestHealthIsPublic(t *testing.T) {
	_, h := newTestAuth(t)
	if rec := do(h, http.MethodGet, "/api/health", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
}

func TestLoginSetsHardenedCookieAndGrantsAccess(t *testing.T) {
	_, h := newTestAuth(t)
	c := login(t, h)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie flags: HttpOnly=%v Secure=%v SameSite=%v", c.HttpOnly, c.Secure, c.SameSite)
	}
	if strings.Contains(c.Value, testPassword) {
		t.Error("cookie carries the password")
	}
	if c.MaxAge <= 0 {
		t.Error("cookie has no expiry")
	}
	if rec := do(h, http.MethodGet, "/api/probe", "", nil, c); rec.Code != http.StatusOK {
		t.Fatalf("authenticated probe status = %d", rec.Code)
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	_, h := newTestAuth(t)
	rec := do(h, http.MethodPost, "/api/login", `{"password":"nope"}`, sameOrigin)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("cookie set on failed login")
	}
}

func TestLoginRateLimitPerIP(t *testing.T) {
	a, h := newTestAuth(t)
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	for i := 0; i < loginMaxPerIP; i++ {
		do(h, http.MethodPost, "/api/login", `{"password":"nope"}`, sameOrigin)
	}
	rec := do(h, http.MethodPost, "/api/login", `{"password":"`+testPassword+`"}`, sameOrigin)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want 429", loginMaxPerIP, rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}

	// Cooldown: once the window passes the right password works again.
	now = now.Add(loginWindow + time.Second)
	if rec := do(h, http.MethodPost, "/api/login", `{"password":"`+testPassword+`"}`, sameOrigin); rec.Code != http.StatusOK {
		t.Fatalf("status after cooldown = %d", rec.Code)
	}
}

func TestExpiredAndTamperedSessionsAreRejected(t *testing.T) {
	a, h := newTestAuth(t)
	c := login(t, h)

	tampered := *c
	tampered.Value = strings.Replace(c.Value, ".", "x.", 1)
	if rec := do(h, http.MethodGet, "/api/probe", "", nil, &tampered); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered cookie status = %d", rec.Code)
	}

	a.now = func() time.Time { return time.Now().Add(sessionTTL + time.Minute) }
	if rec := do(h, http.MethodGet, "/api/probe", "", nil, c); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired cookie status = %d", rec.Code)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	_, h := newTestAuth(t)
	c := login(t, h)
	if rec := do(h, http.MethodPost, "/api/logout", "", sameOrigin, c); rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/probe", "", nil, c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status = %d, want 401", rec.Code)
	}
}

func TestCrossSiteMutationsAreBlocked(t *testing.T) {
	_, h := newTestAuth(t)
	c := login(t, h)

	cases := map[string]map[string]string{
		"foreign origin":    {"Origin": "https://evil.test"},
		"foreign referer":   {"Referer": "https://evil.test/page"},
		"no origin/referer": {},
		"null origin":       {"Origin": "null"},
	}
	for name, headers := range cases {
		if rec := do(h, http.MethodPost, "/api/probe", "{}", headers, c); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
	if rec := do(h, http.MethodPost, "/api/login", `{"password":"`+testPassword+`"}`, map[string]string{"Origin": "https://evil.test"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site login status = %d, want 403", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/probe", "{}", sameOrigin, c); rec.Code != http.StatusOK {
		t.Errorf("same-origin POST status = %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/probe", "{}", map[string]string{"Origin": "http://localhost:5173"}, c); rec.Code != http.StatusOK {
		t.Errorf("allowed dev origin POST status = %d", rec.Code)
	}
}

func TestCORSOnlyForAllowedOrigins(t *testing.T) {
	_, h := newTestAuth(t)
	rec := do(h, http.MethodGet, "/api/health", "", map[string]string{"Origin": "https://evil.test"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("foreign origin got ACAO %q", got)
	}
	rec = do(h, http.MethodGet, "/api/health", "", map[string]string{"Origin": "http://localhost:5173"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("allowed origin ACAO = %q", got)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Error("wildcard CORS")
	}
}

func TestMissingOrShortPasswordKeepsPanelLocked(t *testing.T) {
	for _, pw := range []string{"", "short"} {
		a := NewAuthenticator(pw, "", nil)
		h := NewHandler(Options{Auth: a})
		if rec := do(h, http.MethodPost, "/api/login", `{"password":"`+pw+`"}`, sameOrigin); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("password %q: login status = %d, want 503", pw, rec.Code)
		}
		if rec := do(h, http.MethodGet, "/api/status", "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("password %q: status endpoint = %d, want 401", pw, rec.Code)
		}
	}
}

func TestClientIPTrustsProxyHeaderOnlyFromPrivatePeer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Real-IP", "198.51.100.1")

	r.RemoteAddr = "172.18.0.1:5555"
	if got := clientIP(r); got != "198.51.100.1" {
		t.Errorf("behind proxy = %q", got)
	}
	r.RemoteAddr = "203.0.113.9:5555"
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("spoofed header from public peer = %q", got)
	}
}

func TestSPAFallbackAndNoTraversal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>panel</html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("js"), 0o644)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	os.WriteFile(secret, []byte("top secret"), 0o644)
	defer os.Remove(secret)

	a := NewAuthenticator(testPassword, "", nil)
	h := NewHandler(Options{Auth: a, FrontendDir: dir})

	if rec := do(h, http.MethodGet, "/financeiro/transacoes", "", nil); !strings.Contains(rec.Body.String(), "panel") {
		t.Errorf("client route did not fall back to index.html: %q", rec.Body)
	}
	rec := do(h, http.MethodGet, "/assets/app.js", "", nil)
	if rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: body %q cache %q", rec.Body, rec.Header().Get("Cache-Control"))
	}
	for _, target := range []string{"/../secret.txt", "/assets/../../secret.txt", "/%2e%2e/secret.txt"} {
		if rec := do(h, http.MethodGet, target, "", nil); strings.Contains(rec.Body.String(), "top secret") {
			t.Errorf("%s escaped the frontend dir", target)
		}
	}
}
