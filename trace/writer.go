package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/store"
)

const (
	DefaultQueueSize     = 4096
	DefaultBatchSize     = 500
	DefaultFlushInterval = 2 * time.Second
	DefaultMaxBatchBytes = 5 << 20
	DefaultMaxRetries    = 3
	DefaultRetryBackoff  = 200 * time.Millisecond
)

const versionConflictType = "version_conflict_engine_exception"

type Options struct {
	ES      store.ES
	Sinks   []Sink
	Text    TextPolicy
	Sampler Sampler

	HideInput  bool
	HideOutput bool

	MaxStringLength int
	MaxDepth        int
	MaxArrayLength  int
	MaxObjectKeys   int

	Processors []func(*Observation)

	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	MaxBatchBytes int
	MaxRetries    int
	RetryBackoff  time.Duration
	Logger        *slog.Logger
}

type item struct {
	stream string
	id     string
	raw    []byte
	size   int
}

type Writer struct {
	opts  Options
	cfg   exportConfig
	sinks []Sink

	ch   chan item
	done chan struct{}

	closeOnce sync.Once
	closeCtx  context.Context
	mu        sync.RWMutex

	wg      sync.WaitGroup
	dropped atomic.Int64

	errMu   sync.Mutex
	lastErr error
}

func NewWriter(opts Options) (*Writer, error) {
	if opts.QueueSize <= 0 {
		opts.QueueSize = DefaultQueueSize
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = DefaultFlushInterval
	}
	if opts.MaxBatchBytes <= 0 {
		opts.MaxBatchBytes = DefaultMaxBatchBytes
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = DefaultMaxRetries
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = DefaultRetryBackoff
	}
	if opts.MaxStringLength <= 0 {
		opts.MaxStringLength = DefaultMaxStringLength
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxArrayLength <= 0 {
		opts.MaxArrayLength = DefaultMaxArrayLength
	}
	if opts.MaxObjectKeys <= 0 {
		opts.MaxObjectKeys = DefaultMaxObjectKeys
	}
	if opts.Sampler == nil {
		opts.Sampler = SamplerAlways
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	w := &Writer{
		opts: opts,
		cfg: exportConfig{
			maxStringLength: opts.MaxStringLength,
			maxDepth:        opts.MaxDepth,
			maxArrayLength:  opts.MaxArrayLength,
			maxObjectKeys:   opts.MaxObjectKeys,
			hideInput:       opts.HideInput,
			hideOutput:      opts.HideOutput,
		},
		ch:   make(chan item, opts.QueueSize),
		done: make(chan struct{}),
	}
	for _, s := range opts.Sinks {
		if s != nil && s != Sink(w) {
			w.sinks = append(w.sinks, s)
		}
	}

	// Without a store there is nothing to flush to: the writer keeps forwarding to
	// the configured Sinks, but no ES goroutine is started.
	if w.opts.ES != nil {
		w.wg.Add(1)
		go w.run()
	}
	return w, nil
}

func (w *Writer) sample(traceID string) bool { return w.opts.Sampler(traceID) }

func (w *Writer) policyOf(p TextPolicy) TextPolicy {
	if p != TextMasked {
		return p
	}
	return w.opts.Text
}

func (w *Writer) Enqueue(t Trace) {
	if !w.sample(t.ID) {
		return
	}
	t.TextPolicy = w.policyOf(t.TextPolicy)
	it, ok := makeItem(store.DataStreamTraces, t.ID, t.document(w.cfg))
	if !ok {
		w.countDropped("encode")
		return
	}
	w.enqueue(it)
	w.forward(func(s Sink) { s.Enqueue(t) })
}

func (w *Writer) EnqueueObservation(o Observation) {
	if !w.sample(o.TraceID) {
		return
	}
	for _, p := range w.opts.Processors {
		if p != nil {
			p(&o)
		}
	}
	if o.Name == "" {
		return
	}
	o.TextPolicy = w.policyOf(o.TextPolicy)
	it, ok := makeItem(store.DataStreamObservations, o.ID, o.document(w.cfg))
	if !ok {
		w.countDropped("encode")
		return
	}
	w.enqueue(it)
	w.forward(func(s Sink) { s.EnqueueObservation(o) })
}

func (w *Writer) EnqueueScore(s Score) {
	if !w.sample(s.TraceID) {
		return
	}
	ts := s.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	doc := map[string]any{
		"@timestamp": ts.UTC().Format(time.RFC3339Nano),
		"trace_id":   s.TraceID,
		"name":       s.Name,
		"score":      s.Value,
		"comment":    w.cfg.clip(s.Comment),
		"kind":       "SCORE",
	}
	it, ok := makeItem(store.DataStreamTraces, "", doc)
	if !ok {
		w.countDropped("encode")
		return
	}
	w.enqueue(it)
	w.forward(func(sink Sink) { sink.EnqueueScore(s) })
}

func (w *Writer) forward(fn func(Sink)) {
	for _, s := range w.sinks {
		fn(s)
	}
}

func makeItem(stream, id string, doc map[string]any) (item, bool) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return item{}, false
	}
	return item{stream: stream, id: id, raw: raw, size: estimateSize(raw, stream, id)}, true
}

