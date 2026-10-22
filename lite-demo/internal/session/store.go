package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"some-project/internal/workflow"
)

const (
	// defaultTTL is how long a run stays resumable.
	defaultTTL = 15 * time.Minute
	// defaultMaxRuns bounds the whole store.
	defaultMaxRuns = 1000
	// defaultMaxPerPrincipal bounds the runs of one principal.
	defaultMaxPerPrincipal = 10
)

// Run is one stored scenario: its position, its owner and the last result.
type Run[S any] struct {
	mu        sync.Mutex
	ID        string
	Principal Principal
	Exec      *workflow.Execution[S]
	Expires   time.Time

	done     bool
	resumeID string
	last     Result[S]
}

// RunRef addresses a run and the principal that wants it.
type RunRef struct {
	RunID     string
	Principal Principal
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

// expiry returns the expiration time of a run created now.
func (m *MemoryStore[S]) expiry() time.Time { return time.Now().Add(m.ttl) }

// Save stores a run, pruning expired ones and evicting the oldest when a limit
// is reached.
func (m *MemoryStore[S]) Save(run *Run[S]) error {
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
func (m *MemoryStore[S]) Load(ref RunRef) (*Run[S], error) {
	if ref.RunID == "" {
		return nil, fmt.Errorf("%w: run id is required", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	run := m.runs[ref.RunID]
	if run == nil {
		return nil, ErrNotFound
	}
	if time.Now().After(run.Expires) {
		delete(m.runs, ref.RunID)
		return nil, ErrNotFound
	}
	if !run.Principal.matches(ref.Principal) {
		return nil, ErrNotFound
	}
	return run, nil
}

// Len reports how many runs are stored.
func (m *MemoryStore[S]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runs)
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

func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "run-0"
	}
	return "run-" + hex.EncodeToString(b[:])
}
