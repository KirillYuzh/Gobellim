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

const maxPromptRunes = 4000

// Bot is the chat use case. It validates input and delegates the safe call to the
// engine; the audit port is optional so the use case stays testable without it.
type Bot struct {
	engine ChatEngine
	audit  Auditor
}

func NewBot(engine ChatEngine, audit Auditor) *Bot {
	return &Bot{engine: engine, audit: audit}
}

func (b *Bot) Ask(ctx context.Context, principal domain.Principal, prompt string) (domain.Answer, error) {
	if err := check(principal, prompt); err != nil {
		return domain.Answer{}, err
	}
	answer, err := b.engine.Ask(ctx, ChatRequest{Principal: principal, System: systemPrompt, Prompt: prompt})
	if err != nil {
		return domain.Answer{}, err
	}
	b.record(ctx, principal, "ask")
	return answer, nil
}

// Triage returns a typed Ticket instead of free text: kit derives the JSON Schema
// from domain.Ticket and validates the answer against it before decoding.
func (b *Bot) Triage(ctx context.Context, principal domain.Principal, text string) (domain.Ticket, error) {
	if err := check(principal, text); err != nil {
		return domain.Ticket{}, err
	}
	ticket, err := b.engine.Triage(ctx, principal, text)
	if err != nil {
		return domain.Ticket{}, err
	}
	b.record(ctx, principal, "triage")
	return ticket, nil
}

func (b *Bot) record(ctx context.Context, p domain.Principal, action string) {
	if b.audit == nil {
		return
	}
	// Analytics must never fail the user's request.
	_ = b.audit.Record(ctx, domain.AuditEvent{
		Tenant: p.Tenant,
		UserID: p.UserID,
		Role:   p.Role,
		Action: action,
	})
}

func check(principal domain.Principal, prompt string) error {
	if principal.UserID == "" {
		return domain.ErrUnauthorized
	}
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("prompt is required: %w", domain.ErrInvalidInput)
	}
	if len([]rune(prompt)) > maxPromptRunes {
		return fmt.Errorf("prompt is too long: %w", domain.ErrInvalidInput)
	}
	return nil
}
