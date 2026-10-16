package cache

import (
	"container/list"
	"sync"
	"time"
)

const DefaultMaxEntries = 1024

type entry[V any] struct {
	key     string
	val     V
	expires time.Time
}

type LRU[V any] struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	now   func() time.Time
	ll    *list.List
	items map[string]*list.Element
}

func New[V any](capacity int, ttl time.Duration) *LRU[V] {
	if capacity <= 0 {
		capacity = DefaultMaxEntries
	}
	return &LRU[V]{
		cap:   capacity,
		ttl:   ttl,
		now:   time.Now,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
	}
}

func (c *LRU[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*entry[V])
	if c.ttl > 0 && c.now().After(e.expires) {
		c.remove(el)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *LRU[V]) Put(key string, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry[V])
		e.val = val
		e.expires = c.expiry()
		c.ll.MoveToFront(el)
		return
	}
	e := &entry[V]{key: key, val: val, expires: c.expiry()}
	c.items[key] = c.ll.PushFront(e)
	if c.ll.Len() > c.cap {
		if oldest := c.ll.Back(); oldest != nil {
			c.remove(oldest)
		}
	}
}

func (c *LRU[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *LRU[V]) expiry() time.Time {
	if c.ttl <= 0 {
		return time.Time{}
	}
	return c.now().Add(c.ttl)
}

func (c *LRU[V]) remove(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry[V]).key)
}
