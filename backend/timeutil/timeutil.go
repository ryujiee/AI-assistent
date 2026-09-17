// Package timeutil centralizes every time calculation of the application in the
// America/Sao_Paulo time zone.
//
// The application must never depend on the host, container or database time
// zone: scheduled reminders and morning summaries are only correct when every
// "now", every parsed date and every wall clock comparison happen in Brasília
// time.
package timeutil

import (
	"log"
	"time"
)

// ZoneName is the IANA identifier of the application time zone. It is also the
// value sent to PostgreSQL with SET TIME ZONE, so it must stay a real IANA name
// even when the Go fallback offset is in use.
const ZoneName = "America/Sao_Paulo"

// brazilLocation is resolved once at startup. When the tzdata database is not
// available in the runtime image, a fixed -03:00 offset is used as fallback so
// the application keeps working instead of silently falling back to UTC.
var brazilLocation = loadLocation()

func loadLocation() *time.Location {
	loc, err := time.LoadLocation(ZoneName)
	if err != nil {
		log.Printf("timeutil: could not load %s (%v), using fixed -03:00 offset", ZoneName, err)
		return time.FixedZone("-03", -3*60*60)
	}
	return loc
}

// Location returns the application time zone (America/Sao_Paulo).
func Location() *time.Location {
	return brazilLocation
}

// Now returns the current instant already converted to Brasília time.
func Now() time.Time {
	return time.Now().In(brazilLocation)
}

// StartOfDay returns midnight of the given day in Brasília time.
func StartOfDay(t time.Time) time.Time {
	local := t.In(brazilLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, brazilLocation)
}

// AsLocalWallClock reinterprets a value read from a TIMESTAMP (without time
// zone) column as Brasília time.
//
// The pgx driver preserves only the wall clock of those columns and hands the
// value back tagged as UTC. Formatting such a value is already correct, but any
// arithmetic against a real instant (a Sub, a Before, an Add on top of Now)
// would be off by the zone offset. This function restores the intended zone.
func AsLocalWallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), brazilLocation)
}

// acceptedLayouts lists the date formats the language model may produce when it
// calls a scheduling tool, from the most to the least specific.
var acceptedLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"02/01/2006 15:04",
}

// ParseLocal converts a date string coming from the model into a Brasília time.
//
// A string carrying an explicit offset (RFC 3339, e.g. "2026-09-18T10:00:00Z")
// is converted to the equivalent Brasília instant. A string without offset is
// read as a wall clock already expressed in Brasília time, which is how the
// user states appointments in the chat.
func ParseLocal(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(brazilLocation), nil
	}

	var lastErr error
	for _, layout := range acceptedLayouts {
		t, err := time.ParseInLocation(layout, value, brazilLocation)
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}
