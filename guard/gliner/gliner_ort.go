//go:build ort

// Package gliner is the ML guard layer: an ONNX-only GLiNER2 detector that runs
// behind the ort build tag.
package gliner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
	"unicode/utf8"

	"github.com/daulet/tokenizers"
	orts "github.com/yalue/onnxruntime_go"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/ort"
)

// DefaultModel is the pinned ONNX export of the GLiNER2 guard, relative to
// Config.Dir.
const DefaultModel = "hivetrace__gliner-guard-omni"

// DefaultSessions is the number of ORT sessions in the detector pool.
const DefaultSessions = 2

const unsafeLabel = "unsafe"

// Config selects the guard model. Dir is the models root (KIT_MODELS_DIR or
// ./models), Model the subdirectory holding model.onnx, tokenizer.json and
// config.json. Sessions and Wait tune the inference pool; both have defaults.
type Config struct {
	Dir      string
	Model    string
	Sessions int
	Wait     time.Duration
}

type Detector struct {
	cfg    modelConfig
	wordRe *regexp.Regexp
	tk     *tokenizers.Tokenizer
	pool   *ort.Pool
}

func (c Config) withDefaults() Config {
	if c.Dir == "" {
		c.Dir = os.Getenv("KIT_MODELS_DIR")
	}
	if c.Dir == "" {
		c.Dir = "./models"
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Sessions < 1 {
		c.Sessions = DefaultSessions
	}
	if c.Wait <= 0 {
		c.Wait = ort.DefaultWait
	}
	return c
}

func defaultLibPath() string {
	if runtime.GOOS == "darwin" {
		return ".ort/lib/libonnxruntime.dylib"
	}
	return ".ort/lib/libonnxruntime.so"
}

// New loads the model, tokenizer and config from <Dir>/<Model> and builds the
// session pool. Any failure is fail-closed: it is wrapped in
// core.ErrGuardUnavailable.
func New(cfg Config) (*Detector, error) {
	cfg = cfg.withDefaults()
	dir := filepath.Join(cfg.Dir, cfg.Model)

	mc, err := loadModelConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, unavailable(err)
	}
	wordRe, err := regexp.Compile(wordExpr)
	if err != nil {
		return nil, unavailable(err)
	}
	lib := os.Getenv("KIT_ORT_LIB")
	if lib == "" {
		lib = defaultLibPath()
	}
	orts.SetSharedLibraryPath(lib)
	if !orts.IsInitialized() {
		if err := orts.InitializeEnvironment(); err != nil {
			return nil, unavailable(fmt.Errorf("ort init: %w", err))
		}
	}
	tk, err := tokenizers.FromFile(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		return nil, unavailable(err)
	}
	opts, err := orts.NewSessionOptions()
	if err != nil {
		_ = tk.Close()
		return nil, unavailable(err)
	}
	defer func() { _ = opts.Destroy() }()
	if err := opts.SetIntraOpNumThreads(ort.Threads(cfg.Sessions)); err != nil {
		_ = tk.Close()
		return nil, unavailable(err)
	}

	inputs := []string{"input_ids", "attention_mask", "text_word_indices", "text_word_mask", "entity_schema_indices", "class_schema_indices"}
	outputs := []string{"span_probs", "count_logits", "cls_logits"}
	modelPath := filepath.Join(dir, "model.onnx")

	sessions := make([]ort.Session, 0, cfg.Sessions)
	abort := func() {
		for _, s := range sessions {
			_ = s.Close()
		}
		_ = tk.Close()
	}
	for i := 0; i < cfg.Sessions; i++ {
		s, serr := orts.NewDynamicAdvancedSession(modelPath, inputs, outputs, opts)
		if serr != nil {
			abort()
			return nil, unavailable(serr)
		}
		sessions = append(sessions, &session{inner: s})
	}
	pool, err := ort.NewPool(sessions, cfg.Wait)
	if err != nil {
		abort()
		return nil, unavailable(err)
	}
	return &Detector{cfg: mc, wordRe: wordRe, tk: tk, pool: pool}, nil
}

func (*Detector) Name() string { return "L2" }

func (d *Detector) Detect(ctx context.Context, _ guard.Rail, text string) ([]guard.Finding, error) {
	if text == "" {
		return nil, nil
	}
	p := d.prepare(text)
	sess, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	out, err := d.apply(sess, p)
	d.pool.Release(sess)
	if err != nil {
		return nil, unavailable(fmt.Errorf("inference: %w", err))
	}
	return d.findings(p, out), nil
}

