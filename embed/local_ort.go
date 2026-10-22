//go:build ort

package embed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/daulet/tokenizers"
	orts "github.com/yalue/onnxruntime_go"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/ort"
)

// DefaultSessions is the number of ONNX sessions created when LocalConfig.Sessions
// is not positive.
const DefaultSessions = 2

// DefaultBatchSize is the maximum number of texts encoded in one ONNX batch.
const DefaultBatchSize = 32

const (
	inputIDsName            = "input_ids"
	inputAttentionMaskName  = "attention_mask"
	inputTokenTypeIDsName   = "token_type_ids"
	outputSentenceEmbedding = "sentence_embedding"
	outputLastHiddenState   = "last_hidden_state"
	outputTokenEmbeddings   = "token_embeddings"
)

type localEmbedder struct {
	model   string
	preset  Preset
	dim     int
	tk      *tokenizers.Tokenizer
	pool    *ort.Pool
	direct  bool
	useType bool
}

// NewLocal loads an ONNX encoder and tokenizer from ModelDir(cfg.Dir, cfg.Model).
// It requires model.onnx, tokenizer.json, and config.json, resolves the preset from
// kit_embedder.json or the model name, and creates cfg.Sessions pooled sessions.
func NewLocal(cfg LocalConfig) (Embedder, error) {
	cfg = cfg.withDefaults()
	if cfg.Model == "" {
		return nil, invalidConfig(errors.New("model is required"))
	}

	dir := ModelDir(cfg.Dir, cfg.Model)
	modelPath := filepath.Join(dir, "model.onnx")
	tokenizerPath := filepath.Join(dir, "tokenizer.json")
	configPath := filepath.Join(dir, "config.json")
	for _, path := range []string{modelPath, tokenizerPath, configPath} {
		if _, err := os.Stat(path); err != nil {
			return nil, invalidConfig(fmt.Errorf("model file %s: %w", path, err))
		}
	}

	preset, err := resolvePresetFromDir(dir, cfg.Model)
	if err != nil {
		return nil, err
	}

	if err := initEnvironment(); err != nil {
		return nil, runtimeError(err)
	}

	opts, err := orts.NewSessionOptions()
	if err != nil {
		return nil, runtimeError(fmt.Errorf("session options: %w", err))
	}
	defer func() { _ = opts.Destroy() }()
	if err := opts.SetIntraOpNumThreads(ort.Threads(cfg.Sessions)); err != nil {
		return nil, runtimeError(fmt.Errorf("set intra-op threads: %w", err))
	}

	inputs, outputs, direct, useType, dim, err := inspectGraph(modelPath, opts)
	if err != nil {
		return nil, err
	}

	tk, err := tokenizers.FromFile(tokenizerPath)
	if err != nil {
		return nil, invalidConfig(fmt.Errorf("tokenizer %s: %w", tokenizerPath, err))
	}

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
			return nil, runtimeError(fmt.Errorf("create session %d: %w", i, serr))
		}
		sessions = append(sessions, &session{inner: s})
	}
	pool, err := ort.NewPool(sessions, ort.DefaultWait)
	if err != nil {
		abort()
		return nil, runtimeError(fmt.Errorf("create pool: %w", err))
	}

	return &localEmbedder{
		model:   cfg.Model,
		preset:  preset,
		dim:     dim,
		tk:      tk,
		pool:    pool,
		direct:  direct,
		useType: useType,
	}, nil
}

func (e *localEmbedder) Close() error { return e.pool.Close() }

func (e *localEmbedder) Health() core.HealthStatus {
	if e.pool == nil {
		return core.HealthStatus{Detail: "embed: session pool is not ready"}
	}
	st := e.pool.Stats()
	return core.HealthStatus{OK: true, InUse: st.Size - st.Available, Capacity: st.Size}
}

func (e *localEmbedder) Model() string { return e.model }

func (e *localEmbedder) Dim() int { return e.dim }

func (e *localEmbedder) Query(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.QueryPrefix)
}

func (e *localEmbedder) Passages(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embed(ctx, texts, e.preset.PassagePrefix)
}

func (e *localEmbedder) embed(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = prefix + t
	}
	out := make([][]float32, len(in))
	for start := 0; start < len(in); start += DefaultBatchSize {
		end := start + DefaultBatchSize
		if end > len(in) {
			end = len(in)
		}
		batch, err := e.runBatch(ctx, in[start:end])
		if err != nil {
			return nil, err
		}
		copy(out[start:end], batch)
	}
	return out, nil
}

