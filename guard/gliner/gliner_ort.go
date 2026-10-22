//go:build ort

package gliner

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/daulet/tokenizers"
	orts "github.com/yalue/onnxruntime_go"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/ort"
)

// DefaultModel is the model name used when Config.Model is empty.
const DefaultModel = "hivetrace__gliner-guard-omni"

// DefaultSessions is the default number of ORT sessions in the pool.
const DefaultSessions = 2

// DefaultMaxTokens is the default cap on tokens in the L2 tensor input.
const DefaultMaxTokens = 512

func loadModelConfig(path string) (modelConfig, error) {
	var mc modelConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return mc, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &mc); err != nil {
		return mc, fmt.Errorf("parse config %s: %w", path, err)
	}
	return mc, nil
}

// Config configures the L2 detector: model directory and name, session pool
// size and wait, and the token cap; zero values get defaults.
type Config struct {
	Dir       string
	Model     string
	Sessions  int
	Wait      time.Duration
	MaxTokens int
}

// Detector is the ONNX-backed L2 GLiNER detector; it implements
// guard.NaturalText and reports findings with layer "L2".
type Detector struct {
	cfg       modelConfig
	maxTokens int
	wordRe    *regexp.Regexp
	tk        *tokenizers.Tokenizer
	pool      *ort.Pool
	cache     *lruCache
}

// Health reports pool capacity and in-use sessions, or that the pool is not ready.
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

// New loads the model config, tokenizer, and ONNX sessions, applies defaults,
// and clamps MaxTokens to the model's max_sequence_length.
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
	return &Detector{cfg: mc, maxTokens: maxTokens, wordRe: wordRe, tk: tk, pool: pool, cache: newLRUCache(1024)}, nil
}

// VerifyDir checks the model files and config, then constructs and closes a
// detector to verify the runtime can load the model.
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

// Name returns "L2".
func (*Detector) Name() string { return "L2" }

// UsesNaturalText reports true: L2 reads the original text, not the canonical form.
func (*Detector) UsesNaturalText() bool { return true }

// Detect tokenises text into words, runs one inference, and converts entity
// spans and classification labels into findings; inference errors are wrapped
// as ErrGuardUnavailable. Results are cached by text hash to avoid re-running
// on repeated messages (e.g., history replay).
func (d *Detector) Detect(ctx context.Context, text string) ([]guard.Finding, error) {
	if text == "" {
		return nil, nil
	}
	if fs, ok := d.cache.Get(text); ok {
		return fs, nil
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
	fs := d.findings(p, out)
	d.cache.Add(text, fs)
	return fs, nil
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

func (d *Detector) splitWords(text string) []word {
	matches := d.wordRe.FindAllStringIndex(text, -1)
	out := make([]word, 0, len(matches))
	for _, m := range matches {
		raw := text[m[0]:m[1]]
		start := utf8.RuneCountInString(text[:m[0]])
		out = append(out, word{
			token: strings.ToLower(raw),
			start: start,
			end:   start + utf8.RuneCountInString(raw),
		})
	}
	return out
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
	// nRunes must match the normalized text (what runes/words are based on),
	// not the original text. Otherwise spans are off by 1 when normalizeText adds ".".
	return &prepared{
		runes:     runes,
		words:     words,
		nRunes:    len(runes),
		inputIDs:  seq,
		wordFirst: first,
		attn:      attn,
	}
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

// lruCache is a simple thread-safe LRU cache for detector results.
// It avoids re-running inference on identical text (e.g., history replay).
type lruCache struct {
	mu       sync.Mutex
	capacity int
	list     *list.List
	items    map[string]*list.Element
}

type lruEntry struct {
	key   string
	value []guard.Finding
}

func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		list:     list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

func (c *lruCache) Get(key string) ([]guard.Finding, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.list.MoveToFront(el)
		return el.Value.(*lruEntry).value, true
	}
	return nil, false
}

func (c *lruCache) Add(key string, value []guard.Finding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.list.MoveToFront(el)
		el.Value.(*lruEntry).value = value
		return
	}
	el := c.list.PushFront(&lruEntry{key: key, value: value})
	c.items[key] = el
	if c.list.Len() > c.capacity {
		oldest := c.list.Back()
		if oldest != nil {
			c.list.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry).key)
		}
	}
}
