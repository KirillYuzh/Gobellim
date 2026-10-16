// Package audit is a mock of a second datastore the demo already owns. It shows
// the shape of a real adapter: kit does not know about it and never will.
package audit

import (
	"context"
	"sync"

	"github.com/gobellim/kit/demo/internal/domain"
)

// Memory is a concurrency-safe in-memory audit store. Swap it for your own
// database by implementing usecase.Auditor; nothing else changes.
type Memory struct {
	mu     sync.Mutex
	events []domain.AuditEvent
}

func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Record(_ context.Context, ev domain.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
	return nil
}

func (m *Memory) Stats(_ context.Context) (domain.AuditStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stats := domain.AuditStats{
		Events:   len(m.events),
		ByRole:   make(map[string]int),
		ByAction: make(map[string]int),
	}
	for _, ev := range m.events {
		stats.ByRole[string(ev.Role)]++
		stats.ByAction[ev.Action]++
	}
	return stats, nil
}
