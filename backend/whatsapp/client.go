package whatsapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"

	_ "github.com/lib/pq"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var (
	Client          *whatsmeow.Client
	QRMutex         sync.RWMutex
	LatestQRCode    string
	IsConnected     bool
	MessageCallback func(jid string, text string)
)

func InitWhatsApp(dbURL string) {
	dbLog := waLog.Stdout("Database", "DEBUG", true)
	container, err := sqlstore.New(context.Background(), "postgres", dbURL, dbLog)
	if err != nil {
		log.Fatalf("Failed to initialize whatsmeow sqlstore: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", "DEBUG", true)
	Client = whatsmeow.NewClient(deviceStore, clientLog)
	Client.AddEventHandler(eventHandler)

	if Client.Store.ID == nil {
		go runQRFlow()
	} else {
		err = Client.Connect()
		if err != nil {
			log.Printf("Failed to connect to WhatsApp: %v", err)
		} else {
			QRMutex.Lock()
			IsConnected = true
			QRMutex.Unlock()
			log.Println("Connected to WhatsApp successfully with saved session")
		}
	}
}

func runQRFlow() {
	qrChan, err := Client.GetQRChannel(context.Background())
	if err != nil {
		log.Printf("Failed to get QR channel: %v", err)
		return
	}

	err = Client.Connect()
	if err != nil {
		log.Printf("Failed to connect: %v", err)
		return
	}

	for evt := range qrChan {
		if evt.Event == "code" {
			png, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
			if err != nil {
				log.Printf("Failed to encode QR code: %v", err)
				continue
			}
			base64Img := base64.StdEncoding.EncodeToString(png)
			QRMutex.Lock()
			LatestQRCode = "data:image/png;base64," + base64Img
			QRMutex.Unlock()
			log.Println("New QR Code generated. Scan via web interface.")
		} else if evt.Event == "success" {
			log.Println("WhatsApp login success!")
			QRMutex.Lock()
			LatestQRCode = ""
			IsConnected = true
			QRMutex.Unlock()
		} else {
			log.Printf("QR Flow Event: %v", evt.Event)
		}
	}
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			return
		}

		senderJID := v.Info.Sender.User + "@" + v.Info.Sender.Server
		var text string

		if v.Message.GetConversation() != "" {
			text = v.Message.GetConversation()
		} else if v.Message.GetExtendedTextMessage().GetText() != "" {
			text = v.Message.GetExtendedTextMessage().GetText()
		} else {
			return
		}

		if text == "" {
			return
		}

		log.Printf("Received message from %s: %s", senderJID, text)
		if MessageCallback != nil {
			go MessageCallback(senderJID, text)
		}

	case *events.Connected:
		QRMutex.Lock()
		IsConnected = true
		LatestQRCode = ""
		QRMutex.Unlock()

	case *events.LoggedOut:
		QRMutex.Lock()
		IsConnected = false
		QRMutex.Unlock()
		log.Println("Logged out from WhatsApp. Re-running login flow...")
		go runQRFlow()
	}
}

func SendMessage(jid string, text string) error {
	targetJID, err := types.ParseJID(jid)
	if err != nil {
		return fmt.Errorf("invalid JID: %w", err)
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	_, err = Client.SendMessage(context.Background(), targetJID, msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}
