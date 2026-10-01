package finance

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Money is always int64 cents. Nothing in this package converts through float.

// MaxAmountCents caps a single transaction at R$ 1 bilhão: anything larger is
// a parsing accident, not a household expense.
const MaxAmountCents int64 = 100_000_000_000

var (
	ErrInvalidAmount = errors.New("valor inválido")
	digitsOnly       = regexp.MustCompile(`^[0-9]+$`)
)

// ParseAmount converts a Brazilian-style amount into cents.
//
// Rules, applied in order:
//   - "R$", "reais"/"real" and spaces are ignored; "mil" or "k" multiplies by 1000
//     ("3 mil", "2,5 mil", "1.5k", "mil").
//   - With both "." and ",", the last one is the decimal separator
//     ("1.234,56", "1,234.56").
//   - A single separator followed by exactly 3 digits is a thousands separator
//     ("3.000", "1,500"), unless a multiplier is present ("2,500 mil" is 2500).
//   - A single separator followed by 1 or 2 digits is the decimal separator
//     ("37,90", "37.9").
//   - Repeated separators are thousands separators ("1.000.000").
func ParseAmount(raw string) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "r$", "")
	s = strings.TrimSpace(s)
	for _, suffix := range []string{"reais", "real"} {
		s = strings.TrimSpace(strings.TrimSuffix(s, suffix))
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "mil"):
		multiplier = 1000
		s = strings.TrimSpace(strings.TrimSuffix(s, "mil"))
		if s == "" {
			s = "1"
		}
	case strings.HasSuffix(s, "k"):
		multiplier = 1000
		s = strings.TrimSpace(strings.TrimSuffix(s, "k"))
	}
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimRight(s, ".,")
	if s == "" || !strings.ContainsAny(s[:1], "0123456789") {
		return 0, ErrInvalidAmount
	}

	intPart, frac, err := splitDecimal(s, multiplier > 1)
	if err != nil {
		return 0, err
	}
	if !digitsOnly.MatchString(intPart) || (frac != "" && !digitsOnly.MatchString(frac)) {
		return 0, ErrInvalidAmount
	}
	// 14 significant digits keep (whole*scale+frac)*100*1000 far below int64.
	if len(intPart) > 12 || len(frac) > 4 || len(intPart)+len(frac) > 14 {
		return 0, ErrInvalidAmount
	}

	whole, _ := strconv.ParseInt(intPart, 10, 64)
	scale := int64(1)
	fracVal := int64(0)
	if frac != "" {
		fracVal, _ = strconv.ParseInt(frac, 10, 64)
		for range frac {
			scale *= 10
		}
	}
	// cents = (whole + frac/scale) * 100 * multiplier, kept exact in integers.
	num := (whole*scale + fracVal) * 100 * multiplier
	if num%scale != 0 {
		return 0, ErrInvalidAmount // e.g. "1,234" read as decimal: fractions of a cent
	}
	cents := num / scale
	if cents <= 0 || cents > MaxAmountCents {
		return 0, ErrInvalidAmount
	}
	return cents, nil
}

func splitDecimal(s string, hasMultiplier bool) (intPart, frac string, err error) {
	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	dots, commas := strings.Count(s, "."), strings.Count(s, ",")

	switch {
	case dots > 0 && commas > 0:
		dec := lastComma
		thousands := "."
		if lastDot > lastComma {
			dec, thousands = lastDot, ","
		}
		if strings.Count(s[dec+1:], ".")+strings.Count(s[dec+1:], ",") > 0 {
			return "", "", ErrInvalidAmount
		}
		return strings.ReplaceAll(s[:dec], thousands, ""), s[dec+1:], nil

	case dots+commas == 0:
		return s, "", nil

	case dots+commas > 1:
		sep := "."
		if commas > 0 {
			sep = ","
		}
		groups := strings.Split(s, sep)
		for _, g := range groups[1:] {
			if len(g) != 3 {
				return "", "", ErrInvalidAmount
			}
		}
		return strings.Join(groups, ""), "", nil
	}

	// Exactly one separator.
	idx := lastDot
	if lastComma >= 0 {
		idx = lastComma
	}
	after := s[idx+1:]
	if len(after) == 3 && !hasMultiplier {
		return s[:idx] + after, "", nil
	}
	if len(after) > 2 && !hasMultiplier {
		return "", "", ErrInvalidAmount
	}
	return s[:idx], after, nil
}

var (
	amountToken = regexp.MustCompile(`(?i)(?:r\$\s*)?(\d[\d.,]*)(\s*(?:mil\b|k\b))?`)
	reaisCents  = regexp.MustCompile(`(?i)(\d+)\s*(?:reais|real)\s*e\s*(\d{1,2})\s*centavos?`)
	loneMil     = regexp.MustCompile(`(?i)(^|[^\d\s])\s*\bmil\s+reais\b`)
)

// ExtractAmounts lists every amount written in a message. It backs the
// evidence check: an amount the model reports must appear in the text.
func ExtractAmounts(text string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(v int64) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, m := range reaisCents.FindAllStringSubmatch(text, -1) {
		whole, _ := strconv.ParseInt(m[1], 10, 64)
		cents, _ := strconv.ParseInt(m[2], 10, 64)
		if len(m[2]) == 1 {
			cents *= 10
		}
		if v := whole*100 + cents; v > 0 && v <= MaxAmountCents {
			add(v)
		}
	}
	for _, m := range amountToken.FindAllStringSubmatch(text, -1) {
		if v, err := ParseAmount(m[1] + m[2]); err == nil {
			add(v)
		}
	}
	if loneMil.MatchString(text) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "mil reais") {
		add(100000)
	}
	return out
}

// AmountInText reports whether cents was written in the text.
func AmountInText(cents int64, text string) bool {
	for _, v := range ExtractAmounts(text) {
		if v == cents {
			return true
		}
	}
	return false
}

// FormatBRL renders cents as "R$ 1.234,56".
func FormatBRL(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return sign + "R$ " + groupThousands(cents/100) + "," + twoDigits(cents%100)
}

// FormatBRLShort drops ",00" for whole amounts ("R$ 4.820"), used in reports.
func FormatBRLShort(cents int64) string {
	if cents%100 == 0 {
		sign := ""
		if cents < 0 {
			sign, cents = "-", -cents
		}
		return sign + "R$ " + groupThousands(cents/100)
	}
	return FormatBRL(cents)
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func twoDigits(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
