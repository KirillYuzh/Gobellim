package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/trace"
)

type Trace struct {
	Trace        trace.Trace
	Observations []trace.Observation
}

type Check func(t Trace) error

type Case struct {
	Name     string
	Input    string
	Checks   []Check
	Metadata map[string]any
}

type Result struct {
	Name    string
	Passed  bool
	Err     error
	Trace   Trace
	Elapsed time.Duration
}

func Run(ctx context.Context, rec *trace.Recorder, cases []Case,
	run func(ctx context.Context, input string) error,
) []Result {
	results := make([]Result, 0, len(cases))
	for _, c := range cases {
		results = append(results, runCase(ctx, rec, c, run))
	}
	return results
}

func runCase(ctx context.Context, rec *trace.Recorder, c Case,
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
			failures = append(failures, fmt.Errorf("eval: trace %s: check %d is nil", identify(t), i))
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

func Recorded(rec *trace.Recorder) (Trace, error) {
	if rec == nil {
		return Trace{}, fmt.Errorf("eval: no recorder: %w", core.ErrInvalidConfig)
	}
	snap := rec.Snapshot()
	if len(snap.Traces) == 0 {
		return Trace{}, fmt.Errorf("eval: no trace recorded: %w", core.ErrNotFound)
	}
	last := snap.Traces[len(snap.Traces)-1]
	obs := make([]trace.Observation, 0, len(snap.Observations))
	for _, o := range snap.Observations {
		if last.ID == "" || o.TraceID == last.ID {
			obs = append(obs, o)
		}
	}
	return Trace{Trace: last, Observations: obs}, nil
}

type Report struct {
	Total, Passed, Failed int
	Results               []Result
}

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

func (r Report) OK() bool { return r.Failed == 0 }

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

func identify(t Trace) string {
	switch {
	case t.Trace.ID != "":
		return t.Trace.ID
	case t.Trace.Name != "":
		return t.Trace.Name
	default:
		return "<unknown>"
	}
}

func output(t Trace) string { return t.Trace.Output }
