package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"secretary/config"
	"secretary/db"
	"secretary/engine"
	"secretary/finance"
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

	// 5. Private chat with the configured number: the Secretária. The router
	// only hands over messages from that number, so no check is needed here.
	whatsapp.MessageCallback = func(msg *whatsapp.WhatsAppMessage) {
		text := msg.Text
		if len(msg.AudioBytes) > 0 {
			transcription, err := openai.TranscribeAudio(msg.AudioBytes)
			if err != nil {
				log.Printf("Failed to transcribe audio note: %v", err)
				_ = whatsapp.SendMessage(msg.SenderJID, "⚠️ Desculpe, não consegui processar seu áudio.")
				return
			}
			text = "[Áudio Transcrito]: " + transcription
		}

		response, err := openai.ProcessMessageMultimodal(msg.SenderJID, text, msg.ImageBytes, msg.ImageMime)
		if err != nil {
			log.Printf("Error processing message through OpenAI: %v", err)
			return
		}
		if err := whatsapp.SendMessage(msg.SenderJID, response); err != nil {
			log.Printf("Failed to send WhatsApp response: %v", err)
		}
	}

	// 6. Finance module: messages of the linked group go to the ingestor.
	var gateway finance.Gateway = whatsapp.RealGateway{}
	var fakeGateway *whatsapp.FakeGateway
	if cfg.WhatsAppFake != "" {
		fakeGateway = whatsapp.NewFakeGateway()
		gateway = fakeGateway
	}
	financeSvc := finance.NewService(db.Pool)
	llm := openai.Client()
	if cfg.OpenAIFake {
		llm = finance.FakeLLM{}
		log.Println("OPENAI_FAKE is set: the finance agent uses the local rule-based fake")
	} else if cfg.OpenAIAPIKey == "" {
		llm = nil
	}
	extract := finance.NewOpenAIExtractor(openai.NewStructuredClient(), "gpt-4o")
	if cfg.OpenAIFake {
		extract = finance.FakeExtractor
	}
	agent := &finance.Agent{
		Svc:        financeSvc,
		LLM:        llm,
		Download:   gateway.Download,
		Transcribe: func(_ context.Context, audio []byte) (string, error) { return openai.TranscribeAudio(audio) },
		Receipts:   &finance.ReceiptReader{Svc: financeSvc, Download: gateway.Download, Extract: extract},
	}
	ingestor := finance.NewIngestor(financeSvc, gateway, agent.Handle)
	whatsapp.Routes = whatsapp.RouteConfig{
		TargetJID: func() string {
			jid, _ := db.GetActiveJID()
			return jid
		},
		IsFinanceGroup: ingestor.IsLinked,
	}
	// No group is routed to finance until the ingestor loads the linked groups.
	whatsapp.FinanceHandler = ingestor.Accept
	if cfg.FinanceEnabled {
		go startFinance(ingestor)
	}

	// 7. Initialize WhatsApp Client (which will log in or start QR Flow)
	if cfg.WhatsAppFake != "" {
		whatsapp.InitFake(cfg.WhatsAppFake)
	} else {
		whatsapp.InitWhatsApp(cfg.DatabaseURL, cfg.WhatsAppLogLevel)
	}

	// 8. Start Engine schedulers, cron jobs and alert loops
	engine.StartScheduler()

	// 9. Start HTTP API Web Server (blocks execution)
	auth := web.NewAuthenticator(cfg.AdminPassword, cfg.SessionSecret, cfg.CORSAllowedOrigins)
	financeAPI := &web.FinanceAPI{Enabled: cfg.FinanceEnabled, Svc: financeSvc, Ingestor: ingestor, Fake: fakeGateway}
	web.StartServer(cfg.Port, web.NewHandler(web.Options{
		Auth:            auth,
		FrontendDir:     web.FindFrontendDir(),
		ProtectedRoutes: []func(*http.ServeMux){financeAPI.Register},
	}))
}

// startFinance waits for the finance migration (applied manually) and then
// starts consuming the linked group. Until then group messages are ignored.
func startFinance(ingestor *finance.Ingestor) {
	ctx := context.Background()
	for warned := false; ; warned = true {
		if ok, err := db.IsApplied(ctx, db.Pool, web.FinanceMigration); err == nil && ok {
			break
		}
		if !warned {
			log.Printf("FINANCE_ENABLED is set but migration %s is not applied; waiting for: secretary migrate apply", web.FinanceMigration)
		}
		time.Sleep(30 * time.Second)
	}
	if err := ingestor.Start(ctx); err != nil {
		log.Printf("Finance module failed to start: %v", err)
		return
	}
	log.Println("Finance module started")
}
