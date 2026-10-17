package authz

import (
	"fmt"
	"io"
	"os"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"gopkg.in/yaml.v3"
)

const (
	// PermChat grants use of the chat API.
	PermChat = "chat"
	// PermMemory grants use of memory operations.
	PermMemory = "memory"
	// PermSearch grants search over the store.
	PermSearch = "search"
	// PermIngest grants document ingestion.
	PermIngest = "ingest"
	// PermDelete grants document deletion.
	PermDelete = "delete"
	// PermTrace grants access to traces.
	PermTrace = "trace"
	// PermGenerate grants structured generation.
	PermGenerate = "generate"
	// PermPrune grants pruning of stored data.
	PermPrune = "prune"
)

const wildcard = "*"

// ToolPermission returns the permission name for a tool, "tool:"+name.
func ToolPermission(name string) string { return "tool:" + name }

// Role is a named set of permissions in a policy. The wildcard "*" grants every
// permission.
type Role struct {
	Permissions []string `yaml:"permissions"`
}

// Policy is a parsed policy.yaml RBAC document. It must use version 1 and default
// "deny", and authorization is deny-by-default.
type Policy struct {
	Version int             `yaml:"version"`
	Default string          `yaml:"default"`
	Roles   map[string]Role `yaml:"roles"`
}

// Load opens and parses a policy file with Parse.
func Load(path string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("authz: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse decodes a YAML policy with unknown fields rejected and validates its
// version and default. Invalid input is wrapped with ErrInvalidConfig.
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

// Allows reports whether any of the principal's roles grants permission. An empty
// permission is allowed, and an unknown role grants nothing.
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

// Authorize returns nil when pr is allowed permission. An empty permission is
// allowed, while a zero principal or a denied permission yields ErrUnauthorized.
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

// AllowedTools returns the subset of names for which the principal has the
// corresponding tool permission.
func (p *Policy) AllowedTools(pr core.Principal, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if p.Allows(pr, ToolPermission(n)) {
			out = append(out, n)
		}
	}
	return out
}
