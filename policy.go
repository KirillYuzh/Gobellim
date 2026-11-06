package gobellim

import (
	"context"
	"encoding/json"
	"fmt"
)

// Access says what a tool does to the world. It is the first thing a reader
// should understand about a tool, so the values are verbs, not access-system
// terms. The zero value is invalid and NewTask rejects it.
type Access uint8

const (
	// AccessUnknown is the zero value. It is never a valid tool access.
	AccessUnknown Access = iota
	// AccessRead reads application or external data. Target is required,
	// because the authorizer needs the object to check the ACL.
	AccessRead
	// AccessWrite changes application state. Target is required.
	AccessWrite
	// AccessSend sends data outside the trust boundary. Target must identify
	// the recipient, not just the tool.
	AccessSend
	// AccessDelete destroys data or performs an irreversible action. Target is
	// required.
	AccessDelete
	// AccessCompute is pure work with no application data and no external
	// side access. Target is optional; the permission check still applies.
	AccessCompute
)

// Short aliases for tool declarations.
const (
	Read    = AccessRead
	Write   = AccessWrite
	Send    = AccessSend
	Delete  = AccessDelete
	Compute = AccessCompute
)

// String returns the tag name of the access.
func (a Access) String() string {
	switch a {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessSend:
		return "send"
	case AccessDelete:
		return "delete"
	case AccessCompute:
		return "compute"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the access as its tag name.
func (a Access) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

// UnmarshalJSON decodes an access from its tag name.
func (a *Access) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "read":
		*a = AccessRead
	case "write":
		*a = AccessWrite
	case "send":
		*a = AccessSend
	case "delete":
		*a = AccessDelete
	case "compute":
		*a = AccessCompute
	default:
		return fmt.Errorf("%w: unknown access %q", ErrInvalidConfig, s)
	}
	return nil
}

// Resource is the object an action targets. TenantID binds it to one tenant.
type Resource struct {
	Type     string
	ID       string
	TenantID string
}

// Authorizer adapts the service's existing RBAC/ACL checks. An error or a
// false result is a Deny; there is no permissive default.
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
	// Escalate sends the action to an approval outside the model.
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

// Rule IDs are the stable, machine-readable reasons attached to a Decision.
// Audit dashboards and policy diffs key off them, so a typo silently breaks a
// dashboard. Compare against these values instead of a string literal.
const (
	RuleTenantMismatch   = "tenant-mismatch"
	RuleTargetUnbound    = "target-unbound"
	RuleUngrounded       = "ungrounded"
	RuleAuthzError       = "authz-error"
	RuleRBACACL          = "rbac-acl"
	RuleBusinessValidate = "business-validate"
	RuleCompute          = "compute"
	RuleRead             = "read"
	RuleWrite            = "write"
	RuleWriteUntrusted   = "write-untrusted"
	RuleSend             = "send"
	RuleSendUntrusted    = "send-untrusted"
	RuleDelete           = "delete"
	RuleDeleteUntrusted  = "delete-untrusted"
	RuleUnknownAccess    = "unknown-access"
	RuleApproved         = "approved"
	RuleApprovalForged   = "approval-forged"
	RuleApprovalDrift    = "approval-drift"
	RuleUnknownTool      = "unknown-tool"
	RuleInvalidArgs      = "invalid-args"
	RuleBatchSuspended   = "batch-suspended"
)
