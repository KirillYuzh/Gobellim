package gobellim

import (
	"context"
	"encoding/json"
	"sort"
)

// check runs the checks that every action needs, whether or not an approval
// outside the model accepted it: tenant binding, grounding, RBAC/ACL and
// business validation. A nil result means the checks passed. Every check is
// fail-closed.
//
// The order is deliberate. Grounding is local and cheap, and it rejects an
// argument the model invented before that argument's resource reaches the
// application's ACL: an ACL that audits lookups must not record an ID the model
// made up. Authorization comes before business validation, so an application
// rule never runs for a caller who could not perform the action anyway.
//
// requireGrounding is false only for an approved action: its arguments were
// already resolved and are bound to the digest the approver saw, so there is no
// handle left to check.
func (e *Engine) check(ctx context.Context, p Principal, c prepared, requireGrounding bool) *Decision {
	resource := c.target
	if c.spec.operation != OperationCompute && (resource.Type == "" || resource.ID == "" || resource.TenantID == "") {
		// A target without a tenant cannot be checked against the caller's
		// tenant, and filling TenantID from the principal would make that check
		// pass by construction. An omitted tenant is a configuration error.
		return &Decision{Kind: Deny, RuleIDs: []string{RuleTargetUnbound}, Reason: "action target is not fully bound"}
	}
	if resource.TenantID == "" {
		resource.TenantID = p.Tenant
	}
	if resource.TenantID != p.Tenant {
		return &Decision{Kind: Deny, RuleIDs: []string{RuleTenantMismatch}, Reason: "resource is bound to another tenant"}
	}
	if requireGrounding && !c.grounded {
		return &Decision{Kind: Deny, RuleIDs: []string{RuleUngrounded}, Reason: "argument has no trusted origin"}
	}
	ok, err := e.authz.Allowed(ctx, p, c.spec.permission, resource)
	switch {
	case err != nil:
		return &Decision{Kind: Deny, RuleIDs: []string{RuleAuthzError}, Reason: "authorizer failed"}
	case !ok:
		return &Decision{Kind: Deny, RuleIDs: []string{RuleRBACACL}, Reason: "permission denied"}
	}
	if err := c.tool.validate(ctx, p, c.args); err != nil {
		return &Decision{Kind: Deny, RuleIDs: []string{RuleBusinessValidate}, Reason: "business validation failed"}
	}
	return nil
}

// decide is the Gate. It is the only place that turns an action into Allow,
// Deny or Escalate. Every check is fail-closed.
func (e *Engine) decide(ctx context.Context, p Principal, c *prepared) Decision {
	if d := e.check(ctx, p, *c, true); d != nil {
		return *d
	}

	switch c.spec.operation {
	case OperationCompute:
		return Decision{Kind: Allow, RuleIDs: []string{RuleCompute}, Reason: "compute allowed"}
	case OperationRead:
		return Decision{Kind: Allow, RuleIDs: []string{RuleRead}, Reason: "read allowed"}
	case OperationWrite:
		if c.worst == Trusted {
			return Decision{Kind: Allow, RuleIDs: []string{RuleWrite}, Reason: "write arguments come from trusted state"}
		}
		return Decision{Kind: Escalate, RuleIDs: []string{RuleWriteUntrusted}, Reason: "write arguments are model-derived"}
	case OperationSend:
		if c.worst == Trusted {
			return Decision{Kind: Allow, RuleIDs: []string{RuleSend}, Reason: "send arguments come from trusted state"}
		}
		return Decision{Kind: Deny, RuleIDs: []string{RuleSendUntrusted}, Reason: "send arguments are not trusted"}
	case OperationDelete:
		if c.worst == Trusted {
			return Decision{Kind: Escalate, RuleIDs: []string{RuleDelete}, Reason: "delete action requires approval"}
		}
		return Decision{Kind: Deny, RuleIDs: []string{RuleDeleteUntrusted}, Reason: "delete action with untrusted arguments"}
	default:
		return Decision{Kind: Deny, RuleIDs: []string{RuleUnknownOperation}, Reason: "unknown operation"}
	}
}

// decideApproved re-runs every check except the operation ceiling. An approval
// outside the model of this exact digest replaces the ceiling; it does not
// replace tenant binding, RBAC/ACL, grounding or business validation.
func (e *Engine) decideApproved(ctx context.Context, p Principal, c prepared) Decision {
	if d := e.check(ctx, p, c, false); d != nil {
		return *d
	}
	return Decision{Kind: Allow, RuleIDs: []string{RuleApproved}, Reason: "a trusted approver approved this action"}
}

func (e *Engine) execute(ctx context.Context, p Principal, c prepared) (any, error) {
	return c.tool.run(ctx, p, c.args)
}

func (e *Engine) card(runID, task string, c prepared, d Decision, fourEyes bool) *ApprovalCard {
	args, err := json.Marshal(c.args)
	if err != nil {
		args = nil
	}

	paths := make([]string, 0, len(c.labels))
	for path := range c.labels {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	prov := make([]Provenance, 0, len(paths))
	for _, path := range paths {
		l := c.labels[path]
		source := "model"
		if len(l.Sources) > 0 {
			source = l.Sources[0].Kind
			if l.Sources[0].ID != "" {
				source += ":" + l.Sources[0].ID
			}
		}
		prov = append(prov, Provenance{Arg: path, Source: source, Integrity: l.Integrity})
	}

	card := ApprovalCard{
		RunID:              runID,
		Flow:               task,
		Step:               c.spec.name,
		Tool:               c.spec.name,
		Operation:          c.spec.operation,
		Args:               args,
		Digest:             c.digest,
		Reason:             d.Reason,
		Provenance:         prov,
		RequiredPermission: c.spec.permission,
		FourEyes:           fourEyes,
		ExpiresAt:          e.now().Add(e.approvalTTL),
	}.normalize()
	return &card
}

func gateEventKind(k DecisionKind) EventKind {
	switch k {
	case Allow:
		return EventGateAllowed
	case Escalate:
		return EventGateEscalated
	default:
		return EventGateDenied
	}
}
