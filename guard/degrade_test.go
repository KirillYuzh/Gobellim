package guard

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobellim/kit/internal/core"
)

type logRecord struct {
	level string
	msg   string
	attrs map[string]any
}

type captureHandler struct {
	mu  sync.Mutex
	got *[]logRecord
}

func recordingLogger() (*slog.Logger, *[]logRecord) {
	got := &[]logRecord{}
	return slog.New(&captureHandler{got: got}), got
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, rec slog.Record) error {
	r := logRecord{level: rec.Level.String(), msg: rec.Message, attrs: map[string]any{}}
	rec.Attrs(func(a slog.Attr) bool {
		r.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	*h.got = append(*h.got, r)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestDegradationOnADeclaredOptionalRail(t *testing.T) {
	log, records := recordingLogger()
	l2 := &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}
	p, err := New(Options{
		Layers:               []Detector{&fakeDetector{name: "L1"}, l2},
		OptionalByRail:       map[Rail][]string{RailInput: {"L2"}},
		DegradedWarnInterval: time.Hour,
		Logger:               log,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := p.Check(ctx, RailInput, "text")
	if err != nil {
		t.Fatalf("an optional layer must not fail the check: %v", err)
	}
	if !v.Degraded || !strings.Contains(v.DegradedReason, "L2") {
		t.Fatalf("verdict must be marked degraded: %+v", v)
	}
	if v.Action != Allow {
		t.Fatalf("action = %v, want Allow", v.Action)
	}
	if got := p.Stats().Degradations[RailInput]; got != 1 {
		t.Fatalf("Degradations[input] = %d, want 1", got)
	}
	if got := len(*records); got != 1 {
		t.Fatalf("got %d warnings, want 1", got)
	}
}

func TestDegradationFailsClosedOnANonOptionalRail(t *testing.T) {
	l2 := &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}
	p, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L1"}, l2},
		OptionalByRail: map[Rail][]string{RailInput: {"L2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []Rail{RailOutput, RailToolResult} {
		v, err := p.Check(context.Background(), r, "text")
		if err == nil {
			t.Fatalf("Check(%q) must fail closed, got %+v", r, v)
		}
		if !strings.Contains(err.Error(), "L2") {
			t.Fatalf("error %v must name the failing layer", err)
		}
		if v.Degraded {
			t.Fatalf("a failed check must not report a degraded verdict: %+v", v)
		}
	}
	if got := p.Stats().Degradations[RailInput]; got != 0 {
		t.Fatalf("a failed check must not count a degradation, got %d", got)
	}
	if got := p.Stats().Scans; got != 0 {
		t.Fatalf("a failed check must not count a scan, got %d", got)
	}
}

func TestOptionalByRailRejectsUnknownRails(t *testing.T) {
	_, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L1"}},
		OptionalByRail: map[Rail][]string{"retrieval": {"L2"}},
	})
	if err == nil {
		t.Fatal("New must reject an unknown rail in OptionalByRail")
	}
}

func TestFlatOptionalAppliesToEveryRail(t *testing.T) {
	p, err := New(Options{
		Layers:   []Detector{&fakeDetector{name: "L1"}, &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}},
		Optional: []string{"L2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Rails() {
		v, err := p.Check(context.Background(), r, "text")
		if err != nil {
			t.Fatalf("Check(%q): %v", r, err)
		}
		if !v.Degraded {
			t.Fatalf("Check(%q) must be degraded: %+v", r, v)
		}
	}
	s := p.Stats()
	if len(s.Degradations) != len(Rails()) {
		t.Fatalf("Degradations = %v, want one entry per rail", s.Degradations)
	}
}

func TestDegradationWarnsOncePerInterval(t *testing.T) {
	log, records := recordingLogger()
	l2 := &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := New(Options{
		Layers:               []Detector{&fakeDetector{name: "L1"}, l2},
		OptionalByRail:       map[Rail][]string{RailInput: {"L2"}, RailOutput: {"L2"}},
		DegradedWarnInterval: time.Minute,
		Logger:               log,
		now:                  func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := p.Check(ctx, RailInput, "text"); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(*records); got != 1 {
		t.Fatalf("got %d warnings inside one interval, want 1: %v", got, *records)
	}
	first := (*records)[0]
	if first.level != "WARN" {
		t.Fatalf("level = %q, want WARN", first.level)
	}
	if first.attrs["rail"] != "input" {
		t.Fatalf("warning must name the rail: %+v", first.attrs)
	}
	if got := p.Stats().Degradations[RailInput]; got != 3 {
		t.Fatalf("Degradations[input] = %d, want 3: every degradation counts", got)
	}

	if _, err := p.Check(ctx, RailOutput, "text"); err != nil {
		t.Fatal(err)
	}
	if got := len(*records); got != 2 {
		t.Fatalf("a different rail must warn on its own first degradation, got %d", got)
	}

	if _, err := p.Check(ctx, RailInput, "text"); err != nil {
		t.Fatal(err)
	}
	if got := len(*records); got != 2 {
		t.Fatalf("warned inside the interval: %d warnings", got)
	}

	clock = clock.Add(time.Minute)
	if _, err := p.Check(ctx, RailInput, "text"); err != nil {
		t.Fatal(err)
	}
	if got := len(*records); got != 3 {
		t.Fatalf("want a warning after the interval elapsed, got %d", got)
	}
}

func TestDegradedVerdictsAreNotCachedAcrossRails(t *testing.T) {
	l2 := &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}
	p, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L1"}, l2},
		OptionalByRail: map[Rail][]string{RailInput: {"L2"}, RailOutput: {"L2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, r := range []Rail{RailInput, RailOutput, RailInput} {
		if _, err := p.Check(ctx, r, "same text"); err != nil {
			t.Fatal(err)
		}
	}
	if l2.calls != 3 {
		t.Fatalf("detector ran %d times, want 3: a degraded verdict is never cached", l2.calls)
	}
}

func TestDegradationCountsOnEveryRail(t *testing.T) {
	log, _ := recordingLogger()
	p, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L1"}, &fakeDetector{name: "L2", err: core.ErrGuardUnavailable}},
		OptionalByRail: map[Rail][]string{RailInput: {"L2"}, RailToolResult: {"L2"}},
		Logger:         log,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.Check(ctx, RailInput, "text"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(ctx, RailToolResult, "text"); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.Scans != 2 {
		t.Fatalf("Scans = %d, want 2", s.Scans)
	}
	if s.Degradations[RailInput] != 1 || s.Degradations[RailToolResult] != 1 {
		t.Fatalf("Degradations = %v", s.Degradations)
	}
	if _, ok := s.Degradations[RailOutput]; ok {
		t.Fatalf("Degradations = %v, want no entry for a rail that never degraded", s.Degradations)
	}
}

func TestStatsSnapshotIsACopy(t *testing.T) {
	p, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L1", find: []Finding{{Category: CatInjection, Label: "PHRASE", Layer: "L1"}}}},
		OptionalByRail: map[Rail][]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(context.Background(), RailInput, "text"); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.Degradations != nil {
		t.Fatalf("Degradations = %v, want nil when nothing degraded", s.Degradations)
	}
	if got := p.Stats().Blocked; got != 1 {
		t.Fatalf("Blocked = %d, want 1", got)
	}

	degrading, err := New(Options{
		Layers:         []Detector{&fakeDetector{name: "L2", err: core.ErrGuardUnavailable}},
		OptionalByRail: map[Rail][]string{RailInput: {"L2"}},
		Logger:         mustLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := degrading.Check(context.Background(), RailInput, "text"); err != nil {
		t.Fatal(err)
	}
	first := degrading.Stats()
	if first.Degradations[RailInput] != 1 {
		t.Fatalf("Degradations = %v, want one degradation", first.Degradations)
	}
	first.Degradations[RailInput] = 99
	first.Scans = 99
	again := degrading.Stats()
	if again.Scans != 1 || again.Degradations[RailInput] != 1 {
		t.Fatalf("the snapshot is not a copy: %+v", again)
	}
}

func mustLogger(t *testing.T) *slog.Logger {
	t.Helper()
	log, _ := recordingLogger()
	return log
}

func TestStatsUnderConcurrentChecks(t *testing.T) {
	log, _ := recordingLogger()
	optional := map[Rail][]string{}
	for _, r := range Rails() {
		optional[r] = []string{"L2"}
	}
	p, err := New(Options{
		Layers:         []Detector{quietDetector{}, &failingDetector{name: "L2"}},
		OptionalByRail: optional,
		Logger:         log,
	})
	if err != nil {
		t.Fatal(err)
	}
	rails := Rails()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if _, err := p.Check(context.Background(), rails[i%len(rails)], "text"); err != nil {
				t.Error(err)
			}
		}(i)
		go func() {
			defer wg.Done()
			_ = p.Stats()
		}()
	}
	wg.Wait()
	s := p.Stats()
	if s.Scans != 24 {
		t.Fatalf("Scans = %d, want 24", s.Scans)
	}
	for _, r := range rails {
		if s.Degradations[r] != 8 {
			t.Fatalf("Degradations[%s] = %d, want 8", r, s.Degradations[r])
		}
	}
}

type quietDetector struct{}

func (quietDetector) Name() string { return "L1" }

func (quietDetector) Detect(context.Context, string) ([]Finding, error) { return nil, nil }

type failingDetector struct{ name string }

func (d *failingDetector) Name() string { return d.name }

func (*failingDetector) Detect(context.Context, string) ([]Finding, error) {
	return nil, core.ErrGuardUnavailable
}
