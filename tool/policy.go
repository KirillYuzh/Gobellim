package tool

import (
	"github.com/KirillYuzh/Gobellim/internal/tool"
)

// Policy is a tool's declared behaviour: its side-effect class, the egress host
// allowlist, and whether each distinct call needs approval. It is an Option,
// so it can be handed straight to Tool or RawTool. The zero value is read-only
// with no egress and no approval.
type Policy struct {
	tool.Policy
}

// Effect is the side-effect class of a tool.
type Effect = tool.Effect

const (
	// EffectRead means the tool only reads state.
	EffectRead Effect = tool.EffectRead
	// EffectWrite means the tool mutates state.
	EffectWrite Effect = tool.EffectWrite
	// EffectEgress means the tool sends data outside the trust boundary.
	EffectEgress Effect = tool.EffectEgress
)

// EgressPolicy declares which hosts a tool may send data to.
type EgressPolicy = tool.EgressPolicy

// ApprovalPolicy declares whether each call needs explicit approval.
type ApprovalPolicy = tool.ApprovalPolicy

// apply implements Option: a policy merges into the tool's options.
func (p Policy) apply(o *options) { o.policy = o.policy.Merge(p.Policy) }

// WithEffect returns a new policy with the given effect class.
func (p Policy) WithEffect(e Effect) Policy { return Policy{p.Policy.WithEffect(e)} }

// Hosts returns a new policy with the given egress host allowlist.
func (p Policy) Hosts(hosts ...string) Policy { return Policy{p.Policy.Hosts(hosts...)} }

// AnyHost returns a new egress policy that allows egress to any host.
func (p Policy) AnyHost() Policy {
	return Policy{p.Policy.WithEffect(tool.EffectEgress).AnyHost()}
}

// AllowPII returns a new policy that allows PII in the tool's arguments.
func (p Policy) AllowPII() Policy { return Policy{p.Policy.AllowPII()} }

// Approve returns a new policy that requires approval before each call.
func (p Policy) Approve() Policy { return Policy{p.Policy.Approve()} }

// Merge combines two policies, with other taking precedence.
func (p Policy) Merge(other Policy) Policy { return Policy{p.Policy.Merge(other.Policy)} }

// Read returns a read-only policy, the zero value.
func Read() Policy { return Policy{} }

// Write returns a policy for a tool that mutates state.
func Write() Policy { return Policy{tool.Policy{}.WithEffect(tool.EffectWrite)} }

// Egress returns an egress policy with the given host allowlist. An egress
// tool must name its hosts, or opt out explicitly with AnyHost, or the
// declaration fails to build.
func Egress(hosts ...string) Policy {
	return Policy{tool.Policy{}.WithEffect(tool.EffectEgress).Hosts(hosts...)}
}

// AnyHost returns an egress policy that allows egress to any host.
func AnyHost() Policy { return Policy{}.AnyHost() }

// Approve returns a policy that requires approval before each call.
func Approve() Policy { return Policy{}.Approve() }
