package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestPresetFor(t *testing.T) {
	e5, ok := PresetFor("intfloat/multilingual-e5-small")
	if !ok || e5.Name != "e5" || e5.QueryPrefix != "query: " || e5.PassagePrefix != "passage: " || !e5.Normalize {
		t.Fatalf("e5 preset = %+v ok=%v", e5, ok)
	}
	bge, ok := PresetFor("BAAI/bge-m3")
	if !ok || bge.Name != "bge-m3" || bge.Pooling != PoolingCLS || bge.QueryPrefix != "" {
		t.Fatalf("bge preset = %+v ok=%v", bge, ok)
	}
	if _, ok := PresetFor("unknown/model"); ok {
		t.Fatal("unknown model must have no preset")
	}
}

func TestPresetForE5Variants(t *testing.T) {
	for _, model := range []string{
		"intfloat/multilingual-e5-small",
		"intfloat/multilingual-e5-base",
		"intfloat/multilingual-e5-large",
		"Multilingual-E5-Small",
	} {
		p, ok := PresetFor(model)
		if !ok || p.Name != "e5" {
			t.Fatalf("PresetFor(%q) = %+v ok=%v, want e5", model, p, ok)
		}
	}
	if p, ok := PresetFor("BAAI/bge-m3"); !ok || p.Name != "bge-m3" {
		t.Fatalf("PresetFor(bge-m3) = %+v ok=%v", p, ok)
	}
}

func TestPresetValidate(t *testing.T) {
	good := []Preset{
		presetE5,
		presetBGE,
		{Pooling: PoolingMean, MaxLen: 1},
	}
	for _, p := range good {
		if err := p.Validate(); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", p, err)
		}
	}
	bad := []Preset{
		{MaxLen: 512},
		{Pooling: "max", MaxLen: 512},
		{Pooling: PoolingMean, MaxLen: 0},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Fatalf("Validate(%+v) = nil, want error", p)
		}
	}
}

func TestLoadFileConfig(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "kit_embedder.json")
	if err := os.WriteFile(good, []byte(`{"pooling":"cls","query_prefix":"q: ","passage_prefix":"p: ","normalize":true,"max_len":256}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadFileConfig(good)
	if err != nil {
		t.Fatal(err)
	}
	if p.Pooling != PoolingCLS || p.QueryPrefix != "q: " || p.MaxLen != 256 || !p.Normalize {
		t.Fatalf("preset = %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("kit_embedder.json must round-trip through Validate: %v", err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"pooling":"weird"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFileConfig(bad); err == nil {
		t.Fatal("unknown pooling must fail")
	}

	unknown := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"pooling":"mean","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFileConfig(unknown); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestOpenAIEmbedderAppliesPrefixAndNormalizes(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = body.Input
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`)
		for i := len(body.Input) - 1; i >= 0; i-- {
			if i != len(body.Input)-1 {
				_, _ = io.WriteString(w, ",")
			}
			_, _ = fmt.Fprintf(w, `{"index":%d,"embedding":[3,4]}`, i)
		}
		_, _ = io.WriteString(w, `]}`)
	}))
	defer srv.Close()

	e, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: "intfloat/multilingual-e5-small"})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.Query(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "query: a" || got[1] != "query: b" {
		t.Fatalf("prefixes not applied: %v", got)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("vectors = %v", vecs)
	}
	if diff := vecs[0][0] - 0.6; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("vector not normalized: %v", vecs[0])
	}
	if e.Dim() != 2 {
		t.Fatalf("dim = %d, want 2", e.Dim())
	}

	if _, err := e.Passages(context.Background(), []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "passage: doc" {
		t.Fatalf("passage prefix not applied: %v", got)
	}
}

func TestPresetForIdentifiesFamilyBySegment(t *testing.T) {
	matched := map[string]string{
		"bge-m3":                         "bge-m3",
		"BAAI/bge-m3":                    "bge-m3",
		"bge-m3-tuned":                   "bge-m3",
		"intfloat/multilingual-e5-small": "e5",
		"multilingual-e5":                "e5",
	}
	for model, want := range matched {
		p, ok := PresetFor(model)
		if !ok || p.Name != want {
			t.Fatalf("PresetFor(%q) = %+v ok=%v, want preset %q", model, p, ok, want)
		}
	}
	guessed := []string{
		"my-bge-m3-tuned",
		"custom-multilingual-e5-copy",
		"bge-m3x",
		"xmultilingual-e5",
	}
	for _, model := range guessed {
		if p, ok := PresetFor(model); ok {
			t.Fatalf("PresetFor(%q) = %+v, want no preset: the name does not identify the family", model, p)
		}
	}
}

func TestPresetForModelReportsAmbiguity(t *testing.T) {
	ambiguous := []string{"my-bge-m3-tuned", "custom-multilingual-e5-copy", "bge-m3/multilingual-e5"}
	for _, model := range ambiguous {
		_, err := presetForModel(model)
		if !errors.Is(err, core.ErrInvalidConfig) {
			t.Fatalf("presetForModel(%q) err = %v, want %v", model, err, core.ErrInvalidConfig)
		}
	}
	if _, err := presetForModel("unknown/model"); !errors.Is(err, errNoFamily) {
		t.Fatalf("presetForModel(unknown) err = %v, want %v", err, errNoFamily)
	}
}
