package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

func fixedClock(l *Limiter, at *time.Time) {
	l.now = func() time.Time { return *at }
}

func TestAllowTokenBucket(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 2)
	fixedClock(l, &now)

	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("burst of 2 must allow two calls")
	}
	if l.Allow("a") {
		t.Fatal("third call within the same instant must be limited")
	}
	if !l.Allow("b") {
		t.Fatal("principals must have independent buckets")
	}
	now = now.Add(time.Second)
	if !l.Allow("a") {
		t.Fatal("one second at 1/s must refill one token")
	}
	if l.Allow("a") {
		t.Fatal("refill is 1/s")
	}
}

func TestBucketsBounded(t *testing.T) {
	l := New(1, 1)
	for i := 0; i <= maxBuckets+100; i++ {
		l.Allow(strconv.Itoa(i))
	}
	if got := len(l.buckets); got > maxBuckets {
		t.Fatalf("buckets = %d, want <= %d", got, maxBuckets)
	}
	if !l.Allow("fresh") {
		t.Fatal("a new principal must still get a bucket at the cap")
	}
}

func TestEvictsIdleBucketFirst(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 1)
	fixedClock(l, &now)

	l.Allow("idle")
	now = now.Add(time.Second)
	for i := 0; i < maxBuckets; i++ {
		l.Allow("fresh-" + strconv.Itoa(i))
	}
	if _, ok := l.buckets["idle"]; ok {
		t.Fatal("the least recently used bucket survived eviction")
	}
	if len(l.buckets) > maxBuckets {
		t.Fatalf("buckets = %d, want <= %d", len(l.buckets), maxBuckets)
	}
}
