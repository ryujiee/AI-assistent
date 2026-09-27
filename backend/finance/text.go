package finance

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalize lowercases, trims and strips accents: "Alimentação " -> "alimentacao".
func normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(s))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var (
	cpfPattern   = regexp.MustCompile(`\b\d{3}\.?\d{3}\.?\d{3}-?\d{2}\b`)
	cnpjPattern  = regexp.MustCompile(`\b\d{2}\.?\d{3}\.?\d{3}/?\d{4}-?\d{2}\b`)
	uuidPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	emailPattern = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	phonePattern = regexp.MustCompile(`(?:\+?55\s?)?\(?\b\d{2}\)?\s?9?\d{4}-?\d{4}\b`)
	accountLike  = regexp.MustCompile(`(?i)\b(ag[eê]ncia|ag\.?|conta|c/c|cc)\s*:?\s*[\d.\-/]{3,}`)
	pixE2E       = regexp.MustCompile(`^E\d{8}\d{12}[A-Za-z0-9]{11}$`)
)

// SanitizeText removes personal identifiers (CPF, CNPJ, random PIX keys,
// e-mails, phone numbers, bank agency/account) from free text before it is
// stored. None of them is needed to track an expense.
func SanitizeText(s string) string {
	s = accountLike.ReplaceAllString(s, "$1 [removido]")
	s = cnpjPattern.ReplaceAllString(s, "[removido]")
	s = cpfPattern.ReplaceAllString(s, "[removido]")
	s = uuidPattern.ReplaceAllString(s, "[removido]")
	s = emailPattern.ReplaceAllString(s, "[removido]")
	s = phonePattern.ReplaceAllStringFunc(s, func(m string) string {
		// Short money-like numbers ("1.500", "3000") stay; only phone-shaped
		// sequences with 10+ digits go.
		digits := 0
		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= 10 {
			return "[removido]"
		}
		return m
	})
	return strings.TrimSpace(s)
}

// CleanExternalRef keeps only well-formed PIX end-to-end ids (E + ISPB +
// timestamp + 11 chars). Anything else might be a key or a document number.
func CleanExternalRef(ref string) *string {
	ref = strings.TrimSpace(ref)
	if !pixE2E.MatchString(ref) {
		return nil
	}
	return &ref
}

// truncate keeps free text inside the column limits.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > max {
		return strings.TrimSpace(string(r[:max]))
	}
	return s
}
