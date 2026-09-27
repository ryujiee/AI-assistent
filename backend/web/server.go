package web

import (
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"secretary/db"
	"secretary/whatsapp"
)

type StatusResponse struct {
	Connected bool   `json:"connected"`
	QRCode    string `json:"qrcode"`
	ActiveJID string `json:"active_jid"`
}

type ConfigRequest struct {
	JID string `json:"jid"`
}

type ConfigResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Options wires the HTTP layer. Modules register their protected routes via
// ProtectedRoutes; everything under /api/ except health, session, login and
// logout requires a session.
type Options struct {
	Auth            *Authenticator
	FrontendDir     string
	ProtectedRoutes []func(mux *http.ServeMux)
}

func NewHandler(opts Options) http.Handler {
	auth := opts.Auth

	api := http.NewServeMux()
	api.HandleFunc("/api/status", handleStatus)
	api.HandleFunc("/api/config", handleConfig)
	api.HandleFunc("/api/qrcode/refresh", handleRefreshQRCode)
	for _, register := range opts.ProtectedRoutes {
		register(api)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/session", auth.HandleSession)
	mux.Handle("/api/login", auth.CheckOrigin(http.HandlerFunc(auth.HandleLogin)))
	mux.Handle("/api/logout", auth.CheckOrigin(http.HandlerFunc(auth.HandleLogout)))
	mux.Handle("/api/", auth.CheckOrigin(auth.RequireAuth(api)))

	if dir := opts.FrontendDir; dir != "" {
		slog.Info("web.frontend", "dir", dir)
		mux.Handle("/", spaHandler(dir))
	}

	return securityHeaders(auth.CORS(mux))
}

func StartServer(port string, handler http.Handler) {
	log.Printf("Web server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}

// FindFrontendDir returns the first directory holding the built panel.
func FindFrontendDir() string {
	for _, dir := range []string{"../frontend/dist", "./frontend/dist", "./frontend"} {
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
			return dir
		}
	}
	slog.Warn("web.frontend_missing", "hint", "run npm run build in frontend/")
	return ""
}

// spaHandler serves the Vite build and falls back to index.html so client
// side routes (/financeiro, /secretaria) survive a page reload.
// panelCSP allows only the panel's own code; styles and fonts also from
// Google Fonts (Inter). Images may be data: URLs (the pairing QR code).
const panelCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; img-src 'self' data: blob:; connect-src 'self'; frame-src 'self'; " +
	"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"

func spaHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", panelCSP)
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/api/") {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean)))
		if err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		if strings.HasPrefix(clean, "/assets/") {
			// Vite fingerprints everything under /assets.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	whatsapp.QRMutex.RLock()
	connected := whatsapp.IsConnected
	qrCode := whatsapp.LatestQRCode
	whatsapp.QRMutex.RUnlock()

	activeJID, err := db.GetActiveJID()
	if err != nil {
		activeJID = ""
	}

	res := StatusResponse{
		Connected: connected,
		QRCode:    qrCode,
		ActiveJID: activeJID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(res)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	jid := strings.TrimSpace(req.JID)
	if jid == "" {
		http.Error(w, "JID cannot be empty", http.StatusBadRequest)
		return
	}

	if !strings.Contains(jid, "@") {
		jid = jid + "@s.whatsapp.net"
	}

	if err := db.SaveJID(jid); err != nil {
		log.Printf("Failed to save target JID: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ConfigResponse{Success: false, Message: "Erro interno ao salvar JID"})
		return
	}

	log.Printf("Configured target WhatsApp JID")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ConfigResponse{Success: true, Message: "JID configurado com sucesso"})
}

// handleRefreshQRCode drops the current pairing attempt and asks WhatsApp for a
// new QR code. The codes expire quickly, so a code left on screen for a while
// is usually dead and scanning it does nothing.
func handleRefreshQRCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	err := whatsapp.RestartQRFlow()
	switch {
	case err == nil:
		log.Println("New QR code requested through the web interface")
		json.NewEncoder(w).Encode(ConfigResponse{Success: true, Message: "Gerando um novo QR Code..."})

	case errors.Is(err, whatsapp.ErrAlreadyPaired):
		// Not an internal failure: there is nothing to pair, so say so instead
		// of dropping a working session.
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(ConfigResponse{
			Success: false,
			Message: "O WhatsApp já está vinculado. Desvincule este aparelho no celular (Aparelhos Conectados) para gerar um novo código.",
		})

	default:
		log.Printf("Failed to generate a new QR code: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ConfigResponse{Success: false, Message: "Não foi possível gerar um novo QR Code agora. Tente novamente em instantes."})
	}
}
