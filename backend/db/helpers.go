package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Appointment struct {
	ID             int
	Titulo         string
	Descricao      string
	DataHoraInicio time.Time
	DataHoraFim    time.Time
	Status         string
}

type Timer struct {
	ID              int
	DuracaoSegundos int
	DispararEm      time.Time
	Motivo          string
	Executado       bool
}

type Note struct {
	ID       int
	Texto    string
	CriadoEm time.Time
}

type ChatMessage struct {
	Role    string
	Content string
}

// GetActiveJID returns the configured target JID.
func GetActiveJID() (string, error) {
	var jid string
	err := Pool.QueryRow(context.Background(), "SELECT jid FROM contacts_config WHERE active = true LIMIT 1").Scan(&jid)
	return jid, err
}

// SaveJID updates or inserts the target JID and deactivates others.
func SaveJID(jid string) error {
	_, err := Pool.Exec(context.Background(), 
		"INSERT INTO contacts_config (jid, active) VALUES ($1, true) ON CONFLICT (jid) DO UPDATE SET active = true, updated_at = NOW()", jid)
	if err != nil {
		return err
	}
	_, err = Pool.Exec(context.Background(), "UPDATE contacts_config SET active = false WHERE jid != $1", jid)
	return err
}

// GetChatHistory returns the recent chat messages for context.
func GetChatHistory(jid string, limit int) ([]ChatMessage, error) {
	rows, err := Pool.Query(context.Background(), 
		"SELECT role, content FROM chat_history WHERE jid = $1 ORDER BY timestamp DESC LIMIT $2", jid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []ChatMessage
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.Role, &msg.Content); err != nil {
			return nil, err
		}
		history = append(history, msg)
	}

	// Reverse to have chronological order
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	return history, nil
}

// SaveChatMessage saves a message role and content.
func SaveChatMessage(jid, role, content string) error {
	_, err := Pool.Exec(context.Background(), 
		"INSERT INTO chat_history (jid, role, content) VALUES ($1, $2, $3)", jid, role, content)
	return err
}

// CreateAppointment inserts an appointment and default reminder.
func CreateAppointment(titulo, descricao string, start time.Time) (int, error) {
	var id int
	end := start.Add(1 * time.Hour) // default to 1 hour
	err := Pool.QueryRow(context.Background(), 
		"INSERT INTO appointments (titulo, descricao, data_hora_inicio, data_hora_fim) VALUES ($1, $2, $3, $4) RETURNING id", 
		titulo, descricao, start, end).Scan(&id)
	if err != nil {
		return 0, err
	}

	_, err = Pool.Exec(context.Background(), 
		"INSERT INTO reminders (appointment_id, minutos_antecedencia) VALUES ($1, 15)", id)
	return id, err
}

// CreateTimer inserts a new timer.
func CreateTimer(duracaoSegundos int, dispararEm time.Time, motivo string) (int, error) {
	var id int
	err := Pool.QueryRow(context.Background(), 
		"INSERT INTO timers (duracao_segundos, disparar_em, motivo) VALUES ($1, $2, $3) RETURNING id", 
		duracaoSegundos, dispararEm, motivo).Scan(&id)
	return id, err
}

// MarkTimerExecuted updates the executed status of a timer.
func MarkTimerExecuted(id int) error {
	_, err := Pool.Exec(context.Background(), "UPDATE timers SET executado = true WHERE id = $1", id)
	return err
}

// SaveNote saves a general text note.
func SaveNote(texto string) error {
	_, err := Pool.Exec(context.Background(), "INSERT INTO notes (texto) VALUES ($1)", texto)
	return err
}

// SearchNotesAndCalendar queries both tables.
func SearchNotesAndCalendar(query string) ([]string, error) {
	var results []string

	// Search notes
	rows, err := Pool.Query(context.Background(), 
		"SELECT texto, criado_em FROM notes WHERE texto ILIKE $1 ORDER BY criado_em DESC LIMIT 5", "%"+query+"%")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var text string
			var createdAt time.Time
			if err := rows.Scan(&text, &createdAt); err == nil {
				results = append(results, fmt.Sprintf("[Nota em %s]: %s", createdAt.Format("02/01/2006 15:04"), text))
			}
		}
	}

	// Search appointments
	appRows, err := Pool.Query(context.Background(), 
		"SELECT titulo, descricao, data_hora_inicio FROM appointments WHERE (titulo ILIKE $1 OR descricao ILIKE $1) ORDER BY data_hora_inicio DESC LIMIT 5", "%"+query+"%")
	if err == nil {
		defer appRows.Close()
		for appRows.Next() {
			var title, desc string
			var start time.Time
			if err := appRows.Scan(&title, &desc, &start); err == nil {
				results = append(results, fmt.Sprintf("[Compromisso em %s]: %s - %s", start.Format("02/01/2006 15:04"), title, desc))
			}
		}
	}

	return results, nil
}

// AddShoppingListItems inserts multiple items, checking for duplicates.
func AddShoppingListItems(items []string) (added []string, duplicates []string, err error) {
	for _, item := range items {
		cleaned := strings.TrimSpace(item)
		if cleaned == "" {
			continue
		}

		var existingID int
		err := Pool.QueryRow(context.Background(),
			"SELECT id FROM shopping_list WHERE LOWER(item_name) = LOWER($1)", cleaned).Scan(&existingID)

		if err == nil {
			duplicates = append(duplicates, cleaned)
			continue
		}

		_, err = Pool.Exec(context.Background(),
			"INSERT INTO shopping_list (item_name) VALUES ($1)", cleaned)
		if err != nil {
			return nil, nil, err
		}
		added = append(added, cleaned)
	}
	return added, duplicates, nil
}

// GetShoppingList retrieves all shopping list items.
func GetShoppingList() ([]string, error) {
	rows, err := Pool.Query(context.Background(), "SELECT item_name FROM shopping_list ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err == nil {
			list = append(list, item)
		}
	}
	return list, nil
}

// RemoveShoppingListItems deletes items from the shopping list.
func RemoveShoppingListItems(items []string) (removed []string, err error) {
	for _, item := range items {
		cleaned := strings.TrimSpace(item)
		if cleaned == "" {
			continue
		}

		res, err := Pool.Exec(context.Background(),
			"DELETE FROM shopping_list WHERE LOWER(item_name) = LOWER($1)", cleaned)
		if err != nil {
			return nil, err
		}

		if res.RowsAffected() > 0 {
			removed = append(removed, cleaned)
		}
	}
	return removed, nil
}

// ClearShoppingList truncates the shopping list table.
func ClearShoppingList() error {
	_, err := Pool.Exec(context.Background(), "TRUNCATE TABLE shopping_list")
	return err
}

