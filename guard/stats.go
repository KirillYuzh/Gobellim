package guard

import (
	"sync"
	"time"
)

// LatencyBucketMillis are the upper bounds of the guard latency histogram, in
// milliseconds. A scan falls into the first bucket it does not exceed; the final
// bucket counts everything slower than the last bound, so a snapshot always has
// len(LatencyBucketMillis)+1 counters.
var LatencyBucketMillis = []float64{0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250}

type Stats struct {
	Scans        uint64
	Blocked      uint64
	Truncated    uint64
	Degradations map[Rail]uint64

	// Latency is a histogram of scan durations, aligned with LatencyBucketMillis.
	Latency []uint64
}

type counters struct {
	mu       sync.Mutex
	scans    uint64
	blocked  uint64
	trunc    uint64
	degraded map[Rail]uint64
	latency  []uint64
}

func (c *counters) count(rail Rail, v Verdict) {
	c.mu.Lock()
	c.scans++
	switch v.Action {
	case Reject:
		c.blocked++
	}
	if v.Truncated {
		c.trunc++
	}
	if v.Degraded {
		if c.degraded == nil {
			c.degraded = make(map[Rail]uint64, 1)
		}
		c.degraded[rail]++
	}
	c.mu.Unlock()
}

func (c *counters) observeLatency(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	i := len(LatencyBucketMillis)
	for j, ub := range LatencyBucketMillis {
		if ms <= ub {
			i = j
			break
		}
	}
	c.mu.Lock()
	if c.latency == nil {
		c.latency = make([]uint64, len(LatencyBucketMillis)+1)
	}
	c.latency[i]++
	c.mu.Unlock()
}

func (c *counters) snapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Stats{Scans: c.scans, Blocked: c.blocked, Truncated: c.trunc}
	if len(c.degraded) > 0 {
		s.Degradations = make(map[Rail]uint64, len(c.degraded))
		for rail, n := range c.degraded {
			s.Degradations[rail] = n
		}
	}
	if len(c.latency) > 0 {
		s.Latency = make([]uint64, len(c.latency))
		copy(s.Latency, c.latency)
	}
	return s
}

func (p *Pipeline) Stats() Stats { return p.counters.snapshot() }

func (p *Pipeline) count(rail Rail, v Verdict) { p.counters.count(rail, v) }

func (p *Pipeline) observeLatency(d time.Duration) { p.counters.observeLatency(d) }
