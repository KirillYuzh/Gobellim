package eval

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gobellim/kit/trace"
)

func Includes(substr string) Check {
	return func(t Trace) error {
		if strings.Contains(output(t), substr) {
			return nil
		}
		return fmt.Errorf("eval: trace %s: output does not contain %q", identify(t), substr)
	}
}

func Excludes(substr string) Check {
	return func(t Trace) error {
		if !strings.Contains(output(t), substr) {
			return nil
		}
		return fmt.Errorf("eval: trace %s: output contains forbidden substring %q", identify(t), substr)
	}
}

func Equals(want string) Check {
	return func(t Trace) error {
		if output(t) == want {
			return nil
		}
		return fmt.Errorf("eval: trace %s: output does not equal %q", identify(t), want)
	}
}

func Matches(re *regexp.Regexp) Check {
	if re == nil {
		return func(t Trace) error {
			return fmt.Errorf("eval: trace %s: Matches: nil regexp", identify(t))
		}
	}
	return func(t Trace) error {
		if re.MatchString(output(t)) {
			return nil
		}
		return fmt.Errorf("eval: trace %s: output does not match %s", identify(t), re.String())
	}
}

func Similarity(want string, min float64) Check {
	return func(t Trace) error {
		score := DiceSimilarity(output(t), want)
		if score >= min {
			return nil
		}
		return fmt.Errorf("eval: trace %s: Dice similarity %.3f is below threshold %.3f", identify(t), score, min)
	}
}

func DiceSimilarity(a, b string) float64 {
	ab := wordBigrams(a)
	bb := wordBigrams(b)
	if len(ab) == 0 && len(bb) == 0 {
		if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
			return 1
		}
		return 0
	}
	if len(ab) == 0 || len(bb) == 0 {
		return 0
	}
	return 2 * float64(bigramOverlap(ab, bb)) / float64(len(ab)+len(bb))
}

func wordBigrams(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) < 2 {
		return nil
	}
	out := make([]string, 0, len(fields)-1)
	for i := 1; i < len(fields); i++ {
		out = append(out, fields[i-1]+" "+fields[i])
	}
	return out
}

func bigramOverlap(a, b []string) int {
	counts := make(map[string]int, len(a))
	for _, g := range a {
		counts[g]++
	}
	n := 0
	for _, g := range b {
		if counts[g] > 0 {
			counts[g]--
			n++
		}
	}
	return n
}

func CalledTool(name string) Check {
	return func(t Trace) error {
		names := toolNames(t)
		for _, n := range names {
			if n == name {
				return nil
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("eval: trace %s: tool %q was not called: no tool observations recorded", identify(t), name)
		}
		return fmt.Errorf("eval: trace %s: tool %q was not called (called: %s)", identify(t), name, strings.Join(names, ", "))
	}
}

func DidNotCall(name string) Check {
	return func(t Trace) error {
		for _, n := range toolNames(t) {
			if n == name {
				return fmt.Errorf("eval: trace %s: tool %q was called but must not be", identify(t), name)
			}
		}
		return nil
	}
}

func UsedNoTools() Check {
	return func(t Trace) error {
		names := toolNames(t)
		if len(names) == 0 {
			return nil
		}
		return fmt.Errorf("eval: trace %s: expected no tool calls, got %d (%s)", identify(t), len(names), strings.Join(names, ", "))
	}
}

func MaxToolCalls(n int) Check {
	return func(t Trace) error {
		names := toolNames(t)
		if len(names) <= n {
			return nil
		}
		return fmt.Errorf("eval: trace %s: %d tool calls exceed the maximum of %d (%s)", identify(t), len(names), n, strings.Join(names, ", "))
	}
}

func ToolOrder(names ...string) Check {
	want := append([]string(nil), names...)
	return func(t Trace) error {
		got := toolNames(t)
		if isSubsequence(want, got) {
			return nil
		}
		if len(got) == 0 {
			return fmt.Errorf("eval: trace %s: tool order %v not satisfied: no tool observations recorded", identify(t), want)
		}
		return fmt.Errorf("eval: trace %s: tool order %v not satisfied (called in order: %s)", identify(t), want, strings.Join(got, ", "))
	}
}

func isSubsequence(want, got []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func NoToolErrors() Check {
	return func(t Trace) error {
		var bad []string
		for _, o := range toolObservations(t) {
			if o.Level == trace.LevelError || o.ErrorKind != "" {
				bad = append(bad, o.Name)
			}
		}
		if len(bad) == 0 {
			return nil
		}
		return fmt.Errorf("eval: trace %s: %d tool call(s) errored: %s", identify(t), len(bad), strings.Join(bad, ", "))
	}
}

func All(fs ...Check) Check {
	return func(t Trace) error {
		var failures []error
		for i, f := range fs {
			if f == nil {
				failures = append(failures, fmt.Errorf("eval: trace %s: All: check %d is nil", identify(t), i))
				continue
			}
			if err := f(t); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) == 0 {
			return nil
		}
		return fmt.Errorf("eval: trace %s: All: %d of %d checks failed: %w",
			identify(t), len(failures), len(fs), errors.Join(failures...))
	}
}

func Any(fs ...Check) Check {
	return func(t Trace) error {
		if len(fs) == 0 {
			return fmt.Errorf("eval: trace %s: Any: no checks provided", identify(t))
		}
		var failures []error
		for i, f := range fs {
			if f == nil {
				failures = append(failures, fmt.Errorf("eval: trace %s: Any: check %d is nil", identify(t), i))
				continue
			}
			if err := f(t); err != nil {
				failures = append(failures, err)
			} else {
				return nil
			}
		}
		return fmt.Errorf("eval: trace %s: Any: all %d checks failed: %w",
			identify(t), len(fs), errors.Join(failures...))
	}
}

func Not(f Check) Check {
	return func(t Trace) error {
		if f == nil {
			return fmt.Errorf("eval: trace %s: Not: nil check", identify(t))
		}
		if err := f(t); err != nil {
			return nil
		}
		return fmt.Errorf("eval: trace %s: Not: check unexpectedly passed", identify(t))
	}
}

func toolObservations(t Trace) []trace.Observation {
	out := make([]trace.Observation, 0, len(t.Observations))
	for _, o := range t.Observations {
		if o.Kind == trace.KindSpan {
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartTime.Before(out[j].StartTime)
	})
	return out
}

func toolNames(t Trace) []string {
	obs := toolObservations(t)
	names := make([]string, len(obs))
	for i, o := range obs {
		names[i] = o.Name
	}
	return names
}
