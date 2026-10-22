package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"some-project/internal/agent"
	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/repository"
)

type fakeLLM struct {
	responses []entity.Completion
	requests  []entity.CompletionRequest
	loop      bool
}

func (f *fakeLLM) Chat(_ context.Context, req entity.CompletionRequest) (entity.Completion, error) {
	f.requests = append(f.requests, req)
	if f.loop {
		return entity.Completion{ToolCalls: []entity.ToolCall{{Name: "link"}}}, nil
	}
	if len(f.responses) == 0 {
		return entity.Completion{Content: "done"}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type fakeMock struct {
	lookup repository.Lookup
}

func (f *fakeMock) KB(_ context.Context, query string) ([]entity.Chunk, error) {
	return []entity.Chunk{{ID: "1", Text: "chunk:" + query, Score: 1, Mock: true}}, nil
}

func (f *fakeMock) Lookup(_ context.Context, _ string) (repository.Lookup, error) {
	if f.lookup.Code == "" {
		return repository.Lookup{Code: "a", Body: "body A"}, nil
	}
	return f.lookup, nil
}

func user(role string) entity.User {
	return entity.User{ID: "u-1", Tenant: "acme", Roles: []string{role}}
}

func newUseCase(t *testing.T, llm agent.LLM, mock Mock, budget entity.Budget) *UseCase {
	t.Helper()
	p := &policy.Policy{Default: "deny", Roles: map[string][]string{
		"user":    {"chat", "tool:link"},
		"support": {"chat", "tool:link", "tool:research"},
	}}
	uc, err := New(Deps{LLM: llm, Mock: mock, Policy: p, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Budget: budget})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return uc
}

func msg(text string) []entity.Message {
	return []entity.Message{{Role: "user", Content: text}}
}

func TestChatRunsToolsAndAnswers(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "link"}}},
		{Content: "готово https://example.invalid/go-doc (mock)"},
	}}
	uc := newUseCase(t, llm, &fakeMock{}, entity.Budget{})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("найди")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "ok" || !strings.Contains(res.Answer, "готово") || !strings.Contains(res.Answer, "example.invalid") {
		t.Fatalf("response = %+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "link" {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("llm calls = %d, want 2", len(llm.requests))
	}
	last := llm.requests[1]
	if len(last.Messages) == 0 || last.Messages[len(last.Messages)-1].Role != "tool" {
		t.Fatalf("no tool message was sent back to the model: %+v", last.Messages)
	}
}

func TestChatStopsForInput(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "research", Args: map[string]any{"query": "go"}}}},
	}}
	mock := &fakeMock{lookup: repository.Lookup{Code: "b", Question: "версия?"}}
	uc := newUseCase(t, llm, mock, entity.Budget{})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("support"), Messages: msg("найди")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "need_input" || res.Question != "версия?" || res.Answer != "" {
		t.Fatalf("response = %+v", res)
	}
}

func TestChatStopsAtMaxSteps(t *testing.T) {
	llm := &fakeLLM{loop: true}
	uc := newUseCase(t, llm, &fakeMock{}, entity.Budget{MaxSteps: 2, MaxToolCallsPerStep: 1})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("найди")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "max_steps" {
		t.Fatalf("status = %q, want max_steps", res.Status)
	}
}

func TestChatNeedsPermission(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, &fakeMock{}, entity.Budget{})
	_, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("guest"), Messages: msg("hi")})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestResearchWorkflowBranches(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "research", Args: map[string]any{"query": "go"}}}},
		{Content: "готово"},
	}}
	uc := newUseCase(t, llm, &fakeMock{}, entity.Budget{})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("support"), Messages: msg("research go")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("response = %+v", res)
	}
	// Во втором запросе уже есть результат инструмента research.
	var toolMsg string
	for _, m := range llm.requests[1].Messages {
		if m.Role == "tool" && m.Name == "research" {
			toolMsg = m.Content
		}
	}
	if !strings.Contains(toolMsg, `"code":"a"`) || !strings.Contains(toolMsg, `"data"`) {
		t.Fatalf("research result = %s, want code a and kb data", toolMsg)
	}
}

func TestResearchNeedsPermission(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "research", Args: map[string]any{"query": "go"}}}},
	}}
	uc := newUseCase(t, llm, &fakeMock{}, entity.Budget{})

	_, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("research go")})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
