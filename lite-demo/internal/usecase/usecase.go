// Package usecase — бизнес-сценарии демо: чат и health.
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

// Mock — второй сервис, к которому ходят шаги сценария.
type Mock interface {
	KB(ctx context.Context, query string) ([]entity.Chunk, error)
	Lookup(ctx context.Context, query string) (repository.Lookup, error)
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
			tools.Desc("Добавить ссылку на документацию в конец ответа. Приложение сделает это само."), tools.Read()),
		tools.New("research", u.research,
			tools.Desc("База знаний, затем mock-upstream и ветка по коду ответа."), tools.Egress()),
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
