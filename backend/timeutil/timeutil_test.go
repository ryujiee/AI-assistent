package timeutil

import (
	"testing"
	"time"
)

func TestParseLocalWithoutOffsetKeepsWallClock(t *testing.T) {
	got, err := ParseLocal("2026-09-18T14:30:00")
	if err != nil {
		t.Fatalf("ParseLocal returned an error: %v", err)
	}

	if h, m := got.Hour(), got.Minute(); h != 14 || m != 30 {
		t.Errorf("wall clock changed: got %02d:%02d, want 14:30", h, m)
	}
	if name := got.Location().String(); name != ZoneName {
		t.Errorf("location = %q, want %q", name, ZoneName)
	}
}

func TestParseLocalWithUTCOffsetConvertsToBrasilia(t *testing.T) {
	// 17:30 UTC is 14:30 in Brasília.
	got, err := ParseLocal("2026-09-18T17:30:00Z")
	if err != nil {
		t.Fatalf("ParseLocal returned an error: %v", err)
	}

	if h, m := got.Hour(), got.Minute(); h != 14 || m != 30 {
		t.Errorf("UTC input not converted: got %02d:%02d, want 14:30", h, m)
	}
}

func TestParseLocalAcceptsBrazilianFormat(t *testing.T) {
	got, err := ParseLocal("18/09/2026 09:00")
	if err != nil {
		t.Fatalf("ParseLocal returned an error: %v", err)
	}

	if got.Day() != 18 || got.Month() != time.September || got.Hour() != 9 {
		t.Errorf("unexpected date: %s", got.Format(time.RFC3339))
	}
}

func TestParseLocalRejectsGarbage(t *testing.T) {
	if _, err := ParseLocal("amanhã de manhã"); err == nil {
		t.Error("expected an error for an unparseable value")
	}
}

// TestAsLocalWallClockFixesDriverZone reproduces what the pgx driver returns for
// a TIMESTAMP (without time zone) column: the right wall clock tagged as UTC.
// Arithmetic on such a value is off by the zone offset until it is reinterpreted.
func TestAsLocalWallClockFixesDriverZone(t *testing.T) {
	fromDriver := time.Date(2026, 9, 18, 14, 30, 0, 0, time.UTC)
	got := AsLocalWallClock(fromDriver)

	if h, m := got.Hour(), got.Minute(); h != 14 || m != 30 {
		t.Errorf("wall clock changed: got %02d:%02d, want 14:30", h, m)
	}
	if got.Equal(fromDriver) {
		t.Error("expected a different instant: the UTC-tagged value should shift by the offset")
	}

	_, offset := got.Zone()
	if diff := got.Sub(fromDriver); diff != time.Duration(-offset)*time.Second {
		t.Errorf("instant shift = %v, want %v", diff, time.Duration(-offset)*time.Second)
	}
}

func TestStartOfDayIsMidnightInBrasilia(t *testing.T) {
	got := StartOfDay(time.Date(2026, 9, 18, 23, 45, 0, 0, time.UTC))

	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
		t.Errorf("not midnight: %s", got.Format(time.RFC3339))
	}
	// 23:45 UTC on the 18th is already 20:45 on the 18th in Brasília.
	if got.Day() != 18 {
		t.Errorf("day = %d, want 18", got.Day())
	}
}

func TestNowUsesBrasiliaLocation(t *testing.T) {
	if name := Now().Location().String(); name != ZoneName {
		t.Errorf("Now location = %q, want %q", name, ZoneName)
	}
}
