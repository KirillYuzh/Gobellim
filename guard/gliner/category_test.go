package gliner

import (
	"testing"

	"github.com/gobellim/kit/guard"
)

// The label→category mapping decides whether a turn is rejected, so it is pure
// and covered without the native runtime.
func TestCategoryForClassHonoursGroupTask(t *testing.T) {
	cm := map[string]string{
		"unsafe":      "UNSAFE",
		"adversarial": "INJECTION",
	}
	cases := []struct {
		name  string
		task  string
		label string
		cat   guard.Category
		ok    bool
	}{
		{"safety safe", "safety", "safe", "", false},
		{"safety unsafe", "safety", "unsafe", guard.CatUnsafe, true},
		{"safety unknown fails safe", "safety", "mystery", guard.CatUnsafe, true},
		{"intent informational", "intent", "informational", "", false},
		{"intent instructional", "intent", "instructional", "", false},
		{"intent mapped label still wins", "intent", "adversarial", guard.CatInjection, true},
		{"tone neutral", "tone", "neutral", "", false},
		{"tone distressed", "tone", "distressed", "", false},
		{"unknown task fails safe", "weird", "anything", guard.CatUnsafe, true},
	}
	for _, tc := range cases {
		got, ok := categoryForClass(cm, tc.task, tc.label)
		if ok != tc.ok || got != tc.cat {
			t.Errorf("%s: categoryForClass(%q, %q) = (%q, %v), want (%q, %v)",
				tc.name, tc.task, tc.label, got, ok, tc.cat, tc.ok)
		}
	}
}

// Entity labels default to PII, and category_map still decides.
func TestCategoryForLabelDefaults(t *testing.T) {
	cm := map[string]string{"not_a_secret": "SAFE", "token": "SECRET"}
	if cat, ok := categoryForLabel(cm, "person", guard.CatPII); !ok || cat != guard.CatPII {
		t.Fatalf("person = (%q, %v), want PII", cat, ok)
	}
	if _, ok := categoryForLabel(cm, "not_a_secret", guard.CatPII); ok {
		t.Fatal("a SAFE mapping must suppress the label")
	}
	if cat, ok := categoryForLabel(cm, "token", guard.CatPII); !ok || cat != guard.CatSecret {
		t.Fatalf("token = (%q, %v), want SECRET", cat, ok)
	}
}

// The export writes its threshold as cls_threshold; the go side used to ignore it
// and silently fall back to the built-in default.
func TestThresholdPrecedence(t *testing.T) {
	if got := thresholdFor(0.7, 0, nil, "x"); got != 0.7 {
		t.Fatalf("cls_threshold ignored: got %v, want 0.7", got)
	}
	if got := thresholdFor(0.7, 0.6, nil, "x"); got != 0.6 {
		t.Fatalf("explicit threshold must win: got %v, want 0.6", got)
	}
	if got := thresholdFor(0.7, 0.6, map[string]float64{"x": 0.9}, "x"); got != 0.9 {
		t.Fatalf("per-label threshold must win: got %v, want 0.9", got)
	}
	if got := thresholdFor(0, 0, nil, "x"); got != defaultClassificationThreshold {
		t.Fatalf("fallback = %v, want %v", got, defaultClassificationThreshold)
	}
}
