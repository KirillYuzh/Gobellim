// Package kittest is kit's test kit: a deterministic fake model client, a
// scripted guard detector, and the conformance tests a plugin must pass.
//
// It imports kit's ports (model, guard, trace), never the kit facade, so plugins
// and kit's own internal tests can both use it.
package kittest

import (
	"context"
	"fmt"
	"sync"

	"github.com/KirillYuzh/Gobellim/model"
)

type Scripted struct {
	mu       sync.Mutex
	queue    []model.Response
	requests []model.Request
	err      error
}

func (s *Scripted) Push(resp model.Response) *Scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, resp)
	return s
}

func (s *Scripted) Text(content string) *Scripted {
	return s.Push(model.Response{Message: model.Message{Role: model.RoleAssistant, Content: content}})
}

func (s *Scripted) Call(id, name, args string) *Scripted {
	return s.Push(model.Response{Message: model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: id, Name: name, Args: args}},
	}})
}

func (s *Scripted) Fail(err error) *Scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	return s
}

func (s *Scripted) Requests() []model.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Request(nil), s.requests...)
}

func (s *Scripted) Left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *Scripted) Chat(_ context.Context, req model.Request) (model.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if len(s.queue) == 0 {
		if s.err != nil {
			return model.Response{}, s.err
		}
		return model.Response{}, fmt.Errorf("kittest: the scripted model ran out of responses")
	}
	resp := s.queue[0]
	s.queue = s.queue[1:]
	return resp, nil
}

func (s *Scripted) Model() string { return "scripted" }

// ContextWindowed adds model.ContextWindow to a Scripted, for the guard-window
// vs context-window check (I-06).
type ContextWindowed struct {
	*Scripted
	Tokens int
}

func NewContextWindowed(tokens int) *ContextWindowed {
	return &ContextWindowed{Scripted: &Scripted{}, Tokens: tokens}
}

func (c ContextWindowed) ContextTokens() int { return c.Tokens }

// Regional adds model.Regional to a Scripted; a region outside model.RegionRU
// makes PII masking mandatory (BR-106).
type Regional struct {
	*Scripted
	Where model.Region
}

func NewRegional(region model.Region) *Regional {
	return &Regional{Scripted: &Scripted{}, Where: region}
}

func (r Regional) Region() model.Region { return r.Where }
