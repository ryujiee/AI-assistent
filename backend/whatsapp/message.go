package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Message kinds.
const (
	KindText     = "TEXT"
	KindImage    = "IMAGE"
	KindAudio    = "AUDIO"
	KindDocument = "DOCUMENT"
)

// Message is the metadata of an incoming message, extracted before any
// download. Media stays behind MediaRef (the serialized WhatsApp media
// message) and is only fetched after the router decides the message matters.
type Message struct {
	ChatJID         string
	SenderJID       string // phone-number JID when known, LID otherwise
	SenderPN        string
	SenderLID       string
	MessageID       string
	Timestamp       time.Time
	PushName        string
	IsGroup         bool
	IsFromMe        bool
	IsEdit          bool
	IsForwarded     bool
	QuotedMessageID string
	Kind            string
	Text            string // text, or the caption of a media message
	MediaMime       string
	MediaSize       int64
	MediaFileName   string
	MediaRef        []byte
}

// ExtractMessage reads the metadata of a whatsmeow event without downloading
// anything. ok is false for messages the app does not handle (reactions,
// stickers, protocol messages...).
func ExtractMessage(v *events.Message, resolvePN func(types.JID) string) (Message, bool) {
	m := Message{
		ChatJID:   v.Info.Chat.String(),
		MessageID: v.Info.ID,
		Timestamp: v.Info.Timestamp,
		PushName:  v.Info.PushName,
		IsGroup:   v.Info.IsGroup,
		IsFromMe:  v.Info.IsFromMe,
		IsEdit:    v.IsEdit,
	}
	sender := v.Info.Sender.ToNonAD()
	switch sender.Server {
	case types.HiddenUserServer: // LID
		m.SenderLID = sender.String()
		if alt := v.Info.SenderAlt.ToNonAD(); alt.Server == types.DefaultUserServer {
			m.SenderPN = alt.String()
		} else if resolvePN != nil {
			m.SenderPN = resolvePN(sender)
		}
	default:
		m.SenderPN = sender.String()
		if alt := v.Info.SenderAlt.ToNonAD(); alt.Server == types.HiddenUserServer {
			m.SenderLID = alt.String()
		}
	}
	m.SenderJID = m.SenderPN
	if m.SenderJID == "" {
		m.SenderJID = m.SenderLID
	}

	msg := v.Message
	if doc := msg.GetDocumentWithCaptionMessage().GetMessage(); doc != nil {
		msg = doc
	}
	var ctxInfo *waE2E.ContextInfo
	var media proto.Message
	switch {
	case msg.GetConversation() != "":
		m.Kind, m.Text = KindText, msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		m.Kind, m.Text = KindText, msg.GetExtendedTextMessage().GetText()
		ctxInfo = msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		im := msg.GetImageMessage()
		m.Kind, m.Text, m.MediaMime, m.MediaSize = KindImage, im.GetCaption(), im.GetMimetype(), int64(im.GetFileLength())
		ctxInfo, media = im.GetContextInfo(), im
	case msg.GetAudioMessage() != nil:
		au := msg.GetAudioMessage()
		m.Kind, m.MediaMime, m.MediaSize = KindAudio, au.GetMimetype(), int64(au.GetFileLength())
		ctxInfo, media = au.GetContextInfo(), au
	case msg.GetDocumentMessage() != nil:
		doc := msg.GetDocumentMessage()
		m.Kind, m.Text, m.MediaMime, m.MediaSize = KindDocument, doc.GetCaption(), doc.GetMimetype(), int64(doc.GetFileLength())
		m.MediaFileName = doc.GetFileName()
		ctxInfo, media = doc.GetContextInfo(), doc
	default:
		return m, false
	}
	if ctxInfo != nil {
		m.QuotedMessageID = ctxInfo.GetStanzaID()
		m.IsForwarded = ctxInfo.GetIsForwarded()
	}
	if media != nil {
		ref, err := proto.Marshal(media)
		if err != nil {
			return m, false
		}
		m.MediaRef = ref
	}
	if m.Kind == KindText && strings.TrimSpace(m.Text) == "" {
		return m, false
	}
	return m, true
}

