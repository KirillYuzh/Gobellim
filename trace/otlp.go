package trace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	defaultOTLPServiceName   = "kit"
	defaultOTLPBatchSize     = 128
	defaultOTLPFlushInterval = 2 * time.Second
	defaultOTLPTimeout       = 10 * time.Second
	otlpScopeName            = "kit"
	otlpKindInternal         = 1
	otlpStatusError          = 2
	otlpMaxTextRunes         = 8192
	otlpMaxBuffered          = 4096
	otlpMaxAttempts          = 2
)

type OTLPOptions struct {
	Endpoint      string
	Headers       map[string]string
	ServiceName   string
	BatchSize     int
	FlushInterval time.Duration
	Timeout       time.Duration
	Client        *http.Client
	Logger        *slog.Logger
}

type OTLP struct {
	endpoint  string
	headers   map[string]string
	service   string
	client    *http.Client
	logger    *slog.Logger
	batchSize int
	interval  time.Duration

	mu     sync.Mutex
	traces []Trace
	obs    []Observation
	scores []Score

	flushCh   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	dropped atomic.Int64

	errMu   sync.Mutex
	lastErr error
}

var _ Sink = (*OTLP)(nil)

func NewOTLP(opts OTLPOptions) (*OTLP, error) {
	if opts.Endpoint == "" {
		return nil, errors.New("trace: OTLP endpoint is required")
	}
	if opts.ServiceName == "" {
		opts.ServiceName = defaultOTLPServiceName
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultOTLPBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultOTLPFlushInterval
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultOTLPTimeout
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: opts.Timeout}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	o := &OTLP{
		endpoint:  opts.Endpoint,
		headers:   cloneHeaders(opts.Headers),
		service:   opts.ServiceName,
		client:    opts.Client,
		logger:    opts.Logger,
		batchSize: opts.BatchSize,
		interval:  opts.FlushInterval,
		flushCh:   make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	o.wg.Add(1)
	go o.run()
	return o, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (o *OTLP) Enqueue(t Trace) {
	o.mu.Lock()
	if len(o.traces)+len(o.obs) >= otlpMaxBuffered {
		o.mu.Unlock()
		o.drop("buffer full")
		return
	}
	o.traces = append(o.traces, t)
	n := len(o.traces) + len(o.obs)
	o.mu.Unlock()
	if n >= o.batchSize {
		o.signal()
	}
}

func (o *OTLP) EnqueueObservation(ob Observation) {
	o.mu.Lock()
	if len(o.traces)+len(o.obs) >= otlpMaxBuffered {
		o.mu.Unlock()
		o.drop("buffer full")
		return
	}
	o.obs = append(o.obs, ob)
	n := len(o.traces) + len(o.obs)
	o.mu.Unlock()
	if n >= o.batchSize {
		o.signal()
	}
}

func (o *OTLP) EnqueueScore(s Score) {
	o.mu.Lock()
	if len(o.scores) >= otlpMaxBuffered {
		o.mu.Unlock()
		o.drop("buffer full")
		return
	}
	o.scores = append(o.scores, s)
	o.mu.Unlock()
}

func (o *OTLP) signal() {
	select {
	case o.flushCh <- struct{}{}:
	default:
	}
}

func (o *OTLP) drop(reason string) {
	if n := o.dropped.Add(1); n == 1 || n%100 == 0 {
		o.logger.Warn("trace: otlp dropping span", "reason", reason, "dropped_total", n)
	}
}

func (o *OTLP) run() {
	defer o.wg.Done()
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	for {
		select {
		case <-o.flushCh:
			o.flushOnce(context.Background())
		case <-ticker.C:
			o.flushOnce(context.Background())
		case <-o.done:
			return
		}
	}
}

func (o *OTLP) Flush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o.flushOnce(ctx)
	return o.takeErr()
}

func (o *OTLP) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o.closeOnce.Do(func() { close(o.done) })
	o.wg.Wait()
	o.flushOnce(ctx)
	return o.takeErr()
}

func (o *OTLP) flushOnce(ctx context.Context) {
	spans := o.drain()
	if len(spans) == 0 {
		return
	}
	if err := o.send(ctx, spans); err != nil {
		o.recordErr(err)
		o.logger.Warn("trace: otlp flush failed", "err", err, "spans", len(spans))
	}
}

func (o *OTLP) drain() []otlpSpan {
	o.mu.Lock()
	traces := o.traces
	obs := o.obs
	scores := o.scores
	o.traces, o.obs, o.scores = nil, nil, nil
	o.mu.Unlock()
	return o.build(traces, obs, scores)
}

func (o *OTLP) build(traces []Trace, obs []Observation, scores []Score) []otlpSpan {
	spans := make([]otlpSpan, 0, len(traces)+len(obs)+len(scores))
	root := make(map[string]int, len(traces))
	for _, t := range traces {
		root[t.ID] = len(spans)
		spans = append(spans, traceSpan(t))
	}
	for _, ob := range obs {
		spans = append(spans, observationSpan(ob))
	}
	for _, s := range scores {
		if i, ok := root[s.TraceID]; ok {
			spans[i].Events = append(spans[i].Events, scoreEvent(s))
			continue
		}
		spans = append(spans, scoreSpan(s))
	}
	return spans
}

