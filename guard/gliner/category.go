package gliner

import (
	"regexp"
	"strings"

	"github.com/gobellim/kit/guard"
)

// The label→category mapping is pure logic and deliberately lives outside the
// ort build, so the default test run covers it: a wrong mapping is either a
// guard bypass or a false reject, and neither should need the native runtime to
// reproduce.

var (
	unsafeLabelRe    = regexp.MustCompile(`(?i)(unsafe|harmful|toxic|violence|danger)`)
	injectionLabelRe = regexp.MustCompile(`(?i)(inject|jailbreak|attack|override|exfiltrat|abuse)`)
)

const (
	defaultClassificationThreshold = 0.5
	safeCategory                   = "SAFE"
)

// safeLabels are label names that positively mean "nothing to report".
var safeLabels = map[string]bool{
	"safe": true, "benign": true, "harmless": true,
	"clean": true, "neutral": true, "none": true, "ok": true,
	"informational": true, "informal": true, "formal": true,
	"conversational": true, "creative": true, "factual": true,
	"positive": true, "polite": true,
}

// informationalTasks name classification groups whose labels describe intent,
// tone and the like: they are not safety verdicts, so an unmapped label from such
// a group produces no finding instead of a fail-safe UNSAFE. A group is treated as
// safety-bearing unless its task is listed here, so a new export still fails safe.
var informationalTasks = map[string]bool{
	"intent": true, "tone": true, "topic": true, "sentiment": true,
	"style": true, "language": true, "formality": true, "domain": true,
}

func parseCategory(s string) (guard.Category, bool) {
	switch c := guard.Category(strings.ToUpper(strings.TrimSpace(s))); c {
	case guard.CatPII, guard.CatSecret, guard.CatInjection, guard.CatUnsafe:
		return c, true
	}
	return "", false
}

func categoryIsSafe(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), safeCategory)
}

// categoryForLabel maps one label to a category. An explicit category_map entry
// wins — including SAFE, which suppresses the label; otherwise a safe name is
// dropped, and any other label (or a caller-supplied default) falls back to the
// fail-safe UNSAFE (I-14).
func categoryForLabel(cm map[string]string, label string, def guard.Category) (guard.Category, bool) {
	if s, ok := cm[label]; ok {
		if categoryIsSafe(s) {
			return "", false
		}
		if c, ok := parseCategory(s); ok {
			return c, true
		}
	}
	if def != "" {
		return def, true
	}
	low := strings.ToLower(strings.TrimSpace(label))
	if safeLabels[low] {
		return "", false
	}
	if unsafeLabelRe.MatchString(low) {
		return guard.CatUnsafe, true
	}
	if injectionLabelRe.MatchString(low) {
		return guard.CatInjection, true
	}
	return guard.CatUnsafe, true
}

// categoryForClass maps a label from a named classification group. A label in an
// informational group (intent, tone, …) is a verdict only when the model maps it
// explicitly, so a benign "instructional" or "distressed" label no longer rejects
// the turn; a safety-bearing group keeps the fail-safe (I-14).
func categoryForClass(cm map[string]string, task, label string) (guard.Category, bool) {
	if informationalTasks[strings.ToLower(strings.TrimSpace(task))] {
		s, ok := cm[label]
		if !ok || categoryIsSafe(s) {
			return "", false
		}
		if c, ok := parseCategory(s); ok {
			return c, true
		}
		return "", false
	}
	return categoryForLabel(cm, label, "")
}

// thresholdFor picks a label's threshold: a per-label entry, then the group's
// `threshold`, then the export's `cls_threshold`, then the built-in default.
func thresholdFor(clsThreshold, threshold float64, thresholds map[string]float64, label string) float64 {
	if v, ok := thresholds[label]; ok {
		return v
	}
	if threshold > 0 {
		return threshold
	}
	if clsThreshold > 0 {
		return clsThreshold
	}
	return defaultClassificationThreshold
}
