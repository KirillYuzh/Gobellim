// Package usecase — бизнес-сценарии демо: чат, продолжение исследования и health.
package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"some-project/internal/agent"
	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/session"
	"some-project/internal/tools"
)

// Deps — зависимости сценариев.
type Deps struct {
	LLM    agent.LLM
	Policy *policy.Policy
	Logger *slog.Logger
	Budget entity.Budget
}

// UseCase знает модель, права, инструменты и сценарий. HTTP он не знает.
type UseCase struct {
	agent    *agent.Agent
	policy   *policy.Policy
	research *session.Session[researchState]
}

// New собирает каталог инструментов, сценарий и агент.
func New(d Deps) (*UseCase, error) {
	if d.LLM == nil || d.Policy == nil {
		return nil, fmt.Errorf("usecase: LLM и policy обязательны: %w", entity.ErrConfig)
	}
	u := &UseCase{policy: d.Policy, research: newResearch()}

	set, err := tools.NewRegistry().Register(
		tools.New("link", link,
			tools.Desc("Добавить ссылку в конец ответа. Приложение сделает это само."), tools.Read()),
		tools.New("start_research", u.startResearch,
			tools.Desc("Начать исследование по теме. param — параметр P, если пользователь его назвал."),
			tools.Read()),
	).Build()
	if err != nil {
		return nil, err
	}

	u.agent, err = agent.New(agent.Config{
		LLM:    d.LLM,
		Policy: d.Policy,
		Tools:  set,
		Budget: d.Budget,
		Logger: d.Logger,
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Chat — один ход диалога.
func (u *UseCase) Chat(ctx context.Context, req entity.ChatRequest) (entity.ChatResponse, error) {
	return u.agent.Chat(ctx, req)
}

// ResumeResearch продолжает приостановленный сценарий. Это не инструмент:
// модель не видит run_id, не выбирает момент продолжения и не толкует ответ.
func (u *UseCase) ResumeResearch(ctx context.Context, runID string, req entity.ResumeRequest) (entity.ResumeResponse, error) {
	if err := req.User.Validate(); err != nil {
		return entity.ResumeResponse{}, err
	}
	if !u.policy.Allows(req.User, "research") {
		return entity.ResumeResponse{}, fmt.Errorf("research: %w", entity.ErrUnauthorized)
	}

	res, err := u.research.Resume(ctx, session.ResumeRequest{
		RunID:     runID,
		ResumeID:  req.ResumeID,
		Principal: principal(req.User),
		Text:      req.Text,
		Choice:    req.Choice,
	})
	if err != nil {
		return entity.ResumeResponse{}, mapSessionError(err)
	}
	return toResumeResponse(res), nil
}

// toResumeResponse переводит результат сценария в ответ эндпоинта: либо готовый
// текст, либо следующий вопрос.
func toResumeResponse(res session.Result[researchState]) entity.ResumeResponse {
	out := entity.ResumeResponse{Status: string(res.Status), RunID: res.RunID}
	if res.Question == nil {
		out.Answer = res.State.Result
		return out
	}
	out.Question = &entity.Question{
		Prompt:  res.Question.Prompt,
		Input:   string(res.Question.Input),
		Options: res.Question.Options,
	}
	return out
}
