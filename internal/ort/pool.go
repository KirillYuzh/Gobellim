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

var ErrQueueTimeout = errors.New("kit: ort queue timeout")

const DefaultWait = 2 * time.Second

type Pool struct {
	ch   chan Session
	wait time.Duration
	all  []Session
	once sync.Once
}

func NewPool(sessions []Session, wait time.Duration) (*Pool, error) {
	if len(sessions) == 0 {
		return nil, fmt.Errorf("ort: pool needs at least one session: %w", errors.New("no sessions"))
	}
	if wait <= 0 {
		wait = DefaultWait
	}
	ch := make(chan Session, len(sessions))
	for _, s := range sessions {
		ch <- s
	}
	return &Pool{ch: ch, wait: wait, all: sessions}, nil
}

func (p *Pool) Acquire(ctx context.Context) (Session, error) {
	timer := time.NewTimer(p.wait)
	defer timer.Stop()
	select {
	case s := <-p.ch:
		return s, nil
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
	p.ch <- s
}

func (p *Pool) Close() error {
	var err error
	p.once.Do(func() {
		for _, s := range p.all {
			if cerr := s.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	})
	return err
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
