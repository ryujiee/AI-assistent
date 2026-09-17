package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
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

func StartServer(port string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/qrcode/refresh", handleRefreshQRCode)

	// Serve static files from frontend folder if available
	frontendDir := "../frontend"
	if _, err := os.Stat(frontendDir); os.IsNotExist(err) {
		frontendDir = "./frontend"
	}
	if _, err := os.Stat(frontendDir); err == nil {
		log.Printf("Serving static frontend files from: %s", frontendDir)
		fs := http.FileServer(http.Dir(frontendDir))
		mux.Handle("/", fs)
	}

	handler := enableCORS(mux)

	log.Printf("Web server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
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
	json.NewEncoder(w).Encode(res)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	log.Printf("Configured target WhatsApp JID to: %s", jid)

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
		json.NewEncoder(w).Encode(ConfigResponse{Success: false, Message: "Não foi possível gerar um novo QR Code: " + err.Error()})
	}
}

func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
