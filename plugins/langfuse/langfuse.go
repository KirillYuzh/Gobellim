// Package langfuse exports gobellim audit events to Langfuse.
//
// The sink never sends prompt text, raw arguments or secrets: it only maps the
// labels, digests and counters that gobellim already puts in an audit event.
package langfuse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

// Config configures a Sink.
type Config struct {
	Host          string
	PublicKey     string
	SecretKey     string
	HTTPClient    *http.Client
	BatchSize     int
	FlushInterval time.Duration
	// DryRun writes the ingestion payload to Writer instead of posting it.
	DryRun bool
	Writer io.Writer
	// OnError receives background delivery errors. It must not log secrets.
	OnError func(error)
	// OnDrop receives an event the queue could not accept. Audit loss must be
	// visible to the application, not only to Dropped().
	OnDrop func(gobellim.Event)
	// SendPrincipalID sends the caller's principal id to Langfuse. It is off by
	// default: a principal id is personal data, and an audit sink is not an
	// identity export.
	SendPrincipalID bool
	// SendTenant sends the tenant reference to Langfuse. It is off by default
	// for the same reason.
	SendTenant bool
}

const defaultHost = "https://cloud.langfuse.com"

// Sink is a non-blocking gobellim.Sink.
type Sink struct {
	cfg Config

	events chan gobellim.Event
	mu     sync.RWMutex
	closed bool

	wmu     sync.Mutex
	wg      sync.WaitGroup
	dropped atomic.Uint64
	once    sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

var _ gobellim.Sink = (*Sink)(nil)

// New starts a Sink. Background delivery starts immediately; call Close to
// flush the remaining batch.
func New(cfg Config) (*Sink, error) {
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	cfg.Host = strings.TrimRight(cfg.Host, "/")
	if !cfg.DryRun && (cfg.PublicKey == "" || cfg.SecretKey == "") {
		return nil, fmt.Errorf("langfuse: public and secret keys are required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 32
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 2 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Writer == nil {
		cfg.Writer = io.Discard
	}
	queue := cfg.BatchSize * 16
	if queue < 256 {
		queue = 256
	}
	s := &Sink{cfg: cfg, events: make(chan gobellim.Event, queue)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.loop()
	return s, nil
}

// Emit queues one event. It never blocks the caller; when the queue is full or
// the sink is closing the event is dropped, counted and reported to OnDrop.
func (s *Sink) Emit(_ context.Context, e gobellim.Event) {
	s.mu.RLock()
	closed := s.closed
	if !closed {
		select {
		case s.events <- e:
			s.mu.RUnlock()
			return
		default:
		}
	}
	s.mu.RUnlock()
	s.dropped.Add(1)
	if s.cfg.OnDrop != nil {
		s.cfg.OnDrop(e)
	}
}

// Close flushes queued events and stops the sink. It is safe to call twice.
func (s *Sink) Close(ctx context.Context) error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.events)
		s.mu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.cancel()
		return nil
	case <-ctx.Done():
		// Abort in-flight delivery instead of outliving the caller's deadline.
		s.cancel()
		return ctx.Err()
	}
}

// Dropped reports how many events were lost because the queue was full.
func (s *Sink) Dropped() uint64 { return s.dropped.Load() }

func (s *Sink) loop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]gobellim.Event, 0, s.cfg.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.flush(batch)
		batch = batch[:0]
	}
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= s.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Sink) flush(events []gobellim.Event) {
	batch := make([]ingestionEvent, 0, len(events))
	for _, e := range events {
		batch = append(batch, s.convert(e)...)
	}
	if len(batch) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{"batch": batch})
	if err != nil {
		s.report(err)
		return
	}
	if s.cfg.DryRun {
		s.wmu.Lock()
		_, err := s.cfg.Writer.Write(append(payload, '\n'))
		s.wmu.Unlock()
		if err != nil {
			s.report(err)
		}
		return
	}

	var status int
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.cfg.Host+"/api/public/ingestion", bytes.NewReader(payload))
		if err != nil {
			s.report(err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(s.cfg.PublicKey, s.cfg.SecretKey)
		resp, err := s.cfg.HTTPClient.Do(req)
		if err != nil {
			s.report(err)
			return
		}
		// Drain before closing so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		status = resp.StatusCode
		if status >= 200 && status < 300 {
			return
		}
		if status != http.StatusTooManyRequests && status < 500 {
			break
		}
		if attempt == 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	s.report(fmt.Errorf("langfuse: ingestion returned status %d", status))
}

func (s *Sink) report(err error) {
	if s.cfg.OnError != nil {
		s.cfg.OnError(err)
	}
}

type ingestionEvent struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Body      any    `json:"body"`
}

