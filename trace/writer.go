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

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/store"
)

const (
	DefaultQueueSize     = 4096
	DefaultBatchSize     = 500
	DefaultFlushInterval = 2 * time.Second
	DefaultMaxBatchBytes = 5 << 20
	DefaultMaxRetries    = 3
	DefaultRetryBackoff  = 200 * time.Millisecond
)

type Options struct {
	ES            store.ES
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
	source map[string]any
}

// Writer batches traces and observations and sends them to Elasticsearch
// asynchronously; it never blocks the request path and drops on overflow.
type Writer struct {
	opts    Options
	ch      chan item
	wg      sync.WaitGroup
	once    sync.Once
	closed  atomic.Bool
	dropped atomic.Int64
	mu      sync.Mutex
	lastErr error
}

func NewWriter(opts Options) (*Writer, error) {
	if opts.ES == nil {
		return nil, fmt.Errorf("trace: ES client is required: %w", core.ErrInvalidConfig)
	}
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
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	w := &Writer{opts: opts, ch: make(chan item, opts.QueueSize)}
	w.wg.Add(1)
	go w.run()
	return w, nil
}

func (w *Writer) Enqueue(t Trace) {
	w.enqueue(item{stream: store.DataStreamTraces, id: t.ID, source: t.document()})
}

func (w *Writer) EnqueueObservation(o Observation) {
	w.enqueue(item{stream: store.DataStreamObservations, id: o.ID, source: o.document()})
}

func (w *Writer) EnqueueScore(s Score) {
	ts := s.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	w.enqueue(item{
		stream: store.DataStreamTraces,
		source: map[string]any{
			"@timestamp": ts.UTC().Format(time.RFC3339Nano),
			"trace_id":   s.TraceID,
			"name":       s.Name,
			"score":      s.Value,
			"comment":    s.Comment,
			"kind":       "SCORE",
		},
	})
}

func (w *Writer) enqueue(it item) {
	if w.closed.Load() {
		return
	}
	select {
	case w.ch <- it:
	default:
		if n := w.dropped.Add(1); n == 1 || n%100 == 0 {
			w.opts.Logger.Warn("trace: queue full, dropping document", "dropped_total", n)
		}
	}
}

func (w *Writer) Dropped() int64 { return w.dropped.Load() }

func (w *Writer) Close(context.Context) error {
	w.once.Do(func() {
		w.closed.Store(true)
		close(w.ch)
	})
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *Writer) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]item, 0, w.opts.BatchSize)
	bytesInBatch := 0

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := w.flush(context.Background(), batch); err != nil {
			w.mu.Lock()
			w.lastErr = err
			w.mu.Unlock()
			w.opts.Logger.Warn("trace: flush failed", "err", err, "documents", len(batch))
		}
		batch = batch[:0]
		bytesInBatch = 0
	}

	for {
		select {
		case it, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, it)
			bytesInBatch += len(it.stream) + estimateSize(it.source)
			if len(batch) >= w.opts.BatchSize || bytesInBatch >= w.opts.MaxBatchBytes {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *Writer) flush(ctx context.Context, batch []item) error {
	byStream := make(map[string][]store.Doc)
	var order []string
	for _, it := range batch {
		if _, ok := byStream[it.stream]; !ok {
			order = append(order, it.stream)
		}
		byStream[it.stream] = append(byStream[it.stream], store.Doc{ID: it.id, Source: it.source})
	}

	var firstErr error
	for _, stream := range order {
		body, err := store.BulkCreateBody(stream, byStream[stream])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := w.send(ctx, "/_bulk", body); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
		status, resp, err := w.opts.ES.Do(ctx, http.MethodPost, path, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("trace: bulk status %d: %s", status, store.Snippet(resp))
			continue
		}
		if err := bulkErrors(resp); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return errors.Join(lastErr, core.ErrProvider)
}

func bulkErrors(resp []byte) error {
	var parsed struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil
	}
	if parsed.Errors {
		return fmt.Errorf("trace: bulk reported item errors: %s", store.Snippet(resp))
	}
	return nil
}

func estimateSize(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}
