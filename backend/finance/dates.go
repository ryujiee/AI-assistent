package finance

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"secretary/timeutil"
)

// Dates are civil days ("2006-01-02") in the application time zone
// (timeutil, America/Sao_Paulo). Relative expressions are resolved here, in
// code, so the model never does calendar arithmetic.

const DateLayout = "2006-01-02"

var (
	ErrFutureDate    = errors.New("a data está no futuro")
	ErrVagueDate     = errors.New("data imprecisa")
	ErrInvalidDate   = errors.New("data inválida")
	ErrInvalidPeriod = errors.New("período inválido")
)

var weekdays = map[string]time.Weekday{
	"domingo": time.Sunday, "segunda": time.Monday, "terca": time.Tuesday, "quarta": time.Wednesday,
	"quinta": time.Thursday, "sexta": time.Friday, "sabado": time.Saturday,
}

var weekdayNames = [...]string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}

var monthNames = [...]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

var (
	dayOnly = regexp.MustCompile(`^(?:dia\s+)?(\d{1,2})$`)
	dayMon  = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})(?:/(\d{2}|\d{4}))?$`)
)

// Today returns the current civil day in the application time zone.
func Today(now time.Time) time.Time {
	return timeutil.StartOfDay(now)
}

func civil(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, timeutil.Location())
}

func daysIn(y int, m time.Month) int {
	return civil(y, m+1, 0).Day()
}

// ResolveDate turns what the user said ("ontem", "sexta", "dia 10", "10/09",
// "2026-09-10") into a civil date. Empty means today. Dates in the future
// are rejected: a transaction that already happened cannot be tomorrow.
func ResolveDate(expr string, now time.Time) (string, error) {
	today := Today(now)
	s := normalize(expr)
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "na "), "no "))

	var d time.Time
	switch s {
	case "", "hoje", "agora", "today", "agorinha":
		d = today
	case "ontem", "yesterday":
		d = today.AddDate(0, 0, -1)
	case "anteontem":
		d = today.AddDate(0, 0, -2)
	case "semana passada", "mes passado", "ano passado", "esses dias", "outro dia":
		return "", ErrVagueDate
	default:
		var err error
		if d, err = resolveExplicit(s, today); err != nil {
			return "", err
		}
	}
	if d.After(today) {
		return "", ErrFutureDate
	}
	if d.Year() < 2000 {
		return "", ErrInvalidDate
	}
	return d.Format(DateLayout), nil
}

func resolveExplicit(s string, today time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation(DateLayout, s, timeutil.Location()); err == nil {
		return t, nil
	}

	// Weekdays: "sexta", "sexta-feira", "sexta passada".
	past := strings.HasSuffix(s, " passada") || strings.HasSuffix(s, " passado") || strings.HasPrefix(s, "ultima ") || strings.HasPrefix(s, "ultimo ")
	w := strings.TrimSpace(strings.NewReplacer(" passada", "", " passado", "", "ultima ", "", "ultimo ", "", "-feira", "", " feira", "").Replace(s))
	if wd, ok := weekdays[w]; ok {
		back := (int(today.Weekday()) - int(wd) + 7) % 7
		if back == 0 && past {
			back = 7
		}
		return today.AddDate(0, 0, -back), nil
	}

	// "dia 10" / "10": the most recent day 10 that is not in the future.
	if m := dayOnly.FindStringSubmatch(s); m != nil {
		day, _ := strconv.Atoi(m[1])
		if day < 1 || day > 31 {
			return time.Time{}, ErrInvalidDate
		}
		y, mo := today.Year(), today.Month()
		for i := 0; i < 12; i++ {
			if day <= daysIn(y, mo) {
				if d := civil(y, mo, day); !d.After(today) {
					return d, nil
				}
			}
			mo--
			if mo == 0 {
				mo, y = 12, y-1
			}
		}
		return time.Time{}, ErrInvalidDate
	}

	// "10/09", "10/09/2026", "10/9/26".
	if m := dayMon.FindStringSubmatch(s); m != nil {
		day, _ := strconv.Atoi(m[1])
		mon, _ := strconv.Atoi(m[2])
		if mon < 1 || mon > 12 {
			return time.Time{}, ErrInvalidDate
		}
		y := today.Year()
		explicitYear := m[3] != ""
		if explicitYear {
			y, _ = strconv.Atoi(m[3])
			if y < 100 {
				y += 2000
			}
		}
		if day < 1 || day > daysIn(y, time.Month(mon)) {
			return time.Time{}, ErrInvalidDate
		}
		d := civil(y, time.Month(mon), day)
		if !explicitYear && d.After(today) {
			d = civil(y-1, time.Month(mon), day)
		}
		return d, nil
	}
	return time.Time{}, ErrInvalidDate
}

// Period is an inclusive range of civil days.
type Period struct {
	Key   string `json:"key"`
	Start string `json:"start"`
	End   string `json:"end"`
	Label string `json:"label"`
}

func (p Period) startTime() time.Time {
	t, _ := time.ParseInLocation(DateLayout, p.Start, timeutil.Location())
	return t
}

func (p Period) endTime() time.Time {
	t, _ := time.ParseInLocation(DateLayout, p.End, timeutil.Location())
	return t
}

// Days counts the days in the period.
func (p Period) Days() int {
	return int(p.endTime().Sub(p.startTime()).Hours()/24+0.5) + 1
}

// ElapsedDays counts the days of the period up to today (for averages and
// projections of a period still running).
func (p Period) ElapsedDays(now time.Time) int {
	today := Today(now)
	end := p.endTime()
	if today.Before(end) {
		end = today
	}
	n := int(end.Sub(p.startTime()).Hours()/24+0.5) + 1
	if n < 1 {
		return 1
	}
	return n
}

// IsCurrentMonth reports whether the period is the running month to date.
func (p Period) IsCurrentMonth(now time.Time) bool {
	today := Today(now)
	s := p.startTime()
	return s.Day() == 1 && s.Year() == today.Year() && s.Month() == today.Month() && !p.endTime().Before(today)
}

func mkPeriod(key string, start, end time.Time, label string) Period {
	return Period{Key: key, Start: start.Format(DateLayout), End: end.Format(DateLayout), Label: label}
}

func monthLabel(t time.Time) string {
	name := monthNames[t.Month()-1]
	return strings.ToUpper(name[:1]) + name[1:] + " de " + strconv.Itoa(t.Year())
}

func rangeLabel(s, e time.Time) string {
	if s.Year() != e.Year() {
		return s.Format("02/01/2006") + " a " + e.Format("02/01/2006")
	}
	return s.Format("02/01") + " a " + e.Format("02/01")
}

// PeriodKeys lists the periods the tools and the API accept.
var PeriodKeys = []string{"today", "yesterday", "this_week", "last_week", "this_month", "last_month", "last_7_days", "last_30_days", "this_year", "month", "custom"}

// ResolvePeriod computes the dates of a period key. "month" takes month as
// "YYYY-MM"; "custom" takes start and end as "YYYY-MM-DD".
func ResolvePeriod(key, month, start, end string, now time.Time) (Period, error) {
	today := Today(now)
	firstOfMonth := civil(today.Year(), today.Month(), 1)
	weekStart := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7)) // Monday

	switch key {
	case "", "this_month":
		return mkPeriod("this_month", firstOfMonth, today, monthLabel(today)), nil
	case "today":
		return mkPeriod(key, today, today, "Hoje"), nil
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		return mkPeriod(key, y, y, "Ontem"), nil
	case "this_week":
		return mkPeriod(key, weekStart, today, "Esta semana"), nil
	case "last_week":
		return mkPeriod(key, weekStart.AddDate(0, 0, -7), weekStart.AddDate(0, 0, -1), "Semana passada"), nil
	case "last_month":
		s := firstOfMonth.AddDate(0, -1, 0)
		return mkPeriod(key, s, firstOfMonth.AddDate(0, 0, -1), monthLabel(s)), nil
	case "last_7_days":
		return mkPeriod(key, today.AddDate(0, 0, -6), today, "Últimos 7 dias"), nil
	case "last_30_days":
		return mkPeriod(key, today.AddDate(0, 0, -29), today, "Últimos 30 dias"), nil
	case "this_year":
		return mkPeriod(key, civil(today.Year(), 1, 1), today, "Ano de "+strconv.Itoa(today.Year())), nil
	case "month":
		t, err := time.ParseInLocation("2006-01", month, timeutil.Location())
		if err != nil {
			return Period{}, fmt.Errorf("%w: mês deve ser AAAA-MM", ErrInvalidPeriod)
		}
		e := t.AddDate(0, 1, -1)
		if t.After(today) {
			return Period{}, fmt.Errorf("%w: mês no futuro", ErrInvalidPeriod)
		}
		return mkPeriod(key, t, e, monthLabel(t)), nil
	case "custom":
		s, err1 := time.ParseInLocation(DateLayout, start, timeutil.Location())
		e, err2 := time.ParseInLocation(DateLayout, end, timeutil.Location())
		if err1 != nil || err2 != nil || e.Before(s) {
			return Period{}, fmt.Errorf("%w: use datas AAAA-MM-DD com início <= fim", ErrInvalidPeriod)
		}
		if e.Sub(s) > 5*366*24*time.Hour {
			return Period{}, fmt.Errorf("%w: intervalo longo demais", ErrInvalidPeriod)
		}
		return mkPeriod(key, s, e, rangeLabel(s, e)), nil
	}
	return Period{}, fmt.Errorf("%w: %q", ErrInvalidPeriod, key)
}

// PreviousPeriod is the comparison baseline. A month-to-date period compares
// with the same days of the previous month; whole months with the previous
// whole month; anything else with the same number of days right before.
func PreviousPeriod(p Period, now time.Time) Period {
	s, e := p.startTime(), p.endTime()
	if s.Day() == 1 {
		prevStart := s.AddDate(0, -1, 0)
		lastOfPrev := s.AddDate(0, 0, -1)
		if e.AddDate(0, 0, 1).Day() == 1 { // whole month
			return mkPeriod("previous", prevStart, lastOfPrev, monthLabel(prevStart))
		}
		day := e.Day()
		if day > lastOfPrev.Day() {
			day = lastOfPrev.Day()
		}
		pe := civil(prevStart.Year(), prevStart.Month(), day)
		return mkPeriod("previous", prevStart, pe, monthLabel(prevStart)+" (até dia "+strconv.Itoa(day)+")")
	}
	n := p.Days()
	pe := s.AddDate(0, 0, -1)
	ps := pe.AddDate(0, 0, -(n - 1))
	return mkPeriod("previous", ps, pe, rangeLabel(ps, pe))
}

// CalendarHint lists the last 8 days with weekday names for the prompt, so
// "sexta" or "anteontem" never depend on the model's arithmetic.
func CalendarHint(now time.Time) string {
	today := Today(now)
	var b strings.Builder
	for i := 0; i < 8; i++ {
		d := today.AddDate(0, 0, -i)
		tag := ""
		switch i {
		case 0:
			tag = " (hoje)"
		case 1:
			tag = " (ontem)"
		case 2:
			tag = " (anteontem)"
		}
		fmt.Fprintf(&b, "- %s %s%s\n", weekdayNames[d.Weekday()], d.Format("02/01/2006"), tag)
	}
	return b.String()
}

// HumanDate renders a civil date relative to today ("hoje", "ontem", "12/09").
func HumanDate(date string, now time.Time) string {
	d, err := time.ParseInLocation(DateLayout, date, timeutil.Location())
	if err != nil {
		return date
	}
	today := Today(now)
	switch {
	case d.Equal(today):
		return "hoje"
	case d.Equal(today.AddDate(0, 0, -1)):
		return "ontem"
	case d.Year() == today.Year():
		return d.Format("02/01")
	}
	return d.Format("02/01/2006")
}