type traceBody struct {
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	Name      string         `json:"name"`
	UserID    string         `json:"userId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type spanBody struct {
	ID        string         `json:"id"`
	TraceID   string         `json:"traceId"`
	Name      string         `json:"name"`
	StartTime string         `json:"startTime"`
	EndTime   string         `json:"endTime"`
	Level     string         `json:"level,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	StatusMsg string         `json:"statusMessage,omitempty"`
}

type generationBody struct {
	ID        string         `json:"id"`
	TraceID   string         `json:"traceId"`
	Name      string         `json:"name"`
	Model     string         `json:"model,omitempty"`
	StartTime string         `json:"startTime"`
	EndTime   string         `json:"endTime"`
	Usage     map[string]int `json:"usage,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (s *Sink) convert(e gobellim.Event) []ingestionEvent {
	traceID := e.RunID
	if traceID == "" {
		traceID = fallbackTraceID(e)
	}
	ts := e.Time.UTC()
	stamp := formatTime(ts)
	start := ts
	if e.Latency > 0 {
		start = ts.Add(-e.Latency)
	}

	switch e.Kind {
	case gobellim.EventTaskStarted:
		name := e.Task
		if name == "" {
			name = e.Flow
		}
		if name == "" {
			name = "gobellim"
		}
		return []ingestionEvent{{
			ID: s.newID(), Timestamp: stamp, Type: "trace-create",
			Body: traceBody{
				ID: traceID, Timestamp: stamp, Name: name,
				UserID:   principal(e, s.cfg.SendPrincipalID),
				Metadata: s.metadata(e),
			},
		}}
	case gobellim.EventModelCompleted:
		return []ingestionEvent{{
			ID: s.newID(), Timestamp: stamp, Type: "generation-create",
			Body: generationBody{
				ID: s.newID(), TraceID: traceID, Name: "model", Model: e.Model,
				StartTime: formatTime(start), EndTime: stamp,
				Usage: map[string]int{
					"input":  e.PromptTokens,
					"output": e.CompletionTokens,
					"total":  e.TotalTokens,
				},
				Metadata: s.metadata(e),
			},
		}}
	default:
		name := string(e.Kind)
		if e.Tool != "" {
			name = e.Tool
		}
		level := "DEFAULT"
		if e.Error != "" {
			level = "ERROR"
		}
		return []ingestionEvent{{
			ID: s.newID(), Timestamp: stamp, Type: "span-create",
			Body: spanBody{
				ID: s.newID(), TraceID: traceID, Name: name,
				StartTime: formatTime(start), EndTime: stamp,
				Level: level, Metadata: s.metadata(e), StatusMsg: e.Error,
			},
		}}
	}
}

func principal(e gobellim.Event, send bool) string {
	if !send {
		return ""
	}
	return e.PrincipalID
}

func (s *Sink) metadata(e gobellim.Event) map[string]any {
	m := map[string]any{"kind": string(e.Kind)}
	add := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	add("task", e.Task)
	add("flow", e.Flow)
	add("step", e.Step)
	if s.cfg.SendTenant {
		add("tenant", e.Tenant)
	}
	add("tool", e.Tool)
	add("policy_version", e.PolicyVersion)
	add("args_digest", e.ArgsDigest)
	add("error", e.Error)
	if e.Access != gobellim.AccessUnknown {
		m["access"] = e.Access.String()
	}
	if len(e.RuleIDs) > 0 || e.Decision != gobellim.Deny {
		m["decision"] = e.Decision.String()
	}
	if len(e.RuleIDs) > 0 {
		m["rules"] = e.RuleIDs
	}
	if e.Tool != "" {
		m["integrity"] = e.Integrity.String()
		m["sensitivity"] = e.Sensitivity.String()
	}
	if e.ModelCalls > 0 {
		m["model_calls"] = e.ModelCalls
	}
	if e.ToolCalls > 0 {
		m["tool_calls"] = e.ToolCalls
	}
	return m
}

func fallbackTraceID(e gobellim.Event) string {
	switch {
	case e.Flow != "":
		return "flow:" + e.Flow
	case e.Task != "":
		return "task:" + e.Task
	default:
		return "gobellim"
	}
}

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

func (s *Sink) newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
