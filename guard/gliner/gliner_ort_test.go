//go:build ort

package gliner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const tol = 1e-3

type fixturesFile struct {
	Fixtures []fixture `json:"fixtures"`
}

type fixture struct {
	ID              string        `json:"id"`
	Text            string        `json:"text"`
	NormalizedText  string        `json:"normalized_text"`
	InputIDs        []int64       `json:"input_ids"`
	Words           []fixtureWord `json:"words"`
	TextWordIndices []int64       `json:"text_word_indices"`
	EntitySchemaIdx []int64       `json:"entity_schema_indices"`
	ClassSchemaIdx  []int64       `json:"class_schema_indices"`
	Result          result        `json:"result"`
	ONNX            struct {
		SpanProbs   [][][]float32 `json:"span_probs"`
		CountLogits []float32     `json:"count_logits"`
		CLSLogits   []float32     `json:"cls_logits"`
	} `json:"onnx"`
}

type fixtureWord struct {
	Token string `json:"token"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type result struct {
	Entities map[string][]string `json:"entities"`
	Safety   string              `json:"safety"`
}

func TestFixturesParity(t *testing.T) {
	ref, err := loadModelConfig(filepath.Join("testdata", "config.json"))
	if err != nil {
		t.Fatalf("testdata config: %v", err)
	}
	var ff fixturesFile
	readJSONFile(t, filepath.Join("testdata", "fixtures.json"), &ff)
	if len(ff.Fixtures) == 0 {
		t.Fatal("no fixtures")
	}
	d := testDetector(t)
	if !reflect.DeepEqual(d.cfg, ref) {
		t.Fatal("model config differs from testdata/config.json")
	}
	ok := 0
	for _, f := range ff.Fixtures {
		if err := checkFixture(d, f); err != nil {
			t.Errorf("FAIL %-24s %v", f.ID, err)
			continue
		}
		ok++
		t.Logf("ok   %-24s", f.ID)
	}
	t.Logf("%d/%d fixtures passed", ok, len(ff.Fixtures))
	if ok != len(ff.Fixtures) {
		t.FailNow()
	}
}

func checkFixture(d *Detector, f fixture) error {
	norm := normalizeText(f.Text)
	if norm != f.NormalizedText {
		return fmt.Errorf("normalized text:\n  got  %q\n  want %q", norm, f.NormalizedText)
	}
	words := d.splitWords(norm)
	if len(words) != len(f.Words) {
		return fmt.Errorf("word count: got %d want %d %+v", len(words), len(f.Words), words)
	}
	for i, w := range words {
		if w.token != f.Words[i].Token || w.start != f.Words[i].Start || w.end != f.Words[i].End {
			return fmt.Errorf("word %d: got %+v want %+v", i, w, f.Words[i])
		}
	}

	p := d.prepare(f.Text)
	if err := eqInt64("input_ids", p.inputIDs, f.InputIDs); err != nil {
		return err
	}
	if err := eqInt64("text_word_indices", p.wordFirst, f.TextWordIndices); err != nil {
		return err
	}
	if err := eqInt64("entity_schema_indices", d.cfg.Schema.Entities.SchemaMarkerPositions, f.EntitySchemaIdx); err != nil {
		return err
	}
	if err := eqInt64("class_schema_indices", d.cfg.Schema.Classifications[0].SchemaMarkerPositions, f.ClassSchemaIdx); err != nil {
		return err
	}

	sess, err := d.acquire(context.Background())
	if err != nil {
		return err
	}
	out, err := d.apply(sess, p)
	d.pool.Release(sess)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	if diff := maxDiff(out.spanProbs, flatten3(f.ONNX.SpanProbs)); diff > tol {
		return fmt.Errorf("span_probs diff %g", diff)
	}
	if diff := maxDiff(out.countLogits, f.ONNX.CountLogits); diff > tol {
		return fmt.Errorf("count_logits diff %g", diff)
	}
	if diff := maxDiff(out.clsLogits, f.ONNX.CLSLogits); diff > tol {
		return fmt.Errorf("cls_logits diff %g", diff)
	}

	res := decode(&d.cfg, p, out.spanProbs, out.countLogits, out.clsLogits)
	got := result{Entities: map[string][]string{}, Safety: res.safety}
	if res.entities != nil {
		for _, label := range d.cfg.Schema.Entities.Labels {
			list := make([]string, 0, len(res.entities[label]))
			for _, s := range res.entities[label] {
				list = append(list, s.text)
			}
			got.Entities[label] = list
		}
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(f.Result)
	if string(a) != string(b) {
		return fmt.Errorf("decode:\n  got  %s\n  want %s", a, b)
	}
	return nil
}

func testDetector(t *testing.T) *Detector {
	t.Helper()
	dir := os.Getenv("KIT_MODELS_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "models")
	}
	if _, err := os.Stat(filepath.Join(dir, DefaultModel, "model.onnx")); err != nil {
		t.Skipf("model %s is not present under %s", DefaultModel, dir)
	}
	if os.Getenv("KIT_ORT_LIB") == "" {
		lib, err := filepath.Abs(filepath.Join("..", "..", defaultLibPath()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lib); err != nil {
			t.Skipf("ORT library is not present at %s", lib)
		}
		t.Setenv("KIT_ORT_LIB", lib)
	}
	d, err := New(Config{Dir: dir, Sessions: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func eqInt64(name string, got, want []int64) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s length: got %d want %d", name, len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("%s[%d]: got %d want %d", name, i, got[i], want[i])
		}
	}
	return nil
}

func maxDiff(a, b []float32) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	var worst float64
	for i := range a {
		if d := math.Abs(float64(a[i] - b[i])); d > worst {
			worst = d
		}
	}
	return worst
}

func flatten3(v [][][]float32) []float32 {
	var out []float32
	for _, a := range v {
		for _, b := range a {
			out = append(out, b...)
		}
	}
	return out
}
