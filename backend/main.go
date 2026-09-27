package main

import (
	"context"
	"log"
	"os"

	"secretary/config"
	"secretary/db"
	"secretary/engine"
	"secretary/openai"
	"secretary/web"
	"secretary/whatsapp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrateCommand(os.Args[2:]))
	}

	log.Println("Starting AI Personal Secretary backend...")

	// 1. Load configuration
	cfg := config.LoadConfig()

	// 2. Connect to database
	db.ConnectDB(cfg.DatabaseURL)
	// Only the 001 baseline (the tables the app always created at boot) is
	// applied automatically. Everything else goes through "secretary migrate apply".
	if err := db.EnsureBaseline(context.Background(), db.Pool); err != nil {
		log.Fatalf("Failed to apply the baseline migration: %v", err)
	}
	if pending, err := db.PendingMigrations(context.Background(), db.Pool); err == nil && len(pending) > 0 {
		log.Printf("Pending database migrations: %v (run: secretary migrate apply)", pending)
	}

	// 3. Initialize OpenAI Client
	openai.InitOpenAI(cfg.OpenAIAPIKey)

	// 4. Bind hooks to avoid circular packages dependency
	openai.RegisterTimerCallback = engine.RegisterDynamicTimer
	engine.ProcessMessageFunc = openai.ProcessMessage

	// 5. Setup WhatsApp client Message Event handler
	whatsapp.MessageCallback = func(msg *whatsapp.WhatsAppMessage) {
		// Rule: Only respond to messages from the configured JID in the database
		activeJID, err := db.GetActiveJID()
		if err != nil || activeJID == "" {
			log.Printf("Ignored message from %s: no active target contact JID configured", msg.SenderJID)
			return
		}

		if msg.SenderJID != activeJID {
			log.Printf("Ignored message from %s: does not match target contact JID (%s)", msg.SenderJID, activeJID)
			return
		}

		text := msg.Text
		if len(msg.AudioBytes) > 0 {
			log.Println("Transcribing voice note using OpenAI Whisper...")
			transcription, err := openai.TranscribeAudio(msg.AudioBytes)
			if err != nil {
				log.Printf("Failed to transcribe audio note: %v", err)
				_ = whatsapp.SendMessage(msg.SenderJID, "⚠️ Desculpe, não consegui processar seu áudio.")
				return
			}
			log.Printf("Transcription complete: \"%s\"", transcription)
			text = "[Áudio Transcrito]: " + transcription
		}

		log.Printf("Processing message from target JID: (Text: %s, HasImage: %t)", text, len(msg.ImageBytes) > 0)

		// Send user query (with optional image) to OpenAI
		response, err := openai.ProcessMessageMultimodal(msg.SenderJID, text, msg.ImageBytes, msg.ImageMime)
		if err != nil {
			log.Printf("Error processing message through OpenAI: %v", err)
			return
		}

		// Send reply back to WhatsApp
		if err := whatsapp.SendMessage(msg.SenderJID, response); err != nil {
			log.Printf("Failed to send WhatsApp response: %v", err)
		} else {
			log.Printf("Replied successfully to JID: %s", msg.SenderJID)
		}
	}

	// 6. Initialize WhatsApp Client (which will log in or start QR Flow)
	if cfg.WhatsAppFake != "" {
		whatsapp.InitFake(cfg.WhatsAppFake)
	} else {
		whatsapp.InitWhatsApp(cfg.DatabaseURL, cfg.WhatsAppLogLevel)
	}

	// 7. Start Engine schedulers, cron jobs and alert loops
	engine.StartScheduler()

	// 8. Start HTTP API Web Server (blocks execution)
	auth := web.NewAuthenticator(cfg.AdminPassword, cfg.SessionSecret, cfg.CORSAllowedOrigins)
	web.StartServer(cfg.Port, web.NewHandler(web.Options{
		Auth:        auth,
		FrontendDir: web.FindFrontendDir(),
	}))
}
