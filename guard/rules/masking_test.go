package rules

import (
	"context"
	"testing"

	"github.com/gobellim/kit/guard"
)

// Canonicalisation drops zero-width and bidi runes, so a detector matches in a
// shorter canonical stream than the text that gets masked. The mask must cover
// all of the original match: stopping short of it leaks the tail of a value that
// was already detected.
func TestMaskCoversCanonicalShiftedSpans(t *testing.T) {
	p, err := guard.New(guard.Options{Layers: []guard.Detector{New()}})
	if err != nil {
		t.Fatal(err)
	}
	zwsp := "\u200b"
	cases := []struct{ in, want string }{
		{"почта: ivan.petrov@example.com", "почта: [PII:EMAIL]"},
		{"почта: " + zwsp + "ivan.petrov@example.com", "почта: " + zwsp + "[PII:EMAIL]"},
		{"почта: " + zwsp + zwsp + zwsp + "ivan.petrov@example.com", "почта: " + zwsp + zwsp + zwsp + "[PII:EMAIL]"},
		{"почта: \u202eivan.petrov@example.com\u202c", "почта: \u202e[PII:EMAIL]\u202c"},
		{"инн 7707083893", "инн [PII:PASSPORT,INN]"},
		{"инн " + zwsp + "7707083893", "инн " + zwsp + "[PII:PASSPORT,INN]"},
		{"инн 7707" + zwsp + "083893", "инн [PII:PASSPORT,INN]"},
	}
	for _, tc := range cases {
		v, err := p.Check(context.Background(), guard.RailInput, tc.in)
		if err != nil {
			t.Fatalf("Check(%q): %v", tc.in, err)
		}
		if v.Action != guard.Mask || v.Text != tc.want {
			t.Errorf("Check(%q) = %v %q, want mask %q", tc.in, v.Action, v.Text, tc.want)
		}
	}
}
