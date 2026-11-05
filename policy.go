package gobellim

import (
	"context"
	"encoding/json"
	"fmt"
)

// Effect is the side-effect class of a tool. The zero value is unknown and is
// rejected by NewTask: a tool must declare what it does.
type Effect uint8

const (
	// EffectUnknown is the zero value. It is never a valid tool effect.
	EffectUnknown Effect = iota
	// EffectRead reads state.
	EffectRead
	// EffectWrite mutates state.
	EffectWrite
	// EffectEgress sends data outside the trust boundary.
	EffectEgress
	// EffectDestructive is an irreversible or high-risk action.
	EffectDestructive
)

// Short aliases for tool declarations.
const (
	Read        = EffectRead
	Write       = EffectWrite
	Egress      = EffectEgress
	Destructive = EffectDestructive
)

// String returns the tag name of the effect.
func (e Effect) String() string {
	switch e {
	case EffectRead:
		return "read"
	case EffectWrite:
		return "write"
	case EffectEgress:
		return "egress"
	case EffectDestructive:
		return "destructive"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the effect as its tag name.
func (e Effect) MarshalJSON() ([]byte, error) { return json.Marshal(e.String()) }

// UnmarshalJSON decodes an effect from its tag name.
func (e *Effect) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "read":
		*e = EffectRead
	case "write":
		*e = EffectWrite
	case "egress":
		*e = EffectEgress
	case "destructive":
		*e = EffectDestructive
	default:
		return fmt.Errorf("%w: unknown effect %q", ErrInvalidConfig, s)
	}
	return nil
}

// Resource is the object an action targets. TenantID binds it to one tenant.
type Resource struct {
	Type     string
	ID       string
	TenantID string
}

// Authorizer adapts the service's existing RBAC/ACL checks.
type Authorizer interface {
	Allowed(ctx context.Context, p Principal, permission string, resource Resource) (bool, error)
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(ctx context.Context, p Principal, permission string, resource Resource) (bool, error)

// Allowed calls f.
func (f AuthorizerFunc) Allowed(ctx context.Context, p Principal, permission string, resource Resource) (bool, error) {
	return f(ctx, p, permission, resource)
}

// DecisionKind is the Gate's tri-state answer.
type DecisionKind uint8

const (
	// Deny refuses the action.
	Deny DecisionKind = iota
	// Allow permits the action.
	Allow
	// Escalate sends the action to a human.
	Escalate
)

// String returns the tag name of the decision.
func (d DecisionKind) String() string {
	switch d {
	case Deny:
		return "deny"
	case Allow:
		return "allow"
	case Escalate:
		return "escalate"
	default:
		return "unknown"
	}
}

// Decision is the deterministic result of the Gate.
type Decision struct {
	Kind    DecisionKind
	RuleIDs []string
	Reason  string
}
