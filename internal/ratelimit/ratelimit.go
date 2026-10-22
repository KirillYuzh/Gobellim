package ratelimit

import (
	"sync"
	"time"
)

const (
	gcWindow = 10 * time.Minute
	// maxBuckets hard-caps the bucket map. One principal per key means an
	// unbounded map is an unbounded allocation driven by caller input.
	maxBuckets = 1 << 16
)

type bucket struct {
	tokens float64
	last   time.Time
}

type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
	lastGC  time.Time
}

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

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			l.evictOldest()
		}
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

// evictOldest drops the least recently used bucket.
// ponytail: O(n) scan only while at the cap; swap for a heap if that ever matters.
func (l *Limiter) evictOldest() {
	var (
		oldestKey string
		oldest    time.Time
		found     bool
	)
	for k, b := range l.buckets {
		if !found || b.last.Before(oldest) {
			oldestKey, oldest, found = k, b.last, true
		}
	}
	if found {
		delete(l.buckets, oldestKey)
	}
}
