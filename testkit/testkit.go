// Package testkit provides deterministic fakes for testing a Task and a Flow.
package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	gobellim "github.com/KirillYuzh/Gobellim"
)

// FakeModel plays scripted responses in order and records every request.
type FakeModel struct {
	mu        sync.Mutex
	responses []gobellim.Response
	calls     []gobellim.Request
}

// NewFakeModel returns a model that plays responses in order.
func NewFakeModel(responses ...gobellim.Response) *FakeModel {
	return &FakeModel{responses: append([]gobellim.Response(nil), responses...)}
}

// Chat records the request and returns the next scripted response.
func (m *FakeModel) Chat(_ context.Context, req gobellim.Request) (gobellim.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	if len(m.responses) == 0 {
		return gobellim.Response{}, fmt.Errorf("testkit: no scripted response left")
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

// Calls returns the recorded requests.
func (m *FakeModel) Calls() []gobellim.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]gobellim.Request(nil), m.calls...)
}

// Remaining reports how many scripted responses are left.
func (m *FakeModel) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.responses)
}

// Reply builds a final assistant message.
func Reply(content string) gobellim.Response {
	return gobellim.Response{
		Message:      gobellim.Message{Role: gobellim.RoleAssistant, Content: content},
		FinishReason: gobellim.FinishStop,
	}
}

// Call builds a tool-call response with a stable call ID.
func Call(name string, args any) gobellim.Response {
	return CallWithID("call_1", name, args)
}

// CallWithID builds a tool-call response with an explicit call ID.
func CallWithID(id, name string, args any) gobellim.Response {
	return gobellim.Response{
		Message: gobellim.Message{
			Role: gobellim.RoleAssistant,
			ToolCalls: []gobellim.ToolCall{{
				ID:   id,
				Name: name,
				Args: JSON(args),
			}},
		},
		FinishReason: gobellim.FinishToolCalls,
	}
}

// JSON marshals v for scripted arguments and results.
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// AllowAll authorizes every action.
var AllowAll = gobellim.AuthorizerFunc(func(context.Context, gobellim.Principal, string, gobellim.Resource) (bool, error) {
	return true, nil
})

// DenyAll authorizes no action.
var DenyAll = gobellim.AuthorizerFunc(func(context.Context, gobellim.Principal, string, gobellim.Resource) (bool, error) {
	return false, nil
})

// Recorder collects audit events.
type Recorder struct {
	mu     sync.Mutex
	events []gobellim.Event
}

// Emit appends one event.
func (r *Recorder) Emit(_ context.Context, e gobellim.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// Events returns a copy of the recorded events.
func (r *Recorder) Events() []gobellim.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gobellim.Event(nil), r.events...)
}

// Count reports how many events of kind were recorded.
func (r *Recorder) Count(kind gobellim.EventKind) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}
