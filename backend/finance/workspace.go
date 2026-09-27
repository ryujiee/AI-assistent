package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const workspaceCols = `id, name, group_jid, group_name, group_linked_at, confidence_threshold::float8,
	monthly_summary, weekly_summary, created_at, updated_at`

func scanWorkspace(row pgx.Row) (*Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.GroupJID, &w.GroupName, &w.GroupLinkedAt, &w.ConfidenceThreshold,
		&w.MonthlySummary, &w.WeeklySummary, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &w, err
}

// DefaultWorkspace returns the panel's workspace, creating it (with the
// default categories) the first time. Today the panel has a single admin and
// a single workspace; a multi-workspace setup would map the session to one.
func (s *Service) DefaultWorkspace(ctx context.Context) (*Workspace, error) {
	const first = "SELECT " + workspaceCols + " FROM finance_workspaces ORDER BY id LIMIT 1"
	w, err := scanWorkspace(s.DB.QueryRow(ctx, first))
	if !errors.Is(err, ErrNotFound) {
		return w, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		// Serialize the lazy creation so two first requests cannot both create one.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(727002)"); err != nil {
			return err
		}
		var err error
		if w, err = scanWorkspace(tx.QueryRow(ctx, first)); !errors.Is(err, ErrNotFound) {
			return err
		}
		w, err = insertWorkspace(ctx, tx, "Finanças do casal")
		return err
	})
	return w, err
}

// CreateWorkspace creates a workspace and seeds its categories atomically.
func (s *Service) CreateWorkspace(ctx context.Context, name string) (*Workspace, error) {
	var w *Workspace
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		w, err = insertWorkspace(ctx, tx, name)
		return err
	})
	return w, err
}

func insertWorkspace(ctx context.Context, tx pgx.Tx, name string) (*Workspace, error) {
	w, err := scanWorkspace(tx.QueryRow(ctx, "INSERT INTO finance_workspaces (name) VALUES ($1) RETURNING "+workspaceCols, name))
	if err != nil {
		return nil, err
	}
	return w, SeedDefaultCategories(ctx, tx, w.ID)
}

func (s *Service) GetWorkspace(ctx context.Context, id int64) (*Workspace, error) {
	return scanWorkspace(s.DB.QueryRow(ctx, "SELECT "+workspaceCols+" FROM finance_workspaces WHERE id = $1", id))
}

// WorkspaceByGroup finds the workspace linked to a WhatsApp group.
func (s *Service) WorkspaceByGroup(ctx context.Context, groupJID string) (*Workspace, error) {
	return scanWorkspace(s.DB.QueryRow(ctx, "SELECT "+workspaceCols+" FROM finance_workspaces WHERE group_jid = $1", groupJID))
}

