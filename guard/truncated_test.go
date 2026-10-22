package guard

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVerdictMarksTruncatedInput(t *testing.T) {
	d := &lenDetector{}
	p, err := New(Options{Layers: []Detector{d}, MaxChars: 5})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), RailInput, strings.Repeat("a", 200))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Truncated {
		t.Fatalf("Truncated = false for 200 runes with MaxChars=5: %+v", v)
	}
	if v.InspectedRunes != 5 {
		t.Fatalf("InspectedRunes = %d, want 5", v.InspectedRunes)
	}
	if d.got != 5 {
		t.Fatalf("detector saw %d runes, want 5", d.got)
	}
	if v.Text != strings.Repeat("a", 200) {
		t.Fatalf("Text changed: %d runes", len([]rune(v.Text)))
	}
}

func TestVerdictIsNotTruncatedForShortInput(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "short", strings.Repeat("a", DefaultMaxChars)} {
		v, err := p.Check(context.Background(), RailInput, text)
		if err != nil {
			t.Fatal(err)
		}
		if v.Truncated {
			t.Fatalf("Truncated = true for %d runes", len([]rune(text)))
		}
		if v.InspectedRunes != len([]rune(text)) {
			t.Fatalf("InspectedRunes = %d, want %d", v.InspectedRunes, len([]rune(text)))
		}
	}
}

func TestWindowingDoesNotTruncate(t *testing.T) {
	d := &windowDetector{marker: "zz-no-match", budget: 1 << 20}
	p, err := New(Options{Layers: []Detector{d}, WindowChars: 64, MaxChars: 400})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", 300)
	v, err := p.Check(context.Background(), RailInput, text)
	if err != nil {
		t.Fatal(err)
	}
	if v.Truncated {
		t.Fatalf("windowed text must not be marked truncated: %+v", v)
	}
	if v.InspectedRunes != 300 {
		t.Fatalf("InspectedRunes = %d, want 300", v.InspectedRunes)
	}
}

func TestTruncationIsReportedOnACacheHit(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}, MaxChars: 4, CacheSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := p.Check(ctx, RailInput, strings.Repeat("a", 10))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Check(ctx, RailInput, strings.Repeat("a", 10))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || !second.Truncated {
		t.Fatalf("cached verdict lost the truncated marker: %+v %+v", first, second)
	}
	if second.InspectedRunes != 4 {
		t.Fatalf("cached verdict InspectedRunes = %d, want 4", second.InspectedRunes)
	}
}

func TestVerdictObservationMetadata(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}, MaxChars: 3})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), RailInput, strings.Repeat("a", 9))
	if err != nil {
		t.Fatal(err)
	}
	md := v.ObservationMetadata()
	if got, ok := md["truncated"].(bool); !ok || !got {
		t.Fatalf("metadata truncated = %v (present=%v), want true", md["truncated"], ok)
	}
	if got, ok := md["inspected_chars"].(int); !ok || got != 3 {
		t.Fatalf("metadata inspected_chars = %v, want 3", md["inspected_chars"])
	}

	short, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}})
	if err != nil {
		t.Fatal(err)
	}
	sv, err := short.Check(context.Background(), RailInput, "short")
	if err != nil {
		t.Fatal(err)
	}
	if md := sv.ObservationMetadata(); len(md) != 0 {
		t.Fatalf("a fully inspected verdict must add no metadata: %v", md)
	}
}

func TestStatsCountTruncated(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}, MaxChars: 8})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.Check(ctx, RailInput, strings.Repeat("a", 9)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(ctx, RailInput, "tiny"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(ctx, RailOutput, strings.Repeat("b", 9)); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.Scans != 3 || s.Truncated != 2 {
		t.Fatalf("Stats = %+v, want 3 scans and 2 truncated", s)
	}
}

const testEmail = "user@example.com"

// emailDetector reports every occurrence of testEmail as PII, so a test can pin
// what Check does with findings inside and beyond MaxChars.
type emailDetector struct{}

func (emailDetector) Name() string { return "email" }

func (emailDetector) Detect(_ context.Context, text string) ([]Finding, error) {
	var out []Finding
	for off := 0; off < len(text); {
		i := strings.Index(text[off:], testEmail)
		if i < 0 {
			break
		}
		start := utf8.RuneCountInString(text[:off+i])
		out = append(out, Finding{
			Category: CatPII,
			Label:    "EMAIL",
			Layer:    "email",
			Start:    start,
			End:      start + utf8.RuneCountInString(testEmail),
		})
		off += i + len(testEmail)
	}
	return out, nil
}

// TestTruncatedMaskBecomesReject pins the contract: Mask is never returned for
// text whose tail was not inspected, because the raw tail would ride along with
// the masked prefix.
func TestTruncatedMaskBecomesReject(t *testing.T) {
	prefix := "write to " + testEmail + " "                // finding inside MaxChars
	tail := strings.Repeat("y", 40) + " then " + testEmail // leak beyond MaxChars
	text := prefix + tail

	p, err := New(Options{Layers: []Detector{emailDetector{}}, MaxChars: 40, WindowChars: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, rail := range []Rail{RailOutput, RailToolResult} {
		v, err := p.Check(context.Background(), rail, text)
		if err != nil {
			t.Fatalf("%s: %v", rail, err)
		}
		if !v.Truncated {
			t.Fatalf("%s: Truncated = false, the test needs a truncated scan", rail)
		}
		if v.Action != Reject {
			t.Fatalf("%s: Action = %v, want Reject: an uninspected tail must not be returned with a mask", rail, v.Action)
		}
		if v.Tripwire == nil || v.Tripwire.RuleID != "truncation" {
			t.Fatalf("%s: Tripwire = %+v, want the truncation rule", rail, v.Tripwire)
		}
	}
}

func TestFullyInspectedMaskLeavesNoRawEmail(t *testing.T) {
	text := "write to " + testEmail + " or " + testEmail
	p, err := New(Options{Layers: []Detector{emailDetector{}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), RailOutput, text)
	if err != nil {
		t.Fatal(err)
	}
	if v.Truncated || v.Action != Mask {
		t.Fatalf("Action = %v, Truncated = %v, want a fully inspected Mask", v.Action, v.Truncated)
	}
	if strings.Contains(v.Text, testEmail) {
		t.Fatalf("masked text still contains the raw address: %q", v.Text)
	}
}

func TestTruncateRunesCutsOnARuneBoundary(t *testing.T) {
	text := strings.Repeat("Ядро-Охраны ", 100_000)
	for _, tc := range []struct {
		max  int
		want string
	}{
		{-1, text},
		{0, text},
		{3, "Ядр"},
		{7, "Ядро-Ох"},
		{utf8.RuneCountInString(text), text},
	} {
		got := truncateRunes(text, tc.max)
		if got != tc.want {
			t.Fatalf("truncateRunes(max=%d) = %q, want %q", tc.max, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("truncateRunes(max=%d) returned invalid UTF-8", tc.max)
		}
	}
}
