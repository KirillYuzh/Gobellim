package kitengine

import (
	"context"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
)

// triageSystem is deliberately small: the schema, not the prompt, is what pins
// the answer down.
const triageSystem = "Ты дежурный по поддержке. Разбери обращение."

// Triage runs the structured-output primitive: the JSON Schema comes from
// domain.Ticket, the input and the answer both pass the guard, and an answer
// that does not match the schema falls back instead of being trusted.
func (e *Engine) Triage(ctx context.Context, p domain.Principal, text string) (domain.Ticket, error) {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(p))

	fallback := domain.Ticket{Severity: 3, Summary: "нужна ручная разборка"}
	got, err := kit.Generate[domain.Ticket](ctx, e.app, kit.GenerateRequest[domain.Ticket]{
		System:        triageSystem,
		Prompt:        text,
		ErrorStrategy: kit.Fallback,
		Fallback:      &fallback,
	})
	if err != nil {
		return domain.Ticket{}, translate(err)
	}
	return got.Value, nil
}
