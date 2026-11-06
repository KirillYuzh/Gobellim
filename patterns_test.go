package gobellim

import (
	"strings"
	"testing"
)

// A detector is a regex plus a check. Without the check, an invoice number or a
// timestamp becomes a handle and the model loses information it needs.
func TestPatternsChecksums(t *testing.T) {
	tests := []struct {
		name  string
		valid func(string) bool
		ok    []string
		bad   []string
	}{
		{
			name:  "luhn",
			valid: luhnValid,
			ok:    []string{"4111111111111111", "5555555555554444"},
			bad:   []string{"4111111111111112", "1234567890123456", "1234"},
		},
		{
			name:  "snils",
			valid: snilsValid,
			ok:    []string{"11223344595"},
			bad:   []string{"11223344596", "1122334459"},
		},
		{
			name:  "inn",
			valid: innValid,
			ok:    []string{"7707083893", "500100732259"},
			bad:   []string{"7707083894", "500100732258", "770708389"},
		},
		{
			name:  "ogrn",
			valid: ogrnValid,
			ok:    []string{"1027700132195", "304500116000157"},
			bad:   []string{"1027700132196", "304500116000158"},
		},
		{
			name:  "iban",
			valid: ibanValid,
			ok:    []string{"GB82WEST12345698765432", "DE89370400440532013000"},
			bad:   []string{"GB82WEST12345698765433", "DE89370400440532013001"},
		},
	}
	for _, tc := range tests {
		for _, v := range tc.ok {
			if !tc.valid(v) {
				t.Errorf("%s: %q rejected, want accepted", tc.name, v)
			}
		}
		for _, v := range tc.bad {
			if tc.valid(v) {
				t.Errorf("%s: %q accepted, want rejected", tc.name, v)
			}
		}
	}
}

func TestMaskPatternsMintsHandlesOnlyForCheckedMatches(t *testing.T) {
	hs := newHandleSet()
	label := Label{Integrity: Untrusted, Sensitivity: Patterns}
	in := "card 4111 1111 1111 1111, order 1234567890123456, mail user@example.com"

	got := maskPatterns(in, label, hs)

	if strings.Contains(got, "4111") {
		t.Fatalf("a Luhn-valid card was not masked: %q", got)
	}
	if strings.Contains(got, "user@example.com") {
		t.Fatalf("an email was not masked: %q", got)
	}
	if !strings.Contains(got, "1234567890123456") {
		t.Fatalf("a 16-digit number that fails Luhn was masked: %q", got)
	}
	if len(hs.byRef) != 2 {
		t.Fatalf("handles = %d, want 2 (card and email)", len(hs.byRef))
	}
}

// A handle is never searched for Patterns again: one pass over the original string
// keeps a masked value from being masked twice.
func TestMaskPatternsIsOnePass(t *testing.T) {
	hs := newHandleSet()
	label := Label{Integrity: Untrusted, Sensitivity: Patterns}
	got := maskPatterns("a@example.com and b@example.com", label, hs)

	if strings.Count(got, "<h") != 2 {
		t.Fatalf("masked = %q, want two handles", got)
	}
	if len(hs.byRef) != 2 {
		t.Fatalf("handles = %d, want 2 distinct values", len(hs.byRef))
	}
}
