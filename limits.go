package gobellim

import (
	"fmt"
	"time"
)

// Limits are the deterministic budgets of one Task run. They are enforced by
// the runtime, not by the prompt.
type Limits struct {
	MaxInputBytes   int
	MaxOutputBytes  int
	MaxArgBytes     int
	MaxContextBytes int
	MaxToolCalls    int
	MaxModelCalls   int
	MaxContextItems int
	MaxWallTime     time.Duration
}

const (
	defaultMaxInputBytes   = 32 << 10
	defaultMaxOutputBytes  = 16 << 10
	defaultMaxArgBytes     = 8 << 10
	defaultMaxContextBytes = 128 << 10
	defaultMaxToolCalls    = 3
	defaultMaxModelCalls   = 4
	defaultMaxContextItems = 16
	defaultMaxWallTime     = 60 * time.Second
)

// DefaultLimits returns the conservative defaults. The numbers are starting
// points; benchmark them before advertising them.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:   defaultMaxInputBytes,
		MaxOutputBytes:  defaultMaxOutputBytes,
		MaxArgBytes:     defaultMaxArgBytes,
		MaxContextBytes: defaultMaxContextBytes,
		MaxToolCalls:    defaultMaxToolCalls,
		MaxModelCalls:   defaultMaxModelCalls,
		MaxContextItems: defaultMaxContextItems,
		MaxWallTime:     defaultMaxWallTime,
	}
}

// withFallback fills every zero field from base: the engine passes DefaultLimits,
// a task passes the engine's limits so it can tighten one budget without
// restating the rest.
func (l Limits) withFallback(base Limits) Limits {
	if l.MaxInputBytes == 0 {
		l.MaxInputBytes = base.MaxInputBytes
	}
	if l.MaxOutputBytes == 0 {
		l.MaxOutputBytes = base.MaxOutputBytes
	}
	if l.MaxArgBytes == 0 {
		l.MaxArgBytes = base.MaxArgBytes
	}
	if l.MaxContextBytes == 0 {
		l.MaxContextBytes = base.MaxContextBytes
	}
	if l.MaxToolCalls == 0 {
		l.MaxToolCalls = base.MaxToolCalls
	}
	if l.MaxModelCalls == 0 {
		l.MaxModelCalls = base.MaxModelCalls
	}
	if l.MaxContextItems == 0 {
		l.MaxContextItems = base.MaxContextItems
	}
	if l.MaxWallTime == 0 {
		l.MaxWallTime = base.MaxWallTime
	}
	return l
}

func (l Limits) validate() error {
	switch {
	case l.MaxInputBytes < 0, l.MaxOutputBytes < 0, l.MaxArgBytes < 0, l.MaxContextBytes < 0,
		l.MaxToolCalls < 0, l.MaxModelCalls < 0, l.MaxContextItems < 0:
		return fmt.Errorf("%w: limits must not be negative", ErrInvalidConfig)
	case l.MaxWallTime < 0:
		return fmt.Errorf("%w: MaxWallTime must not be negative", ErrInvalidConfig)
	}
	return nil
}
