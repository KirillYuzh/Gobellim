//go:build ort

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

const DefaultModel = "hivetrace__gliner-guard-omni"

const DefaultSessions = 2

const DefaultMaxTokens = 512

type Config struct {
	Dir       string
	Model     string
	Sessions  int
	Wait      time.Duration
	MaxTokens int
}

type Detector struct {
	cfg       modelConfig
	maxTokens int
	wordRe    *regexp.Regexp
	tk        *tokenizers.Tokenizer
	pool      *ort.Pool
}

func (d *Detector) Health() core.HealthStatus {
	if d.pool == nil {
		return core.HealthStatus{Detail: "gliner: session pool is not ready"}
	}
	st := d.pool.Stats()
	return core.HealthStatus{OK: true, InUse: st.Size - st.Available, Capacity: st.Size}
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
	if c.MaxTokens == 0 {
		c.MaxTokens = DefaultMaxTokens
	}
	return c
}

func defaultLibPath() string {
	if runtime.GOOS == "darwin" {
		return ".ort/lib/libonnxruntime.dylib"
	}
	return ".ort/lib/libonnxruntime.so"
}

func New(cfg Config) (*Detector, error) {
	cfg = cfg.withDefaults()
	dir := filepath.Join(cfg.Dir, cfg.Model)

	mc, err := loadModelConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, unavailable(err)
	}
	if cfg.MaxTokens < 1 {
		return nil, invalidConfig(fmt.Errorf("config: max_tokens %d must be > 0", cfg.MaxTokens))
	}
	maxTokens := cfg.MaxTokens
	if mc.MaxSequenceLength > 0 && maxTokens > mc.MaxSequenceLength {
		maxTokens = mc.MaxSequenceLength
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
	outputs := append([]string{"span_probs", "count_logits"}, mc.classOutputs()...)
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
	return &Detector{cfg: mc, maxTokens: maxTokens, wordRe: wordRe, tk: tk, pool: pool}, nil
}

func VerifyDir(dir, model string) error {
	dir = resolveModelsDir(dir)
	if err := verifyFiles(dir, model); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, model, "config.json")
	if _, err := loadModelConfig(cfgPath); err != nil {
		return invalidConfig(fmt.Errorf("config %s: %w", cfgPath, err))
	}
	d, err := New(Config{Dir: dir, Model: model})
	if err != nil {
		return err
	}
	return d.pool.Close()
}

func (*Detector) Name() string { return "L2" }

func (*Detector) UsesNaturalText() bool { return true }

func (d *Detector) Detect(ctx context.Context, text string) ([]guard.Finding, error) {
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

	limit := d.maxTokens
	if limit < 1 {
		limit = DefaultMaxTokens
	}

	seq := make([]int64, 0, len(d.cfg.PrefixIDs)+limit)
	seq = append(seq, d.cfg.PrefixIDs...)
	first := make([]int64, 0, len(words))
	kept := 0
	for _, w := range words {
		ids, _ := d.tk.Encode(w.token, false)
		if !keepWord(len(seq), len(ids), limit, kept > 0) {
			break
		}
		first = append(first, int64(len(seq)))
		for _, id := range ids {
			seq = append(seq, int64(id))
		}
		kept++
	}
	words = words[:kept]
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
	clsLogits   [][]float32
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
	groups := d.cfg.Schema.Classifications
	mw, cm := d.cfg.MaxWidth, d.cfg.CountMax

	mask := make([]bool, nw)
	for i := range mask {
		mask[i] = true
	}
	spanProbs := make([]float32, len(labels)*nw*mw)
	countLogits := make([]float32, cm)
	clsIdx := d.cfg.classSchemaIndices()

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
	tClsIdx, err := newTensor(&set, orts.NewShape(1, int64(len(clsIdx))), clsIdx)
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

	clsTensors := make([]*orts.Tensor[float32], len(groups))
	outs := make([]orts.Value, 0, 2+len(groups))
	outs = append(outs, tProbs, tCount)
	for gi := range groups {
		t, terr := newTensor(&set, orts.NewShape(1, int64(len(groups[gi].Labels))), make([]float32, len(groups[gi].Labels)))
		if terr != nil {
			return nil, terr
		}
		clsTensors[gi] = t
		outs = append(outs, t)
	}

	if err := s.inner.Run(
		[]orts.Value{tIn, tAttn, tWord, tMask, tEnt, tClsIdx},
		outs,
	); err != nil {
		return nil, err
	}
	out := &outputs{
		spanProbs:   append([]float32(nil), tProbs.GetData()...),
		countLogits: append([]float32(nil), tCount.GetData()...),
		clsLogits:   make([][]float32, len(groups)),
	}
	for gi, t := range clsTensors {
		out.clsLogits[gi] = append([]float32(nil), t.GetData()...)
	}
	return out, nil
}

