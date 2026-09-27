package whatsapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
)

// fakeMode is set when WHATSAPP_FAKE is configured: the process never opens a
// WhatsApp connection, so local development and screenshots cannot touch a
// real account.
var fakeMode bool

// InitFake replaces the WhatsApp connection with local state. mode "qr" shows
// a dummy pairing code; anything else simulates a linked device.
func InitFake(mode string) {
	fakeMode = true
	QRMutex.Lock()
	defer QRMutex.Unlock()
	if mode == "qr" {
		png, err := qrcode.Encode("fake-pairing-code-for-local-development", qrcode.Medium, 256)
		if err == nil {
			LatestQRCode = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		IsConnected = false
	} else {
		IsConnected = true
	}
	slog.Warn("whatsapp.fake_mode", "mode", mode, "note", "no WhatsApp connection is opened")
}

// In fake mode MediaRef carries the file bytes themselves.
func fakeDownload(ref []byte) ([]byte, error) {
	if len(ref) == 0 {
		return nil, ErrMediaUnavailable
	}
	return ref, nil
}

// FakeSelfJID is the bot account of the fake gateway.
const FakeSelfJID = "5511900000000@s.whatsapp.net"

// SentMessage is a message the fake gateway "sent".
type SentMessage struct {
	ID       string    `json:"id"`
	ChatJID  string    `json:"chat_jid"`
	Text     string    `json:"text"`
	QuotedID string    `json:"quoted_id,omitempty"`
	At       time.Time `json:"at"`
}

// FakeGateway stands in for WhatsApp in development and tests: fictitious
// groups, an outbox instead of real sends, media served from memory.
type FakeGateway struct {
	mu     sync.Mutex
	groups []Group
	outbox []SentMessage
	seq    int
}

func NewFakeGateway() *FakeGateway {
	self := Participant{JID: FakeSelfJID, PhoneNumber: FakeSelfJID, DisplayName: "Secretária"}
	ana := Participant{JID: "5511900000001@s.whatsapp.net", PhoneNumber: "5511900000001@s.whatsapp.net", LID: "100000000000001@lid", DisplayName: "Ana"}
	bruno := Participant{JID: "5511900000002@s.whatsapp.net", PhoneNumber: "5511900000002@s.whatsapp.net", LID: "100000000000002@lid", DisplayName: "Bruno"}
	tio := Participant{JID: "5511900000003@s.whatsapp.net", PhoneNumber: "5511900000003@s.whatsapp.net", DisplayName: "Tio Carlos"}
	return &FakeGateway{groups: []Group{
		{JID: "120363000000000001@g.us", Name: "Financeiro Ana & Bruno", Participants: []Participant{self, ana, bruno}},
		{JID: "120363000000000002@g.us", Name: "Família", Participants: []Participant{self, ana, bruno, tio}},
		{JID: "120363000000000003@g.us", Name: "Futebol de quinta", Participants: []Participant{self, bruno, tio}},
	}}
}

func (f *FakeGateway) Connected() bool {
	QRMutex.RLock()
	defer QRMutex.RUnlock()
	return IsConnected
}

func (f *FakeGateway) JoinedGroups(ctx context.Context) ([]Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Group(nil), f.groups...), nil
}

func (f *FakeGateway) GroupInfo(ctx context.Context, jid string) (*Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.groups {
		if g.JID == jid {
			return &g, nil
		}
	}
	return nil, fmt.Errorf("grupo não encontrado")
}

func (f *FakeGateway) SendText(ctx context.Context, chatJID, text string, quote *Quote) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	msg := SentMessage{ID: fmt.Sprintf("FAKEOUT%06d", f.seq), ChatJID: chatJID, Text: text, At: time.Now()}
	if quote != nil {
		msg.QuotedID = quote.MessageID
	}
	f.outbox = append(f.outbox, msg)
	return msg.ID, nil
}

func (f *FakeGateway) Download(ctx context.Context, kind string, ref []byte) ([]byte, error) {
	return fakeDownload(ref)
}

func (f *FakeGateway) SelfJIDs() []string { return []string{FakeSelfJID} }

// Outbox returns what the fake gateway sent, oldest first.
func (f *FakeGateway) Outbox() []SentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SentMessage(nil), f.outbox...)
}