func (o *OTLP) send(ctx context.Context, spans []otlpSpan) error {
	body, err := json.Marshal(o.payload(spans))
	if err != nil {
		return fmt.Errorf("trace: encode otlp payload: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < otlpMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := o.post(ctx, body); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (o *OTLP) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("trace: otlp export status %d", resp.StatusCode)
	}
	return nil
}

func (o *OTLP) recordErr(err error) {
	if err == nil {
		return
	}
	o.errMu.Lock()
	o.lastErr = errors.Join(o.lastErr, err)
	o.errMu.Unlock()
}

func (o *OTLP) takeErr() error {
	o.errMu.Lock()
	defer o.errMu.Unlock()
	err := o.lastErr
	o.lastErr = nil
	return err
}

func (o *OTLP) payload(spans []otlpSpan) otlpPayload {
	return otlpPayload{
		ResourceSpans: []otlpResourceSpans{{
			Resource: otlpResource{Attributes: []otlpKeyValue{stringAttr("service.name", o.service)}},
			ScopeSpans: []otlpScopeSpans{{
				Scope: otlpScope{Name: otlpScopeName},
				Spans: spans,
			}},
		}},
	}
}

func otlpTraceID(id string) string {
	sum := sha256.Sum256([]byte("trace:" + id))
	return hex.EncodeToString(sum[:16])
}

func otlpSpanID(id string) string {
	sum := sha256.Sum256([]byte("span:" + id))
	return hex.EncodeToString(sum[:8])
}

func nanoString(t time.Time) string { return strconv.FormatUint(uint64(t.UnixNano()), 10) }

func spanTimes(start, end time.Time) (string, string) {
	if start.IsZero() {
		start = time.Now()
	}
	if end.IsZero() {
		end = start
	}
	return nanoString(start), nanoString(end)
}

func traceSpan(t Trace) otlpSpan {
	start, end := spanTimes(t.StartTime, t.EndTime)
	sp := otlpSpan{
		TraceID:           otlpTraceID(t.ID),
		SpanID:            otlpSpanID(t.ID),
		Name:              t.Name,
		Kind:              otlpKindInternal,
		StartTimeUnixNano: start,
		EndTimeUnixNano:   end,
	}
	sp.Attributes = append(sp.Attributes, usageAttrs(t.Usage)...)
	sp.Attributes = append(sp.Attributes, tagsAttrs(t.Tags)...)
	sp.Attributes = append(sp.Attributes, metadataAttr(t.Metadata)...)
	sp.Attributes = append(sp.Attributes, auditAttrs(t.GuardVersion, t.GuardModel, t.PolicyHash, t.TextPolicy)...)
	sp.Attributes = append(sp.Attributes, textAttrs(t.Input, t.Output)...)
	return sp
}

func observationSpan(o Observation) otlpSpan {
	start, end := spanTimes(o.StartTime, o.EndTime)
	parent := o.ParentID
	if parent == "" {
		parent = o.TraceID
	}
	sp := otlpSpan{
		TraceID:           otlpTraceID(o.TraceID),
		SpanID:            otlpSpanID(o.ID),
		ParentSpanID:      otlpSpanID(parent),
		Name:              o.Name,
		Kind:              otlpKindInternal,
		StartTimeUnixNano: start,
		EndTimeUnixNano:   end,
	}
	if o.Model != "" {
		sp.Attributes = append(sp.Attributes, stringAttr("gen_ai.request.model", o.Model))
	}
	if o.Kind != "" {
		sp.Attributes = append(sp.Attributes, stringAttr("kit.kind", string(o.Kind)))
	}
	if o.ErrorKind != "" {
		sp.Attributes = append(sp.Attributes, stringAttr("kit.error_kind", o.ErrorKind))
	}
	if o.GuardRail != "" {
		sp.Attributes = append(sp.Attributes, stringAttr("kit.guard_rail", o.GuardRail))
	}
	if o.GuardAction != "" {
		sp.Attributes = append(sp.Attributes, stringAttr("kit.guard_action", o.GuardAction))
	}
	if len(o.GuardCategories) > 0 {
		sp.Attributes = append(sp.Attributes, stringSliceAttr("kit.guard_categories", o.GuardCategories))
	}
	if o.GuardDegraded {
		sp.Attributes = append(sp.Attributes, boolAttr("kit.guard_degraded", true))
	}
	sp.Attributes = append(sp.Attributes, usageAttrs(o.Usage)...)
	sp.Attributes = append(sp.Attributes, tagsAttrs(o.Tags)...)
	sp.Attributes = append(sp.Attributes, metadataAttr(o.Metadata)...)
	sp.Attributes = append(sp.Attributes, auditAttrs(o.GuardVersion, o.GuardModel, o.PolicyHash, o.TextPolicy)...)
	sp.Attributes = append(sp.Attributes, textAttrs(o.Input, o.Output)...)
	if o.Level == LevelError {
		sp.Status = &otlpStatus{Code: otlpStatusError, Message: o.StatusMessage}
	}
	return sp
}

func scoreEvent(s Score) otlpEvent {
	return otlpEvent{
		Name:         "score",
		TimeUnixNano: eventTime(s.Timestamp),
		Attributes: []otlpKeyValue{
			stringAttr("score.name", s.Name),
			doubleAttr("score.value", s.Value),
			stringAttr("score.comment", s.Comment),
		},
	}
}

func scoreSpan(s Score) otlpSpan {
	start := s.Timestamp
	if start.IsZero() {
		start = time.Now()
	}
	return otlpSpan{
		TraceID:           otlpTraceID(s.TraceID),
		SpanID:            otlpSpanID("score:" + s.TraceID + ":" + s.Name),
		Name:              "score",
		Kind:              otlpKindInternal,
		StartTimeUnixNano: nanoString(start),
		EndTimeUnixNano:   nanoString(start),
		Attributes: []otlpKeyValue{
			stringAttr("kit.score_trace_id", s.TraceID),
			stringAttr("score.name", s.Name),
			doubleAttr("score.value", s.Value),
			stringAttr("score.comment", s.Comment),
		},
	}
}

func eventTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return nanoString(t)
}

