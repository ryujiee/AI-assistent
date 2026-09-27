package config

import (
	"os"
	"strings"
)

type Config struct {
	DatabaseURL  string
	OpenAIAPIKey string
	Port         string

	// AdminPassword protects the web panel. When it is missing or too short
	// the panel stays locked instead of running without authentication.
	AdminPassword string
	// SessionSecret signs the session cookie. When empty a random secret is
	// generated at boot, which logs everybody out on every restart.
	SessionSecret string
	// CORSAllowedOrigins lists extra origins allowed to call the API with
	// credentials. The panel itself is served from the same origin, so this is
	// empty in production.
	CORSAllowedOrigins []string

	// FinanceEnabled turns the finance module on. It never creates tables:
	// the finance migrations are applied manually with "secretary migrate apply".
	FinanceEnabled bool
	// WhatsAppFake replaces the real WhatsApp connection with an in-memory
	// fake for local development ("connected" or "qr"). Never set in production.
	WhatsAppFake string
	// WhatsAppLogLevel is the whatsmeow log level (INFO by default: DEBUG dumps
	// protocol traffic).
	WhatsAppLogLevel string
}

func LoadConfig() *Config {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://secretary_user:secretary_password@localhost:5432/secretary_db?sslmode=disable"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	waLogLevel := strings.ToUpper(os.Getenv("WHATSAPP_LOG_LEVEL"))
	if waLogLevel == "" {
		waLogLevel = "INFO"
	}

	return &Config{
		DatabaseURL:        dbURL,
		OpenAIAPIKey:       os.Getenv("OPENAI_API_KEY"),
		Port:               port,
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		SessionSecret:      os.Getenv("SESSION_SECRET"),
		CORSAllowedOrigins: splitList(os.Getenv("CORS_ALLOWED_ORIGINS")),
		FinanceEnabled:     isTrue(os.Getenv("FINANCE_ENABLED")),
		WhatsAppFake:       strings.ToLower(strings.TrimSpace(os.Getenv("WHATSAPP_FAKE"))),
		WhatsAppLogLevel:   waLogLevel,
	}
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
