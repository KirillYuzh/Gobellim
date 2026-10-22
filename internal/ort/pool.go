package ort

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

type Session interface {
	Close() error
}

var (
	ErrQueueTimeout = errors.New("kit: ort queue timeout")
	ErrPoolClosed   = errors.New("kit: ort pool closed")
)

const DefaultWait = 2 * time.Second

type Pool struct {
	ch     chan Session
	done   chan struct{}
	wait   time.Duration
	all    []Session
	mu     sync.Mutex
	closed bool
}

var ErrNoSessions = errors.New("ort: no sessions")

func NewPool(sessions []Session, wait time.Duration) (*Pool, error) {
	if len(sessions) == 0 {
		return nil, fmt.Errorf("ort: pool needs at least one session: %w", ErrNoSessions)
	}
	if wait <= 0 {
		wait = DefaultWait
	}
	ch := make(chan Session, len(sessions))
	for _, s := range sessions {
		ch <- s
	}
	return &Pool{ch: ch, done: make(chan struct{}), wait: wait, all: append([]Session(nil), sessions...)}, nil
}

func (p *Pool) Acquire(ctx context.Context) (Session, error) {
	if p.isClosed() {
		return nil, ErrPoolClosed
	}
	timer := time.NewTimer(p.wait)
	defer timer.Stop()
	select {
	case s := <-p.ch:
		if p.isClosed() {
			_ = s.Close()
			return nil, ErrPoolClosed
		}
		return s, nil
	case <-p.done:
		return nil, ErrPoolClosed
	case <-timer.C:
		return nil, fmt.Errorf("ort: acquire: %w", ErrQueueTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Pool) Release(s Session) {
	if s == nil {
		return
	}
	if p.isClosed() {
		// Close already drained the pool; a session returned after it would
		// never be closed, so close it here.
		_ = s.Close()
		return
	}
	select {
	case p.ch <- s:
	default:
		// Double release or a session from another pool: close it instead of
		// leaking the native arena.
		_ = s.Close()
	}
}

type Stats struct {
	Size      int
	Available int
	Wait      time.Duration
}

func (p *Pool) Stats() Stats {
	return Stats{Size: len(p.all), Available: len(p.ch), Wait: p.wait}
}

func (p *Pool) Close() error {
	var err error
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.done)
	p.mu.Unlock()

	var sessions []Session
	for {
		select {
		case s := <-p.ch:
			sessions = append(sessions, s)
		default:
			for _, s := range sessions {
				if cerr := s.Close(); cerr != nil && err == nil {
					err = cerr
				}
			}
			return err
		}
	}
}

func (p *Pool) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func Threads(numSessions int) int {
	if numSessions < 1 {
		numSessions = 1
	}
	n := runtime.NumCPU() / numSessions
	if n < 1 {
		n = 1
	}
	return n
}
