package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/gobellim/kit/demo/internal/domain"
)

const systemPrompt = "Ты ассистент поддержки. Отвечай кратко и только по делу. " +
	"Для погоды и роли пользователя вызывай инструменты, не выдумывай данные."

type Bot struct {
	engine ChatEngine
}

func NewBot(engine ChatEngine) *Bot {
	return &Bot{engine: engine}
}

func (b *Bot) Ask(ctx context.Context, principal domain.Principal, prompt string) (domain.Answer, error) {
	if principal.UserID == "" {
		return domain.Answer{}, domain.ErrUnauthorized
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return domain.Answer{}, fmt.Errorf("prompt is required: %w", domain.ErrInvalidInput)
	}
	if len([]rune(prompt)) > 4000 {
		return domain.Answer{}, fmt.Errorf("prompt is too long: %w", domain.ErrInvalidInput)
	}
	return b.engine.Ask(ctx, ChatRequest{Principal: principal, System: systemPrompt, Prompt: prompt})
}
