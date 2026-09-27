package web

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"secretary/finance"
	"secretary/whatsapp"
)

type groupView struct {
	JID              string `json:"jid"`
	Name             string `json:"name"`
	ParticipantCount int    `json:"participant_count"`
	Linked           bool   `json:"linked"`
}

type whatsappStatus struct {
	Connected bool         `json:"connected"`
	Fake      bool         `json:"fake"`
	Group     *linkedGroup `json:"group"`
	Members   []memberView `json:"members"`
}

type linkedGroup struct {
	Name             string     `json:"name"`
	LinkedAt         *time.Time `json:"linked_at"`
	ParticipantCount *int       `json:"participant_count"`
}

func (api *FinanceAPI) registerWhatsApp(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/finance/whatsapp", api.ws(api.whatsappStatus))
	mux.HandleFunc("GET /api/finance/whatsapp/groups", api.ws(api.listGroups))
	mux.HandleFunc("POST /api/finance/whatsapp/group", api.ws(api.linkGroup))
	mux.HandleFunc("DELETE /api/finance/whatsapp/group", api.ws(api.unlinkGroup))
	mux.HandleFunc("POST /api/finance/whatsapp/members/sync", api.ws(api.syncMembers))

	// Development only: drive the fake WhatsApp gateway. Never registered
	// when a real WhatsApp session is in use.
	if api.Fake != nil {
		mux.HandleFunc("POST /api/dev/whatsapp/messages", api.devInject)
		mux.HandleFunc("GET /api/dev/whatsapp/outbox", api.devOutbox)
	}
}

func (api *FinanceAPI) whatsappStatus(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	res := whatsappStatus{Connected: api.Ingestor.GW.Connected(), Fake: api.Fake != nil}
	if ws.GroupJID != nil {
		g := &linkedGroup{LinkedAt: ws.GroupLinkedAt}
		if ws.GroupName != nil {
			g.Name = *ws.GroupName
		}
		if res.Connected {
			// Keep the label and the members fresh; the JID never changes.
			if err := api.Ingestor.SyncMembers(r.Context(), ws); err == nil {
				if info, err := api.Ingestor.GW.GroupInfo(r.Context(), *ws.GroupJID); err == nil {
					g.Name = info.Name
					n := len(api.Ingestor.HumanParticipants(info))
					g.ParticipantCount = &n
				}
			}
		}
		res.Group = g
	}
	members, err := api.Svc.ListMembers(r.Context(), ws.ID)
	if err != nil {
		financeError(w, err)
		return
	}
	res.Members = toMemberViews(members)
	writeJSON(w, http.StatusOK, res)
}

func (api *FinanceAPI) listGroups(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	groups, err := api.Ingestor.GW.JoinedGroups(r.Context())
	if err != nil {
		whatsappError(w, err)
		return
	}
	out := make([]groupView, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		out = append(out, groupView{
			JID: g.JID, Name: g.Name,
			ParticipantCount: len(api.Ingestor.HumanParticipants(g)),
			Linked:           ws.GroupJID != nil && *ws.GroupJID == g.JID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func whatsappError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, whatsapp.ErrNotConnected):
		writeError(w, http.StatusServiceUnavailable, "whatsapp_disconnected", "Conecte o WhatsApp na aba Secretária para listar os grupos.")
	case errors.Is(err, finance.ErrGroupUnavailable):
		writeError(w, http.StatusBadRequest, "group_unavailable", "Esse grupo não está disponível para a conta conectada.")
	default:
		financeError(w, err)
	}
}

func (api *FinanceAPI) linkGroup(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	var body struct {
		JID string `json:"jid"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !strings.HasSuffix(body.JID, "@g.us") {
		writeError(w, http.StatusBadRequest, "validation", "Escolha um grupo da lista.")
		return
	}
	if _, err := api.Ingestor.LinkGroup(r.Context(), ws.ID, body.JID); err != nil {
		whatsappError(w, err)
		return
	}
	updated, err := api.Svc.GetWorkspace(r.Context(), ws.ID)
	if err != nil {
		financeError(w, err)
		return
	}
	api.whatsappStatus(w, r, updated)
}

func (api *FinanceAPI) unlinkGroup(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	updated, err := api.Ingestor.UnlinkGroup(r.Context(), ws.ID)
	if err != nil {
		financeError(w, err)
		return
	}
	api.whatsappStatus(w, r, updated)
}

func (api *FinanceAPI) syncMembers(w http.ResponseWriter, r *http.Request, ws *finance.Workspace) {
	if err := api.Ingestor.SyncMembers(r.Context(), ws); err != nil {
		whatsappError(w, err)
		return
	}
	api.listMembers(w, r, ws)
}

var devSeq atomic.Int64

type devMessage struct {
	ChatJID         string `json:"chat_jid"`
	SenderJID       string `json:"sender_jid"`
	PushName        string `json:"push_name"`
	Text            string `json:"text"`
	Kind            string `json:"kind"`
	MediaBase64     string `json:"media_base64"`
	Mime            string `json:"mime"`
	FileName        string `json:"file_name"`
	QuotedMessageID string `json:"quoted_message_id"`
	MessageID       string `json:"message_id"`
	IsGroup         *bool  `json:"is_group"`
}

// devInject feeds a fictitious message through the real router, exactly
// like an event coming from whatsmeow.
func (api *FinanceAPI) devInject(w http.ResponseWriter, r *http.Request) {
	var b devMessage
	if !decodeBody(w, r, &b) {
		return
	}
	m := whatsapp.Message{
		ChatJID: b.ChatJID, SenderJID: b.SenderJID, SenderPN: b.SenderJID, PushName: b.PushName,
		Text: b.Text, Kind: b.Kind, MediaMime: b.Mime, MediaFileName: b.FileName,
		QuotedMessageID: b.QuotedMessageID, MessageID: b.MessageID, Timestamp: time.Now(),
		IsGroup: strings.HasSuffix(b.ChatJID, "@g.us"),
	}
	if b.IsGroup != nil {
		m.IsGroup = *b.IsGroup
	}
	if m.Kind == "" {
		m.Kind = whatsapp.KindText
	}
	if m.MessageID == "" {
		m.MessageID = fmt.Sprintf("FAKEIN%d%04d", time.Now().Unix(), devSeq.Add(1))
	}
	if b.MediaBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(b.MediaBase64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Mídia inválida.")
			return
		}
		m.MediaRef, m.MediaSize = data, int64(len(data))
	}
	route := whatsapp.Dispatch(m)
	writeJSON(w, http.StatusOK, map[string]any{"route": []string{"ignore", "secretary", "finance"}[route], "message_id": m.MessageID})
}

func (api *FinanceAPI) devOutbox(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"messages": api.Fake.Outbox()})
}
