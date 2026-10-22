package eval

import (
	"sync"
	"time"
)

// Kind classifies an observation.
type Kind string

const (
	// KindSpan is a span around an operation.
	KindSpan Kind = "SPAN"
	// KindGeneration is a model generation.
	KindGeneration Kind = "GENERATION"
	// KindEvent is a point-in-time event such as a guard verdict.
	KindEvent Kind = "EVENT"
)

// Level is the severity of an observation; empty is treated as LevelDefault.
type Level string

const (
	// LevelDefault is the normal level.
	LevelDefault Level = "DEFAULT"
	// LevelError marks an observation as failed.
	LevelError Level = "ERROR"
)

// Usage carries token counts and monetary cost for a trace or observation.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

// Observation is a single timed operation or event inside a Trace; spans,
// generations and events are all modeled here. Guard fields record the rail,
// action and detector that produced the observation.
type Observation struct {
	ID              string
	TraceID         string
	ParentID        string
	Name            string
	Kind            Kind
	Level           Level
	ErrorKind       string
	Model           string
	Input           string
	Output          string
	StatusMessage   string
	GuardRail       string
	GuardAction     string
	GuardCategories []string
	Tags            []string
	StartTime       time.Time
	EndTime         time.Time
	Usage           Usage
	Metadata        map[string]any

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   string

	GuardDegraded bool
}

// TraceRecord is the root record of one user-facing operation such as chat,
// ingest, search, generate or memory.
type TraceRecord struct {
	ID        string
	Name      string
	UserID    string
	SessionID string
	Tenant    string
	Input     string
	Output    string
	Tags      []string
	StartTime time.Time
	EndTime   time.Time
	Usage     Usage
	Metadata  map[string]any

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   string
}

// Score is a numeric evaluation attached to a trace by TraceID.
type Score struct {
	TraceID   string
	Name      string
	Value     float64
	Comment   string
	Timestamp time.Time
	Metadata  map[string]any
}

// Snapshot is a copy of the documents recorded by a Recorder.
type Snapshot struct {
	Traces       []TraceRecord
	Observations []Observation
	Scores       []Score
}

// Recorder is a concurrency-safe in-memory sink intended for tests and eval.
type Recorder struct {
	mu   sync.Mutex
	snap Snapshot
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Enqueue appends the trace to the recorded snapshot.
func (r *Recorder) Enqueue(t TraceRecord) {
	r.mu.Lock()
	r.snap.Traces = append(r.snap.Traces, t)
	r.mu.Unlock()
}

// EnqueueObservation appends the observation to the recorded snapshot.
func (r *Recorder) EnqueueObservation(o Observation) {
	r.mu.Lock()
	r.snap.Observations = append(r.snap.Observations, o)
	r.mu.Unlock()
}

// EnqueueScore appends the score to the recorded snapshot.
func (r *Recorder) EnqueueScore(s Score) {
	r.mu.Lock()
	r.snap.Scores = append(r.snap.Scores, s)
	r.mu.Unlock()
}

// Snapshot returns a copy of everything recorded so far.
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Snapshot{
		Traces:       append([]TraceRecord(nil), r.snap.Traces...),
		Observations: append([]Observation(nil), r.snap.Observations...),
		Scores:       append([]Score(nil), r.snap.Scores...),
	}
}

// Reset discards all recorded documents.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.snap = Snapshot{}
	r.mu.Unlock()
}

// Fanout is a sink that forwards each document to every non-nil sink in the
// slice.
type Fanout []Sink

// Sink receives exported traces, observations and scores.
type Sink interface {
	Enqueue(TraceRecord)
	EnqueueObservation(Observation)
	EnqueueScore(Score)
}

// NewFanout returns a Fanout over the given sinks; nil entries are skipped.
func NewFanout(sinks ...Sink) Fanout { return Fanout(sinks) }

// Enqueue forwards the trace to every non-nil sink.
func (f Fanout) Enqueue(t TraceRecord) {
	for _, s := range f {
		if s != nil {
			s.Enqueue(t)
		}
	}
}

// EnqueueObservation forwards the observation to every non-nil sink.
func (f Fanout) EnqueueObservation(o Observation) {
	for _, s := range f {
		if s != nil {
			s.EnqueueObservation(o)
		}
	}
}

// EnqueueScore forwards the score to every non-nil sink.
func (f Fanout) EnqueueScore(s Score) {
	for _, sink := range f {
		if sink != nil {
			sink.EnqueueScore(s)
		}
	}
}
