package policy

import (
	"os"
	"path/filepath"
	"testing"

	"some-project/internal/entity"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadIsDenyByDefault(t *testing.T) {
	p, err := Load(write(t, `{"roles":{"user":["chat","tool:kb_search"]}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Default != "deny" {
		t.Fatalf("default = %q, want deny", p.Default)
	}
	u := entity.User{Roles: []string{"user"}}
	if !p.Allows(u, "chat") || !p.Allows(u, "tool:kb_search") {
		t.Fatal("granted permissions were refused")
	}
	if p.Allows(u, "tool:research") {
		t.Fatal("ungranted permission was allowed")
	}
	if p.Allows(entity.User{Roles: []string{"stranger"}}, "chat") {
		t.Fatal("unknown role was allowed")
	}
}

func TestLoadSupportsAllowDefaultAndWildcard(t *testing.T) {
	p, err := Load(write(t, `{"default":"allow","roles":{"admin":["*"]}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !p.Allows(entity.User{Roles: []string{"admin"}}, "anything") {
		t.Fatal("wildcard did not grant")
	}
	if !p.Allows(entity.User{Roles: []string{"stranger"}}, "anything") {
		t.Fatal("allow default did not grant")
	}
}

func TestLoadRejectsBrokenFiles(t *testing.T) {
	cases := map[string]string{
		"bad json":    `{`,
		"bad default": `{"default":"maybe","roles":{"u":["chat"]}}`,
		"no roles":    `{"default":"deny"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("Load succeeded")
			}
		})
	}
}