func (d *Detector) acquire(ctx context.Context) (*session, error) {
	s, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	sess, ok := s.(*session)
	if !ok {
		d.pool.Release(s)
		return nil, unavailable(errors.New("unexpected session type"))
	}
	return sess, nil
}

func unavailable(err error) error {
	return fmt.Errorf("gliner: %w: %w", core.ErrGuardUnavailable, err)
}

type session struct {
	inner *orts.DynamicAdvancedSession
}

func (s *session) Close() error { return s.inner.Destroy() }

type prepared struct {
	runes     []rune
	words     []word
	nRunes    int
	inputIDs  []int64
	wordFirst []int64
	attn      []int64
}

func (d *Detector) prepare(text string) *prepared {
	norm := normalizeText(text)
	runes := []rune(norm)
	words := d.splitWords(norm)

	seq := make([]int64, 0, len(d.cfg.PrefixIDs)+len(words)*4)
	seq = append(seq, d.cfg.PrefixIDs...)
	first := make([]int64, 0, len(words))
	for _, w := range words {
		first = append(first, int64(len(seq)))
		ids, _ := d.tk.Encode(w.token, false)
		for _, id := range ids {
			seq = append(seq, int64(id))
		}
	}
	attn := make([]int64, len(seq))
	for i := range attn {
		attn[i] = 1
	}
	return &prepared{
		runes:     runes,
		words:     words,
		nRunes:    utf8.RuneCountInString(text),
		inputIDs:  seq,
		wordFirst: first,
		attn:      attn,
	}
}

type outputs struct {
	spanProbs   []float32
	countLogits []float32
	clsLogits   []float32
}

type tensorSet struct {
	values []orts.Value
}

func newTensor[T orts.TensorData](set *tensorSet, shape orts.Shape, data []T) (*orts.Tensor[T], error) {
	t, err := orts.NewTensor(shape, data)
	if err != nil {
		return nil, err
	}
	set.values = append(set.values, t)
	return t, nil
}

func (s *tensorSet) destroy() {
	for _, v := range s.values {
		_ = v.Destroy()
	}
	s.values = nil
}

func (d *Detector) apply(s *session, p *prepared) (*outputs, error) {
	n, nw := len(p.inputIDs), len(p.words)
	labels := d.cfg.Schema.Entities.Labels
	cls := d.cfg.Schema.Classifications[0]
	mw, cm := d.cfg.MaxWidth, d.cfg.CountMax

	mask := make([]bool, nw)
	for i := range mask {
		mask[i] = true
	}
	spanProbs := make([]float32, len(labels)*nw*mw)
	countLogits := make([]float32, cm)
	clsLogits := make([]float32, len(cls.Labels))

	var set tensorSet
	defer set.destroy()

	tIn, err := newTensor(&set, orts.NewShape(1, int64(n)), p.inputIDs)
	if err != nil {
		return nil, err
	}
	tAttn, err := newTensor(&set, orts.NewShape(1, int64(n)), p.attn)
	if err != nil {
		return nil, err
	}
	tWord, err := newTensor(&set, orts.NewShape(1, int64(nw)), p.wordFirst)
	if err != nil {
		return nil, err
	}
	tMask, err := newTensor(&set, orts.NewShape(1, int64(nw)), mask)
	if err != nil {
		return nil, err
	}
	tEnt, err := newTensor(&set, orts.NewShape(1, int64(len(labels)+1)), d.cfg.Schema.Entities.SchemaMarkerPositions)
	if err != nil {
		return nil, err
	}
	tClsIdx, err := newTensor(&set, orts.NewShape(1, int64(len(cls.Labels)+1)), cls.SchemaMarkerPositions)
	if err != nil {
		return nil, err
	}
	tProbs, err := newTensor(&set, orts.NewShape(1, int64(len(labels)), int64(nw), int64(mw)), spanProbs)
	if err != nil {
		return nil, err
	}
	tCount, err := newTensor(&set, orts.NewShape(1, int64(cm)), countLogits)
	if err != nil {
		return nil, err
	}
	tCls, err := newTensor(&set, orts.NewShape(1, int64(len(cls.Labels))), clsLogits)
	if err != nil {
		return nil, err
	}

	if err := s.inner.Run(
		[]orts.Value{tIn, tAttn, tWord, tMask, tEnt, tClsIdx},
		[]orts.Value{tProbs, tCount, tCls},
	); err != nil {
		return nil, err
	}
	return &outputs{
		spanProbs:   append([]float32(nil), tProbs.GetData()...),
		countLogits: append([]float32(nil), tCount.GetData()...),
		clsLogits:   append([]float32(nil), tCls.GetData()...),
	}, nil
}

