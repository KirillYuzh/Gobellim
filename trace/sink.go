package trace

import "sync"

type Sink interface {
	Enqueue(Trace)
	EnqueueObservation(Observation)
	EnqueueScore(Score)
}

type Snapshot struct {
	Traces       []Trace
	Observations []Observation
	Scores       []Score
}

type Recorder struct {
	mu   sync.Mutex
	snap Snapshot
}

func NewRecorder() *Recorder { return &Recorder{} }

func (r *Recorder) Enqueue(t Trace) {
	r.mu.Lock()
	r.snap.Traces = append(r.snap.Traces, t)
	r.mu.Unlock()
}

func (r *Recorder) EnqueueObservation(o Observation) {
	r.mu.Lock()
	r.snap.Observations = append(r.snap.Observations, o)
	r.mu.Unlock()
}

func (r *Recorder) EnqueueScore(s Score) {
	r.mu.Lock()
	r.snap.Scores = append(r.snap.Scores, s)
	r.mu.Unlock()
}

func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Snapshot{
		Traces:       append([]Trace(nil), r.snap.Traces...),
		Observations: append([]Observation(nil), r.snap.Observations...),
		Scores:       append([]Score(nil), r.snap.Scores...),
	}
}

func (r *Recorder) Reset() {
	r.mu.Lock()
	r.snap = Snapshot{}
	r.mu.Unlock()
}

type Fanout []Sink

func NewFanout(sinks ...Sink) Fanout { return Fanout(sinks) }

func (f Fanout) Enqueue(t Trace) {
	for _, s := range f {
		if s != nil {
			s.Enqueue(t)
		}
	}
}

func (f Fanout) EnqueueObservation(o Observation) {
	for _, s := range f {
		if s != nil {
			s.EnqueueObservation(o)
		}
	}
}

func (f Fanout) EnqueueScore(s Score) {
	for _, sink := range f {
		if sink != nil {
			sink.EnqueueScore(s)
		}
	}
}
