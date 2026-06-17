package engine

import (
	"context"
	"fmt"
	"log"
	"time"

	"secretary/db"
	"secretary/whatsapp"

	"github.com/robfig/cron/v3"
)

// Define local function variables or channel hooks to avoid circular dependencies if we need to call openai.
var ProcessMessageFunc func(jid string, userMessage string) (string, error)

func StartScheduler() {
	// Initialize Cron scheduler
	c := cron.New(cron.WithLocation(time.Local))
	
	// Morning summary: daily at 07:30 AM local time
	_, err := c.AddFunc("30 7 * * *", RunMorningSummary)
	if err != nil {
		log.Printf("Failed to schedule morning summary cron: %v", err)
	}

	c.Start()
	log.Println("Cron Scheduler started successfully")

	// Start early warnings worker (running every 1 minute)
	go startEarlyWarningsWorker()

	// Restore pending timers from the database
	go restorePendingTimers()
}

// RegisterDynamicTimer creates a live memory timer that alerts on WhatsApp when done.
func RegisterDynamicTimer(timerID int, duration time.Duration, jid string, reason string) {
	log.Printf("Scheduling dynamic timer %d for JID %s in %v (reason: %s)", timerID, jid, duration, reason)
	time.AfterFunc(duration, func() {
		log.Printf("Timer %d fired! Sending WhatsApp alert to %s", timerID, jid)
		
		msg := fmt.Sprintf("⏰ *Alerta de Timer!* O tempo acabou para: %s", reason)
		if err := whatsapp.SendMessage(jid, msg); err != nil {
			log.Printf("Failed to send WhatsApp alert for timer %d: %v", timerID, err)
		}

		if err := db.MarkTimerExecuted(timerID); err != nil {
			log.Printf("Failed to mark timer %d as executed in database: %v", timerID, err)
		}
	})
}

// RunMorningSummary gathers today's appointments and calls the AI to format it.
func RunMorningSummary() {
	log.Println("Running scheduled daily morning summary...")
	jid, err := db.GetActiveJID()
	if err != nil || jid == "" {
		log.Printf("Morning Summary: No active contact JID configured. Skipping.")
		return
	}

	appointments, err := getTodayAppointments()
	if err != nil {
		log.Printf("Morning Summary: Failed to get today's appointments: %v", err)
		return
	}

	prompt := "Bom dia! Como minha secretária pessoal de IA, crie uma mensagem de bom dia simpática, organizada e motivadora. Apresente o resumo dos meus compromissos agendados para hoje. "
	if len(appointments) == 0 {
		prompt += "Não tenho nenhum compromisso agendados para hoje."
	} else {
		prompt += "Aqui está a lista de compromissos de hoje:\n"
		for _, app := range appointments {
			timeStr := app.DataHoraInicio.Format("15:04")
			prompt += fmt.Sprintf("- *%s*: %s (início: %s)\n", app.Titulo, app.Descricao, timeStr)
		}
	}

	if ProcessMessageFunc != nil {
		summary, err := ProcessMessageFunc(jid, prompt)
		if err != nil {
			log.Printf("Morning Summary: OpenAI call failed: %v", err)
			return
		}
		if err := whatsapp.SendMessage(jid, summary); err != nil {
			log.Printf("Morning Summary: Failed to send message: %v", err)
		}
	} else {
		log.Println("Morning Summary: ProcessMessageFunc callback is nil. Summary could not be run.")
	}
}

func getTodayAppointments() ([]db.Appointment, error) {
	// Query for appointments starting between today at 00:00 and today at 23:59:59
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	rows, err := db.Pool.Query(context.Background(),
		"SELECT id, titulo, descricao, data_hora_inicio, data_hora_fim, status FROM appointments WHERE data_hora_inicio >= $1 AND data_hora_inicio < $2 ORDER BY data_hora_inicio ASC",
		startOfDay, endOfDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []db.Appointment
	for rows.Next() {
		var app db.Appointment
		err := rows.Scan(&app.ID, &app.Titulo, &app.Descricao, &app.DataHoraInicio, &app.DataHoraFim, &app.Status)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, nil
}

func startEarlyWarningsWorker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	log.Println("Early warnings worker started")

	for range ticker.C {
		log.Println("Checking for pending early reminders...")
		
		// Find active JID
		jid, err := db.GetActiveJID()
		if err != nil || jid == "" {
			continue // No active user to notify
		}

		// Find reminders due
		query := `
			SELECT r.id, a.titulo, a.data_hora_inicio, r.minutos_antecedencia
			FROM reminders r
			JOIN appointments a ON r.appointment_id = a.id
			WHERE r.enviado = false
			  AND a.data_hora_inicio - (r.minutos_antecedencia * interval '1 minute') <= NOW()
			  AND a.data_hora_inicio > NOW()
		`

		rows, err := db.Pool.Query(context.Background(), query)
		if err != nil {
			log.Printf("Reminders Worker: SQL error: %v", err)
			continue
		}

		type ActiveReminder struct {
			ID                 int
			Titulo             string
			DataHoraInicio     time.Time
			MinutosAntecedencia int
		}

		var activeReminders []ActiveReminder
		for rows.Next() {
			var ar ActiveReminder
			if err := rows.Scan(&ar.ID, &ar.Titulo, &ar.DataHoraInicio, &ar.MinutosAntecedencia); err == nil {
				activeReminders = append(activeReminders, ar)
			}
		}
		rows.Close()

		for _, r := range activeReminders {
			msg := fmt.Sprintf("🔔 *Lembrete de Compromisso!*\n\nVocê tem *%s* hoje às *%s* (daqui a %d minutos).",
				r.Titulo, r.DataHoraInicio.Format("15:04"), r.MinutosAntecedencia)
			
			if err := whatsapp.SendMessage(jid, msg); err == nil {
				_, dbErr := db.Pool.Exec(context.Background(), "UPDATE reminders SET enviado = true WHERE id = $1", r.ID)
				if dbErr != nil {
					log.Printf("Reminders Worker: Failed to mark reminder %d as sent: %v", r.ID, dbErr)
				} else {
					log.Printf("Reminders Worker: Successfully sent reminder for '%s' to %s", r.Titulo, jid)
				}
			} else {
				log.Printf("Reminders Worker: Failed to send reminder: %v", err)
			}
		}
	}
}

func restorePendingTimers() {
	log.Println("Restoring pending timers from database...")
	
	jid, err := db.GetActiveJID()
	if err != nil || jid == "" {
		log.Println("Timer Restorer: No active JID found. Cannot restore timers.")
		return
	}

	rows, err := db.Pool.Query(context.Background(),
		"SELECT id, disparar_em, motivo FROM timers WHERE executado = false")
	if err != nil {
		log.Printf("Timer Restorer: Failed to query pending timers: %v", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var id int
		var dispararEm time.Time
		var motivo string

		if err := rows.Scan(&id, &dispararEm, &motivo); err != nil {
			log.Printf("Timer Restorer: Failed to scan row: %v", err)
			continue
		}

		duration := dispararEm.Sub(now)
		if duration <= 0 {
			// Timer expired while system was offline
			log.Printf("Timer %d expired while offline, firing late alert.", id)
			msg := fmt.Sprintf("⏰ *Alerta de Lembrete Atrasado!* O tempo acabou para: %s (expirou enquanto eu estava offline)", motivo)
			_ = whatsapp.SendMessage(jid, msg)
			_ = db.MarkTimerExecuted(id)
		} else {
			// Reschedule
			RegisterDynamicTimer(id, duration, jid, motivo)
		}
	}
}