func (e *localEmbedder) runBatch(ctx context.Context, texts []string) ([][]float32, error) {
	n := len(texts)
	ids := make([][]int64, n)
	lengths := make([]int, n)
	for i, text := range texts {
		enc, err := e.tk.EncodeWithOptionsErr(text, true, tokenizers.WithReturnOffsets())
		if err != nil {
			return nil, runtimeError(fmt.Errorf("tokenize: %w", err))
		}
		tokens := len(enc.IDs)
		if tokens > e.preset.MaxLen {
			tokens = e.preset.MaxLen
		}
		row := make([]int64, tokens)
		for j := 0; j < tokens; j++ {
			row[j] = int64(enc.IDs[j])
		}
		ids[i] = row
		lengths[i] = tokens
	}

	perm := orderByLength(lengths)
	maxLen := 0
	for _, l := range lengths {
		if l > maxLen {
			maxLen = l
		}
	}
	if maxLen == 0 {
		maxLen = 1
	}

	batch := len(texts)
	inputIDs := make([]int64, batch*maxLen)
	attention := make([]int64, batch*maxLen)
	for k, src := range perm {
		base := k * maxLen
		copy(inputIDs[base:base+lengths[src]], ids[src])
		for j := 0; j < lengths[src]; j++ {
			attention[base+j] = 1
		}
	}

	shape := orts.NewShape(int64(batch), int64(maxLen))
	tIDs, err := orts.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, runtimeError(fmt.Errorf("input_ids tensor: %w", err))
	}
	defer func() { _ = tIDs.Destroy() }()
	tMask, err := orts.NewTensor(shape, attention)
	if err != nil {
		return nil, runtimeError(fmt.Errorf("attention_mask tensor: %w", err))
	}
	defer func() { _ = tMask.Destroy() }()

	inputs := []orts.Value{tIDs, tMask}
	if e.useType {
		tType, terr := orts.NewTensor(shape, make([]int64, batch*maxLen))
		if terr != nil {
			return nil, runtimeError(fmt.Errorf("token_type_ids tensor: %w", terr))
		}
		defer func() { _ = tType.Destroy() }()
		inputs = append(inputs, tType)
	}

	sess, err := e.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer e.pool.Release(sess)

	outputs := []orts.Value{nil}
	if err := sess.inner.Run(inputs, outputs); err != nil {
		return nil, runtimeError(fmt.Errorf("inference: %w", err))
	}
	out, ok := outputs[0].(*orts.Tensor[float32])
	if !ok {
		if outputs[0] != nil {
			_ = outputs[0].Destroy()
		}
		return nil, runtimeError(errors.New("unexpected output tensor type"))
	}
	defer func() { _ = out.Destroy() }()
	data := out.GetData()

	vectors := make([][]float32, batch)
	for k := 0; k < batch; k++ {
		var vec []float32
		if e.direct {
			if (k+1)*e.dim > len(data) {
				return nil, runtimeError(fmt.Errorf("sentence_embedding too short: %d values for %d vectors of %d", len(data), batch, e.dim))
			}
			vec = append([]float32(nil), data[k*e.dim:(k+1)*e.dim]...)
		} else {
			end := k*maxLen*e.dim + maxLen*e.dim
			if end > len(data) {
				return nil, runtimeError(fmt.Errorf("last_hidden_state too short: %d values for %d tokens", len(data), batch*maxLen))
			}
			hidden := data[k*maxLen*e.dim : end]
			mask := attention[k*maxLen : (k+1)*maxLen]
			if e.preset.Pooling == PoolingCLS {
				vec = clsPool(hidden, maxLen, e.dim)
			} else {
				vec = meanPool(hidden, mask, maxLen, e.dim)
			}
		}
		if e.preset.Normalize {
			NormalizeL2(vec)
		}
		vectors[k] = vec
	}
	return restoreOrder(vectors, perm), nil
}

func (e *localEmbedder) acquire(ctx context.Context) (*session, error) {
	s, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, runtimeError(err)
	}
	sess, ok := s.(*session)
	if !ok {
		e.pool.Release(s)
		return nil, runtimeError(errors.New("unexpected session type"))
	}
	return sess, nil
}

type session struct {
	inner *orts.DynamicAdvancedSession
}

func (s *session) Close() error { return s.inner.Destroy() }

