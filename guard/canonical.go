package guard

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func isInvisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r == 0x00AD,
		r == 0x034F,
		r == 0x061C,
		r == 0x180E,
		r == 0x2060,
		r == 0xFEFF:
		return true
	case r >= 0x2061 && r <= 0x2064:
		return true
	}
	return false
}

var confusables = map[rune][]rune{
	'a': {'а', 'α'},
	'b': {'в'},
	'c': {'с'},
	'd': {'ԁ'},
	'e': {'е', 'ε'},
	'g': {'ɡ'},
	'h': {'н', 'һ'},
	'i': {'і', 'ї', 'ι', 'ı'},
	'j': {'ј'},
	'k': {'к', 'κ'},
	'l': {'ӏ'},
	'm': {'м'},
	'n': {'Ν', 'ν'},
	'o': {'о', 'ο', 'օ'},
	'p': {'р', 'ρ'},
	's': {'ѕ', 'σ', 'ς'},
	't': {'т', 'τ', 'տ'},
	'u': {'μ', 'υ'},
	'x': {'х', 'χ'},
	'y': {'у'},
	'z': {'ζ'},
}

var homoglyphs = buildHomoglyphs()

func buildHomoglyphs() map[rune]rune {
	latins := make([]rune, 0, len(confusables))
	for latin := range confusables {
		latins = append(latins, latin)
	}
	sort.Slice(latins, func(i, j int) bool { return latins[i] < latins[j] })

	out := make(map[rune]rune, 64)
	for _, latin := range latins {
		for _, r := range confusables[latin] {
			out[r] = latin
			if lo := unicode.ToLower(r); lo != r {
				out[lo] = latin
			}
			if up := unicode.ToUpper(r); up != r && unicode.ToLower(up) == r {
				out[up] = latin
			}
		}
	}
	return out
}

func Canonicalize(text string) string {
	out, _ := canonicalizeMap(text)
	return out
}

type runeSpans struct {
	start []int
	end   []int
}

func (s runeSpans) len() int { return len(s.start) }

func canonicalizeMap(text string) (string, runeSpans) {
	san, sp1 := sanitizeSpans(text)
	nfc1, sp2 := nfkcSpans(san)
	folded, sp3 := foldSpans(nfc1)
	out, sp4 := nfkcSpans(folded)
	return out, composeSpans(composeSpans(composeSpans(sp4, sp3), sp2), sp1)
}

func sanitizeSpans(text string) (string, runeSpans) {
	var b strings.Builder
	b.Grow(len(text))
	var sp runeSpans
	for i, ri := 0, 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			j := i + 1
			for j < len(text) {
				if _, sz := utf8.DecodeRuneInString(text[j:]); sz != 1 {
					break
				}
				j++
			}
			b.WriteRune(utf8.RuneError)
			sp.start = append(sp.start, ri)
			sp.end = append(sp.end, ri+(j-i))
			i, ri = j, ri+(j-i)
			continue
		}
		b.WriteRune(r)
		sp.start = append(sp.start, ri)
		sp.end = append(sp.end, ri+1)
		i, ri = i+size, ri+1
	}
	return b.String(), sp
}

func nfkcSpans(s string) (string, runeSpans) {
	var b strings.Builder
	b.Grow(len(s))
	var sp runeSpans
	for i, ri := 0, 0; i < len(s); {
		j := i + norm.NFKC.NextBoundaryInString(s[i:], true)
		if j <= i || j > len(s) {
			j = len(s)
		}
		seg := s[i:j]
		out := norm.NFKC.String(seg)
		in := utf8.RuneCountInString(seg)
		for range out {
			sp.start = append(sp.start, ri)
			sp.end = append(sp.end, ri+in)
		}
		b.WriteString(out)
		i, ri = j, ri+in
	}
	return b.String(), sp
}

