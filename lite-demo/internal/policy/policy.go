// Package policy — карта «роль → разрешения» с запретом по умолчанию.
package policy

import (
	"encoding/json"
	"fmt"
	"os"

	"some-project/internal/entity"
)

// Policy — карта ролей и разрешений.
type Policy struct {
	Default string              `json:"default"`
	Roles   map[string][]string `json:"roles"`
}

// Load читает policy.json. Отсутствующий default означает запрет.
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	switch p.Default {
	case "":
		p.Default = "deny"
	case "deny", "allow":
	default:
		return nil, fmt.Errorf("policy: default must be deny or allow, got %q: %w", p.Default, entity.ErrConfig)
	}
	if len(p.Roles) == 0 {
		return nil, fmt.Errorf("policy: no roles in %s: %w", path, entity.ErrConfig)
	}
	return &p, nil
}

// Allows выдаёт разрешение, если оно есть у любой роли или у роли есть "*".
// Без явного разрешения действует default.
func (p *Policy) Allows(u entity.User, permission string) bool {
	if permission == "" {
		return false
	}
	for _, role := range u.Roles {
		for _, granted := range p.Roles[role] {
			if granted == "*" || granted == permission {
				return true
			}
		}
	}
	return p.Default == "allow"
}
