package gobellim

import (
	"context"
	"sync"
	"time"
)

// ClaimStore makes an approved action single-use. Claim must be atomic: exactly
// one caller may claim a nonce. A non-nil error is a failed claim, never an
// allow.
//
// flow.Store is the same interface, and store/sql plus flow.MemStore satisfy it
// without importing this package.
type ClaimStore interface {
	Claim(ctx context.Context, nonce string, expiresAt time.Time) (bool, error)
}

// MemClaims is the default in-process ClaimStore: one approved action executes
// once per process. Expired nonces are dropped in amortized O(1).
//
// Now is the clock used to decide whether a stored nonce is still live. An
// Engine and a Flow point it at their own clock, so a deterministic clock
// cannot disagree with its expiry checks. A nil value means time.Now.
type MemClaims struct {
	Now     func() time.Time
	mu      sync.Mutex
	claimed map[string]time.Time
	n       int
}

const claimsSweepEvery = 512

// NewMemClaims returns an empty in-process ClaimStore.
func NewMemClaims() *MemClaims {
	return &MemClaims{claimed: make(map[string]time.Time)}
}

// Claim records nonce once and reports whether this caller won the race.
func (m *MemClaims) Claim(_ context.Context, nonce string, expiresAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if exp, ok := m.claimed[nonce]; ok && now.Before(exp) {
		return false, nil
	}
	m.claimed[nonce] = expiresAt
	m.n++
	if m.n%claimsSweepEvery == 0 {
		m.forgetLocked(now)
	}
	return true, nil
}

// Forget drops expired nonces. Errors from a forgotten claim are impossible;
// the map only ever loses entries that can no longer be claimed.
func (m *MemClaims) Forget(before time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forgetLocked(before)
}

func (m *MemClaims) forgetLocked(before time.Time) {
	for nonce, exp := range m.claimed {
		if exp.Before(before) {
			delete(m.claimed, nonce)
		}
	}
}

func (m *MemClaims) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}
