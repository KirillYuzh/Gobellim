package gliner

import (
	"regexp"
	"strings"

	"github.com/gobellim/kit/guard"
)

var (
	unsafeLabelRe    = regexp.MustCompile(`(?i)(unsafe|harmful|toxic|violence|danger)`)
	injectionLabelRe = regexp.MustCompile(`(?i)(inject|jailbreak|attack|override|exfiltrat|abuse)`)
)

const (
	defaultClassificationThreshold = 0.5
	safeCategory                   = "SAFE"
)

var safeLabels = map[string]bool{
	"safe": true, "benign": true, "harmless": true,
	"clean": true, "neutral": true, "none": true, "ok": true,
	"informational": true, "informal": true, "formal": true,
	"conversational": true, "creative": true, "factual": true,
	"positive": true, "polite": true,
}

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
