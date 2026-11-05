package gobellim

import "fmt"

// Principal identifies the caller. It comes from the application's
// authentication layer and is never taken from model text.
type Principal struct {
	UserID string
	Tenant string
	Roles  []string
}

// HasRole reports whether the principal carries role.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Validate requires a user and a tenant. A principal without them is
// unauthorized, not merely incomplete.
func (p Principal) Validate() error {
	if p.UserID == "" || p.Tenant == "" {
		return fmt.Errorf("%w: principal user and tenant are required", ErrUnauthorized)
	}
	return nil
}

// principalField maps an input struct field onto a Principal field for the
// `source=principal` tag. It recognises only the names documented in
// docs: Tenant, TenantID, User, UserID.
func principalField(name string) (string, bool) {
	switch name {
	case "Tenant", "TenantID":
		return "tenant", true
	case "User", "UserID":
		return "user", true
	default:
		return "", false
	}
}

func principalValue(p Principal, field string) (string, bool) {
	switch field {
	case "tenant":
		return p.Tenant, true
	case "user":
		return p.UserID, true
	default:
		return "", false
	}
}
