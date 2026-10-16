package eval

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gobellim/kit/trace"
)

func tr(id, out string, obs ...trace.Observation) Trace {
	return Trace{Trace: trace.Trace{ID: id, Name: "chat", Output: out}, Observations: obs}
}

func span(name string) trace.Observation {
	return trace.Observation{TraceID: "t1", Name: name, Kind: trace.KindSpan}
}

func spanAt(name string, at time.Time) trace.Observation {
	return trace.Observation{TraceID: "t1", Name: name, Kind: trace.KindSpan, StartTime: at}
}

func generation() trace.Observation {
	return trace.Observation{TraceID: "t1", Name: "gpt", Kind: trace.KindGeneration}
}

func event() trace.Observation {
	return trace.Observation{TraceID: "t1", Name: "guard", Kind: trace.KindEvent}
}

func TestChecks(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	ordered := []trace.Observation{
		spanAt("fetch", base.Add(2*time.Second)),
		spanAt("search", base),
		spanAt("write", base.Add(time.Second)),
	}

	cases := []struct {
		name  string
		check Check
		trace Trace
		want  bool
	}{
		{"includes pass", Includes("Paris"), tr("t1", "I went to Paris."), true},
		{"includes fail", Includes("Paris"), tr("t1", "I went to Berlin."), false},
		{"excludes pass", Excludes("Berlin"), tr("t1", "I went to Paris."), true},
		{"excludes fail", Excludes("Paris"), tr("t1", "I went to Paris."), false},
		{"equals pass", Equals("exact"), tr("t1", "exact"), true},
		{"equals fail", Equals("exact"), tr("t1", "exactly"), false},
		{"matches pass", Matches(regexp.MustCompile(`^P\w+`)), tr("t1", "Paris"), true},
		{"matches fail", Matches(regexp.MustCompile(`^\d+$`)), tr("t1", "Paris"), false},
		{"matches nil", Matches(nil), tr("t1", "anything"), false},
		{"similarity pass", Similarity("the quick brown fox", 0.9), tr("t1", "the quick brown fox"), true},
		{"similarity near pass", Similarity("the quick brown dog", 0.6), tr("t1", "the quick brown fox"), true},
		{"similarity fail", Similarity("the quick brown dog", 0.9), tr("t1", "the quick brown fox"), false},
		{"similarity fallback pass", Similarity("Hello", 1.0), tr("t1", "  hello "), true},
		{"similarity fallback fail", Similarity("Hello", 0.5), tr("t1", "World"), false},
		{"similarity empty pass", Similarity("", 1.0), tr("t1", ""), true},

		{"called tool pass", CalledTool("search"), tr("t1", "", span("search")), true},
		{"called tool missing", CalledTool("fetch"), tr("t1", "", span("search")), false},
		{"called tool empty", CalledTool("search"), tr("t1", ""), false},
		{"called tool ignores event", CalledTool("guard"), tr("t1", "", event()), false},
		{"did not call pass", DidNotCall("fetch"), tr("t1", "", span("search")), true},
		{"did not call fail", DidNotCall("search"), tr("t1", "", span("search")), false},
		{"used no tools pass", UsedNoTools(), tr("t1", "", generation(), event()), true},
		{"used no tools fail", UsedNoTools(), tr("t1", "", span("search")), false},
		{"max tool calls pass", MaxToolCalls(2), tr("t1", "", span("a"), span("b")), true},
		{"max tool calls over", MaxToolCalls(1), tr("t1", "", span("a"), span("b")), false},
		{"max tool calls empty", MaxToolCalls(0), tr("t1", "", generation()), true},
		{"tool order subsequence", ToolOrder("search", "fetch"), tr("t1", "", ordered...), true},
		{"tool order exact", ToolOrder("search", "write", "fetch"), tr("t1", "", ordered...), true},
		{"tool order wrong", ToolOrder("fetch", "search"), tr("t1", "", ordered...), false},
		{"tool order missing", ToolOrder("search", "delete"), tr("t1", "", ordered...), false},
		{"tool order empty obs", ToolOrder("search"), tr("t1", ""), false},
		{"tool order no names", ToolOrder(), tr("t1", ""), true},
		{"no tool errors pass", NoToolErrors(), tr("t1", "", span("search")), true},
		{"no tool errors level", NoToolErrors(), tr("t1", "",
			trace.Observation{TraceID: "t1", Name: "search", Kind: trace.KindSpan, Level: trace.LevelError}), false},
		{"no tool errors kind", NoToolErrors(), tr("t1", "",
			trace.Observation{TraceID: "t1", Name: "search", Kind: trace.KindSpan, ErrorKind: "internal"}), false},
		{"no tool errors empty", NoToolErrors(), tr("t1", ""), true},
		{"no tool errors ignores event", NoToolErrors(), tr("t1", "",
			trace.Observation{TraceID: "t1", Name: "guard", Kind: trace.KindEvent, Level: trace.LevelError}), true},

		{"all pass", All(Includes("Paris"), Excludes("Berlin")), tr("t1", "Paris"), true},
		{"all fail", All(Includes("Paris"), Includes("Berlin")), tr("t1", "Paris"), false},
		{"all empty", All(), tr("t1", "anything"), true},
		{"any pass", Any(Includes("Berlin"), Includes("Paris")), tr("t1", "Paris"), true},
		{"any fail", Any(Includes("Berlin"), Includes("Rome")), tr("t1", "Paris"), false},
		{"any empty", Any(), tr("t1", "anything"), false},
		{"not pass", Not(Includes("Berlin")), tr("t1", "Paris"), true},
		{"not fail", Not(Includes("Paris")), tr("t1", "Paris"), false},
		{"not nil", Not(nil), tr("t1", "Paris"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check(tc.trace)
			if got := err == nil; got != tc.want {
				t.Fatalf("pass = %v, want %v (err = %v)", got, tc.want, err)
			}
			if err != nil && tc.trace.Trace.ID != "" {
				if !strings.Contains(err.Error(), tc.trace.Trace.ID) {
					t.Errorf("error %q does not name the trace %q", err, tc.trace.Trace.ID)
				}
			}
		})
	}
}

func TestDiceSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"the quick brown fox", "the quick brown fox", 1},
		{"the quick brown fox", "the quick brown dog", 2.0 / 3.0},
		{"the quick brown fox", "completely different words here", 0},
		{"hello", "  HELLO ", 1},
		{"hello", "world", 0},
		{"", "", 1},
		{"", "word", 0},
	}
	for _, tc := range cases {
		if got := DiceSimilarity(tc.a, tc.b); got != tc.want {
			t.Errorf("DiceSimilarity(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRunIsolatesCasesWithReset(t *testing.T) {
	rec := trace.NewRecorder()
	var seen []string
	run := func(_ context.Context, input string) error {
		seen = append(seen, input)
		rec.Enqueue(trace.Trace{ID: "t1", Name: "chat", Output: input})
		if input == "first" {
			rec.EnqueueObservation(trace.Observation{TraceID: "t1", Name: "search", Kind: trace.KindSpan})
		}
		return nil
	}

	cases := []Case{
		{Name: "uses search", Input: "first", Checks: []Check{CalledTool("search")}},
		{Name: "uses no tools", Input: "second", Checks: []Check{UsedNoTools(), Includes("second")}},
	}
	results := Run(context.Background(), rec, cases, run)

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, res := range results {
		if !res.Passed {
			t.Errorf("case %q failed: %v", res.Name, res.Err)
		}
	}
	if want := []string{"first", "second"}; !equalStrings(seen, want) {
		t.Errorf("run received %v, want %v", seen, want)
	}
	if got := len(rec.Snapshot().Traces); got != 1 {
		t.Errorf("recorder holds %d traces after Run, want 1 (Reset not called?)", got)
	}
}

func TestRunReportsRunError(t *testing.T) {
	rec := trace.NewRecorder()
	boom := errors.New("boom")
	checked := false
	run := func(_ context.Context, _ string) error {
		rec.Enqueue(trace.Trace{ID: "t1", Name: "chat", Output: "partial"})
		return boom
	}
	check := func(Trace) error { checked = true; return nil }

	results := Run(context.Background(), rec, []Case{{Name: "c", Input: "x", Checks: []Check{check}}}, run)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	res := results[0]
	if res.Passed {
		t.Fatal("a failed run must be reported as failed")
	}
	if !errors.Is(res.Err, boom) {
		t.Fatalf("Err = %v, want it to wrap boom", res.Err)
	}
	if checked {
		t.Fatal("checks must not run when run returns an error")
	}
	if res.Trace.Trace.ID != "t1" {
		t.Errorf("partially produced trace not captured: %+v", res.Trace.Trace)
	}
}

func TestRunFailsWhenNoTraceRecorded(t *testing.T) {
	rec := trace.NewRecorder()
	run := func(context.Context, string) error { return nil }
	results := Run(context.Background(), rec, []Case{{Name: "empty"}}, run)
	if results[0].Passed {
		t.Fatal("a case that records no trace must fail")
	}
	if results[0].Err == nil {
		t.Fatal("expected an error explaining the missing trace")
	}
}

func TestRunNilRecorderDoesNotPanic(t *testing.T) {
	results := Run(context.Background(), nil, []Case{{Name: "c"}}, func(context.Context, string) error { return nil })
	if len(results) != 1 || results[0].Passed {
		t.Fatalf("nil recorder must yield a failed result, got %+v", results)
	}
}

func TestRecorded(t *testing.T) {
	rec := trace.NewRecorder()
	if _, err := Recorded(rec); err == nil {
		t.Fatal("want an error when the recorder is empty")
	}
	rec.Enqueue(trace.Trace{ID: "t1", Name: "chat", Output: "a"})
	rec.Enqueue(trace.Trace{ID: "t2", Name: "chat", Output: "b"})
	rec.EnqueueObservation(trace.Observation{TraceID: "t1", Name: "old", Kind: trace.KindSpan})
	rec.EnqueueObservation(trace.Observation{TraceID: "t2", Name: "new", Kind: trace.KindSpan})

	got, err := Recorded(rec)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trace.ID != "t2" {
		t.Errorf("returned trace %q, want the last one (t2)", got.Trace.ID)
	}
	if len(got.Observations) != 1 || got.Observations[0].Name != "new" {
		t.Errorf("observations = %+v, want only t2's", got.Observations)
	}
}

func TestRecordedWithoutTraceIDKeepsAllObservations(t *testing.T) {
	rec := trace.NewRecorder()
	rec.Enqueue(trace.Trace{ID: "", Name: "chat", Output: "a"})
	rec.EnqueueObservation(trace.Observation{Name: "search", Kind: trace.KindSpan})

	got, err := Recorded(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 1 {
		t.Fatalf("observations = %+v, want all of them when the trace has no ID", got.Observations)
	}
}

func TestReportString(t *testing.T) {
	results := []Result{
		{Name: "alpha", Passed: true, Trace: tr("t1", "SECRET OUTPUT")},
		{Name: "beta", Passed: false, Err: errors.New(`eval: trace t2: output does not contain "x"`)},
		{Name: "gamma", Passed: false, Err: errors.New("first\nsecond")},
	}
	report := Summarize(results)
	if report.Total != 3 || report.Passed != 1 || report.Failed != 2 {
		t.Fatalf("counters = %d/%d/%d, want 3/1/2", report.Total, report.Passed, report.Failed)
	}
	if report.OK() {
		t.Fatal("OK must be false when a case failed")
	}

	out := report.String()
	lines := strings.Split(out, "\n")
	if len(lines) != len(results) {
		t.Fatalf("want one line per case (%d), got %d: %q", len(results), len(lines), out)
	}
	if lines[0] != "PASS alpha" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if want := `FAIL beta: eval: trace t2: output does not contain "x"`; lines[1] != want {
		t.Errorf("line 1 = %q, want %q", lines[1], want)
	}
	if want := "FAIL gamma: first; second"; lines[2] != want {
		t.Errorf("line 2 = %q, want %q (newlines must be flattened)", lines[2], want)
	}
	if strings.Contains(out, "SECRET OUTPUT") {
		t.Error("Report.String must not print trace contents")
	}
}

func TestReportEmptyAndAllPass(t *testing.T) {
	if got := Summarize(nil); got.String() != "" || !got.OK() {
		t.Errorf("empty report = %q, OK = %v", got.String(), got.OK())
	}
	report := Summarize([]Result{{Name: "a", Passed: true}, {Name: "b", Passed: true}})
	if !report.OK() {
		t.Fatal("OK must be true when every case passed")
	}
	if report.String() != "PASS a\nPASS b" {
		t.Errorf("report = %q", report.String())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
