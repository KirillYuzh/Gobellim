package tool

import "fmt"

// Effect is the side-effect class of a tool.
type Effect int

const (
	// EffectRead means the tool only reads state.
	EffectRead Effect = iota
	// EffectWrite means the tool mutates state.
	EffectWrite
	// EffectEgress means the tool sends data outside the trust boundary.
	EffectEgress
)

func (e Effect) String() string {
	switch e {
	case EffectRead:
		return "read"
	case EffectWrite:
		return "write"
	case EffectEgress:
		return "egress"
	default:
		return "unknown"
	}
}

// EgressPolicy declares which hosts a tool may send data to.
type EgressPolicy struct {
	Hosts []string
	Any   bool
}

// ApprovalPolicy declares whether each call needs explicit approval.
type ApprovalPolicy struct {
	Required bool
}

// Policy combines effect, egress, and approval into one explicit declaration.
// The zero value is read-only with no egress and no approval.
type Policy struct {
	effect   Effect
	Egress   EgressPolicy
	Approval ApprovalPolicy
	pii      bool
}

// Effect returns the tool's effect class.
func (p Policy) Effect() Effect { return p.effect }

// WithEffect returns a new policy with the given effect class.
func (p Policy) WithEffect(e Effect) Policy {
	p.effect = e
	return p
}

// IsEgress reports whether e is EffectEgress.
func (e Effect) IsEgress() bool { return e == EffectEgress }

// RequiresApproval returns true if the tool requires approval before each call.
func (p Policy) RequiresApproval() bool { return p.Approval.Required }

// AllowsPII reports whether the tool may receive PII unmasked in its
// arguments. Masking still depends on the model's region: outside RegionRU the
// PII is never rehydrated, whatever this flag says.
func (p Policy) AllowsPII() bool { return p.pii }

// AllowPII returns a new policy that allows PII in its arguments.
func (p Policy) AllowPII() Policy {
	p.pii = true
	return p
}

// EgressAny returns true if the tool allows egress to any host.
func (p Policy) EgressAny() bool { return p.Egress.Any }

// EgressHosts returns the tool's egress host allowlist.
func (p Policy) EgressHosts() []string { return p.Egress.Hosts }

// Validate checks the policy for invalid combinations.
func (p Policy) Validate(name string) error {
	if p.Effect() == EffectEgress && !p.Egress.Any && len(p.Egress.Hosts) == 0 {
		return fmt.Errorf("tool: %s: egress tool needs Egress(hosts...) or AnyHost(): %w", name, ErrInvalidConfig)
	}
	return nil
}

// Merge combines two policies, with b taking precedence for non-zero values.
// Egress is never downgraded: merging a write or read policy into an egress
// policy keeps it egress, so the host allowlist cannot be dropped by option
// order.
func (p Policy) Merge(other Policy) Policy {
	switch {
	case p.effect == EffectEgress || other.Effect() == EffectEgress:
		p.effect = EffectEgress
	case other.Effect() != EffectRead:
		p.effect = other.Effect()
	}
	// Egress: merge hosts, prefer Any
	if other.Egress.Any || len(other.Egress.Hosts) > 0 {
		p.Egress = other.Egress
	}
	// Approval: use other if required
	if other.Approval.Required {
		p.Approval = other.Approval
	}
	// PII: an explicit opt-in is additive and cannot be undone by a merge.
	if other.pii {
		p.pii = true
	}
	return p
}

// Hosts returns a new policy with the given egress host allowlist.
func (p Policy) Hosts(hosts ...string) Policy {
	p.Egress.Hosts = append([]string(nil), hosts...)
	p.Egress.Any = false
	return p
}

// AnyHost returns a new policy allowing egress to any host.
func (p Policy) AnyHost() Policy {
	p.Egress.Any = true
	p.Egress.Hosts = nil
	return p
}

// Approve returns a new policy requiring approval before each call.
func (p Policy) Approve() Policy {
	p.Approval.Required = true
	return p
}
