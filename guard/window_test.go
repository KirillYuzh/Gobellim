package guard

import (
	"context"
	"strings"
	"testing"
)

type windowDetector struct {
	marker string
	budget int
	cat    Category
	seen   []string
}

func (d *windowDetector) Name() string { return "L1" }

func (d *windowDetector) Detect(_ context.Context, text string) ([]Finding, error) {
	d.seen = append(d.seen, text)
	runes := []rune(text)
	if d.budget > 0 && len(runes) > d.budget {
		runes = runes[:d.budget]
		text = string(runes)
	}
	var out []Finding
	for at := 0; ; {
		i := strings.Index(text[at:], d.marker)
		if i < 0 {
			return out, nil
		}
		start := at + i
		end := start + len(d.marker)
		out = append(out, Finding{
			Category: d.cat, Label: "MARK", Layer: "L1",
			Start: len([]rune(text[:start])), End: len([]rune(text[:end])),
		})
		at = end
	}
}

func TestPipelineScansTheWholeText(t *testing.T) {
	const budget = 64
	text := strings.Repeat("x", 200) + "mark" + strings.Repeat("y", 200)

	t.Run("windowed", func(t *testing.T) {
		d := &windowDetector{marker: "mark", budget: budget, cat: CatPII}
		p, err := New(Options{Layers: []Detector{d}, WindowChars: budget})
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Check(context.Background(), RailInput, text)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Findings) != 1 {
			t.Fatalf("findings = %+v, want the tail span", v.Findings)
		}
		if v.Findings[0].Start != 200 || v.Findings[0].End != 204 {
			t.Fatalf("offset = %d..%d, want 200..204", v.Findings[0].Start, v.Findings[0].End)
		}
		want := strings.Repeat("x", 200) + "[PII:MARK:1]" + strings.Repeat("y", 200)
		if v.Action != Mask || v.Text != want {
			t.Fatalf("action = %v, text = %q", v.Action, v.Text)
		}
		if len(d.seen) < 2 {
			t.Fatalf("text must be scanned in windows, got %d call(s)", len(d.seen))
		}
	})

	t.Run("a span split across a boundary is still found", func(t *testing.T) {
		split := strings.Repeat("x", 190) + "mark" + strings.Repeat("y", 210)
		d := &windowDetector{marker: "mark", budget: budget, cat: CatPII}
		p, err := New(Options{Layers: []Detector{d}, WindowChars: budget})
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Check(context.Background(), RailInput, split)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Findings) != 1 || v.Findings[0].Start != 190 {
			t.Fatalf("findings = %+v, want one span at 190", v.Findings)
		}
	})

	t.Run("one window leaves the tail invisible", func(t *testing.T) {
		d := &windowDetector{marker: "mark", budget: budget, cat: CatPII}
		p, err := New(Options{Layers: []Detector{d}, WindowChars: 4096})
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Check(context.Background(), RailInput, text)
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Findings) != 0 || v.Action != Allow {
			t.Fatalf("without windowing the tail must stay invisible: %+v", v.Findings)
		}
	})
}

func TestPipelineWindowingRejectsFromTheTail(t *testing.T) {
	d := &windowDetector{marker: "mark", budget: 64, cat: CatInjection}
	p, err := New(Options{Layers: []Detector{d}, WindowChars: 64})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), RailInput, strings.Repeat("x", 200)+"mark"+strings.Repeat("y", 200))
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != Reject {
		t.Fatalf("an injection in the tail must reject, got %v", v.Action)
	}
}

func TestDefaultWindowSplitsLongText(t *testing.T) {
	d := &windowDetector{marker: "zz-no-match", budget: 1 << 20}
	p, err := New(Options{Layers: []Detector{d}, MaxChars: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(context.Background(), RailInput, strings.Repeat("обработка ", 400)); err != nil {
		t.Fatal(err)
	}
	if len(d.seen) < 2 {
		t.Fatalf("the default window must split a long input: %d call(s)", len(d.seen))
	}
	for _, w := range d.seen {
		if n := len([]rune(w)); n > DefaultWindowChars {
			t.Fatalf("window of %d runes exceeds DefaultWindowChars (%d)", n, DefaultWindowChars)
		}
	}
}

func TestDedupeKeepsDistinctFindings(t *testing.T) {
	same := Finding{Category: CatPII, Label: "SNILS", Start: 1, End: 5, Layer: "L1"}
	other := Finding{Category: CatUnsafe, Label: "TOXIC", Start: 1, End: 5, Layer: "L1"}
	got := dedupe([]Finding{same, same, other})
	if len(got) != 2 {
		t.Fatalf("dedupe = %+v, want the repeated span dropped once", got)
	}
}
