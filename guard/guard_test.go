package guard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gobellim/kit/internal/core"
)

type fakeDetector struct {
	name  string
	find  []Finding
	err   error
	calls int
}

func (f *fakeDetector) Name() string { return f.name }
func (f *fakeDetector) Detect(context.Context, Rail, string) ([]Finding, error) {
	f.calls++
	return f.find, f.err
}

func TestPipelineAllow(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), RailInput, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != Allow || v.Text != "hello" {
		t.Fatalf("got %+v", v)
	}
}

func TestPipelineRejectFastFail(t *testing.T) {
	first := &fakeDetector{name: "L1", find: []Finding{{Category: CatInjection, Label: "PHRASE", Layer: "L1"}}}
	second := &fakeDetector{name: "L2"}
	p, _ := New(Options{Layers: []Detector{first, second}})

	v, err := p.Check(context.Background(), RailInput, "ignore previous instructions")
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != Reject {
		t.Fatalf("want Reject, got %v", v.Action)
	}
	if second.calls != 0 {
		t.Fatalf("fast-fail broken: L2 called %d times", second.calls)
	}
}

func TestPipelineMask(t *testing.T) {
	text := "номер 112-233-445 95 конец"
	start := utf8.RuneCountInString("номер ")
	end := start + utf8.RuneCountInString("112-233-445 95")
	calls := &fakeDetector{name: "L1", find: []Finding{{Category: CatPII, Label: "SNILS", Start: start, End: end, Layer: "L1"}}}
	p, _ := New(Options{Layers: []Detector{calls}})

	v, err := p.Check(context.Background(), RailOutput, text)
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != Mask {
		t.Fatalf("want Mask, got %v", v.Action)
	}
	want := "номер [PII:SNILS] конец"
	if v.Text != want {
		t.Fatalf("masked = %q, want %q", v.Text, want)
	}
}

func TestPipelineFailClosed(t *testing.T) {
	boom := &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}
	p, _ := New(Options{Layers: []Detector{boom}})
	_, err := p.Check(context.Background(), RailInput, "text")
	if !errors.Is(err, core.ErrGuardUnavailable) {
		t.Fatalf("want ErrGuardUnavailable, got %v", err)
	}
}

func TestPipelineCachesVerdicts(t *testing.T) {
	d := &fakeDetector{name: "L1"}
	p, _ := New(Options{Layers: []Detector{d}})
	for i := 0; i < 3; i++ {
		if _, err := p.Check(context.Background(), RailInput, "same text"); err != nil {
			t.Fatal(err)
		}
	}
	if d.calls != 1 {
		t.Fatalf("cache miss: detector called %d times, want 1", d.calls)
	}
}

type lenDetector struct{ got int }

func (d *lenDetector) Name() string { return "len" }
func (d *lenDetector) Detect(_ context.Context, _ Rail, text string) ([]Finding, error) {
	d.got = utf8.RuneCountInString(text)
	return nil, nil
}

func TestPipelineTruncates(t *testing.T) {

	d := &lenDetector{}
	p, _ := New(Options{Layers: []Detector{d}, MaxChars: 5})
	if _, err := p.Check(context.Background(), RailInput, strings.Repeat("a", 200)); err != nil {
		t.Fatal(err)
	}
	if d.got != 5 {
		t.Fatalf("detector saw %d runes, want 5", d.got)
	}
}

func TestAllowAll(t *testing.T) {
	p, _ := New(Options{Layers: []Detector{AllowAll()}})
	v, err := p.Check(context.Background(), RailInput, "anything")
	if err != nil || v.Action != Allow {
		t.Fatalf("AllowAll: %+v err=%v", v, err)
	}
}
