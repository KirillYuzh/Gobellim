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

// Scripted is a model.Client that returns queued responses in order and records
// every request; it is safe for concurrent use.
type Scripted struct {
	mu       sync.Mutex
	queue    []model.Response
	requests []model.Request
	err      error
}

// Push appends resp to the queue and returns the receiver for chaining.
func (s *Scripted) Push(resp model.Response) *Scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, resp)
	return s
}

// Text queues an assistant response containing content.
func (s *Scripted) Text(content string) *Scripted {
	return s.Push(model.Response{Message: model.Message{Role: model.RoleAssistant, Content: content}})
}

// Call queues an assistant response containing a single tool call.
func (s *Scripted) Call(id, name, args string) *Scripted {
	return s.Push(model.Response{Message: model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: id, Name: name, Args: args}},
	}})
}

// Fail makes Chat return err once the queue is empty.
func (s *Scripted) Fail(err error) *Scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	return s
}

// Requests returns a copy of the requests received so far.
func (s *Scripted) Requests() []model.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Request(nil), s.requests...)
}

// Left returns the number of queued responses not yet consumed.
func (s *Scripted) Left() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Chat records req and returns the next queued response, or Fail's error when
// the queue is empty.
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

// Model implements model.Client and identifies this client as "scripted".
func (s *Scripted) Model() string { return "scripted" }

// ContextWindowed adds model.ContextWindow to a Scripted, for the guard-window
// vs context-window check (I-06).
type ContextWindowed struct {
	*Scripted
	Tokens int
}

// NewContextWindowed returns a Scripted model reporting the given context window.
func NewContextWindowed(tokens int) *ContextWindowed {
	return &ContextWindowed{Scripted: &Scripted{}, Tokens: tokens}
}

// ContextTokens implements model.ContextWindow.
func (c ContextWindowed) ContextTokens() int { return c.Tokens }

// Regional adds model.Regional to a Scripted; a region outside model.RegionRU
// makes PII masking mandatory (BR-106).
type Regional struct {
	*Scripted
	Where model.Region
}

// NewRegional returns a Scripted model in the given region.
func NewRegional(region model.Region) *Regional {
	return &Regional{Scripted: &Scripted{}, Where: region}
}

// Region implements model.Regional.
func (r Regional) Region() model.Region { return r.Where }
