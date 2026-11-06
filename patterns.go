package gobellim

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PII detection for a field tagged sensitivity=patterns. A match becomes a
// handle, the same way a secret field becomes one handle.
//
// Two ideas are borrowed from cloud-ru guardrails-llm-filter: a detector is not
// a regex but a regex plus a check, and the checks are what make the masking
// useful. A 16-digit number is not a card unless Luhn accepts it, and an
// 11-digit number is not a SNILS unless its checksum matches. Without that,
// invoices, timestamps and order numbers turn into handles and the model loses
// information it needs.
//
// This is still not a DLP system. A match is masked; a miss is not a promise.
type patternDetector struct {
	re    *regexp.Regexp
	valid func(match string) bool
}

var (
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	ibanRE  = regexp.MustCompile(`\b[A-Z]{2}[0-9]{2}[A-Z0-9]{10,30}\b`)
	cardRE  = regexp.MustCompile(`\b(?:[0-9][ \-]?){12,18}[0-9]\b`)
	snilsRE = regexp.MustCompile(`\b[0-9]{3}-[0-9]{3}-[0-9]{3}[ \-][0-9]{2}\b`)
	ogrnRE  = regexp.MustCompile(`\b(?:[0-9]{13}|[0-9]{15})\b`)
	innRE   = regexp.MustCompile(`\b(?:[0-9]{10}|[0-9]{12})\b`)
	phoneRE = regexp.MustCompile(`(?:\+7|\b8)[\s\-]?\(?[0-9]{3}\)?[\s\-]?[0-9]{3}[\s\-]?[0-9]{2}[\s\-]?[0-9]{2}\b`)
)

var patternDetectors = []patternDetector{
	{re: emailRE},
	{re: ibanRE, valid: ibanValid},
	{re: cardRE, valid: func(m string) bool { return luhnValid(digitsOnly(m)) }},
	{re: snilsRE, valid: func(m string) bool { return snilsValid(digitsOnly(m)) }},
	{re: ogrnRE, valid: ogrnValid},
	{re: innRE, valid: innValid},
	{re: phoneRE},
}

type patternMatch struct{ start, end int }

// maskPatterns replaces every detected value with its handle. The original stays in
// the handle set, so the model can reference it and the answer can be restored.
func maskPatterns(s string, l Label, hs *handleSet) string {
	if s == "" {
		return s
	}
	matches := findPatterns(s)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	pos := 0
	for _, m := range matches {
		b.WriteString(s[pos:m.start])
		b.WriteString(hs.put(s[m.start:m.end], l))
		pos = m.end
	}
	b.WriteString(s[pos:])
	return b.String()
}

// findPatterns returns non-overlapping matches, leftmost and longest first. One pass
// over the original string, so a handle is never searched for Patterns again.
func findPatterns(s string) []patternMatch {
	var all []patternMatch
	for _, d := range patternDetectors {
		for _, loc := range d.re.FindAllStringIndex(s, -1) {
			if d.valid != nil && !d.valid(s[loc[0]:loc[1]]) {
				continue
			}
			all = append(all, patternMatch{start: loc[0], end: loc[1]})
		}
	}
	if len(all) < 2 {
		return all
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end > all[j].end
	})
	out := all[:0]
	last := 0
	for _, m := range all {
		if m.start < last {
			continue
		}
		out = append(out, m)
		last = m.end
	}
	return out
}

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// luhnValid accepts a card number only when its check digit is correct.
func luhnValid(d string) bool {
	if len(d) < 12 {
		return false
	}
	sum := 0
	double := false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// snilsValid checks the two control digits of an 11-digit SNILS.
func snilsValid(d string) bool {
	if len(d) != 11 {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(d[i]-'0') * (9 - i)
	}
	control := sum % 101
	if control > 99 {
		control = 0
	}
	return d[9] == byte('0'+control/10) && d[10] == byte('0'+control%10)
}

var (
	innWeights10 = []int{2, 4, 10, 3, 5, 9, 4, 6, 8}
	innWeights11 = []int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
	innWeights12 = []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
)

// innValid checks a 10-digit company or 12-digit person INN.
func innValid(d string) bool {
	switch len(d) {
	case 10:
		return innCheckDigit(d[:9], innWeights10) == int(d[9]-'0')
	case 12:
		return innCheckDigit(d[:10], innWeights11) == int(d[10]-'0') &&
			innCheckDigit(d[:11], innWeights12) == int(d[11]-'0')
	}
	return false
}

func innCheckDigit(d string, weights []int) int {
	sum := 0
	for i := 0; i < len(d); i++ {
		sum += int(d[i]-'0') * weights[i]
	}
	return sum % 11 % 10
}

// ogrnValid checks the control digit of a 13-digit OGRN or 15-digit OGRNIP.
func ogrnValid(d string) bool {
	switch len(d) {
	case 13:
		n, err := strconv.ParseInt(d[:12], 10, 64)
		return err == nil && n%11%10 == int64(d[12]-'0')
	case 15:
		n, err := strconv.ParseInt(d[:14], 10, 64)
		return err == nil && n%13%10 == int64(d[14]-'0')
	}
	return false
}

// ibanValid reorders the IBAN as the spec requires and checks it modulo 97.
func ibanValid(raw string) bool {
	s := strings.ToUpper(strings.ReplaceAll(raw, " ", ""))
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	sum := 0
	for i := 0; i < len(s); i++ {
		c := s[(i+4)%len(s)]
		switch {
		case c >= '0' && c <= '9':
			sum = (sum*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			sum = (sum*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return sum == 1
}
