package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gobellim/kit/demo/internal/domain"
)

type fakeEngine struct {
	answer domain.Answer
	ticket domain.Ticket
	asked  int
	triage int
}

func (f *fakeEngine) Ask(context.Context, ChatRequest) (domain.Answer, error) {
	f.asked++
	return f.answer, nil
}

func (f *fakeEngine) Triage(context.Context, domain.Principal, string) (domain.Ticket, error) {
	f.triage++
	return f.ticket, nil
}

type fakeAudit struct{ events []domain.AuditEvent }

func (f *fakeAudit) Record(_ context.Context, ev domain.AuditEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeAudit) Stats(context.Context) (domain.AuditStats, error) {
	return domain.AuditStats{Events: len(f.events)}, nil
}

func TestBotValidatesBeforeCallingTheEngine(t *testing.T) {
	engine := &fakeEngine{}
	bot := NewBot(engine, nil)
	principal := domain.Principal{UserID: "u-1", Role: domain.RoleUser, Tenant: domain.Tenant}

	if _, err := bot.Ask(context.Background(), domain.Principal{}, "привет"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("missing principal err = %v, want ErrUnauthorized", err)
	}
	if _, err := bot.Ask(context.Background(), principal, "   "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("blank prompt err = %v, want ErrInvalidInput", err)
	}
	if _, err := bot.Ask(context.Background(), principal, strings.Repeat("a", maxPromptRunes+1)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("long prompt err = %v, want ErrInvalidInput", err)
	}
	if engine.asked != 0 {
		t.Fatalf("engine.asked = %d, want 0: validation must happen first", engine.asked)
	}
}

func TestBotRecordsAuditWithoutOwningTheStore(t *testing.T) {
	engine := &fakeEngine{answer: domain.Answer{Content: "14 дней"}, ticket: domain.Ticket{Severity: 4, Summary: "оплата"}}
	audit := &fakeAudit{}
	bot := NewBot(engine, audit)
	principal := domain.Principal{UserID: "u-1", Role: domain.RoleUser, Tenant: domain.Tenant}

	if _, err := bot.Ask(context.Background(), principal, "сколько дней на возврат?"); err != nil {
		t.Fatal(err)
	}
	ticket, err := bot.Triage(context.Background(), principal, "не проходит оплата")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Severity != 4 || ticket.Summary != "оплата" {
		t.Fatalf("ticket = %+v", ticket)
	}
	if len(audit.events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(audit.events))
	}
	if audit.events[0].Action != "ask" || audit.events[1].Action != "triage" {
		t.Fatalf("audit actions = %q, %q", audit.events[0].Action, audit.events[1].Action)
	}
}
