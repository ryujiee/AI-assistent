package whatsapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

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
	// currentQRSession tracks the live pairing session so a new one can wait
	// for it to shut down. Guarded by qrFlowMutex.
	currentQRSession *qrSession
)

// qrSession is one pairing attempt: a QR channel plus the goroutine reading it.
type qrSession struct {
	// done is closed when the reader goroutine has exited, which means
	// whatsmeow has closed the channel and stopped feeding it events.
	done chan struct{}
}

// qrSessionShutdownTimeout bounds how long a restart waits for the previous
// pairing session to close before giving up on a clean handover.
const qrSessionShutdownTimeout = 10 * time.Second

// ErrAlreadyPaired is returned when a new QR code is requested but the device
// is already linked to a phone. WhatsApp only issues pairing codes for an
// unlinked device, so the session has to be dropped first.
var ErrAlreadyPaired = errors.New("este dispositivo já está vinculado a um WhatsApp")

func InitWhatsApp(dbURL, logLevel string) {
	dbLog := waLog.Stdout("Database", logLevel, true)
	container, err := sqlstore.New(context.Background(), "postgres", dbURL, dbLog)
	if err != nil {
		log.Fatalf("Failed to initialize whatsmeow sqlstore: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", logLevel, true)
	Client = whatsmeow.NewClient(deviceStore, clientLog)
	Client.AddEventHandler(eventHandler)

	if Client.Store.ID == nil {
		go func() {
			if err := StartQRFlow(); err != nil {
				log.Printf("Failed to start the initial QR flow: %v", err)
			}
		}()
	} else {
		// IsConnected flips on events.Connected, once the login really completed.
		if err := Client.Connect(); err != nil {
			log.Printf("Failed to connect to WhatsApp: %v", err)
		} else {
			log.Println("Connecting to WhatsApp with the saved session...")
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

	// Tear the previous pairing session down completely before opening a new
	// one. whatsmeow registers one event handler per QR channel, and a handler
	// that has not closed yet also picks up the codes of the next session and
	// republishes them from its own goroutine, so every restart would leave
	// another writer fighting over LatestQRCode.
	if currentQRSession != nil || Client.IsConnected() {
		Client.Disconnect()
	}
	if session := currentQRSession; session != nil {
		select {
		case <-session.done:
		case <-time.After(qrSessionShutdownTimeout):
			log.Println("Timed out waiting for the previous QR session to close, continuing anyway")
		}
		currentQRSession = nil
	}

	qrChan, err := Client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get QR channel: %w", err)
	}

	if err := Client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	session := &qrSession{done: make(chan struct{})}
	currentQRSession = session
	go func() {
		defer close(session.done)
		consumeQRChannel(qrChan)
	}()
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
		// Metadata first, routing second, downloads last: media of chats the
		// app does not handle is never fetched.
		m, ok := ExtractMessage(v, resolvePN)
		if !ok {
			return
		}
		Dispatch(m)

	case *events.Connected:
		QRMutex.Lock()
		IsConnected = true
		LatestQRCode = ""
		QRMutex.Unlock()

	case *events.Disconnected:
		QRMutex.Lock()
		IsConnected = false
		QRMutex.Unlock()
		log.Println("Disconnected from WhatsApp (whatsmeow reconnects automatically)")

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

// resolvePN maps a LID to the phone-number JID when the session knows it.
func resolvePN(lid types.JID) string {
	if Client == nil || Client.Store == nil {
		return ""
	}
	pn, err := Client.Store.LIDs.GetPNForLID(context.Background(), lid)
	if err != nil || pn.IsEmpty() {
		return ""
	}
	return pn.ToNonAD().String()
}

// Connected reports whether the session is logged in and online.
func Connected() bool {
	QRMutex.RLock()
	defer QRMutex.RUnlock()
	return IsConnected && (Client != nil || fakeMode)
}

func SendMessage(jid string, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil // never send a blank message
	}
	if fakeMode {
		log.Println("Fake WhatsApp: outgoing message dropped")
		return nil
	}
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