func usageAttrs(u Usage) []otlpKeyValue {
	var out []otlpKeyValue
	if u.PromptTokens != 0 {
		out = append(out, intAttr("gen_ai.usage.input_tokens", int64(u.PromptTokens)))
	}
	if u.CompletionTokens != 0 {
		out = append(out, intAttr("gen_ai.usage.output_tokens", int64(u.CompletionTokens)))
	}
	if u.Cost != 0 {
		out = append(out, doubleAttr("kit.cost", u.Cost))
	}
	return out
}

func tagsAttrs(tags []string) []otlpKeyValue {
	if len(tags) == 0 {
		return nil
	}
	return []otlpKeyValue{stringSliceAttr("kit.tags", tags)}
}

func metadataAttr(md map[string]any) []otlpKeyValue {
	if len(md) == 0 {
		return nil
	}
	b, err := json.Marshal(md)
	if err != nil {
		return nil
	}
	return []otlpKeyValue{stringAttr("kit.metadata", string(b))}
}

func auditAttrs(version, model, policyHash string, p TextPolicy) []otlpKeyValue {
	var out []otlpKeyValue
	if version != "" {
		out = append(out, stringAttr("kit.guard_version", version))
	}
	if model != "" {
		out = append(out, stringAttr("kit.guard_model", model))
	}
	if policyHash != "" {
		out = append(out, stringAttr("kit.policy_hash", policyHash))
	}
	return append(out, stringAttr("kit.text_policy", p.String()))
}

func textAttrs(input, output string) []otlpKeyValue {
	var out []otlpKeyValue
	if input != "" {
		out = append(out, stringAttr("gen_ai.prompt", clipRunes(input, otlpMaxTextRunes)))
	}
	if output != "" {
		out = append(out, stringAttr("gen_ai.completion", clipRunes(output, otlpMaxTextRunes)))
	}
	return out
}

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

func stringAttr(key, val string) otlpKeyValue {
	v := val
	return otlpKeyValue{Key: key, Value: otlpValue{StringValue: &v}}
}

func intAttr(key string, val int64) otlpKeyValue {
	s := strconv.FormatInt(val, 10)
	return otlpKeyValue{Key: key, Value: otlpValue{IntValue: &s}}
}

func doubleAttr(key string, val float64) otlpKeyValue {
	v := val
	return otlpKeyValue{Key: key, Value: otlpValue{DoubleValue: &v}}
}

func boolAttr(key string, val bool) otlpKeyValue {
	v := val
	return otlpKeyValue{Key: key, Value: otlpValue{BoolValue: &v}}
}

func stringSliceAttr(key string, vals []string) otlpKeyValue {
	values := make([]otlpValue, len(vals))
	for i, s := range vals {
		v := s
		values[i] = otlpValue{StringValue: &v}
	}
	return otlpKeyValue{Key: key, Value: otlpValue{ArrayValue: &otlpArray{Values: values}}}
}

type otlpPayload struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            *otlpStatus    `json:"status,omitempty"`
	Events            []otlpEvent    `json:"events,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpEvent struct {
	Name         string         `json:"name"`
	TimeUnixNano string         `json:"timeUnixNano,omitempty"`
	Attributes   []otlpKeyValue `json:"attributes,omitempty"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue *string    `json:"stringValue,omitempty"`
	IntValue    *string    `json:"intValue,omitempty"`
	DoubleValue *float64   `json:"doubleValue,omitempty"`
	BoolValue   *bool      `json:"boolValue,omitempty"`
	ArrayValue  *otlpArray `json:"arrayValue,omitempty"`
}

type otlpArray struct {
	Values []otlpValue `json:"values"`
}
