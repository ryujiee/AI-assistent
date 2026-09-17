package whatsapp

import (
	"context"
	"encoding/base64"
	"errors"
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

type WhatsAppMessage struct {
	SenderJID  string
	Text       string
	ImageBytes []byte
	ImageMime  string
	AudioBytes []byte
}

var (
	Client          *whatsmeow.Client
	QRMutex         sync.RWMutex
	LatestQRCode    string
	IsConnected     bool
	MessageCallback func(msg *WhatsAppMessage)

	// qrFlowMutex serializes the pairing flow. Only one QR channel may be open
	// at a time, otherwise two goroutines would race to publish LatestQRCode.
	qrFlowMutex sync.Mutex
)

// ErrAlreadyPaired is returned when a new QR code is requested but the device
// is already linked to a phone. WhatsApp only issues pairing codes for an
// unlinked device, so the session has to be dropped first.
var ErrAlreadyPaired = errors.New("este dispositivo já está vinculado a um WhatsApp")

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
		go func() {
			if err := StartQRFlow(); err != nil {
				log.Printf("Failed to start the initial QR flow: %v", err)
			}
		}()
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

// StartQRFlow opens a pairing session and starts publishing QR codes.
func StartQRFlow() error {
	qrFlowMutex.Lock()
	defer qrFlowMutex.Unlock()
	return startQRFlow()
}

// RestartQRFlow throws away the current pairing attempt and asks WhatsApp for a
// brand new QR code.
//
// A QR code is only valid for a short window, and the codes WhatsApp hands out
// in one batch eventually run out. When that happens the image on screen is
// stale and scanning it does nothing, so the user needs a way to ask for a
// fresh one instead of waiting for a restart of the process.
func RestartQRFlow() error {
	qrFlowMutex.Lock()
	defer qrFlowMutex.Unlock()

	if Client == nil {
		return errors.New("cliente do WhatsApp não inicializado")
	}
	if Client.Store.ID != nil {
		return ErrAlreadyPaired
	}

	// Disconnecting closes the previous QR channel, which ends the goroutine
	// reading from it and unregisters its event handler.
	Client.Disconnect()

	QRMutex.Lock()
	LatestQRCode = ""
	IsConnected = false
	QRMutex.Unlock()

	log.Println("Discarded the previous pairing attempt, requesting a new QR code...")
	return startQRFlow()
}

// startQRFlow requires qrFlowMutex to be held by the caller.
func startQRFlow() error {
	if Client == nil {
		return errors.New("cliente do WhatsApp não inicializado")
	}

	qrChan, err := Client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get QR channel: %w", err)
	}

	if err := Client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	go consumeQRChannel(qrChan)
	return nil
}

func consumeQRChannel(qrChan <-chan whatsmeow.QRChannelItem) {
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
			// Every other event is terminal: the code batch ran out, the
			// socket dropped or the pairing failed. Clear the image so the
			// interface stops offering a code that no longer works.
			log.Printf("QR Flow Event: %v (pairing attempt ended)", evt.Event)
			QRMutex.Lock()
			LatestQRCode = ""
			QRMutex.Unlock()
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
		if v.Info.Sender.Server == "lid" && Client != nil && Client.Store != nil {
			pnJID, err := Client.Store.LIDs.GetPNForLID(context.Background(), v.Info.Sender)
			if err == nil && !pnJID.IsEmpty() {
				senderJID = pnJID.User + "@" + pnJID.Server
				log.Printf("Resolved LID %s to PN JID %s", v.Info.Sender.String(), senderJID)
			}
		}

		var text string
		var imageBytes []byte
		var imageMime string
		var audioBytes []byte

		if v.Message.GetConversation() != "" {
			text = v.Message.GetConversation()
		} else if v.Message.GetExtendedTextMessage().GetText() != "" {
			text = v.Message.GetExtendedTextMessage().GetText()
		} else if v.Message.ImageMessage != nil {
			text = v.Message.GetImageMessage().GetCaption()
			imageMime = v.Message.GetImageMessage().GetMimetype()
			
			log.Println("Downloading image from WhatsApp message...")
			data, err := Client.Download(context.Background(), v.Message.GetImageMessage())
			if err != nil {
				log.Printf("Failed to download image: %v", err)
			} else {
				imageBytes = data
				log.Printf("Downloaded image (%d bytes)", len(data))
			}
		} else if v.Message.AudioMessage != nil {
			log.Println("Downloading audio note from WhatsApp message...")
			data, err := Client.Download(context.Background(), v.Message.GetAudioMessage())
			if err != nil {
				log.Printf("Failed to download audio: %v", err)
			} else {
				audioBytes = data
				log.Printf("Downloaded audio (%d bytes)", len(data))
			}
		} else {
			return
		}

		if text == "" && len(imageBytes) == 0 && len(audioBytes) == 0 {
			return
		}

		log.Printf("Received message from %s (hasText: %t, hasImage: %t, hasAudio: %t)", 
			senderJID, text != "", len(imageBytes) > 0, len(audioBytes) > 0)

		if MessageCallback != nil {
			go MessageCallback(&WhatsAppMessage{
				SenderJID:  senderJID,
				Text:       text,
				ImageBytes: imageBytes,
				ImageMime:  imageMime,
				AudioBytes: audioBytes,
			})
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
		go func() {
			if err := StartQRFlow(); err != nil {
				log.Printf("Failed to restart the QR flow after logout: %v", err)
			}
		}()
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
