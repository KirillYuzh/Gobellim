//go:build ort

package gliner

import (
	"math"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
)

func newMultiGroupConfig() modelConfig {
	mc := modelConfig{
		PrefixIDs: []int64{1, 2, 3},
		CategoryMap: map[string]string{
			"pii_leak":    "PII",
			"override_me": "INJECTION",
		},
		MaxWidth: 8,
		CountMax: 20,
	}
	mc.Decode.DefaultSpanThreshold = 0.5
	mc.Schema.Entities.Labels = []string{"person", "email", "phone"}
	mc.Schema.Entities.SchemaMarkerPositions = []int64{1, 6, 8, 10}
	mc.Schema.Classifications = []classification{
		{
			Task:                  "safety",
			Labels:                []string{"safe", "unsafe"},
			Activation:            "softmax",
			SchemaMarkerPositions: []int64{18, 22, 24},
		},
		{
			Task:                  "adversarial",
			Labels:                []string{"pii_leak", "override_me", "totally_unknown", "benign", "lowconf"},
			MultiLabel:            true,
			Activation:            "sigmoid",
			Output:                "adversarial_logits",
			Threshold:             0.5,
			Thresholds:            map[string]float64{"lowconf": 0.9},
			SchemaMarkerPositions: []int64{18, 28, 30, 32, 34, 36},
		},
	}
	return mc
}

func TestConfigValidateMultiGroup(t *testing.T) {
	mc := newMultiGroupConfig()
	if err := mc.validate(); err != nil {
		t.Fatalf("valid multi-group config rejected: %v", err)
	}

	mutate := func(f func(*modelConfig)) modelConfig {
		mc := newMultiGroupConfig()
		f(&mc)
		return mc
	}
	cases := []struct {
		name string
		cfg  modelConfig
	}{
		{"no groups", mutate(func(mc *modelConfig) { mc.Schema.Classifications = nil })},
		{"empty labels", mutate(func(mc *modelConfig) { mc.Schema.Classifications[1].Labels = nil })},
		{"marker length", mutate(func(mc *modelConfig) {
			mc.Schema.Classifications[1].SchemaMarkerPositions = []int64{18, 28}
		})},
		{"bad activation", mutate(func(mc *modelConfig) {
			mc.Schema.Classifications[1].Activation = "relu"
		})},
		{"multi_label without sigmoid", mutate(func(mc *modelConfig) {
			mc.Schema.Classifications[1].Activation = "softmax"
		})},
		{"multi_label empty activation", mutate(func(mc *modelConfig) {
			mc.Schema.Classifications[1].Activation = ""
		})},
		{"duplicate output", mutate(func(mc *modelConfig) {
			mc.Schema.Classifications[1].Output = ""
		})},
		{"unknown category", mutate(func(mc *modelConfig) {
			mc.CategoryMap["pii_leak"] = "BANANA"
		})},
	}
	for _, tc := range cases {
		if err := tc.cfg.validate(); err == nil {
			t.Errorf("%s: validate accepted an invalid config", tc.name)
		}
	}
}

func TestClassTensorsMultiGroup(t *testing.T) {
	mc := newMultiGroupConfig()
	got := mc.classOutputs()
	want := []string{"cls_logits", "adversarial_logits"}
	if len(got) != len(want) {
		t.Fatalf("classOutputs: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("classOutputs[%d]: got %q want %q", i, got[i], want[i])
		}
	}
	idx := mc.classSchemaIndices()
	wantIdx := []int64{18, 22, 24, 28, 30, 32, 34, 36}
	if len(idx) != len(wantIdx) {
		t.Fatalf("classSchemaIndices: got %v want %v", idx, wantIdx)
	}
	for i := range wantIdx {
		if idx[i] != wantIdx[i] {
			t.Fatalf("classSchemaIndices[%d]: got %d want %d", i, idx[i], wantIdx[i])
		}
	}
}

