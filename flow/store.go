package flow

import (
	"context"
	"sync"
	"time"
)

// Store makes resume single-use across replicas. Claim must be atomic: exactly
// one caller may claim a nonce. A non-nil error is also a failed claim.
//
// L0 (no Store) keeps resume single-use only inside one process; L1 adds this
// interface and closes the replay window between replicas.
type Store interface {
	Claim(ctx context.Context, nonce string, expiresAt time.Time) (bool, error)
}

// MemStore is an in-process Store for tests and single-replica deployments.
// Expired nonces are dropped lazily on Claim.
type MemStore struct {
	mu      sync.Mutex
	claimed map[string]time.Time
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{claimed: make(map[string]time.Time)}
}

// Claim records nonce once and reports whether this caller won the race.
func (m *MemStore) Claim(_ context.Context, nonce string, expiresAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for n, exp := range m.claimed {
		if now.After(exp) {
			delete(m.claimed, n)
		}
	}
	if _, ok := m.claimed[nonce]; ok {
		return false, nil
	}
	m.claimed[nonce] = expiresAt
	return true, nil
}
