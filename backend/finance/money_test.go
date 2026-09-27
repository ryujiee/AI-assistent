package finance

import (
	"slices"
	"strings"
	"testing"
	"time"

	"secretary/timeutil"
)

func TestParseAmount(t *testing.T) {
	cases := map[string]int64{
		"37,90":       3790,
		"37.90":       3790,
		"37,9":        3790,
		"3.000":       300000,
		"3 mil":       300000,
		"3mil":        300000,
		"2,5 mil":     250000,
		"1.5k":        150000,
		"mil":         100000,
		"R$ 89,99":    8999,
		"r$89,99":     8999,
		"R$ 1.234,56": 123456,
		"1,234.56":    123456,
		"1.000.000":   100000000,
		"50":          5000,
		"50 reais":    5000,
		"250,00":      25000,
		"0,50":        50,
		"1,500":       150000,
		"12.":         1200,
	}
	for in, want := range cases {
		got, err := ParseAmount(in)
		if err != nil || got != want {
			t.Errorf("ParseAmount(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestParseAmountRejects(t *testing.T) {
	for _, in := range []string{"", "abc", "0", "0,00", "-5", "1,2,3", "1.23.4", "12,3456", "R$", "999999999999999"} {
		if got, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) = %d, want error", in, got)
		}
	}
}

func TestExtractAmounts(t *testing.T) {
	cases := map[string][]int64{
		"gastei 50 no mercado":                   {5000},
		"Paguei 80 reais no mercado.":            {8000},
		"gastei 37,90 no ifood":                  {3790},
		"Recebi 3.000":                           {300000},
		"recebi 3 mil do freela":                 {300000},
		"foram 37 reais e 90 centavos":           {3790, 3700, 9000},
		"comprei combustível por R$ 250,00 hoje": {25000},
		"gastei mil reais no conserto":           {100000},
		"na verdade foi 97":                      {9700},
		"paguei a internet":                      nil,
	}
	for in, want := range cases {
		got := ExtractAmounts(in)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("ExtractAmounts(%q) = %v, missing %d", in, got, w)
			}
		}
		if want == nil && len(got) != 0 {
			t.Errorf("ExtractAmounts(%q) = %v, want none", in, got)
		}
	}
	if !AmountInText(3790, "gastei 37,90") || AmountInText(8900, "gastei 80 no mercado") {
		t.Error("AmountInText evidence check wrong")
	}
}

func TestFormatBRL(t *testing.T) {
	cases := map[int64]string{0: "R$ 0,00", 5: "R$ 0,05", 8740: "R$ 87,40", 123456: "R$ 1.234,56", 100000000: "R$ 1.000.000,00", -1050: "-R$ 10,50"}
	for in, want := range cases {
		if got := FormatBRL(in); got != want {
			t.Errorf("FormatBRL(%d) = %q, want %q", in, got, want)
		}
	}
	if got := FormatBRLShort(482000); got != "R$ 4.820" {
		t.Errorf("FormatBRLShort = %q", got)
	}
	if got := FormatBRLShort(8740); got != "R$ 87,40" {
		t.Errorf("FormatBRLShort cents = %q", got)
	}
}

// Sunday 27/09/2026, 10:00 in Brasília.
var fixedNow = time.Date(2026, 9, 27, 10, 0, 0, 0, timeutil.Location())