// Route is where a message goes.
type Route int

const (
	RouteIgnore Route = iota
	RouteSecretary
	RouteFinance
)

// RouteConfig answers the two questions the router needs. Both are cheap
// lookups (the target number, the cached finance groups).
type RouteConfig struct {
	TargetJID      func() string
	IsFinanceGroup func(chatJID string) bool
}

// Decide routes a message:
//   - private chat from the configured number -> Secretária (unchanged behavior)
//   - the finance group -> finance module (replies go to the group)
//   - anything else, including other groups -> ignored, nothing downloaded
func Decide(m Message, cfg RouteConfig) Route {
	if m.IsFromMe {
		return RouteIgnore
	}
	if strings.HasSuffix(m.ChatJID, "@broadcast") || strings.HasSuffix(m.ChatJID, "@newsletter") {
		return RouteIgnore
	}
	if m.IsGroup {
		if cfg.IsFinanceGroup != nil && cfg.IsFinanceGroup(m.ChatJID) {
			return RouteFinance
		}
		return RouteIgnore
	}
	target := ""
	if cfg.TargetJID != nil {
		target = cfg.TargetJID()
	}
	if target != "" && (m.SenderJID == target || m.SenderPN == target) {
		return RouteSecretary
	}
	return RouteIgnore
}

var (
	// Routes is set by main.
	Routes RouteConfig
	// FinanceHandler receives finance group messages (metadata only).
	FinanceHandler func(Message)
)

// Dispatch routes an extracted message. The fake gateway calls it too, so
// development exercises the same path as production.
func Dispatch(m Message) Route {
	route := Decide(m, Routes)
	switch route {
	case RouteSecretary:
		go handleSecretaryMessage(m)
	case RouteFinance:
		if FinanceHandler != nil {
			FinanceHandler(m)
		}
	}
	return route
}

// handleSecretaryMessage keeps the original private-chat flow: download the
// media, then hand everything to MessageCallback.
func handleSecretaryMessage(m Message) {
	if MessageCallback == nil {
		return
	}
	out := &WhatsAppMessage{SenderJID: m.SenderJID, Text: m.Text}
	switch m.Kind {
	case KindImage:
		data, err := downloadMedia(context.Background(), m.Kind, m.MediaRef)
		if err != nil {
			slog.Warn("whatsapp.download_failed", "kind", m.Kind, "error", err)
		} else {
			out.ImageBytes, out.ImageMime = data, m.MediaMime
		}
	case KindAudio:
		data, err := downloadMedia(context.Background(), m.Kind, m.MediaRef)
		if err != nil {
			slog.Warn("whatsapp.download_failed", "kind", m.Kind, "error", err)
		} else {
			out.AudioBytes = data
		}
	case KindDocument:
		return // the Secretária never handled documents
	}
	if out.Text == "" && len(out.ImageBytes) == 0 && len(out.AudioBytes) == 0 {
		return
	}
	MessageCallback(out)
}

// downloadMedia is swapped in tests to prove ignored chats download nothing.
var downloadMedia = DownloadMedia

// ErrMediaUnavailable is returned when a media reference cannot be fetched.
var ErrMediaUnavailable = errors.New("mídia indisponível")

// DownloadMedia fetches and decrypts a media message from its reference.
func DownloadMedia(ctx context.Context, kind string, ref []byte) ([]byte, error) {
	if fakeMode {
		return fakeDownload(ref)
	}
	if Client == nil || len(ref) == 0 {
		return nil, ErrMediaUnavailable
	}
	var pm proto.Message
	switch kind {
	case KindImage:
		pm = &waE2E.ImageMessage{}
	case KindAudio:
		pm = &waE2E.AudioMessage{}
	case KindDocument:
		pm = &waE2E.DocumentMessage{}
	default:
		return nil, ErrMediaUnavailable
	}
	if err := proto.Unmarshal(ref, pm); err != nil {
		return nil, ErrMediaUnavailable
	}
	dm, ok := pm.(whatsmeow.DownloadableMessage)
	if !ok {
		return nil, ErrMediaUnavailable
	}
	return Client.Download(ctx, dm)
}
