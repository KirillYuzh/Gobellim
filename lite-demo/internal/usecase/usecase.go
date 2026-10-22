// Package usecase — бизнес-сценарии демо: чат и health.
package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"some-project/internal/agent"
	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/tools"
)

// Deps — зависимости сценариев.
type Deps struct {
	LLM    agent.LLM
	Policy *policy.Policy
	Logger *slog.Logger
	Budget entity.Budget
}

// UseCase знает модель, права и инструменты. HTTP он не знает.
type UseCase struct {
	agent *agent.Agent
}

// New собирает каталог инструментов и агент.
func New(d Deps) (*UseCase, error) {
	if d.LLM == nil || d.Policy == nil {
		return nil, fmt.Errorf("usecase: LLM и policy обязательны: %w", entity.ErrConfig)
	}
	u := &UseCase{}

	set, err := tools.NewRegistry().Register(
		tools.New("link", link,
			tools.Desc("Добавить ссылку в конец ответа. Приложение сделает это само."), tools.Read()),
		tools.New("research", u.research,
			tools.Desc("Mock-сценарий. Ветки: a — готовый факт; b — только если пользователь ещё не ответил, тогда задай ему вопрос про функции или потоки; "+
				"functions или threads — если пользователь уже ответил, какой темы он хочет; other — ошибка upstream."),
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
	return u.agent.Chat(ctx, req.User, req.Messages)
}
