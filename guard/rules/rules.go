package rules

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gobellim/kit/guard"
)

var injectionPhrases = foldPhrases([]string{
	"ignore previous instructions",
	"ignore all previous instructions",
	"disregard previous instructions",
	"forget your instructions",
	"reveal your system prompt",
	"print your instructions",
	"you are now",
	"игнорируй предыдущие инструкции",
	"игнорируй все инструкции",
	"забудь инструкции",
	"покажи системный промпт",
	"раскрой системный промпт",
})

var injectionPatterns = []string{
	`(?i)ignore\s+(all\s+)?(previous|above|prior)\s+(instructions|prompts?)`,
	`(?i)disregard\s+(the\s+)?(system|previous)`,
	`(?i)(reveal|show|print|repeat)\s+(me\s+)?(your\s+)?(system\s+)?(prompt|instructions)`,
	`(?i)you\s+are\s+now\s+`,
	`(?i)new\s+instructions?\s*:`,
	`(?i)игнориру(й|йте)\s+`,
	`(?i)(покажи|раскрой|повтори)\s+(свой\s+)?(систем|промпт)`,
	`(?i)новые\s+инструкции`,
}

type matcher struct {
	label    string
	category guard.Category
	re       *regexp.Regexp
	group    int
	validate func(string) bool
}

