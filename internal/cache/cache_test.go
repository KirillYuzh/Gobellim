package cache

import (
	"testing"
	"time"
)

func TestGetPut(t *testing.T) {
	c := New[string](2, 0)

	if _, ok := c.Get("missing"); ok {
		t.Fatal("Get on an empty cache reported a hit")
	}
	c.Put("a", "1")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q, %v; want 1, true", v, ok)
	}
	c.Put("a", "2")
	if v, _ := c.Get("a"); v != "2" {
		t.Fatalf("Put did not update the value: got %q", v)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestTTL(t *testing.T) {
	now := time.Unix(0, 0)
	c := New[string](4, time.Minute)
	c.now = func() time.Time { return now }

	c.Put("a", "1")
	now = now.Add(30 * time.Second)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry expired before its TTL")
	}
	now = now.Add(31 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expired entry was returned")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0 after expiry", c.Len())
	}
}

func TestEviction(t *testing.T) {
	c := New[string](2, 0)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("c", "3")

	if _, ok := c.Get("a"); ok {
		t.Fatal("the least recently used entry survived eviction")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("the newest entry was evicted")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

func TestGetRefreshesRecency(t *testing.T) {
	c := New[string](2, 0)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Get("a")
	c.Put("c", "3")

	if _, ok := c.Get("b"); ok {
		t.Fatal("b was not the least recently used entry")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a was evicted despite a recent Get")
	}
}

func TestNewDefaultsCapacity(t *testing.T) {
	if got := New[int](0, 0).cap; got != DefaultMaxEntries {
		t.Fatalf("cap = %d, want %d", got, DefaultMaxEntries)
	}
}
