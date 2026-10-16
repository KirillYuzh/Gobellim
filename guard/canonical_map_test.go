package guard

import "testing"

func TestCanonicalizeMapInvariants(t *testing.T) {
	texts := []string{
		"",
		"plain ascii",
		"почта: \u200bivan.petrov@example.com",
		"a\u200b\u200d\ufeffb",
		"e\u0301x",
		"\ufb01le",
		"\u202erop\u202c",
		"0\xf6\u013f",
		"\u0430\u0431\u0432",
		"\uff11\uff12\uff13",
	}
	for _, text := range texts {
		canon, sp := canonicalizeMap(text)
		if canon != Canonicalize(text) {
			t.Fatalf("canonicalizeMap(%q) = %q, Canonicalize = %q", text, canon, Canonicalize(text))
		}
		if sp.len() != len([]rune(canon)) {
			t.Fatalf("%q: %d spans for %d canonical runes", text, sp.len(), len([]rune(canon)))
		}
		total := len([]rune(text))
		prev := -1
		for i := 0; i < sp.len(); i++ {
			if sp.start[i] < 0 || sp.end[i] > total || sp.start[i] >= sp.end[i] {
				t.Fatalf("%q: rune %d has range [%d,%d) outside [0,%d)", text, i, sp.start[i], sp.end[i], total)
			}
			if sp.start[i] < prev {
				t.Fatalf("%q: rune %d starts at %d, before the previous rune's %d", text, i, sp.start[i], prev)
			}
			prev = sp.start[i]
		}
	}
}

func TestRemapSpanCoversTheMatch(t *testing.T) {
	text := "почта: \u200bivan.petrov@example.com"
	_, sp := canonicalizeMap(text)
	runes := []rune(text)
	start, end := remapSpan(sp, 7, 30, len(runes))
	if got := string(runes[start:end]); got != "ivan.petrov@example.com" {
		t.Fatalf("remap = [%d,%d) = %q, want the whole address", start, end, got)
	}
}

func TestRemapSpanClamps(t *testing.T) {
	_, sp := canonicalizeMap("abc")
	total := 3
	if start, end := remapSpan(sp, 9, 12, total); start > total || end > total || start > end {
		t.Fatalf("remap = [%d,%d) for an out-of-range span, want a clamped span", start, end)
	}
	if start, end := remapSpan(sp, 1, 1, total); start != 1 || end != 1 {
		t.Fatalf("remap of an empty span = [%d,%d), want [1,1)", start, end)
	}
}
