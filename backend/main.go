package main

import (
	"log"

	"secretary/config"
	"secretary/db"
	"secretary/engine"
	"secretary/openai"
	"secretary/web"
	"secretary/whatsapp"
)

func main() {
	log.Println("Starting AI Personal Secretary backend...")

	// 1. Load configuration
	cfg := config.LoadConfig()

	// 2. Connect to database
	db.ConnectDB(cfg.DatabaseURL)
	db.RunMigrations()

	// 3. Initialize OpenAI Client
	openai.InitOpenAI(cfg.OpenAIAPIKey)

	// 4. Bind hooks to avoid circular packages dependency
	openai.RegisterTimerCallback = engine.RegisterDynamicTimer
	engine.ProcessMessageFunc = openai.ProcessMessage

	// 5. Setup WhatsApp client Message Event handler
	whatsapp.MessageCallback = func(senderJID string, text string) {
		// Rule: Only respond to messages from the configured JID in the database
		activeJID, err := db.GetActiveJID()
		if err != nil || activeJID == "" {
			log.Printf("Ignored message from %s: no active target contact JID configured", senderJID)
			return
		}

		if senderJID != activeJID {
			log.Printf("Ignored message from %s: does not match target contact JID (%s)", senderJID, activeJID)
			return
		}

		log.Printf("Processing message from target JID: %s", text)

		// Send user query to OpenAI and process tools loop
		response, err := openai.ProcessMessage(senderJID, text)
		if err != nil {
			log.Printf("Error processing message through OpenAI: %v", err)
			return
		}

		// Send reply back to WhatsApp
		if err := whatsapp.SendMessage(senderJID, response); err != nil {
			log.Printf("Failed to send WhatsApp response: %v", err)
		} else {
			log.Printf("Replied successfully to JID: %s", senderJID)
		}
	}

	// 6. Initialize WhatsApp Client (which will log in or start QR Flow)
	whatsapp.InitWhatsApp(cfg.DatabaseURL)

	// 7. Start Engine schedulers, cron jobs and alert loops
	engine.StartScheduler()

	// 8. Start HTTP API Web Server (blocks execution)
	web.StartServer(cfg.Port)
}