var matchers = []matcher{
	{label: "OPENAI_KEY", category: guard.CatSecret, re: canonicalRe(`(?i)sk-[A-Za-z0-9_\-]{16,}`)},
	{label: "AWS_ACCESS_KEY_ID", category: guard.CatSecret, re: canonicalRe(`(?i)AKIA[0-9A-Z]{16}`)},
	{label: "GITHUB_TOKEN", category: guard.CatSecret, re: canonicalRe(`(?i)gh[pousr]_[A-Za-z0-9]{20,}`)},
	{label: "PRIVATE_KEY", category: guard.CatSecret, re: canonicalRe(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{label: "JWT", category: guard.CatSecret, re: canonicalRe(`(?i)eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
	{label: "BEARER", category: guard.CatSecret, re: canonicalRe(`(?i)Bearer\s+[A-Za-z0-9._\-]{20,}`)},
	{label: "GOOGLE_API_KEY", category: guard.CatSecret, re: canonicalRe(`(?i)AIza[0-9A-Za-z_\-]{35}`)},
	{label: "SLACK_TOKEN", category: guard.CatSecret, re: canonicalRe(`(?i)xox[baprs]-[0-9A-Za-z\-]{10,}`)},

	{label: "BANK_CARD", category: guard.CatPII, re: canonicalRe(`(?i)\b(?:\d[ \-]?){11,18}\d\b`), validate: func(s string) bool { return validLuhn(digits(s)) }},
	{label: "IBAN", category: guard.CatPII, re: canonicalRe(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`), validate: validIBAN},
	{label: "IBAN", category: guard.CatPII, re: canonicalRe(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]){11,30}\b`), validate: validIBAN},
	{label: "CVV", category: guard.CatSecret, re: canonicalRe(`(?i)\b(?:cvv|cvc)\b[\s:#=]*(\d{3,4})\b`), group: 1},
	{label: "PASSPORT", category: guard.CatPII, re: canonicalRe(`\b\d{2}\s?\d{2}\s?\d{6}\b`)},

	{label: "SNILS", category: guard.CatPII, re: regexp.MustCompile(`\b\d{3}-\d{3}-\d{3}\s\d{2}\b`), validate: func(s string) bool { return validSNILS(digits(s)) }},
	{label: "INN", category: guard.CatPII, re: regexp.MustCompile(`\b\d{10}\b`), validate: func(s string) bool { return validINN10(s) }},
	{label: "INN", category: guard.CatPII, re: regexp.MustCompile(`\b\d{12}\b`), validate: func(s string) bool { return validINN12(s) }},
	{label: "OGRN", category: guard.CatPII, re: regexp.MustCompile(`\b\d{13}\b`), validate: func(s string) bool { return validOGRN(s, 13) }},
	{label: "OGRNIP", category: guard.CatPII, re: regexp.MustCompile(`\b\d{15}\b`), validate: func(s string) bool { return validOGRN(s, 15) }},

	{label: "EMAIL", category: guard.CatPII, re: regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	{label: "PHONE", category: guard.CatPII, re: regexp.MustCompile(`\+7[\s\-]?\(?\d{3}\)?[\s\-]?\d{3}[\s\-]?\d{2}[\s\-]?\d{2}`)},
}

func init() {
	for _, p := range injectionPatterns {
		matchers = append(matchers, matcher{
			label:    "PATTERN",
			category: guard.CatInjection,
			re:       canonicalRe(p),
		})
	}
}

func canonicalRe(expr string) *regexp.Regexp {
	return regexp.MustCompile(guard.Canonicalize(expr))
}

func foldPhrases(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = guard.Canonicalize(s)
	}
	return out
}

type Detector struct{}

func New() *Detector { return &Detector{} }

func (*Detector) Name() string { return "L1" }

func (*Detector) Detect(_ context.Context, text string) ([]guard.Finding, error) {
	if text == "" {
		return nil, nil
	}
	var findings []guard.Finding
	findings = append(findings, phrases(text)...)
	for _, m := range matchers {
		for _, loc := range m.re.FindAllStringSubmatchIndex(text, -1) {
			g := m.group * 2
			if g+1 >= len(loc) || loc[g] < 0 {
				continue
			}
			start, end := loc[g], loc[g+1]
			snippet := text[start:end]
			if m.validate != nil && !m.validate(snippet) {
				continue
			}
			findings = append(findings, guard.Finding{
				Category: m.category,
				Label:    m.label,
				Start:    utf8.RuneCountInString(text[:start]),
				End:      utf8.RuneCountInString(text[:end]),
				Score:    1,
				Layer:    "L1",
			})
		}
	}
	return findings, nil
}

func phrases(text string) []guard.Finding {
	lower := strings.ToLower(text)
	var out []guard.Finding
	for _, ph := range injectionPhrases {
		from := 0
		for {
			i := strings.Index(lower[from:], ph)
			if i < 0 {
				break
			}
			b := from + i
			out = append(out, guard.Finding{
				Category: guard.CatInjection,
				Label:    "PHRASE",
				Start:    utf8.RuneCountInString(text[:b]),
				End:      utf8.RuneCountInString(text[:b+len(ph)]),
				Score:    1,
				Layer:    "L1",
			})
			from = b + len(ph)
		}
	}
	return out
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validLuhn(d string) bool {
	if len(d) < 12 || len(d) > 19 {
		return false
	}
	sum := 0
	parity := len(d) % 2
	for i := 0; i < len(d); i++ {
		n := int(d[i] - '0')
		if i%2 == parity {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
	}
	return sum%10 == 0
}

var ibanRe = regexp.MustCompile(`^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$`)

func validIBAN(s string) bool {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if !ibanRe.MatchString(s) {
		return false
	}
	rearranged := s[4:] + s[:4]
	mod := 0
	for _, r := range rearranged {
		var v int
		switch {
		case r >= '0' && r <= '9':
			v = int(r - '0')
		case r >= 'A' && r <= 'Z':
			v = int(r-'A') + 10
		default:
			return false
		}
		if v < 10 {
			mod = (mod*10 + v) % 97
		} else {
			mod = (mod*100 + v) % 97
		}
	}
	return mod == 1
}

func validSNILS(d string) bool {
	if len(d) != 11 {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(d[i]-'0') * (9 - i)
	}
	var cs int
	switch {
	case sum < 100:
		cs = sum
	case sum == 100 || sum == 101:
		cs = 0
	default:
		cs = sum % 101
		if cs == 100 {
			cs = 0
		}
	}
	control := int(d[9]-'0')*10 + int(d[10]-'0')
	return cs == control
}

func validINN10(d string) bool {
	if len(d) != 10 {
		return false
	}
	w := [9]int{2, 4, 10, 3, 5, 9, 4, 6, 8}
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(d[i]-'0') * w[i]
	}
	return sum%11%10 == int(d[9]-'0')
}

func validINN12(d string) bool {
	if len(d) != 12 {
		return false
	}
	w11 := [10]int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
	s11 := 0
	for i := 0; i < 10; i++ {
		s11 += int(d[i]-'0') * w11[i]
	}
	if s11%11%10 != int(d[10]-'0') {
		return false
	}
	w12 := [11]int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
	s12 := 0
	for i := 0; i < 11; i++ {
		s12 += int(d[i]-'0') * w12[i]
	}
	return s12%11%10 == int(d[11]-'0')
}

func validOGRN(d string, n int) bool {
	if len(d) != n {
		return false
	}
	body := d[:n-1]
	var num int
	for i := 0; i < len(body); i++ {
		num = num*10 + int(body[i]-'0')
		if num > 1<<62 {
			num %= 11
		}
	}
	return (num%11)%10 == int(d[n-1]-'0')
}
