package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Trace is one recorded trace together with its observations.
type Trace struct {
	Trace        TraceRecord
	Observations []Observation
}

// Check inspects a trace and returns an error on failure.
type Check func(Trace) error

// Case is one eval scenario: an input to run and the checks to apply.
type Case struct {
	Name   string
	Input  string
	Checks []Check
}

// Result is the outcome of one Case.
type Result struct {
	Name    string
	Passed  bool
	Err     error
	Trace   Trace
	Elapsed time.Duration
}

// Run executes each case sequentially, resetting rec before every case.
func Run(ctx context.Context, rec *Recorder, cases []Case,
	run func(ctx context.Context, input string) error,
) []Result {
	results := make([]Result, 0, len(cases))
	for _, c := range cases {
		results = append(results, runCase(ctx, rec, c, run))
	}
	return results
}

func runCase(ctx context.Context, rec *Recorder, c Case,
	run func(ctx context.Context, input string) error,
) Result {
	start := time.Now()
	res := Result{Name: c.Name}
	if rec == nil {
		res.Err = fmt.Errorf("eval: case %q: nil recorder: %w", c.Name, core.ErrInvalidConfig)
		res.Elapsed = time.Since(start)
		return res
	}
	if run == nil {
		res.Err = fmt.Errorf("eval: case %q: nil run function: %w", c.Name, core.ErrInvalidConfig)
		res.Elapsed = time.Since(start)
		return res
	}

	rec.Reset()

	if err := run(ctx, c.Input); err != nil {
		if t, rerr := Recorded(rec); rerr == nil {
			res.Trace = t
		}
		res.Err = fmt.Errorf("eval: case %q: run: %w", c.Name, err)
		res.Elapsed = time.Since(start)
		return res
	}

	t, err := Recorded(rec)
	if err != nil {
		res.Err = fmt.Errorf("eval: case %q: %w", c.Name, err)
		res.Elapsed = time.Since(start)
		return res
	}
	res.Trace = t

	var failures []error
	for i, check := range c.Checks {
		if check == nil {
			failures = append(failures, fmt.Errorf("eval: trace %s: check %d is nil", traceID(t), i))
			continue
		}
		if cerr := check(t); cerr != nil {
			failures = append(failures, cerr)
		}
	}
	res.Err = errors.Join(failures...)
	res.Passed = res.Err == nil
	res.Elapsed = time.Since(start)
	return res
}

// Recorded returns the last record from rec's snapshot with its observations.
func Recorded(rec *Recorder) (Trace, error) {
	if rec == nil {
		return Trace{}, fmt.Errorf("eval: no recorder: %w", core.ErrInvalidConfig)
	}
	snap := rec.Snapshot()
	if len(snap.Traces) == 0 {
		return Trace{}, fmt.Errorf("eval: no trace recorded: %w", core.ErrNotFound)
	}
	last := snap.Traces[len(snap.Traces)-1]
	obs := make([]Observation, 0, len(snap.Observations))
	for _, o := range snap.Observations {
		if last.ID == "" || o.TraceID == last.ID {
			obs = append(obs, o)
		}
	}
	return Trace{Trace: last, Observations: obs}, nil
}

// Report aggregates eval results.
type Report struct {
	Total, Passed, Failed int
	Results               []Result
}

// Summarize counts passed and failed results.
func Summarize(results []Result) Report {
	r := Report{Total: len(results), Results: results}
	for _, res := range results {
		if res.Passed {
			r.Passed++
		} else {
			r.Failed++
		}
	}
	return r
}

// OK reports whether all cases passed.
func (r Report) OK() bool { return r.Failed == 0 }

// String renders one line per result.
func (r Report) String() string {
	var b strings.Builder
	for i, res := range r.Results {
		if i > 0 {
			b.WriteByte('\n')
		}
		if res.Passed {
			b.WriteString("PASS ")
			b.WriteString(res.Name)
			continue
		}
		b.WriteString("FAIL ")
		b.WriteString(res.Name)
		if res.Err != nil {
			b.WriteString(": ")
			b.WriteString(oneLine(res.Err.Error()))
		}
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "; ")
	s = strings.ReplaceAll(s, "\n", "; ")
	return s
}

func traceID(t Trace) string {
	if t.Trace.ID != "" {
		return t.Trace.ID
	}
	if t.Trace.Name != "" {
		return t.Trace.Name
	}
	return "<unknown>"
}

func output(t Trace) string { return t.Trace.Output }