func estimateSize(raw []byte, stream, id string) int {
	return len(raw) + len(stream) + len(id) + 32
}

func (w *Writer) enqueue(it item) {
	if w.opts.ES == nil {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	select {
	case <-w.done:
		return
	default:
	}
	select {
	case w.ch <- it:
	case <-w.done:
	default:
		w.countDropped("queue full")
	}
}

func (w *Writer) countDropped(reason string) {
	if n := w.dropped.Add(1); n == 1 || n%100 == 0 {
		w.opts.Logger.Warn("trace: dropping document", "reason", reason, "dropped_total", n)
	}
}

func (w *Writer) Dropped() int64 { return w.dropped.Load() }

func (w *Writer) QueueDepth() int { return len(w.ch) }

func (w *Writer) QueueCapacity() int { return cap(w.ch) }

func (w *Writer) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closeCtx = ctx
		close(w.done)
		w.mu.Unlock()
	})
	w.wg.Wait()

	w.errMu.Lock()
	defer w.errMu.Unlock()
	err := w.lastErr
	w.lastErr = nil
	return err
}

func (w *Writer) recordErr(err error) {
	if err == nil {
		return
	}
	w.errMu.Lock()
	w.lastErr = errors.Join(w.lastErr, err)
	w.errMu.Unlock()
}

func (w *Writer) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]item, 0, w.opts.BatchSize)
	bytesInBatch := 0

	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := w.flush(ctx, batch); err != nil {
			w.recordErr(err)
			w.opts.Logger.Warn("trace: flush failed", "err", err, "documents", len(batch))
		}
		batch = batch[:0]
		bytesInBatch = 0
	}

	for {
		select {
		case it := <-w.ch:
			batch = append(batch, it)
			bytesInBatch += it.size
			if len(batch) >= w.opts.BatchSize || bytesInBatch >= w.opts.MaxBatchBytes {
				flush(context.Background())
			}
		case <-ticker.C:
			flush(context.Background())
		case <-w.done:
			w.mu.RLock()
			finalCtx := w.closeCtx
			w.mu.RUnlock()
			if finalCtx == nil {
				finalCtx = context.Background()
			}
			for {
				select {
				case it := <-w.ch:
					batch = append(batch, it)
					bytesInBatch += it.size
				default:
					flush(finalCtx)
					return
				}
			}
		}
	}
}

func (w *Writer) flush(ctx context.Context, batch []item) error {
	byStream := make(map[string][]item)
	var order []string
	for _, it := range batch {
		if _, ok := byStream[it.stream]; !ok {
			order = append(order, it.stream)
		}
		byStream[it.stream] = append(byStream[it.stream], it)
	}

	var errs []error
	for _, stream := range order {
		body, err := encodeBulk("create", stream, byStream[stream])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := w.send(ctx, "/_bulk", body); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func encodeBulk(op, index string, items []item) ([]byte, error) {
	var buf bytes.Buffer
	for _, it := range items {
		meta := map[string]any{"_index": index}
		if it.id != "" {
			meta["_id"] = it.id
		}
		action, err := json.Marshal(map[string]any{op: meta})
		if err != nil {
			return nil, fmt.Errorf("trace: encode bulk action: %w", err)
		}
		buf.Write(action)
		buf.WriteByte('\n')
		buf.Write(it.raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func bulkRequest(path string, body []byte) store.Request {
	req := store.NDJSONRequest(http.MethodPost, path, body)
	req.Idempotent = true
	return req
}

func (w *Writer) send(ctx context.Context, path string, body []byte) error {
	var lastErr error
	for attempt := 0; attempt <= w.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.opts.RetryBackoff * time.Duration(attempt)):
			}
		}
		status, resp, err := w.opts.ES.Do(ctx, bulkRequest(path, body))
		if err != nil {
			lastErr = err
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("trace: bulk status %d: %s", status, store.Snippet(resp))
			continue
		}
		retry, bulkErr := bulkOutcome(resp)
		if !retry {
			return nil
		}
		lastErr = bulkErr
	}
	return errors.Join(lastErr, core.ErrProvider)
}

func bulkOutcome(resp []byte) (bool, error) {
	err := store.BulkErrors(resp)
	if err == nil {
		return false, nil
	}
	if !store.IsVersionConflict(err) {
		return true, err
	}
	if realErr := realItemFailure(resp); realErr != nil {
		return true, realErr
	}
	return false, nil
}

func realItemFailure(resp []byte) error {
	var parsed struct {
		Items []map[string]struct {
			Status int `json:"status"`
			Error  *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil
	}
	for _, item := range parsed.Items {
		for action, res := range item {
			if res.Error == nil {
				continue
			}
			if res.Status == http.StatusConflict || res.Error.Type == versionConflictType {
				continue
			}
			return fmt.Errorf("trace: bulk %s failed with status %d: %s: %s: %w",
				action, res.Status, res.Error.Type, res.Error.Reason, store.ErrBulkItem)
		}
	}
	return nil
}
