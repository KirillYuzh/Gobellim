package guard

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"

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
	if text == "" {
		return ""
	}
	// NFKC leaves a rune unnormalised when the text carries invalid UTF-8, so the
	// result would not be a fixed point: sanitise first.
	s := strings.ToValidUTF8(text, "\uFFFD")
	return norm.NFKC.String(fold(norm.NFKC.String(s)))
}

func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 0xFF10 && r <= 0xFF19:
			b.WriteRune('0' + (r - 0xFF10))
			continue
		case isInvisible(r):
			continue
		}
		if folded, ok := homoglyphs[r]; ok {
			b.WriteRune(folded)
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
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
