package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
)

func TestAuditLogRecordsAllowedOperation(t *testing.T) {
	var buf bytes.Buffer
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicy, func(c *Config) {
		c.AuditLog = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})

	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "secret prompt"},
	}}); err != nil {
		t.Fatal(err)
	}

	rec := lastAudit(t, &buf)
	if rec["operation"] != "chat" || rec["user_id"] != "u-1" || rec["tenant"] != "acme" {
		t.Fatalf("audit = %v", rec)
	}
	if rec["outcome"] != "ok" || rec["error_kind"] != "" {
		t.Fatalf("outcome = %v/%v", rec["outcome"], rec["error_kind"])
	}
	if rec["trace_id"] == "" {
		t.Fatal("trace_id must join the trace")
	}
	if strings.Contains(buf.String(), "secret prompt") {
		t.Fatal("audit must not carry message text")
	}
}

func TestAuditLogRecordsPolicyDenial(t *testing.T) {
	var buf bytes.Buffer
	app := newRealApp(t, discardES{}, &scripted{t: t}, nil, testPolicy, func(c *Config) {
		c.AuditLog = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	// The user role has chat, not search.
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u-2", Roles: []string{"user"}, Tenant: "acme"})

	if _, err := app.Search(ctx, SearchRequest{Query: "q"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}

	rec := lastAudit(t, &buf)
	if rec["operation"] != "search" || rec["outcome"] != "denied" || rec["error_kind"] != "unauthorized" {
		t.Fatalf("audit = %v", rec)
	}
	if rec["user_id"] != "u-2" || rec["tenant"] != "acme" {
		t.Fatalf("principal = %v/%v", rec["user_id"], rec["tenant"])
	}
}

func TestAuditLogRecordsMissingPrincipal(t *testing.T) {
	var buf bytes.Buffer
	app := newRealApp(t, discardES{}, &scripted{t: t}, nil, testPolicy, func(c *Config) {
		c.AuditLog = slog.New(slog.NewJSONHandler(&buf, nil))
	})

	if _, err := app.Chat(context.Background(), ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "hi"},
	}}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}

	rec := lastAudit(t, &buf)
	if rec["operation"] != "chat" || rec["outcome"] != "denied" || rec["user_id"] != "" {
		t.Fatalf("audit = %v", rec)
	}
}

func TestAuditLogDisabledIsSilent(t *testing.T) {
	app := newRealApp(t, discardES{}, &scripted{t: t, queue: []model.Response{textResponse("ok")}}, nil, testPolicy, nil)
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
}

func lastAudit(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatal("no audit record")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("decode audit: %v (line %q)", err, lines[len(lines)-1])
	}
	return rec
}
