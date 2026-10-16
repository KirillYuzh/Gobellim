package authz

import (
	"fmt"
	"io"
	"os"

	"github.com/gobellim/kit/internal/core"
	"gopkg.in/yaml.v3"
)

const (
	PermSearch   = "search"
	PermIngest   = "ingest"
	PermDelete   = "delete"
	PermTrace    = "trace"
	PermGenerate = "generate"
	PermPrune    = "prune"
)

const wildcard = "*"

func ToolPermission(name string) string { return "tool:" + name }

type Role struct {
	Permissions []string `yaml:"permissions"`
}

type Policy struct {
	Version int             `yaml:"version"`
	Default string          `yaml:"default"`
	Roles   map[string]Role `yaml:"roles"`
}

func Load(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("authz: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

func Parse(r io.Reader) (*Policy, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("authz: parse policy: %w: %w", err, core.ErrInvalidConfig)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Policy) validate() error {
	if p.Version != 1 {
		return fmt.Errorf("authz: unsupported policy version %d: %w", p.Version, core.ErrInvalidConfig)
	}
	if p.Default != "deny" {
		return fmt.Errorf("authz: default must be %q, got %q: %w", "deny", p.Default, core.ErrInvalidConfig)
	}
	return nil
}

func (p *Policy) grants(role, permission string) bool {
	r, ok := p.Roles[role]
	if !ok {
		return false
	}
	for _, perm := range r.Permissions {
		if perm == permission || perm == wildcard {
			return true
		}
	}
	return false
}

func (p *Policy) Allows(pr core.Principal, permission string) bool {
	if permission == "" {
		return true
	}
	for _, role := range pr.Roles {
		if p.grants(role, permission) {
			return true
		}
	}
	return false
}

func (p *Policy) Authorize(pr core.Principal, permission string) error {
	if permission == "" {
		return nil
	}
	if pr.IsZero() {
		return fmt.Errorf("authz: no principal for %q: %w", permission, core.ErrUnauthorized)
	}
	if !p.Allows(pr, permission) {
		return fmt.Errorf("authz: principal %q denied %q: %w", pr.UserID, permission, core.ErrUnauthorized)
	}
	return nil
}

func (p *Policy) AllowedTools(pr core.Principal, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if p.Allows(pr, ToolPermission(n)) {
			out = append(out, n)
		}
	}
	return out
}
