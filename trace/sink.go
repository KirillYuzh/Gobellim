package trace

import "sync"

// Sink receives exported traces, observations and scores.
type Sink interface {
	Enqueue(Trace)
	EnqueueObservation(Observation)
	EnqueueScore(Score)
}

// Snapshot is a copy of the documents recorded by a Recorder.
type Snapshot struct {
	Traces       []Trace
	Observations []Observation
	Scores       []Score
}

// Recorder is a concurrency-safe in-memory Sink intended for tests and eval.
type Recorder struct {
	mu   sync.Mutex
	snap Snapshot
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Enqueue appends the trace to the recorded snapshot.
func (r *Recorder) Enqueue(t Trace) {
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
		Traces:       append([]Trace(nil), r.snap.Traces...),
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

// Fanout is a Sink that forwards each document to every non-nil sink in the slice.
type Fanout []Sink

// NewFanout returns a Fanout over the given sinks; nil entries are skipped.
func NewFanout(sinks ...Sink) Fanout { return Fanout(sinks) }

// Enqueue forwards the trace to every non-nil sink.
func (f Fanout) Enqueue(t Trace) {
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