// LinkedGroups maps every linked group JID to its workspace and link time,
// for the message router.
func (s *Service) LinkedGroups(ctx context.Context) (map[string]LinkedGroup, error) {
	rows, err := s.DB.Query(ctx, "SELECT id, group_jid, group_linked_at FROM finance_workspaces WHERE group_jid IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]LinkedGroup{}
	for rows.Next() {
		var g LinkedGroup
		var jid string
		if err := rows.Scan(&g.WorkspaceID, &jid, &g.LinkedAt); err != nil {
			return nil, err
		}
		out[jid] = g
	}
	return out, rows.Err()
}

type LinkedGroup struct {
	WorkspaceID int64
	LinkedAt    time.Time
}

type SettingsPatch struct {
	ConfidenceThreshold *float64 `json:"confidence_threshold"`
	MonthlySummary      *string  `json:"monthly_summary"`
	WeeklySummary       *bool    `json:"weekly_summary"`
	Name                *string  `json:"name"`
}

func (s *Service) UpdateSettings(ctx context.Context, wsID int64, p SettingsPatch) (*Workspace, error) {
	if p.ConfidenceThreshold != nil && (*p.ConfidenceThreshold < 0.5 || *p.ConfidenceThreshold > 1) {
		return nil, invalid("O limiar de confiança deve ficar entre 0,50 e 1,00.")
	}
	if p.MonthlySummary != nil && !contains([]string{"off", "last_day", "first_day"}, *p.MonthlySummary) {
		return nil, invalid("Opção de resumo mensal inválida.")
	}
	if p.Name != nil {
		n := truncate(*p.Name, 80)
		if n == "" {
			return nil, invalid("Informe um nome.")
		}
		p.Name = &n
	}
	return scanWorkspace(s.DB.QueryRow(ctx, `
		UPDATE finance_workspaces SET
			confidence_threshold = COALESCE($2, confidence_threshold),
			monthly_summary = COALESCE($3, monthly_summary),
			weekly_summary = COALESCE($4, weekly_summary),
			name = COALESCE($5, name),
			updated_at = now()
		WHERE id = $1 RETURNING `+workspaceCols, wsID, p.ConfidenceThreshold, p.MonthlySummary, p.WeeklySummary, p.Name))
}

// LinkGroup points the workspace at a WhatsApp group. Only messages sent after
// linkedAt are processed: changing the group never imports old history.
func (s *Service) LinkGroup(ctx context.Context, wsID int64, groupJID, groupName string, linkedAt time.Time) (*Workspace, error) {
	w, err := scanWorkspace(s.DB.QueryRow(ctx, `
		UPDATE finance_workspaces SET group_jid = $2, group_name = $3, group_linked_at = $4, updated_at = now()
		WHERE id = $1 RETURNING `+workspaceCols, wsID, groupJID, truncate(groupName, 120), linkedAt))
	if err != nil && strings.Contains(err.Error(), "finance_workspaces_group_jid_key") {
		return nil, fmt.Errorf("%w: grupo já vinculado a outro espaço", ErrConflict)
	}
	return w, err
}

func (s *Service) UnlinkGroup(ctx context.Context, wsID int64) (*Workspace, error) {
	return scanWorkspace(s.DB.QueryRow(ctx, `
		UPDATE finance_workspaces SET group_jid = NULL, group_name = NULL, group_linked_at = NULL, updated_at = now()
		WHERE id = $1 RETURNING `+workspaceCols, wsID))
}

// UpdateGroupName keeps the cached group name fresh (the JID never changes).
func (s *Service) UpdateGroupName(ctx context.Context, wsID int64, name string) error {
	_, err := s.DB.Exec(ctx, "UPDATE finance_workspaces SET group_name = $2 WHERE id = $1 AND group_name IS DISTINCT FROM $2", wsID, truncate(name, 120))
	return err
}

// ---- members ----

const memberCols = "id, workspace_id, jid, lid, phone_number, display_name, created_at, updated_at"

func scanMembers(rows pgx.Rows) ([]Member, error) {
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.JID, &m.LID, &m.PhoneNumber, &m.DisplayName, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) ListMembers(ctx context.Context, wsID int64) ([]Member, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+memberCols+" FROM finance_members WHERE workspace_id = $1 ORDER BY id", wsID)
	if err != nil {
		return nil, err
	}
	return scanMembers(rows)
}

// MemberInput describes a WhatsApp participant.
type MemberInput struct {
	JID         string
	LID         string
	PhoneNumber string
	DisplayName string
}

