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
	MaxToolCalls    int
	MaxModelCalls   int
	MaxContextItems int
	MaxWallTime     time.Duration
}

const (
	defaultMaxInputBytes   = 32 << 10
	defaultMaxOutputBytes  = 16 << 10
	defaultMaxArgBytes     = 8 << 10
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
		MaxToolCalls:    defaultMaxToolCalls,
		MaxModelCalls:   defaultMaxModelCalls,
		MaxContextItems: defaultMaxContextItems,
		MaxWallTime:     defaultMaxWallTime,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxInputBytes == 0 {
		l.MaxInputBytes = d.MaxInputBytes
	}
	if l.MaxOutputBytes == 0 {
		l.MaxOutputBytes = d.MaxOutputBytes
	}
	if l.MaxArgBytes == 0 {
		l.MaxArgBytes = d.MaxArgBytes
	}
	if l.MaxToolCalls == 0 {
		l.MaxToolCalls = d.MaxToolCalls
	}
	if l.MaxModelCalls == 0 {
		l.MaxModelCalls = d.MaxModelCalls
	}
	if l.MaxContextItems == 0 {
		l.MaxContextItems = d.MaxContextItems
	}
	if l.MaxWallTime == 0 {
		l.MaxWallTime = d.MaxWallTime
	}
	return l
}

func (l Limits) validate() error {
	switch {
	case l.MaxInputBytes < 0, l.MaxOutputBytes < 0, l.MaxArgBytes < 0,
		l.MaxToolCalls < 0, l.MaxModelCalls < 0, l.MaxContextItems < 0:
		return fmt.Errorf("%w: limits must not be negative", ErrInvalidConfig)
	case l.MaxWallTime < 0:
		return fmt.Errorf("%w: MaxWallTime must not be negative", ErrInvalidConfig)
	}
	return nil
}
