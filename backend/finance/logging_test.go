package finance

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogsAndInboxCarryNoPersonalData(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	h := newAgentHarness(t)
	h.send(t, from(h.ana, "gastei 123,45 no mercado, meu CPF é 123.456.789-09"), create(item("EXPENSE", 12345, "Mercado", "", 0.95)), echo())

	logs := strings.ToLower(buf.String())
	if !strings.Contains(logs, "finance.agent.turn") {
		t.Fatal("agent turn not logged")
	}
	for _, secret := range []string{"123,45", "123.456.789-09", "gastei", "cpf", "5511900000001", "ana"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q:\n%s", secret, buf.String())
		}
	}
	var stored string
	h.s.DB.QueryRow(context.Background(), "SELECT text FROM finance_inbox ORDER BY id DESC LIMIT 1").Scan(&stored)
	if strings.Contains(stored, "123.456.789-09") || !strings.Contains(stored, "123,45") {
		t.Errorf("inbox text = %q", stored)
	}
}