// UpsertMember registers a participant, matching an existing member by any of
// its identities (phone JID or LID). An existing display name is kept: the
// user may have renamed the member in the panel.
func (s *Service) UpsertMember(ctx context.Context, wsID int64, in MemberInput) (*Member, error) {
	if in.JID == "" {
		return nil, invalid("participante sem identificador")
	}
	name := truncate(in.DisplayName, 60)
	if name == "" {
		name = placeholderName(in.PhoneNumber, in.JID)
	}
	if existing, err := s.FindMember(ctx, wsID, in.JID, in.LID, in.PhoneNumber); err == nil {
		var m Member
		err := s.DB.QueryRow(ctx, `
			UPDATE finance_members SET
				lid = COALESCE(lid, NULLIF($3, '')),
				phone_number = COALESCE(phone_number, NULLIF($4, '')),
				-- A placeholder name is replaced by the WhatsApp name once known;
				-- a name typed in the panel is never overwritten.
				display_name = CASE WHEN display_name LIKE 'Participante%' AND $5 <> '' THEN $5 ELSE display_name END,
				updated_at = now()
			WHERE id = $1 AND workspace_id = $2 RETURNING `+memberCols, existing.ID, wsID, in.LID, in.PhoneNumber, truncate(in.DisplayName, 60)).
			Scan(&m.ID, &m.WorkspaceID, &m.JID, &m.LID, &m.PhoneNumber, &m.DisplayName, &m.CreatedAt, &m.UpdatedAt)
		return &m, err
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var m Member
	err := s.DB.QueryRow(ctx, `
		INSERT INTO finance_members (workspace_id, jid, lid, phone_number, display_name)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5)
		ON CONFLICT (workspace_id, jid) DO UPDATE SET updated_at = now()
		RETURNING `+memberCols, wsID, in.JID, in.LID, in.PhoneNumber, name).
		Scan(&m.ID, &m.WorkspaceID, &m.JID, &m.LID, &m.PhoneNumber, &m.DisplayName, &m.CreatedAt, &m.UpdatedAt)
	return &m, err
}

// FindMember matches any of the given identities against jid, lid or phone.
func (s *Service) FindMember(ctx context.Context, wsID int64, ids ...string) (*Member, error) {
	var clean []string
	for _, id := range ids {
		if id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return nil, ErrNotFound
	}
	rows, err := s.DB.Query(ctx, "SELECT "+memberCols+` FROM finance_members
		WHERE workspace_id = $1 AND (jid = ANY($2) OR lid = ANY($2) OR phone_number = ANY($2)) ORDER BY id LIMIT 1`, wsID, clean)
	if err != nil {
		return nil, err
	}
	ms, err := scanMembers(rows)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return &ms[0], nil
}

func (s *Service) GetMember(ctx context.Context, wsID, id int64) (*Member, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+memberCols+" FROM finance_members WHERE workspace_id = $1 AND id = $2", wsID, id)
	if err != nil {
		return nil, err
	}
	ms, err := scanMembers(rows)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return &ms[0], nil
}

func (s *Service) RenameMember(ctx context.Context, wsID, id int64, name string) (*Member, error) {
	name = truncate(name, 60)
	if name == "" {
		return nil, invalid("Informe um nome.")
	}
	var m Member
	err := s.DB.QueryRow(ctx, `UPDATE finance_members SET display_name = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 RETURNING `+memberCols, wsID, id, name).
		Scan(&m.ID, &m.WorkspaceID, &m.JID, &m.LID, &m.PhoneNumber, &m.DisplayName, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

// ResolveMember finds a member by display name ("Ana", "minha esposa" is left
// to the model). Accent and case insensitive.
func (s *Service) ResolveMember(ctx context.Context, wsID int64, name string) (*Member, error) {
	members, err := s.ListMembers(ctx, wsID)
	if err != nil {
		return nil, err
	}
	n := normalize(name)
	for i := range members {
		if normalize(members[i].DisplayName) == n {
			return &members[i], nil
		}
	}
	for i := range members {
		if strings.HasPrefix(normalize(members[i].DisplayName), n+" ") {
			return &members[i], nil
		}
	}
	return nil, ErrNotFound
}

func decodeJSON[T any](raw []byte) (*T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// placeholderName is used until the participant's WhatsApp name is known:
// "Participante ···0001".
func placeholderName(ids ...string) string {
	for _, id := range ids {
		user, _, _ := strings.Cut(id, "@")
		if len(user) >= 4 {
			return "Participante ···" + user[len(user)-4:]
		}
	}
	return "Participante"
}
