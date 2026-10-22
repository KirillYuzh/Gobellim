package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	// defaultTTL is how long a run stays resumable.
	defaultTTL = 15 * time.Minute
	// defaultMaxRuns bounds the whole store.
	defaultMaxRuns = 1000
	// defaultMaxPerPrincipal bounds the runs of one principal.
	defaultMaxPerPrincipal = 10
)

// Run is the durable part of a scenario: its owner, its expiry and the last
// result. A Store persists it; the workflow execution is rebuilt from
// Last.State and the pending stage, so a restart does not lose suspended runs.
type Run[S any] struct {
	ID        string    `json:"id"`
	Principal Principal `json:"principal"`
	Expires   time.Time `json:"expires"`
	ResumeID  string    `json:"resume_id,omitempty"`
	Last      Result[S] `json:"last"`

	mu   sync.Mutex
	seq  int64 // store version, set by Load; a store rejects a stale Save
	term int64 // primary term for Elasticsearch optimistic concurrency
}

// Store keeps runs. Session is the only consumer: it loads a run, checks the
// owner and the answer, resumes the execution and saves the new state. Load and
// Save take the request context so an external store can honour its deadline.
type Store[S any] interface {
	Load(ctx context.Context, runID string, p Principal) (*Run[S], error)
	Save(ctx context.Context, run *Run[S]) error
	// Expiry returns the expiration of a run created now.
	Expiry() time.Time
}

// MemoryStore keeps runs in memory with a TTL and size limits. It is the
// default store; a process restart loses suspended runs.
type MemoryStore[S any] struct {
	mu     sync.Mutex
	runs   map[string]*Run[S]
	ttl    time.Duration
	max    int
	maxPer int
}

type storeConfig struct {
	ttl    time.Duration
	max    int
	maxPer int
}

// StoreOption configures a MemoryStore.
type StoreOption func(*storeConfig)

// TTL sets how long a suspended or finished run stays resumable.
func TTL(d time.Duration) StoreOption {
	return func(c *storeConfig) {
		if d > 0 {
			c.ttl = d
		}
	}
}

// MaxRuns sets the hard limit of stored runs.
func MaxRuns(n int) StoreOption {
	return func(c *storeConfig) {
		if n > 0 {
			c.max = n
		}
	}
}

// MaxRunsPerPrincipal sets how many runs one principal may keep.
func MaxRunsPerPrincipal(n int) StoreOption {
	return func(c *storeConfig) {
		if n > 0 {
			c.maxPer = n
		}
	}
}

// NewMemoryStore returns a store with safe defaults: a 15 minute TTL, 1000 runs
// in total and 10 runs per principal.
func NewMemoryStore[S any](opts ...StoreOption) *MemoryStore[S] {
	c := storeConfig{ttl: defaultTTL, max: defaultMaxRuns, maxPer: defaultMaxPerPrincipal}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return &MemoryStore[S]{runs: make(map[string]*Run[S]), ttl: c.ttl, max: c.max, maxPer: c.maxPer}
}

// Expiry returns the expiration time of a run created now.
func (m *MemoryStore[S]) Expiry() time.Time { return time.Now().Add(m.ttl) }

// Save stores a run, pruning expired ones and evicting the oldest when a limit
// is reached.
func (m *MemoryStore[S]) Save(_ context.Context, run *Run[S]) error {
	if run == nil || run.ID == "" {
		return fmt.Errorf("%w: run id is required", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneLocked()
	_, exists := m.runs[run.ID]
	if exists {
		m.runs[run.ID] = run
		return nil
	}
	for m.maxPer > 0 && m.countLocked(run.Principal) >= m.maxPer {
		m.evictLocked(run.Principal)
	}
	for m.max > 0 && len(m.runs) >= m.max {
		m.evictAnyLocked()
	}
	m.runs[run.ID] = run
	return nil
}

// Load returns a run to its owner. An expired or foreign run is ErrNotFound.
func (m *MemoryStore[S]) Load(_ context.Context, runID string, p Principal) (*Run[S], error) {
	if runID == "" {
		return nil, fmt.Errorf("%w: run id is required", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	run := m.runs[runID]
	if run == nil {
		return nil, ErrNotFound
	}
	if time.Now().After(run.Expires) {
		delete(m.runs, runID)
		return nil, ErrNotFound
	}
	if !run.Principal.matches(p) {
		return nil, ErrNotFound
	}
	return run, nil
}

func (m *MemoryStore[S]) pruneLocked() {
	now := time.Now()
	for id, run := range m.runs {
		if now.After(run.Expires) {
			delete(m.runs, id)
		}
	}
}

func (m *MemoryStore[S]) countLocked(p Principal) int {
	n := 0
	for _, run := range m.runs {
		if run.Principal.matches(p) {
			n++
		}
	}
	return n
}

// evictLocked drops the run of p that expires first.
func (m *MemoryStore[S]) evictLocked(p Principal) {
	var oldest *Run[S]
	for _, run := range m.runs {
		if !run.Principal.matches(p) {
			continue
		}
		if oldest == nil || run.Expires.Before(oldest.Expires) {
			oldest = run
		}
	}
	if oldest != nil {
		delete(m.runs, oldest.ID)
		return
	}
	m.evictAnyLocked()
}

// evictAnyLocked drops the run that expires first.
func (m *MemoryStore[S]) evictAnyLocked() {
	var oldest *Run[S]
	for _, run := range m.runs {
		if oldest == nil || run.Expires.Before(oldest.Expires) {
			oldest = run
		}
	}
	if oldest != nil {
		delete(m.runs, oldest.ID)
	}
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: run id: %w", err)
	}
	return "run-" + hex.EncodeToString(b[:]), nil
}
