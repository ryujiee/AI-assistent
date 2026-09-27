package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "secretary_session"
	sessionTTL        = 7 * 24 * time.Hour
	// minPasswordLength keeps a trivially guessable ADMIN_PASSWORD from
	// unlocking a panel that exposes the WhatsApp pairing QR code and financial data.
	minPasswordLength = 12

	loginWindow         = 15 * time.Minute
	loginMaxPerIP       = 5
	loginMaxGlobal      = 30
	loginGlobalCooldown = 5 * time.Minute
)

// Authenticator guards the panel with a single admin password and a signed,
// stateless session cookie. The cookie carries only a random session id and
// an expiry; logout revokes the id in memory until it would have expired.
type Authenticator struct {
	configured     bool
	passwordDigest [32]byte
	secret         []byte
	now            func() time.Time
	limiter        *loginLimiter
	allowedOrigins map[string]bool

	mu      sync.Mutex
	revoked map[string]time.Time
}

func NewAuthenticator(password, sessionSecret string, allowedOrigins []string) *Authenticator {
	a := &Authenticator{
		now:            time.Now,
		revoked:        map[string]time.Time{},
		allowedOrigins: map[string]bool{},
	}
	a.limiter = newLoginLimiter(func() time.Time { return a.now() })
	for _, o := range allowedOrigins {
		a.allowedOrigins[strings.TrimRight(o, "/")] = true
	}

	if len(password) >= minPasswordLength {
		a.configured = true
		a.passwordDigest = sha256.Sum256([]byte(password))
	} else {
		slog.Error("auth.not_configured", "reason", "ADMIN_PASSWORD missing or shorter than 12 characters; the panel stays locked")
	}

	if len(sessionSecret) >= 32 {
		a.secret = []byte(sessionSecret)
	} else {
		a.secret = make([]byte, 32)
		if _, err := rand.Read(a.secret); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		slog.Warn("auth.ephemeral_secret", "reason", "SESSION_SECRET missing or shorter than 32 characters; sessions end on every restart")
	}
	return a
}

// checkPassword compares digests so neither the content nor the length of the
// password leaks through timing.
func (a *Authenticator) checkPassword(candidate string) bool {
	if !a.configured {
		return false
	}
	d := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(d[:], a.passwordDigest[:]) == 1
}

func (a *Authenticator) sign(payload string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Authenticator) newSessionValue() (string, time.Time) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	exp := a.now().Add(sessionTTL)
	payload := "v1|" + hex.EncodeToString(id) + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + a.sign(payload), exp
}

// parseSession returns the session id when the cookie is authentic, not
// expired and not revoked.
func (a *Authenticator) parseSession(value string) (string, bool) {
	encoded, sig, ok := strings.Cut(value, ".")
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(a.sign(payload))) {
		return "", false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 || parts[0] != "v1" {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return "", false
	}
	a.mu.Lock()
	_, revoked := a.revoked[parts[1]]
	a.mu.Unlock()
	if revoked {
		return "", false
	}
	return parts[1], true
}

func (a *Authenticator) revoke(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, exp := range a.revoked {
		if now.After(exp) {
			delete(a.revoked, k)
		}
	}
	a.revoked[id] = now.Add(sessionTTL)
}

// Authenticated reports whether the request carries a valid session.
func (a *Authenticator) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	_, ok := a.parseSession(c.Value)
	return ok
}

// RequireAuth rejects requests without a valid session.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Authenticated(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Faça login para continuar.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckOrigin blocks cross-site state-changing requests. The session cookie is
// SameSite=Strict already; the Origin (or Referer) check is the second layer
// for browsers or proxies that do not honor it.
func (a *Authenticator) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		source := r.Header.Get("Origin")
		if source == "" || source == "null" {
			source = r.Header.Get("Referer")
		}
		if !a.sameOrAllowedOrigin(r, source) {
			writeError(w, http.StatusForbidden, "forbidden_origin", "Origem da requisição não permitida.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) sameOrAllowedOrigin(r *http.Request, source string) bool {
	if source == "" {
		return false
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return a.allowedOrigins[u.Scheme+"://"+u.Host]
}

// CORS only answers origins explicitly listed in CORS_ALLOWED_ORIGINS. The
// panel is same-origin, so in production no CORS header is ever sent.
func (a *Authenticator) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" && a.allowedOrigins[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		// Browsers accept Secure cookies on http://localhost, so development
		// works without a flag that could be left off in production.
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *Authenticator) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Método não permitido.")
		return
	}
	if !a.configured {
		writeError(w, http.StatusServiceUnavailable, "auth_not_configured",
			"Login indisponível: configure ADMIN_PASSWORD (mínimo de 12 caracteres) no servidor.")
		return
	}

	ip := clientIP(r)
	if wait, blocked := a.limiter.blocked(ip); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"Muitas tentativas. Aguarde alguns minutos e tente novamente.")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Requisição inválida.")
		return
	}

	if !a.checkPassword(req.Password) {
		a.limiter.fail(ip)
		slog.Warn("auth.login_failed", "ip_hash", hashForLog(ip))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Senha incorreta.")
		return
	}

	a.limiter.reset(ip)
	value, _ := a.newSessionValue()
	a.setCookie(w, value, int(sessionTTL.Seconds()))
	slog.Info("auth.login_ok", "ip_hash", hashForLog(ip))
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (a *Authenticator) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Método não permitido.")
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if id, ok := a.parseSession(c.Value); ok {
			a.revoke(id)
		}
	}
	a.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (a *Authenticator) HandleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":   a.Authenticated(r),
		"auth_configured": a.configured,
	})
}

// clientIP trusts X-Real-IP only when the direct peer is a local proxy
// (nginx on the host reaches the container through a private address).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer != nil && (peer.IsLoopback() || peer.IsPrivate()) {
		if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
			return real.String()
		}
	}
	return host
}

// hashForLog keeps identifiers correlatable in logs without writing them out.
func hashForLog(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:12]
}

// loginLimiter blocks an IP after loginMaxPerIP failures inside loginWindow,
// and every login for loginGlobalCooldown when failures pile up from many
// addresses (the IP seen behind a proxy is not always trustworthy).
//
// ponytail: in-memory, per-process state; move to the database if the backend
// ever runs more than one replica.
type loginLimiter struct {
	now func() time.Time

	mu            sync.Mutex
	perIP         map[string][]time.Time
	global        []time.Time
	blockedGlobal time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, perIP: map[string][]time.Time{}}
}

func prune(ts []time.Time, since time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(since) {
			out = append(out, t)
		}
	}
	return out
}

func (l *loginLimiter) blocked(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Before(l.blockedGlobal) {
		return l.blockedGlobal.Sub(now), true
	}
	fails := prune(l.perIP[ip], now.Add(-loginWindow))
	l.perIP[ip] = fails
	if len(fails) >= loginMaxPerIP {
		return fails[0].Add(loginWindow).Sub(now), true
	}
	return 0, false
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	since := now.Add(-loginWindow)
	l.perIP[ip] = append(prune(l.perIP[ip], since), now)
	l.global = append(prune(l.global, since), now)
	if len(l.global) >= loginMaxGlobal {
		l.blockedGlobal = now.Add(loginGlobalCooldown)
		l.global = nil
		slog.Warn("auth.login_global_cooldown")
	}
	if len(l.perIP) > 10000 {
		for k, v := range l.perIP {
			if len(prune(v, since)) == 0 {
				delete(l.perIP, k)
			}
		}
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.perIP, ip)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers with a stable code for the frontend and a friendly
// message; internal error details never reach the client.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
