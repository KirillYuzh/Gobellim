package authz

import (
	"errors"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
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
		name    string
		pr      core.Principal
		perm    string
		want    bool
		wantErr error
	}{
		{name: "support search", pr: support, perm: PermSearch, want: true},
		{name: "support tool", pr: support, perm: ToolPermission("get_ticket"), want: true},
		{name: "support delete denied", pr: support, perm: PermDelete, wantErr: core.ErrUnauthorized},
		{name: "admin wildcard", pr: admin, perm: PermDelete, want: true},
		{name: "unknown role denied", pr: ghost, perm: PermSearch, wantErr: core.ErrUnauthorized},
		{name: "empty permission denied", pr: ghost, perm: "", wantErr: core.ErrInvalidConfig},
		{name: "empty permission denied for admin too", pr: admin, perm: "", wantErr: core.ErrInvalidConfig},
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
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// An empty permission is a configuration mistake, not a granted capability:
// it must never mean "allowed".
func TestAllowsRejectsEmptyPermission(t *testing.T) {
	p := mustParse(t, sample)
	admin := core.Principal{UserID: "u2", Roles: []string{"admin"}, Tenant: "t1"}
	if p.Allows(admin, "") {
		t.Fatal("an empty permission must not be allowed, not even for a wildcard role")
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

// A policy file must hold exactly one document. A second document would be
// silently ignored, so the operator would see a successful policy check while
// half the roles never apply.
func TestParseRejectsSecondDocument(t *testing.T) {
	doc := "version: 1\ndefault: deny\nroles:\n  a:\n    permissions: [search]\n---\nversion: 1\ndefault: deny\nroles:\n  b:\n    permissions: [delete]\n"
	_, err := Parse(strings.NewReader(doc))
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}
