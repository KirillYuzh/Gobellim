package gobellim

import (
	"context"
	"encoding/json"
	"sort"
)

// check runs the checks that every action needs, whether or not a human
// approved it: tenant binding, grounding, RBAC/ACL and business validation.
// A nil result means the checks passed. Every check is fail-closed.
//
// The order is deliberate. Grounding is local and cheap, and it rejects an
// argument the model invented before that argument's resource reaches the
// application's ACL: an ACL that audits lookups must not record an ID the model
// made up. Authorization comes before business validation, so an application
// rule never runs for a caller who could not perform the action anyway.
//
// requireGrounding is false only for an approved action: its arguments were
// already resolved and are bound to the digest the human saw, so there is no
// handle left to check.
func (e *Engine) check(ctx context.Context, p Principal, c prepared, requireGrounding bool) *Decision {
	resource := c.resource
	if resource.TenantID == "" {
		resource.TenantID = p.Tenant
	}
	if resource.TenantID != p.Tenant {
		return &Decision{Kind: Deny, RuleIDs: []string{"tenant-mismatch"}, Reason: "resource is bound to another tenant"}
	}
	if requireGrounding && !c.grounded {
		return &Decision{Kind: Deny, RuleIDs: []string{"ungrounded"}, Reason: "argument has no trusted origin"}
	}
	ok, err := e.authz.Allowed(ctx, p, c.spec.permission, resource)
	switch {
	case err != nil:
		return &Decision{Kind: Deny, RuleIDs: []string{"authz-error"}, Reason: "authorizer failed"}
	case !ok:
		return &Decision{Kind: Deny, RuleIDs: []string{"rbac-acl"}, Reason: "permission denied"}
	}
	if err := c.tool.validate(ctx, p, c.args); err != nil {
		return &Decision{Kind: Deny, RuleIDs: []string{"business-validate"}, Reason: "business validation failed"}
	}
	return nil
}

// decide is the Gate. It is the only place that turns an action into Allow,
// Deny or Escalate. Every check is fail-closed.
func (e *Engine) decide(ctx context.Context, p Principal, c *prepared) Decision {
	if d := e.check(ctx, p, *c, true); d != nil {
		return *d
	}
	// The business validator passed, so a handle-backed argument is now
	// promoted to Validated: its origin was minted by the runtime and the
	// application confirmed the business meaning.
	c.worst = c.effectiveWorst()

	switch c.spec.effect {
	case EffectRead:
		return Decision{Kind: Allow, RuleIDs: []string{"read"}, Reason: "read allowed"}
	case EffectWrite:
		if c.worst >= Validated {
			return Decision{Kind: Allow, RuleIDs: []string{"write"}, Reason: "write allowed"}
		}
		return Decision{Kind: Escalate, RuleIDs: []string{"write-untrusted"}, Reason: "write arguments are model-derived"}
	case EffectEgress:
		if c.worst >= Validated {
			return Decision{Kind: Allow, RuleIDs: []string{"egress"}, Reason: "egress allowed"}
		}
		return Decision{Kind: Deny, RuleIDs: []string{"egress-untrusted"}, Reason: "egress arguments are not validated"}
	case EffectDestructive:
		if c.worst >= Validated {
			return Decision{Kind: Escalate, RuleIDs: []string{"destructive"}, Reason: "destructive action requires approval"}
		}
		return Decision{Kind: Deny, RuleIDs: []string{"destructive-untrusted"}, Reason: "destructive action with untrusted arguments"}
	default:
		return Decision{Kind: Deny, RuleIDs: []string{"unknown-effect"}, Reason: "unknown effect"}
	}
}

// decideApproved re-runs every check except the effect ceiling. A human
// approval of this exact digest replaces the ceiling; it does not replace
// tenant binding, RBAC/ACL, grounding or business validation.
func (e *Engine) decideApproved(ctx context.Context, p Principal, c prepared) Decision {
	if d := e.check(ctx, p, c, false); d != nil {
		return *d
	}
	return Decision{Kind: Allow, RuleIDs: []string{"approved"}, Reason: "a human approved this action"}
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
		Effect:             c.spec.effect,
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
