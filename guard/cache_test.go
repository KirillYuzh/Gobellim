package guard

import (
	"context"
	"testing"
)

type lastRuneDetector struct{ calls int }

func (*lastRuneDetector) Name() string { return "L1" }

func (d *lastRuneDetector) Detect(_ context.Context, text string) ([]Finding, error) {
	d.calls++
	n := len([]rune(text))
	if n == 0 {
		return nil, nil
	}
	return []Finding{{Category: CatPII, Label: "LAST", Layer: "L1", Start: n - 1, End: n}}, nil
}

func TestCacheDoesNotAliasLayouts(t *testing.T) {
	d := &lastRuneDetector{}
	p, err := New(Options{Layers: []Detector{d}, CacheSize: 8, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	plain := "ab"
	withZeroWidth := "a\u200bb"
	if Canonicalize(plain) != Canonicalize(withZeroWidth) {
		t.Fatalf("the fixtures must share a canonical form: %q vs %q", Canonicalize(plain), Canonicalize(withZeroWidth))
	}

	va, err := p.Check(ctx, RailInput, plain)
	if err != nil {
		t.Fatal(err)
	}
	vb, err := p.Check(ctx, RailInput, withZeroWidth)
	if err != nil {
		t.Fatal(err)
	}

	if got := va.Findings[0]; got.Start != 1 || got.End != 2 {
		t.Fatalf("plain text span = %d..%d, want 1..2", got.Start, got.End)
	}
	if got := vb.Findings[0]; got.Start != 2 || got.End != 3 {
		t.Fatalf("zero-width text span = %d..%d, want 2..3 (cache aliased a different layout)", got.Start, got.End)
	}
	if d.calls != 2 {
		t.Fatalf("detector ran %d time(s), want 2: an aliased hit skipped detection", d.calls)
	}
	if want := "a\u200b[PII:LAST:1]"; vb.Text != want {
		t.Fatalf("masked zero-width text = %q, want %q", vb.Text, want)
	}
}
