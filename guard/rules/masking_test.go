package rules

import (
	"context"
	"testing"

	"github.com/gobellim/kit/guard"
)

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
		{"инн 7707083893", "инн [PII:INN]"},
		{"инн " + zwsp + "7707083893", "инн " + zwsp + "[PII:INN]"},
		{"инн 7707" + zwsp + "083893", "инн [PII:INN]"},
		{"паспорт " + zwsp + "4509 123456", "паспорт " + zwsp + "[PII:PASSPORT]"},
		{"паспорт 4509" + zwsp + " 123456", "паспорт [PII:PASSPORT]"},
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