func TestResolveDate(t *testing.T) {
	cases := map[string]string{
		"":                "2026-09-27",
		"hoje":            "2026-09-27",
		"ontem":           "2026-09-26",
		"Anteontem":       "2026-09-25",
		"sexta":           "2026-09-25",
		"sexta-feira":     "2026-09-25",
		"na sexta":        "2026-09-25",
		"sábado":          "2026-09-26",
		"domingo":         "2026-09-27",
		"domingo passado": "2026-09-20",
		"segunda":         "2026-09-21",
		"dia 10":          "2026-09-10",
		"dia 30":          "2026-08-30",
		"dia 31":          "2026-08-31",
		"10/09":           "2026-09-10",
		"15/12":           "2025-12-15",
		"10/09/2026":      "2026-09-10",
		"2026-09-01":      "2026-09-01",
	}
	for in, want := range cases {
		got, err := ResolveDate(in, fixedNow)
		if err != nil || got != want {
			t.Errorf("ResolveDate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolveDateRejects(t *testing.T) {
	for in, want := range map[string]error{
		"2026-09-28":     ErrFutureDate,
		"amanhã":         ErrInvalidDate,
		"semana passada": ErrVagueDate,
		"32/01":          ErrInvalidDate,
		"31/02/2026":     ErrInvalidDate,
		"1999-01-01":     ErrInvalidDate,
	} {
		if _, err := ResolveDate(in, fixedNow); err != want {
			t.Errorf("ResolveDate(%q) err = %v, want %v", in, err, want)
		}
	}
}

func TestResolvePeriod(t *testing.T) {
	cases := []struct {
		key, month, start, end string
		wantStart, wantEnd     string
	}{
		{"this_month", "", "", "", "2026-09-01", "2026-09-27"},
		{"last_month", "", "", "", "2026-08-01", "2026-08-31"},
		{"last_7_days", "", "", "", "2026-09-21", "2026-09-27"},
		{"last_30_days", "", "", "", "2026-08-29", "2026-09-27"},
		{"this_week", "", "", "", "2026-09-21", "2026-09-27"},
		{"last_week", "", "", "", "2026-09-14", "2026-09-20"},
		{"today", "", "", "", "2026-09-27", "2026-09-27"},
		{"month", "2026-02", "", "", "2026-02-01", "2026-02-28"},
		{"custom", "", "2026-09-10", "2026-09-20", "2026-09-10", "2026-09-20"},
	}
	for _, c := range cases {
		p, err := ResolvePeriod(c.key, c.month, c.start, c.end, fixedNow)
		if err != nil || p.Start != c.wantStart || p.End != c.wantEnd {
			t.Errorf("ResolvePeriod(%s) = %+v, %v; want %s..%s", c.key, p, err, c.wantStart, c.wantEnd)
		}
	}
	for _, bad := range [][4]string{{"custom", "", "2026-09-20", "2026-09-10"}, {"month", "2026-13", "", ""}, {"month", "2027-01", "", ""}, {"weird", "", "", ""}} {
		if _, err := ResolvePeriod(bad[0], bad[1], bad[2], bad[3], fixedNow); err == nil {
			t.Errorf("ResolvePeriod(%v) accepted", bad)
		}
	}
}

func TestPreviousPeriod(t *testing.T) {
	mtd, _ := ResolvePeriod("this_month", "", "", "", fixedNow)
	if p := PreviousPeriod(mtd, fixedNow); p.Start != "2026-08-01" || p.End != "2026-08-27" {
		t.Errorf("month-to-date baseline = %+v", p)
	}
	aug, _ := ResolvePeriod("month", "2026-08", "", "", fixedNow)
	if p := PreviousPeriod(aug, fixedNow); p.Start != "2026-07-01" || p.End != "2026-07-31" {
		t.Errorf("whole month baseline = %+v", p)
	}
	mar31 := time.Date(2026, 3, 31, 12, 0, 0, 0, timeutil.Location())
	mtdMar, _ := ResolvePeriod("this_month", "", "", "", mar31)
	if p := PreviousPeriod(mtdMar, mar31); p.End != "2026-02-28" {
		t.Errorf("clamped baseline = %+v", p)
	}
	custom, _ := ResolvePeriod("custom", "", "2026-09-10", "2026-09-20", fixedNow)
	if p := PreviousPeriod(custom, fixedNow); p.Start != "2026-08-30" || p.End != "2026-09-09" {
		t.Errorf("custom baseline = %+v", p)
	}
	if d := mtd.ElapsedDays(fixedNow); d != 27 {
		t.Errorf("elapsed days = %d", d)
	}
}

func TestSanitizeText(t *testing.T) {
	in := "PIX para João CPF 123.456.789-09 chave 1b4e28ba-2fa1-11d2-883f-0016d3cca427 email joao@x.com fone (11) 99999-8888 ag 1234 conta 12345-6 valor 1.500"
	out := SanitizeText(in)
	for _, leaked := range []string{"123.456.789-09", "1b4e28ba", "joao@x.com", "99999-8888", "12345-6"} {
		if strings.Contains(out, leaked) {
			t.Errorf("sanitized text still has %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "1.500") || !strings.Contains(out, "João") {
		t.Errorf("sanitize removed useful text: %s", out)
	}
	if CleanExternalRef("E1234567820260927100012345678901") == nil {
		t.Error("valid E2E id rejected")
	}
	if CleanExternalRef("123.456.789-09") != nil {
		t.Error("CPF accepted as external ref")
	}
}
