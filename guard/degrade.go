package guard

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gobellim/kit/internal/core"
)

const DefaultDegradedWarnInterval = time.Minute

type degradationPolicy struct {
	everywhere map[string]bool
	byRail     map[Rail]map[string]bool
}

func newDegradationPolicy(flat []string, byRail map[Rail][]string) (degradationPolicy, error) {
	p := degradationPolicy{
		everywhere: make(map[string]bool, len(flat)),
		byRail:     make(map[Rail]map[string]bool, len(byRail)),
	}
	for _, name := range flat {
		p.everywhere[name] = true
	}
	for rail, names := range byRail {
		if !ValidRail(rail) {
			return degradationPolicy{}, fmt.Errorf("guard: unknown rail %q in the degradation policy: %w",
				string(rail), core.ErrInvalidConfig)
		}
		set := make(map[string]bool, len(names))
		for _, name := range names {
			set[name] = true
		}
		p.byRail[rail] = set
	}
	return p, nil
}

func (p degradationPolicy) allows(rail Rail, layerName, detectorName string) bool {
	if p.everywhere[layerName] || p.everywhere[detectorName] {
		return true
	}
	byRail, ok := p.byRail[rail]
	if !ok {
		return false
	}
	return byRail[layerName] || byRail[detectorName]
}

type warner struct {
	mu       sync.Mutex
	log      *slog.Logger
	interval time.Duration
	now      func() time.Time
	last     map[Rail]time.Time
}

func newWarner(log *slog.Logger, interval time.Duration, now func() time.Time) *warner {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &warner{log: log, interval: interval, now: now, last: map[Rail]time.Time{}}
}

func (w *warner) warn(rail Rail, version, reason string) {
	at := w.now()
	w.mu.Lock()
	last, seen := w.last[rail]
	if seen && at.Sub(last) < w.interval {
		w.mu.Unlock()
		return
	}
	w.last[rail] = at
	w.mu.Unlock()
	w.log.Warn("guard: layer degraded, verdict produced without it",
		"rail", string(rail),
		"version", version,
		"reason", reason)
}
