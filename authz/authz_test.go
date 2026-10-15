package authz

import (
	"errors"
	"strings"
	"testing"

	"github.com/gobellim/kit/internal/core"
)

const sample = `
version: 1
default: deny
roles:
  support:
    permissions:
      - search
      - tool:get_ticket
  admin:
    permissions:
      - "*"
`

func mustParse(t *testing.T, doc string) *Policy {
	t.Helper()
	p, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestAuthorize(t *testing.T) {
	p := mustParse(t, sample)

	support := core.Principal{UserID: "u1", Roles: []string{"support"}, Tenant: "t1"}
	admin := core.Principal{UserID: "u2", Roles: []string{"admin"}, Tenant: "t1"}
	ghost := core.Principal{UserID: "u3", Roles: []string{"other"}, Tenant: "t1"}

	cases := []struct {
		name string
		pr   core.Principal
		perm string
		want bool
	}{
		{"support search", support, PermSearch, true},
		{"support tool", support, ToolPermission("get_ticket"), true},
		{"support delete denied", support, PermDelete, false},
		{"admin wildcard", admin, PermDelete, true},
		{"unknown role denied", ghost, PermSearch, false},
		{"empty permission allowed", ghost, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Authorize(tc.pr, tc.perm)
			if tc.want && err != nil {
				t.Fatalf("want allow, got %v", err)
			}
			if !tc.want && err == nil {
				t.Fatal("want deny, got nil")
			}
			if !tc.want && !errors.Is(err, core.ErrUnauthorized) {
				t.Fatalf("want ErrUnauthorized, got %v", err)
			}
		})
	}
}

func TestAuthorizeNoPrincipal(t *testing.T) {
	p := mustParse(t, sample)
	err := p.Authorize(core.Principal{}, PermSearch)
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAllowedTools(t *testing.T) {
	p := mustParse(t, sample)
	support := core.Principal{UserID: "u1", Roles: []string{"support"}, Tenant: "t1"}
	got := p.AllowedTools(support, []string{"get_ticket", "delete_all", "search"})
	if len(got) != 1 || got[0] != "get_ticket" {
		t.Fatalf("AllowedTools = %v, want [get_ticket]", got)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse(strings.NewReader("version: 1\ndefault: deny\nextra: true\n"))
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestFixtures(t *testing.T) {
	if _, err := Load("../testdata/policy.valid.yaml"); err != nil {
		t.Fatalf("valid fixture must load: %v", err)
	}
	if _, err := Load("../testdata/policy.unknown-field.yaml"); err == nil {
		t.Fatal("unknown-field fixture must fail")
	}
}

func TestParseRejectsBadDefault(t *testing.T) {
	_, err := Parse(strings.NewReader("version: 1\ndefault: allow\n"))
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestParseRejectsBadVersion(t *testing.T) {
	_, err := Parse(strings.NewReader("version: 2\ndefault: deny\n"))
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}
