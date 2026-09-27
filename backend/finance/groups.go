package finance

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"secretary/whatsapp"
)

// ErrGroupUnavailable means the group is not one the bot belongs to.
var ErrGroupUnavailable = errors.New("grupo indisponível")

// LinkGroup links the workspace to a WhatsApp group the bot belongs to,
// registers its participants as members and updates the router. The group is
// identified by its JID; the name is only a label. Messages sent before this
// moment are never processed.
func (ing *Ingestor) LinkGroup(ctx context.Context, wsID int64, jid string) (*Workspace, error) {
	g, err := ing.GW.GroupInfo(ctx, jid)
	if err != nil {
		if errors.Is(err, whatsapp.ErrNotConnected) {
			return nil, err
		}
		return nil, ErrGroupUnavailable
	}
	w, err := ing.Svc.LinkGroup(ctx, wsID, g.JID, g.Name, time.Now())
	if err != nil {
		return nil, err
	}
	if err := ing.syncMembers(ctx, wsID, g); err != nil {
		return nil, err
	}
	if err := ing.RefreshGroups(ctx); err != nil {
		return nil, err
	}
	slog.Info("finance.group_linked", "workspace", wsID, "participants", len(g.Participants))
	return w, nil
}

func (ing *Ingestor) UnlinkGroup(ctx context.Context, wsID int64) (*Workspace, error) {
	w, err := ing.Svc.UnlinkGroup(ctx, wsID)
	if err != nil {
		return nil, err
	}
	return w, ing.RefreshGroups(ctx)
}

// SyncMembers re-reads the participants of the linked group.
func (ing *Ingestor) SyncMembers(ctx context.Context, w *Workspace) error {
	if w.GroupJID == nil {
		return nil
	}
	g, err := ing.GW.GroupInfo(ctx, *w.GroupJID)
	if err != nil {
		return err
	}
	if w.GroupName == nil || *w.GroupName != g.Name {
		if err := ing.Svc.UpdateGroupName(ctx, w.ID, g.Name); err != nil {
			return err
		}
	}
	return ing.syncMembers(ctx, w.ID, g)
}

func (ing *Ingestor) isSelf(p whatsapp.Participant) bool {
	for _, self := range ing.GW.SelfJIDs() {
		if self == p.JID || self == p.PhoneNumber || self == p.LID {
			return true
		}
	}
	return false
}

func (ing *Ingestor) syncMembers(ctx context.Context, wsID int64, g *whatsapp.Group) error {
	for _, p := range ing.HumanParticipants(g) {
		jid := p.PhoneNumber
		if jid == "" {
			jid = p.JID
		}
		if _, err := ing.Svc.UpsertMember(ctx, wsID, MemberInput{JID: jid, LID: p.LID, PhoneNumber: p.PhoneNumber, DisplayName: p.DisplayName}); err != nil {
			return err
		}
	}
	return nil
}

// HumanParticipants drops the bot itself from a participant list.
func (ing *Ingestor) HumanParticipants(g *whatsapp.Group) []whatsapp.Participant {
	var out []whatsapp.Participant
	for _, p := range g.Participants {
		if !ing.isSelf(p) {
			out = append(out, p)
		}
	}
	return out
}