func foldSpans(s string) (string, runeSpans) {
	var b strings.Builder
	b.Grow(len(s))
	var sp runeSpans
	ri := 0
	for _, r := range s {
		switch {
		case r >= 0xFF10 && r <= 0xFF19:
			b.WriteRune('0' + (r - 0xFF10))
		case isInvisible(r):
			ri++
			continue
		default:
			if folded, ok := homoglyphs[r]; ok {
				b.WriteRune(folded)
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
		}
		sp.start = append(sp.start, ri)
		sp.end = append(sp.end, ri+1)
		ri++
	}
	return b.String(), sp
}

func composeSpans(a, b runeSpans) runeSpans {
	out := runeSpans{start: make([]int, len(a.start)), end: make([]int, len(a.start))}
	for i := range a.start {
		out.start[i] = b.start[a.start[i]]
		out.end[i] = b.end[a.end[i]-1]
	}
	return out
}

var (
	base64Re  = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}|[A-Za-z0-9_-]{16,}={0,2}`)
	hexRe     = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	percentRe = regexp.MustCompile(`(?:%[0-9A-Fa-f]{2})+`)
)

const (
	maxDecodedVariants = 24
	maxDecodeRounds    = 2
	maxDecodedLen      = 4096
)

func DecodedVariants(text string) []string {
	out := []string{}
	if text == "" {
		return out
	}
	seen := make(map[string]bool)
	var first string
	var printable, binary []string
	add := func(s string) {
		if s == "" || len(s) > maxDecodedLen {
			return
		}
		if seen[s] {
			return
		}
		seen[s] = true
		switch {
		case first == "":
			first = s
		case readableText(s):
			printable = append(printable, s)
		default:
			binary = append(binary, s)
		}
	}

	decode(text, add)
	for round := 1; round < maxDecodeRounds; round++ {
		before := len(printable) + len(binary)
		for _, s := range append(append([]string{}, printable...), binary...) {
			decode(s, add)
		}
		if first != "" {
			decode(first, add)
		}
		if len(printable)+len(binary) == before {
			break
		}
	}
	if first != "" {
		out = append(out, first)
	}
	for _, s := range append(printable, binary...) {
		if len(out) >= maxDecodedVariants {
			break
		}
		out = append(out, s)
	}
	return out
}

func readableText(s string) bool {
	for _, r := range s {
		switch {
		case r == ' ', r == '\t', r == '\n', r == '\r':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r >= 0x0370 && r <= 0x03FF,
			r >= 0x0400 && r <= 0x04FF:
		case strings.ContainsRune(".,;:!?-_/()[]{}\"'`+=*:@#$%&<>~^|\\_", r):
		default:
			return false
		}
	}
	return true
}

func decode(text string, add func(string)) {
	for _, m := range base64Re.FindAllString(text, -1) {
		tryBase64(m, add)
	}
	for _, m := range hexRe.FindAllString(text, -1) {
		if d, err := hex.DecodeString(m); err == nil {
			add(string(d))
		}
	}
	for _, loc := range percentRe.FindAllStringIndex(text, -1) {
		if d, err := url.PathUnescape(text[loc[0]:loc[1]]); err == nil {
			add(d)
		}
		if d, err := url.PathUnescape(percentToken(text, loc[0], loc[1])); err == nil {
			add(d)
		}
	}
	for _, token := range formTokens(text) {
		if !strings.ContainsAny(token, "+%") {
			continue
		}
		if d, err := url.QueryUnescape(token); err == nil {
			add(d)
		}
	}
}

func formTokens(text string) []string {
	var out []string
	start := -1
	for i := range len(text) {
		if percentDelim(text[i]) {
			if start >= 0 {
				out = append(out, text[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, text[start:])
	}
	return out
}

func percentToken(text string, start, end int) string {
	for start > 0 && !percentDelim(text[start-1]) {
		start--
	}
	for end < len(text) && !percentDelim(text[end]) {
		end++
	}
	return text[start:end]
}

func percentDelim(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v', '"', '\'', '<', '>', '`':
		return true
	}
	return false
}

func tryBase64(s string, add func(string)) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if d, err := enc.DecodeString(s); err == nil {
			add(string(d))
		}
	}
}
