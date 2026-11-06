package gobellim

import (
	"context"
	"fmt"
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
// once per process. It is bounded, because an unbounded map turns a convenient
// default into a memory leak. When the bound is reached and no entry has
// expired, Claim returns ErrClaimStoreFull; use store/sql or another shared
// store for production volumes.
//
// Now is the clock used to decide whether a stored nonce is still live. An
// Engine and a Flow point it at their own clock, so a deterministic clock
// cannot disagree with its expiry checks. A nil value means time.Now.
type MemClaims struct {
	Now        func() time.Time
	MaxEntries int

	mu      sync.Mutex
	claimed map[string]time.Time
	n       int
}

const (
	claimsSweepEvery = 512
	// defaultMaxClaims bounds the in-process store. One hour of TTL at a few tens
	// of bytes per entry keeps a full map in the tens of megabytes, and a store
	// that is full fails closed instead of evicting a live nonce. A service that
	// approves faster than this should use store/sql and a shared store anyway.
	defaultMaxClaims = 1 << 18
)

// NewMemClaims returns an empty in-process ClaimStore.
func NewMemClaims() *MemClaims {
	return &MemClaims{claimed: make(map[string]time.Time), MaxEntries: defaultMaxClaims}
}

// Claim records nonce once and reports whether this caller won the race.
func (m *MemClaims) Claim(_ context.Context, nonce string, expiresAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if exp, ok := m.claimed[nonce]; ok && now.Before(exp) {
		return false, nil
	}
	m.n++
	if m.n%m.sweepEveryLocked() == 0 {
		m.forgetLocked(now)
	}
	if limit := m.max(); limit > 0 && len(m.claimed) >= limit {
		m.forgetLocked(now)
		if len(m.claimed) >= limit {
			// A full store must fail closed. Silently evicting a live nonce
			// would make an already-used approval executable again.
			return false, fmt.Errorf("%w: %d live entries", ErrClaimStoreFull, len(m.claimed))
		}
	}
	m.claimed[nonce] = expiresAt
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

// sweepEveryLocked keeps the amortised cost of the sweep constant. A fixed
// interval turns a full-map scan into O(n/interval) work per claim, so the
// store would slow down exactly when it is busiest. Scanning once per eighth of
// the current size keeps that at a handful of map operations, at the price of a
// bounded pause when the map is at its cap.
func (m *MemClaims) sweepEveryLocked() int {
	n := len(m.claimed) / 8
	if n < claimsSweepEvery {
		return claimsSweepEvery
	}
	return n
}

func (m *MemClaims) max() int {
	if m.MaxEntries < 0 {
		return 0
	}
	if m.MaxEntries == 0 {
		return defaultMaxClaims
	}
	return m.MaxEntries
}

func (m *MemClaims) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}
