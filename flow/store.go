package flow

import (
	"context"
	"sync"
	"time"
)

// Store makes resume single-use across replicas. Claim must be atomic: exactly
// one caller may claim a nonce. A non-nil error is also a failed claim.
//
// A flow without an explicit Store uses a process-local MemStore, so a token
// cannot be resumed twice in one replica. Sharing the claim between replicas
// still needs a real Store such as store/sql.
type Store interface {
	Claim(ctx context.Context, nonce string, expiresAt time.Time) (bool, error)
}

// MemStore is an in-process Store for tests and single-replica deployments.
// An expired nonce may be claimed again; Prune is not required.
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
	if exp, ok := m.claimed[nonce]; ok && time.Now().Before(exp) {
		return false, nil
	}
	m.claimed[nonce] = expiresAt
	return true, nil
}

// Forget drops expired nonces. Call it from a background job when the map is
// expected to grow; Claim itself stays O(1) either way.
func (m *MemStore) Forget(before time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for nonce, exp := range m.claimed {
		if exp.Before(before) {
			delete(m.claimed, nonce)
		}
	}
}
