// Package usecase — бизнес-сценарии демо: чат, followup и health.
package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"some-project/internal/agent"
	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/repository"
	"some-project/internal/tools"
)

// Mock — второй сервис, к которому ходят инструменты.
type Mock interface {
	KB(ctx context.Context, query string) ([]entity.Chunk, error)
	Lookup(ctx context.Context, query string) (repository.Lookup, error)
	Followup(ctx context.Context, query, input string) (repository.Followup, error)
}

// Deps — зависимости сценариев.
type Deps struct {
	LLM    agent.LLM
	Mock   Mock
	Policy *policy.Policy
	Logger *slog.Logger
	Budget entity.Budget
}

// UseCase знает модель, права и инструменты. HTTP он не знает.
type UseCase struct {
	agent *agent.Agent
	mock  Mock
}

// New собирает каталог инструментов и агент.
func New(d Deps) (*UseCase, error) {
	if d.LLM == nil || d.Mock == nil || d.Policy == nil {
		return nil, fmt.Errorf("usecase: LLM, mock и policy обязательны: %w", entity.ErrConfig)
	}
	u := &UseCase{mock: d.Mock}

	set, err := tools.NewRegistry().Register(
		tools.New("link", link,
			tools.Desc("Добавить заготовленную mock-ссылку в конец ответа."), tools.Read()),
		tools.New("kb_search", u.kbSearch,
			tools.Desc("Поиск по mock-базе знаний о Go. Возвращает чанки со score."), tools.Read()),
		tools.New("remote_lookup", u.remoteLookup,
			tools.Desc("Запрос к mock-сервису. Возвращает код a, b или other."), tools.Egress()),
		tools.New("remote_followup", u.remoteFollowup,
			tools.Desc("Второй запрос к mock-сервису с уточнением пользователя."),
			tools.Egress(), tools.Perm("tool:remote_lookup")),
		tools.New("research", u.research,
			tools.Desc("Сценарий: база знаний, затем mock-upstream, затем ветка по коду ответа."),
			tools.Egress()),
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
	return u.agent.Chat(ctx, req.User, req.Messages)
}

// Followup — один запрос к стороннему сервису через тот же инструмент, что
// вызывает модель.
func (u *UseCase) Followup(ctx context.Context, req entity.FollowupRequest) (entity.ChatResponse, error) {
	res, err := u.agent.CallTool(ctx, req.User, "remote_followup", map[string]any{
		"query": req.Query,
		"input": req.Input,
	})
	if err != nil {
		return entity.ChatResponse{}, err
	}
	if res.Error != "" {
		return entity.ChatResponse{}, fmt.Errorf("followup: %s: %w", res.Error, entity.ErrUpstream)
	}
	return entity.ChatResponse{Status: "ok", Answer: res.Body}, nil
}