func TestDecodeSingleGroupSoftmaxCompat(t *testing.T) {
	mc := newMultiGroupConfig()
	mc.Schema.Classifications = mc.Schema.Classifications[:1]

	logits := []float32{-1, 2}
	res := decode(&mc, &prepared{}, nil, []float32{1, 0}, [][]float32{logits})

	want := softmax(logits)
	best := argmax64(want)
	if res.safety != "unsafe" || res.score != want[best] {
		t.Fatalf("legacy view: got (%q, %v) want (%q, %v)", res.safety, res.score, "unsafe", want[best])
	}
	if len(res.classifications) != 1 {
		t.Fatalf("classifications: got %d want 1", len(res.classifications))
	}
	if c := res.classifications[0]; c.group != 0 || c.label != "unsafe" || c.score != want[best] {
		t.Fatalf("classification[0]: got %+v", c)
	}
}

func TestDecodeMultiGroupSigmoidAndCategoryMap(t *testing.T) {
	mc := newMultiGroupConfig()
	d := &Detector{cfg: mc}
	p := &prepared{nRunes: 5}

	out := &outputs{
		countLogits: []float32{1, 0, 0},
		clsLogits: [][]float32{
			{-1, 2},
			{3, 2, 1, -2, 1.5},
		},
	}

	fs := d.findings(p, out)
	if len(fs) != 4 {
		t.Fatalf("findings: got %d (%+v) want 4", len(fs), fs)
	}

	want := []struct {
		cat   guard.Category
		label string
	}{
		{guard.CatUnsafe, "unsafe"},
		{guard.CatPII, "pii_leak"},
		{guard.CatInjection, "override_me"},
		{guard.CatUnsafe, "totally_unknown"},
	}
	for i, w := range want {
		if fs[i].Category != w.cat || fs[i].Label != w.label {
			t.Errorf("finding %d: got (%s, %s) want (%s, %s)", i, fs[i].Category, fs[i].Label, w.cat, w.label)
		}
		if fs[i].Layer != "L2" || fs[i].Start != 0 || fs[i].End != p.nRunes {
			t.Errorf("finding %d: bad span/layer %+v", i, fs[i])
		}
		if math.IsNaN(fs[i].Score) || fs[i].Score <= 0 {
			t.Errorf("finding %d: bad score %v", i, fs[i].Score)
		}
	}
	for _, f := range fs {
		if f.Label == "benign" || f.Label == "lowconf" {
			t.Errorf("threshold-suppressed label emitted a finding: %+v", f)
		}
	}

	wantPII := 1 / (1 + math.Exp(-3.0))
	if math.Abs(fs[1].Score-wantPII) > 1e-12 {
		t.Errorf("pii_leak score: got %v want %v", fs[1].Score, wantPII)
	}
}

func TestCategoryMapSafeAndUnmapped(t *testing.T) {
	mc := newMultiGroupConfig()
	mc.CategoryMap["totally_unknown"] = "SAFE"
	g := &mc.Schema.Classifications[1]
	g.Labels = append(g.Labels, "mystery")
	g.SchemaMarkerPositions = append(g.SchemaMarkerPositions, 38)
	if err := mc.validate(); err != nil {
		t.Fatalf("validate rejected a category_map SAFE mapping: %v", err)
	}

	d := &Detector{cfg: mc}
	p := &prepared{nRunes: 5}
	out := &outputs{
		countLogits: []float32{1, 0, 0},
		clsLogits: [][]float32{
			{-1, 2},
			{3, 2, 1, -2, 1.5, 2.0},
		},
	}
	fs := d.findings(p, out)

	sawMystery := false
	for _, f := range fs {
		if f.Label == "totally_unknown" {
			t.Errorf("SAFE-mapped label emitted a finding: %+v", f)
		}
		if f.Label == "mystery" {
			sawMystery = true
			if f.Category != guard.CatUnsafe {
				t.Errorf("unmapped label: got category %q want UNSAFE", f.Category)
			}
		}
	}
	if !sawMystery {
		t.Errorf("unmapped label produced no finding; want fail-safe UNSAFE: %+v", fs)
	}
	if len(fs) != 4 {
		t.Errorf("findings: got %d (%+v) want 4", len(fs), fs)
	}
}
