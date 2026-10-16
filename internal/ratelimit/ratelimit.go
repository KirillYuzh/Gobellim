// Package ratelimit provides a per-key token bucket with idle eviction.
package ratelimit

import (
	"sync"
	"time"
)

const gcWindow = 10 * time.Minute

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a per-key token bucket. Keys are created on demand and swept once
// they have been idle for gcWindow, so a stream of distinct keys cannot grow
// the map without bound.
type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
	lastGC  time.Time
}

// New returns a limiter that allows perSecond tokens per key per second with a
// bucket of burst tokens. Non-positive values fall back to one token per
// second and a burst of one.
func New(perSecond float64, burst int) *Limiter {
	if perSecond <= 0 {
		perSecond = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		rate:    perSecond,
		burst:   float64(burst),
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether one token is available for the key, consuming it.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastGC) < gcWindow {
		return
	}
	l.lastGC = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > gcWindow {
			delete(l.buckets, k)
		}
	}
}