func (d *Detector) findings(p *prepared, out *outputs) []guard.Finding {
	res := decode(&d.cfg, p, out.spanProbs, out.countLogits, out.clsLogits)
	var fs []guard.Finding
	for _, label := range d.cfg.Schema.Entities.Labels {
		cat, ok := d.categoryFor(label, guard.CatPII)
		if !ok {
			continue
		}
		for _, e := range res.entities[label] {
			end := e.end
			if end > p.nRunes {
				end = p.nRunes
			}
			if e.start >= end {
				continue
			}
			fs = append(fs, guard.Finding{
				Category: cat,
				Label:    label,
				Start:    e.start,
				End:      end,
				Score:    e.score,
				Layer:    "L2",
			})
		}
	}
	for _, c := range res.classifications {
		cat, ok := d.categoryForClass(c.group, c.label)
		if !ok {
			continue
		}
		fs = append(fs, guard.Finding{
			Category: cat,
			Label:    c.label,
			Start:    0,
			End:      p.nRunes,
			Score:    c.score,
			Layer:    "L2",
		})
	}
	return fs
}

func (d *Detector) categoryFor(label string, def guard.Category) (guard.Category, bool) {
	return categoryForLabel(d.cfg.CategoryMap, label, def)
}

func (d *Detector) categoryForClass(group int, label string) (guard.Category, bool) {
	task := ""
	if group >= 0 && group < len(d.cfg.Schema.Classifications) {
		task = d.cfg.Schema.Classifications[group].Task
	}
	return categoryForClass(d.cfg.CategoryMap, task, label)
}

type classification struct {
	Task                  string             `json:"task"`
	Labels                []string           `json:"labels"`
	MultiLabel            bool               `json:"multi_label"`
	Activation            string             `json:"activation"`
	Output                string             `json:"output"`
	ClsThreshold          float64            `json:"cls_threshold"`
	Threshold             float64            `json:"threshold"`
	Thresholds            map[string]float64 `json:"thresholds"`
	SchemaMarkerPositions []int64            `json:"schema_marker_positions"`
}

func (c *classification) activation() string {
	if c.Activation == "" {
		return "softmax"
	}
	return c.Activation
}

func (c *classification) outputName() string {
	if c.Output == "" {
		return "cls_logits"
	}
	return c.Output
}

func (c *classification) thresholdFor(label string) float64 {
	return thresholdFor(c.ClsThreshold, c.Threshold, c.Thresholds, label)
}

type modelConfig struct {
	PrefixIDs   []int64           `json:"prefix_ids"`
	CategoryMap map[string]string `json:"category_map"`
	Prompt      struct {
		PrefixIDs []int64 `json:"prefix_ids"`
	} `json:"prompt"`
	MaxSequenceLength int `json:"max_sequence_length"`
	MaxWidth          int `json:"max_width"`
	CountMax          int `json:"count_max"`
	Schema            struct {
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

func (c *modelConfig) classOutputs() []string {
	out := make([]string, 0, len(c.Schema.Classifications))
	for i := range c.Schema.Classifications {
		out = append(out, c.Schema.Classifications[i].outputName())
	}
	return out
}

func (c *modelConfig) classSchemaIndices() []int64 {
	groups := c.Schema.Classifications
	if len(groups) == 0 || len(groups[0].SchemaMarkerPositions) == 0 {
		return nil
	}
	out := []int64{groups[0].SchemaMarkerPositions[0]}
	for i := range groups {
		if len(groups[i].SchemaMarkerPositions) == 0 {
			continue
		}
		out = append(out, groups[i].SchemaMarkerPositions[1:]...)
	}
	return out
}

func (c *modelConfig) validate() error {
	switch {
	case len(c.PrefixIDs) == 0:
		return errors.New("config: prompt prefix_ids is empty")
	case c.MaxSequenceLength < 0:
		return errors.New("config: max_sequence_length is negative")
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
	case len(c.Schema.Classifications) == 0:
		return errors.New("config: at least one classification group is required")
	}
	seen := make(map[string]int, len(c.Schema.Classifications))
	for i := range c.Schema.Classifications {
		cls := &c.Schema.Classifications[i]
		switch {
		case len(cls.Labels) == 0:
			return fmt.Errorf("config: classification %d labels are empty", i)
		case len(cls.SchemaMarkerPositions) != len(cls.Labels)+1:
			return fmt.Errorf("config: classification %d schema_marker_positions do not match labels", i)
		case cls.Activation != "" && cls.Activation != "softmax" && cls.Activation != "sigmoid":
			return fmt.Errorf("config: classification %d activation %q is not softmax or sigmoid", i, cls.Activation)
		case cls.MultiLabel && cls.activation() != "sigmoid":
			return fmt.Errorf("config: classification %d sets multi_label without sigmoid activation", i)
		}
		out := cls.outputName()
		if prev, ok := seen[out]; ok {
			return fmt.Errorf("config: classification %d output %q duplicates classification %d", i, out, prev)
		}
		seen[out] = i
	}
	for label, cat := range c.CategoryMap {
		if categoryIsSafe(cat) {
			continue
		}
		if _, ok := parseCategory(cat); !ok {
			return fmt.Errorf("config: category_map[%q] has unknown category %q", label, cat)
		}
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
