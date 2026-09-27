package whatsapp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

const (
	target       = "5511999990000@s.whatsapp.net"
	financeGroup = "120363000000000001@g.us"
	otherGroup   = "120363000000000009@g.us"
)

func routes() RouteConfig {
	return RouteConfig{
		TargetJID:      func() string { return target },
		IsFinanceGroup: func(jid string) bool { return jid == financeGroup },
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		m    Message
		want Route
	}{
		{"private from target", Message{ChatJID: target, SenderJID: target, SenderPN: target, Kind: KindText}, RouteSecretary},
		{"private from stranger", Message{ChatJID: "5511888880000@s.whatsapp.net", SenderJID: "5511888880000@s.whatsapp.net"}, RouteIgnore},
		{"finance group", Message{ChatJID: financeGroup, SenderJID: target, IsGroup: true}, RouteFinance},
		{"target in another group", Message{ChatJID: otherGroup, SenderJID: target, IsGroup: true}, RouteIgnore},
		{"own message in finance group", Message{ChatJID: financeGroup, SenderJID: target, IsGroup: true, IsFromMe: true}, RouteIgnore},
		{"status broadcast", Message{ChatJID: "status@broadcast", SenderJID: target}, RouteIgnore},
	}
	for _, c := range cases {
		if got := Decide(c.m, routes()); got != c.want {
			t.Errorf("%s: route = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDispatchNeverDownloadsForIgnoredChats(t *testing.T) {
	var downloads, finance, secretary atomic.Int32
	downloadMedia = func(context.Context, string, []byte) ([]byte, error) { downloads.Add(1); return []byte("x"), nil }
	defer func() { downloadMedia = DownloadMedia }()
	Routes = routes()
	FinanceHandler = func(Message) { finance.Add(1) }
	MessageCallback = func(*WhatsAppMessage) { secretary.Add(1) }
	defer func() { Routes, FinanceHandler, MessageCallback = RouteConfig{}, nil, nil }()

	img := Message{ChatJID: otherGroup, SenderJID: target, IsGroup: true, Kind: KindImage, MediaRef: []byte("ref")}
	if r := Dispatch(img); r != RouteIgnore {
		t.Fatalf("other group image routed to %v", r)
	}
	img.ChatJID = financeGroup
	if r := Dispatch(img); r != RouteFinance {
		t.Fatalf("finance group image routed to %v", r)
	}
	time.Sleep(50 * time.Millisecond)
	if downloads.Load() != 0 {
		t.Fatalf("router downloaded media %d times; finance downloads later, others never", downloads.Load())
	}
	if finance.Load() != 1 || secretary.Load() != 0 {
		t.Fatalf("finance=%d secretary=%d", finance.Load(), secretary.Load())
	}

	Dispatch(Message{ChatJID: target, SenderJID: target, Kind: KindImage, MediaRef: []byte("ref")})
	time.Sleep(50 * time.Millisecond)
	if downloads.Load() != 1 || secretary.Load() != 1 {
		t.Fatalf("private image: downloads=%d secretary=%d", downloads.Load(), secretary.Load())
	}
}

func evt(chat, sender types.JID, msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer},
			ID:            "3EB0ABCDEF", Timestamp: time.Unix(1790000000, 0), PushName: "Ana",
		},
		Message: msg,
	}
}

func TestExtractMessage(t *testing.T) {
	group := types.NewJID("120363000000000001", types.GroupServer)
	pn := types.NewJID("5511900000001", types.DefaultUserServer)
	lid := types.NewJID("100000000000001", types.HiddenUserServer)

	m, ok := ExtractMessage(evt(group, pn, &waE2E.Message{Conversation: proto.String("gastei 50 no mercado")}), nil)
	if !ok || m.Kind != KindText || m.Text != "gastei 50 no mercado" || m.SenderJID != pn.String() || !m.IsGroup || m.ChatJID != group.String() {
		t.Fatalf("text = %+v", m)
	}

	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String("na verdade foi 60"),
		ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("BOTREPLY1"), IsForwarded: proto.Bool(true)},
	}}
	m, _ = ExtractMessage(evt(group, pn, reply), nil)
	if m.QuotedMessageID != "BOTREPLY1" || !m.IsForwarded {
		t.Fatalf("quoted = %+v", m)
	}

	img := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("pix"), Mimetype: proto.String("image/jpeg"), FileLength: proto.Uint64(2048), MediaKey: []byte{1, 2, 3}}}
	m, ok = ExtractMessage(evt(group, pn, img), nil)
	if !ok || m.Kind != KindImage || m.Text != "pix" || m.MediaSize != 2048 || len(m.MediaRef) == 0 {
		t.Fatalf("image = %+v", m)
	}

	pdf := &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("boleto.pdf"), Caption: proto.String("boleto")},
	}}}
	m, ok = ExtractMessage(evt(group, pn, pdf), nil)
	if !ok || m.Kind != KindDocument || m.MediaMime != "application/pdf" || m.Text != "boleto" {
		t.Fatalf("pdf = %+v", m)
	}

	e := evt(group, lid, &waE2E.Message{Conversation: proto.String("oi")})
	e.Info.SenderAlt = pn
	m, _ = ExtractMessage(e, nil)
	if m.SenderLID != lid.String() || m.SenderPN != pn.String() || m.SenderJID != pn.String() {
		t.Fatalf("lid sender = %+v", m)
	}
	m, _ = ExtractMessage(evt(group, lid, &waE2E.Message{Conversation: proto.String("oi")}), func(types.JID) string { return "" })
	if m.SenderJID != lid.String() {
		t.Fatalf("unresolved lid sender = %+v", m)
	}

	if _, ok := ExtractMessage(evt(group, pn, &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}), nil); ok {
		t.Fatal("reaction accepted")
	}
	if _, ok := ExtractMessage(evt(group, pn, &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("  ")}}), nil); ok {
		t.Fatal("empty text accepted")
	}
}
