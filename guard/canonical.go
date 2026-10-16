package guard

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func isInvisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r == 0x2060,
		r == 0xFEFF:
		return true
	}
	return false
}

var homoglyphs = map[rune]rune{
	'А': 'a', 'В': 'b', 'Е': 'e', 'К': 'k', 'М': 'm', 'Н': 'h',
	'О': 'o', 'Р': 'p', 'С': 'c', 'Т': 't', 'У': 'y', 'Х': 'x',
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
}

// Canonicalize folds text to a comparable form: NFKC, then case folding with
// invisible/homoglyph/full-width normalisation, then NFKC again — the second
// pass composes sequences that the case folding made composable.
func Canonicalize(text string) string {
	out, _ := canonicalizeMap(text)
	return out
}

// runeSpans maps every rune of a derived string to the half-open range of runes
// in the source string that produced it. Ranges are ordered and never empty, so a
// derived span [a,b) covers source [start[a], end[b-1]).
type runeSpans struct {
	start []int
	end   []int
}

func (s runeSpans) len() int { return len(s.start) }

// canonicalizeMap returns Canonicalize(text) together with the source range of
// every rune of the result, indexing []rune(text). Offsets from a detector are in
// canonical space, but text is masked in the original space: canonicalisation can
// drop runes (zero-width, bidi) or join them, so findings must be mapped back or a
// mask would cover the wrong characters and leak the tail of a span.
func canonicalizeMap(text string) (string, runeSpans) {
	san, sp1 := sanitizeSpans(text)
	nfc1, sp2 := nfkcSpans(san)
	folded, sp3 := foldSpans(nfc1)
	out, sp4 := nfkcSpans(folded)
	return out, composeSpans(composeSpans(composeSpans(sp4, sp3), sp2), sp1)
}

// sanitizeSpans mirrors strings.ToValidUTF8(text, "\uFFFD"): each maximal run of
// invalid bytes collapses to a single U+FFFD. Spans index []rune(text), where
// []rune counts every invalid byte as its own rune.
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

// nfkcSpans applies NFKC one normalisation segment at a time, so it can report
// which source runes produced each output rune. Splitting at NFKC boundaries
// keeps the concatenation of the per-segment results equal to normalising the
// whole string.
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

// foldSpans is the case/invisible/homoglyph/digit fold, rune for rune. An
// invisible rune emits nothing, so it owns no canonical rune.
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

// composeSpans folds two hops into one: a maps a derived string to an
// intermediate one, b maps that intermediate to the original.
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
	maxDecodedVariants = 8
	maxDecodedLen      = 4096
)

func DecodedVariants(text string) []string {
	out := []string{}
	if text == "" {
		return out
	}
	seen := make(map[string]bool)
	add := func(s string) {
		if len(out) >= maxDecodedVariants || s == "" {
			return
		}
		if len(s) > maxDecodedLen {
			s = s[:maxDecodedLen]
		}
		if seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	for _, m := range base64Re.FindAllString(text, -1) {
		tryBase64(m, add)
	}
	for _, m := range hexRe.FindAllString(text, -1) {
		if d, err := hex.DecodeString(m); err == nil {
			add(string(d))
		}
	}
	for _, m := range percentRe.FindAllString(text, -1) {
		if d, err := url.PathUnescape(m); err == nil {
			add(d)
		}
	}
	return out
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
