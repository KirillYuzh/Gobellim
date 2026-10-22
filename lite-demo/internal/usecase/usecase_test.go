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

func user(role string) entity.User {
	return entity.User{ID: "u-1", Tenant: "acme", Roles: []string{role}}
}

func newUseCase(t *testing.T, llm agent.LLM, budget entity.Budget) *UseCase {
	t.Helper()
	p := &policy.Policy{Default: "deny", Roles: map[string][]string{
		"user":    {"chat", "tool:link"},
		"support": {"chat", "tool:link", "tool:research"},
	}}
	uc, err := New(Deps{LLM: llm, Policy: p, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Budget: budget})
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
		{Content: "готово"},
	}}
	uc := newUseCase(t, llm, entity.Budget{})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("найди")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "ok" || res.Answer != "готово\n\nhttps://example.invalid/go-doc (mock)" {
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
	// Ссылку модель видеть не должна: иначе она вставит её второй раз.
	if got := last.Messages[len(last.Messages)-1].Content; got != `{"body":"Готово: ссылка добавлена в конец ответа."}` {
		t.Fatalf("tool message = %s", got)
	}
}

func TestChatStopsAtMaxSteps(t *testing.T) {
	llm := &fakeLLM{loop: true}
	uc := newUseCase(t, llm, entity.Budget{MaxSteps: 2, MaxToolCallsPerStep: 1})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("найди")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "max_steps" {
		t.Fatalf("status = %q, want max_steps", res.Status)
	}
}

func TestChatNeedsPermission(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	_, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("guest"), Messages: msg("hi")})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestResearchRoute(t *testing.T) {
	cases := []struct {
		branch, query, want string
	}{
		{"", "go modules", "a"},
		{"a", "go modules", "a"},
		{"b", "go modules", "b"},
		{"other", "go modules", "other"},
		{"b", "go modules functions", "functions"},
		{"b", "go modules потоки", "threads"},
		{"", "горутины в go", "threads"},
		{"b", "go modules функции", "functions"},
	}
	for _, tc := range cases {
		if got := researchRoute(&researchState{Branch: tc.branch, Query: tc.query}); got != tc.want {
			t.Fatalf("branch %q, query %q: route = %q, want %q", tc.branch, tc.query, got, tc.want)
		}
	}
}

func TestResearchFollowupBranches(t *testing.T) {
	for branch, want := range map[string]string{"functions": "функции", "threads": "горутины"} {
		st := researchState{Query: "go", Branch: branch}
		if err := researchFlow.Run(context.Background(), &st); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if st.Result.Code != "b" || !strings.Contains(st.Result.Body, want) {
			t.Fatalf("branch %q: result = %+v", branch, st.Result)
		}
	}
}

func TestResearchNeedsPermission(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "research", Args: map[string]any{"query": "go"}}}},
	}}
	uc := newUseCase(t, llm, entity.Budget{})

	_, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("research go")})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
