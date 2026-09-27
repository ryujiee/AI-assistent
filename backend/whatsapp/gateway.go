package whatsapp

import (
	"context"
	"errors"
	"sort"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// Participant of a WhatsApp group.
type Participant struct {
	JID         string
	PhoneNumber string
	LID         string
	DisplayName string
}

// Group is a WhatsApp group the connected account belongs to.
type Group struct {
	JID          string
	Name         string
	Participants []Participant
}

// Quote identifies the message a reply answers, so the reply shows up
// attached to it in the chat.
type Quote struct {
	MessageID string
	SenderJID string
	Text      string
}

// ErrNotConnected is returned when WhatsApp is not linked or offline.
var ErrNotConnected = errors.New("whatsapp não conectado")

// RealGateway is the finance module's view of the live whatsmeow session.
type RealGateway struct{}

func (RealGateway) Connected() bool {
	QRMutex.RLock()
	defer QRMutex.RUnlock()
	return IsConnected && Client != nil
}

// contactName looks a participant up in the session's contact store
// (address book name, then the name they chose on WhatsApp).
func contactName(ctx context.Context, jids ...types.JID) string {
	if Client == nil || Client.Store == nil || Client.Store.Contacts == nil {
		return ""
	}
	for _, jid := range jids {
		if jid.IsEmpty() {
			continue
		}
		c, err := Client.Store.Contacts.GetContact(ctx, jid.ToNonAD())
		if err != nil || !c.Found {
			continue
		}
		for _, n := range []string{c.FullName, c.PushName, c.FirstName, c.BusinessName} {
			if n != "" {
				return n
			}
		}
	}
	return ""
}

func toGroup(ctx context.Context, info *types.GroupInfo) Group {
	g := Group{JID: info.JID.String(), Name: info.Name}
	for _, p := range info.Participants {
		part := Participant{JID: p.JID.ToNonAD().String(), DisplayName: p.DisplayName}
		if part.DisplayName == "" {
			part.DisplayName = contactName(ctx, p.PhoneNumber, p.JID, p.LID)
		}
		if !p.PhoneNumber.IsEmpty() {
			part.PhoneNumber = p.PhoneNumber.ToNonAD().String()
		}
		if !p.LID.IsEmpty() {
			part.LID = p.LID.ToNonAD().String()
		}
		if part.PhoneNumber == "" && p.JID.Server == types.DefaultUserServer {
			part.PhoneNumber = part.JID
		}
		g.Participants = append(g.Participants, part)
	}
	return g
}

func (gw RealGateway) JoinedGroups(ctx context.Context) ([]Group, error) {
	if !gw.Connected() {
		return nil, ErrNotConnected
	}
	infos, err := Client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(infos))
	for _, info := range infos {
		out = append(out, toGroup(ctx, info))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (gw RealGateway) GroupInfo(ctx context.Context, jid string) (*Group, error) {
	if !gw.Connected() {
		return nil, ErrNotConnected
	}
	parsed, err := types.ParseJID(jid)
	if err != nil || parsed.Server != types.GroupServer {
		return nil, errors.New("grupo inválido")
	}
	info, err := Client.GetGroupInfo(ctx, parsed)
	if err != nil {
		return nil, err
	}
	g := toGroup(ctx, info)
	return &g, nil
}

// SendText sends a text message, optionally as a reply to another message.
func (gw RealGateway) SendText(ctx context.Context, chatJID, text string, quote *Quote) (string, error) {
	if !gw.Connected() {
		return "", ErrNotConnected
	}
	to, err := types.ParseJID(chatJID)
	if err != nil {
		return "", err
	}
	msg := &waE2E.Message{Conversation: proto.String(text)}
	if quote != nil && quote.MessageID != "" {
		msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      proto.String(quote.MessageID),
				Participant:   proto.String(quote.SenderJID),
				QuotedMessage: &waE2E.Message{Conversation: proto.String(quote.Text)},
			},
		}}
	}
	resp, err := Client.SendMessage(ctx, to, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (RealGateway) Download(ctx context.Context, kind string, ref []byte) ([]byte, error) {
	return DownloadMedia(ctx, kind, ref)
}

// SelfJIDs lists the bot's own identities, never registered as members.
func (RealGateway) SelfJIDs() []string {
	if Client == nil || Client.Store == nil {
		return nil
	}
	var out []string
	if Client.Store.ID != nil {
		out = append(out, Client.Store.ID.ToNonAD().String())
	}
	if !Client.Store.LID.IsEmpty() {
		out = append(out, Client.Store.LID.ToNonAD().String())
	}
	return out
}
