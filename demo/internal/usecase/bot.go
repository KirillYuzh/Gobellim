package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/gobellim/kit/demo/internal/domain"
)

const systemPrompt = "Ты ассистент поддержки. Отвечай кратко и только по делу. " +
	"Факты о возврате, доставке, гарантии, оплате, поддержке и программе лояльности " +
	"бери только через kb_search и не выдумывай. " +
	"Делай не больше двух вызовов kb_search на один запрос: если данных нет, скажи об этом прямо. " +
	"Если нужного инструмента нет среди доступных, ответь, что не можешь выполнить запрос."

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
