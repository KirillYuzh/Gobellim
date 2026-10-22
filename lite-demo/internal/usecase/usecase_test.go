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
	"some-project/internal/session"
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
		"support": {"chat", "tool:link", "tool:start_research", "research"},
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

func supportCtx() context.Context {
	return entity.WithUser(context.Background(), user("support"))
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

func TestChatReturnsQuestion(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "start_research", Args: map[string]any{"query": "go"}}}},
		{Content: "Какой параметр P?"},
	}}
	uc := newUseCase(t, llm, entity.Budget{})

	res, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("support"), Messages: msg("исследуй go")})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Status != "awaiting_user" || res.RunID == "" || res.Question == nil {
		t.Fatalf("response = %+v", res)
	}
	if res.Question.Input != string(session.InputText) {
		t.Fatalf("question = %+v", res.Question)
	}
	// run_id — ключ запуска приложения, модель его не видит.
	toolMsg := llm.requests[1].Messages[len(llm.requests[1].Messages)-1]
	if strings.Contains(toolMsg.Content, res.RunID) || strings.Contains(toolMsg.Content, "run_id") {
		t.Fatalf("tool message leaks the run id: %s", toolMsg.Content)
	}
}

// resume — ответ пользователя прямо в приложение, без модели.
func resume(t *testing.T, uc *UseCase, runID string, req entity.ResumeRequest) entity.ResumeResponse {
	t.Helper()
	res, err := uc.ResumeResearch(supportCtx(), runID, req)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	return res
}

func TestResearchAsksParamThenPath(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})

	start, err := uc.startResearch(supportCtx(), startIn{Query: "go modules"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if start.Status != "awaiting_user" || start.RunID == "" || !strings.Contains(start.Prompt, "параметр") {
		t.Fatalf("start = %+v", start)
	}

	path := resume(t, uc, start.RunID, entity.ResumeRequest{User: user("support"), Text: "go modules"})
	if path.Status != "awaiting_user" || path.RunID != start.RunID || path.Question == nil {
		t.Fatalf("path question = %+v", path)
	}
	if path.Question.Input != string(session.InputChoice) || len(path.Question.Options) != 2 {
		t.Fatalf("question = %+v", path.Question)
	}

	done := resume(t, uc, start.RunID, entity.ResumeRequest{User: user("support"), Choice: "A"})
	if done.Status != "completed" || !strings.Contains(done.Answer, "Путь A") {
		t.Fatalf("done = %+v", done)
	}
}

func TestResearchPathBAsksTopic(t *testing.T) {
	for choice, want := range map[string]string{"X": "Ответ X", "Y": "Ответ Y"} {
		t.Run(choice, func(t *testing.T) {
			uc := newUseCase(t, &fakeLLM{}, entity.Budget{})

			start, err := uc.startResearch(supportCtx(), startIn{Query: "go", Param: "go modules"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if start.Status != "awaiting_user" || !strings.Contains(start.Prompt, "путь") {
				t.Fatalf("start = %+v", start)
			}

			topic := resume(t, uc, start.RunID, entity.ResumeRequest{User: user("support"), Choice: "B"})
			if topic.Status != "awaiting_user" || topic.Question == nil || len(topic.Question.Options) != 2 {
				t.Fatalf("topic question = %+v", topic)
			}

			done := resume(t, uc, start.RunID, entity.ResumeRequest{User: user("support"), Choice: choice})
			if done.Status != "completed" || !strings.Contains(done.Answer, want) {
				t.Fatalf("done = %+v", done)
			}
		})
	}
}

func TestResearchRejectsBadChoice(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	start, err := uc.startResearch(supportCtx(), startIn{Query: "go", Param: "p"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = uc.ResumeResearch(supportCtx(), start.RunID, entity.ResumeRequest{User: user("support"), Choice: "C"})
	if !errors.Is(err, entity.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestResearchUnknownRun(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	_, err := uc.ResumeResearch(supportCtx(), "run-nope", entity.ResumeRequest{User: user("support"), Choice: "A"})
	if !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResearchForeignRunIsNotFound(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	start, err := uc.startResearch(supportCtx(), startIn{Query: "go"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	other := entity.User{ID: "u-2", Tenant: "acme", Roles: []string{"support"}}
	_, err = uc.ResumeResearch(entity.WithUser(context.Background(), other), start.RunID,
		entity.ResumeRequest{User: other, Text: "p"})
	if !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResearchResumeIsIdempotent(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	start, err := uc.startResearch(supportCtx(), startIn{Query: "go", Param: "p"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	req := entity.ResumeRequest{User: user("support"), ResumeID: "r1", Choice: "A"}
	first := resume(t, uc, start.RunID, req)
	again := resume(t, uc, start.RunID, req)
	if first != again {
		t.Fatalf("retry returned %+v, want %+v", again, first)
	}
	req.ResumeID, req.Choice = "r2", "B"
	if _, err := uc.ResumeResearch(supportCtx(), start.RunID, req); !errors.Is(err, entity.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for a finished run", err)
	}
}

func TestResearchNeedsPermission(t *testing.T) {
	llm := &fakeLLM{responses: []entity.Completion{
		{ToolCalls: []entity.ToolCall{{Name: "start_research", Args: map[string]any{"query": "go"}}}},
	}}
	uc := newUseCase(t, llm, entity.Budget{})

	_, err := uc.Chat(context.Background(), entity.ChatRequest{User: user("user"), Messages: msg("research go")})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestResumeNeedsPermission(t *testing.T) {
	uc := newUseCase(t, &fakeLLM{}, entity.Budget{})
	guest := entity.User{ID: "u-2", Tenant: "acme", Roles: []string{"guest"}}
	_, err := uc.ResumeResearch(entity.WithUser(context.Background(), guest), "run-x",
		entity.ResumeRequest{User: guest, Choice: "A"})
	if !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