func (d *Detector) findings(p *prepared, out *outputs) []guard.Finding {
	res := decode(&d.cfg, p, out.spanProbs, out.countLogits, out.clsLogits)
	var fs []guard.Finding
	for _, label := range d.cfg.Schema.Entities.Labels {
		for _, e := range res.entities[label] {
			end := e.end
			if end > p.nRunes {
				end = p.nRunes
			}
			if e.start >= end {
				continue
			}
			fs = append(fs, guard.Finding{
				Category: guard.CatPII,
				Label:    label,
				Start:    e.start,
				End:      end,
				Score:    e.score,
				Layer:    "L2",
			})
		}
	}
	if res.safety == unsafeLabel {
		fs = append(fs, guard.Finding{
			Category: guard.CatUnsafe,
			Label:    res.safety,
			Start:    0,
			End:      p.nRunes,
			Score:    res.score,
			Layer:    "L2",
		})
	}
	return fs
}

type classification struct {
	Task                  string   `json:"task"`
	Labels                []string `json:"labels"`
	MultiLabel            bool     `json:"multi_label"`
	Activation            string   `json:"activation"`
	SchemaMarkerPositions []int64  `json:"schema_marker_positions"`
}

type modelConfig struct {
	PrefixIDs []int64 `json:"prefix_ids"`
	Prompt    struct {
		PrefixIDs []int64 `json:"prefix_ids"`
	} `json:"prompt"`
	MaxWidth int `json:"max_width"`
	CountMax int `json:"count_max"`
	Schema   struct {
		Entities struct {
			Labels                []string           `json:"labels"`
			Thresholds            map[string]float64 `json:"thresholds"`
			SchemaMarkerPositions []int64            `json:"schema_marker_positions"`
		} `json:"entities"`
		Classifications []classification `json:"classifications"`
	} `json:"schema"`
	Decode struct {
		DefaultSpanThreshold float64 `json:"default_span_threshold"`
	} `json:"decode"`
}

func (c *modelConfig) classification() *classification {
	if len(c.Schema.Classifications) != 1 {
		return nil
	}
	return &c.Schema.Classifications[0]
}

func (c *modelConfig) validate() error {
	cls := c.classification()
	switch {
	case len(c.PrefixIDs) == 0:
		return errors.New("config: prompt prefix_ids is empty")
	case c.MaxWidth < 1:
		return errors.New("config: max_width is not set")
	case c.CountMax < 1:
		return errors.New("config: count_max is not set")
	case c.Decode.DefaultSpanThreshold <= 0:
		return errors.New("config: default_span_threshold is not set")
	case len(c.Schema.Entities.Labels) == 0:
		return errors.New("config: entity labels are empty")
	case len(c.Schema.Entities.SchemaMarkerPositions) != len(c.Schema.Entities.Labels)+1:
		return errors.New("config: entity schema_marker_positions do not match labels")
	case cls == nil:
		return errors.New("config: exactly one classification group is required")
	case len(cls.Labels) < 2:
		return errors.New("config: classification labels are empty")
	case len(cls.SchemaMarkerPositions) != len(cls.Labels)+1:
		return errors.New("config: class schema_marker_positions do not match labels")
	case cls.MultiLabel:
		return errors.New("config: multi_label classification is not supported")
	case cls.Activation != "" && cls.Activation != "softmax":
		return errors.New("config: only softmax classification is supported")
	}
	return nil
}

func loadModelConfig(path string) (modelConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return modelConfig{}, err
	}
	var mc modelConfig
	if err := json.Unmarshal(raw, &mc); err != nil {
		return modelConfig{}, err
	}
	if len(mc.Prompt.PrefixIDs) > 0 {
		mc.PrefixIDs = mc.Prompt.PrefixIDs
	}
	if err := mc.validate(); err != nil {
		return modelConfig{}, err
	}
	return mc, nil
}