func inspectGraph(modelPath string, opts *orts.SessionOptions) (inputs, outputs []string, direct, useType bool, dim int, err error) {
	infos, outInfos, gerr := orts.GetInputOutputInfoWithOptions(modelPath, opts)
	if gerr != nil {
		return nil, nil, false, false, 0, runtimeError(fmt.Errorf("inspect graph: %w", gerr))
	}

	byInput := make(map[string]orts.InputOutputInfo, len(infos))
	for _, in := range infos {
		byInput[in.Name] = in
	}
	for _, name := range []string{inputIDsName, inputAttentionMaskName} {
		in, ok := byInput[name]
		if !ok {
			return nil, nil, false, false, 0, invalidConfig(fmt.Errorf("graph is missing required input %q", name))
		}
		if in.DataType != orts.TensorElementDataTypeInt64 {
			return nil, nil, false, false, 0, invalidConfig(fmt.Errorf("input %q has element type %s, want int64", name, in.DataType))
		}
	}
	inputs = []string{inputIDsName, inputAttentionMaskName}
	if _, ok := byInput[inputTokenTypeIDsName]; ok {
		useType = true
		inputs = append(inputs, inputTokenTypeIDsName)
	}

	byOutput := make(map[string]orts.InputOutputInfo, len(outInfos))
	for _, out := range outInfos {
		byOutput[out.Name] = out
	}
	switch {
	case hasOutput(byOutput, outputSentenceEmbedding):
		outputs = []string{outputSentenceEmbedding}
		direct = true
		dim = lastDim(byOutput[outputSentenceEmbedding].Dimensions)
	case hasOutput(byOutput, outputLastHiddenState):
		outputs = []string{outputLastHiddenState}
		dim = lastDim(byOutput[outputLastHiddenState].Dimensions)
	case hasOutput(byOutput, outputTokenEmbeddings):
		outputs = []string{outputTokenEmbeddings}
		dim = lastDim(byOutput[outputTokenEmbeddings].Dimensions)
	default:
		return nil, nil, false, false, 0, invalidConfig(fmt.Errorf(
			"graph has no supported embedding output (want %q, %q or %q)",
			outputSentenceEmbedding, outputLastHiddenState, outputTokenEmbeddings))
	}
	if dim <= 0 {
		return nil, nil, false, false, 0, invalidConfig(fmt.Errorf("cannot determine embedding dim from output %q", outputs[0]))
	}
	return inputs, outputs, direct, useType, dim, nil
}

func hasOutput(m map[string]orts.InputOutputInfo, name string) bool {
	_, ok := m[name]
	return ok
}

func lastDim(shape orts.Shape) int {
	if len(shape) == 0 {
		return 0
	}
	d := shape[len(shape)-1]
	if d <= 0 {
		return 0
	}
	return int(d)
}

func (c LocalConfig) withDefaults() LocalConfig {
	if c.Dir == "" {
		c.Dir = os.Getenv("KIT_MODELS_DIR")
	}
	if c.Dir == "" {
		c.Dir = "./models"
	}
	if c.Sessions < 1 {
		c.Sessions = DefaultSessions
	}
	return c
}

// ortEnv guards the process-global ONNX Runtime environment.
var ortEnv onceInit

// initEnvironment initializes the ONNX Runtime environment. The environment is
// process-global, so concurrent NewLocal calls must not race to initialize it:
// the first caller initializes it and every caller sees the same result.
func initEnvironment() error {
	return ortEnv.do(initializeEnvironment)
}

func initializeEnvironment() error {
	lib := os.Getenv("KIT_ORT_LIB")
	if lib == "" {
		lib = defaultLibPath()
	}
	orts.SetSharedLibraryPath(lib)
	if orts.IsInitialized() {
		return nil
	}
	if err := orts.InitializeEnvironment(); err != nil {
		return fmt.Errorf("ort init: %w", err)
	}
	return nil
}

func defaultLibPath() string {
	if runtime.GOOS == "darwin" {
		return ".ort/lib/libonnxruntime.dylib"
	}
	return ".ort/lib/libonnxruntime.so"
}

func invalidConfig(err error) error {
	return fmt.Errorf("embed: local: %w: %w", err, core.ErrInvalidConfig)
}

func runtimeError(err error) error {
	return fmt.Errorf("embed: local: %w: %w", err, core.ErrProvider)
}

// VerifyDir checks the model files and preset, then loads and closes the encoder
// to verify that the ONNX runtime can initialize it.
func VerifyDir(dir, model string) error {
	if err := verifyDir(dir, model); err != nil {
		return err
	}
	e, err := NewLocal(LocalConfig{Dir: dir, Model: model})
	if err != nil {
		return err
	}
	if c, ok := e.(Closer); ok {
		return c.Close()
	}
	return nil
}

func meanPool(hidden []float32, mask []int64, tokens, dim int) []float32 {
	out := make([]float32, dim)
	count := 0
	for t := 0; t < tokens; t++ {
		if mask[t] == 0 {
			continue
		}
		base := t * dim
		if base+dim > len(hidden) {
			break
		}
		count++
		for d := 0; d < dim; d++ {
			out[d] += hidden[base+d]
		}
	}
	if count == 0 {
		return out
	}
	inv := float32(1) / float32(count)
	for d := range out {
		out[d] *= inv
	}
	return out
}

func clsPool(hidden []float32, tokens, dim int) []float32 {
	out := make([]float32, dim)
	if tokens <= 0 || len(hidden) < dim {
		return out
	}
	copy(out, hidden[:dim])
	return out
}

func orderByLength(lengths []int) []int {
	perm := make([]int, len(lengths))
	for i := range perm {
		perm[i] = i
	}
	sort.SliceStable(perm, func(a, b int) bool {
		return lengths[perm[a]] < lengths[perm[b]]
	})
	return perm
}

func restoreOrder(vectors [][]float32, perm []int) [][]float32 {
	out := make([][]float32, len(vectors))
	for k, idx := range perm {
		out[idx] = vectors[k]
	}
	return out
}
