// Package tools is a small, typed tool engine: a declaration derives a JSON
// Schema from its argument type, the registry compiles a deterministic set, and
// every invocation validates the arguments and the result.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

// Effect is the side-effect class of a tool. The demo reports it in the log;
// the permission in the policy file is what actually gates the call.
type Effect int

const (
	// EffectRead means the tool only reads state.
	EffectRead Effect = iota
	// EffectWrite means the tool mutates state.
	EffectWrite
	// EffectEgress means the tool sends data outside the service.
	EffectEgress
)

// String returns the effect name.
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

// Policy declares a tool's effect. It is an Option, so it is passed straight to
// New. The zero value is read-only.
type Policy struct{ effect Effect }

// Effect returns the declared effect.
func (p Policy) Effect() Effect { return p.effect }

// apply implements Option.
func (p Policy) apply(o *options) { o.effect = p.effect }

// Read returns a read-only policy, the zero value.
func Read() Policy { return Policy{} }

// Write returns a policy for a tool that mutates state.
func Write() Policy { return Policy{effect: EffectWrite} }

// Egress returns a policy for a tool that calls another service.
func Egress() Policy { return Policy{effect: EffectEgress} }

type options struct {
	description string
	permission  string
	effect      Effect
}

// Option configures a tool declaration.
type Option interface{ apply(*options) }

type optionFunc func(*options)

func (f optionFunc) apply(o *options) { f(o) }

// Desc sets the description shown to the model.
func Desc(s string) Option { return optionFunc(func(o *options) { o.description = s }) }

// Perm overrides the default tool:<name> permission.
func Perm(p string) Option { return optionFunc(func(o *options) { o.permission = p }) }

// ToolPermission returns the default RBAC permission for a tool.
func ToolPermission(name string) string { return "tool:" + name }

// Def is the runtime contract of a declared tool: enough to describe it to the
// model, gate it by permission, and invoke it.
type Def interface {
	Name() string
	Description() string
	Permission() string
	Effect() Effect
	Schema() json.RawMessage
	Invoke(ctx context.Context, args json.RawMessage) (string, error)
	Err() error
}

// Tool is a typed tool declaration. In is the argument type, Out the result
// type. Create one with New and register it via Registry.
type Tool[In, Out any] struct {
	name        string
	desc        string
	permission  string
	effect      Effect
	fn          func(context.Context, In) (Out, error)
	schema      json.RawMessage
	compiled    *stjs.Schema
	outCompiled *stjs.Schema
	err         error
}

// New declares a typed tool. The JSON Schema is derived from In, and the result
// is serialized from Out and checked against Out's schema. Declaration errors
// surface at Registry.Build.
func New[In, Out any](name string, fn func(context.Context, In) (Out, error), opts ...Option) *Tool[In, Out] {
	t := &Tool[In, Out]{name: name, fn: fn}
	if name == "" {
		t.err = fmt.Errorf("tool: name is required")
		return t
	}
	if fn == nil {
		t.err = fmt.Errorf("tool: %s: handler is required", name)
		return t
	}
	o := options{permission: ToolPermission(name)}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&o)
		}
	}
	t.desc = o.description
	t.permission = o.permission
	t.effect = o.effect

	schema, err := SchemaFor[In]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema: %w", name, err)
		return t
	}
	compiled, err := CompileSchema(name, schema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema: %w", name, err)
		return t
	}
	outSchema, err := SchemaFor[Out]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema: %w", name, err)
		return t
	}
	outCompiled, err := CompileSchema(name+"_out", outSchema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema: %w", name, err)
		return t
	}
	t.schema = schema
	t.compiled = compiled
	t.outCompiled = outCompiled
	return t
}

// Name returns the tool name.
func (t *Tool[In, Out]) Name() string { return t.name }

// Description returns the description shown to the model.
func (t *Tool[In, Out]) Description() string { return t.desc }

// Permission returns the RBAC permission the tool is gated by.
func (t *Tool[In, Out]) Permission() string { return t.permission }

// Effect returns the declared effect.
func (t *Tool[In, Out]) Effect() Effect { return t.effect }

// Schema returns the input JSON Schema.
func (t *Tool[In, Out]) Schema() json.RawMessage { return t.schema }

// Err returns any declaration error, or nil.
func (t *Tool[In, Out]) Err() error { return t.err }

// Invoke validates args against the input schema, runs the typed function,
// validates the result against the output schema, and returns it as JSON.
func (t *Tool[In, Out]) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if t.compiled != nil {
		var v any
		if err := json.Unmarshal(args, &v); err != nil {
			return "", fmt.Errorf("tool: %s: invalid JSON arguments: %w", t.name, err)
		}
		if err := t.compiled.Validate(v); err != nil {
			return "", fmt.Errorf("tool: %s: arguments do not match schema: %w", t.name, err)
		}
	}
	var in In
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("tool: %s: decode arguments: %w", t.name, err)
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("tool: %s: encode result: %w", t.name, err)
	}
	if t.outCompiled != nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", fmt.Errorf("tool: %s: result is not JSON: %w", t.name, err)
		}
		if err := t.outCompiled.Validate(v); err != nil {
			return "", fmt.Errorf("tool: %s: result does not match schema: %w", t.name, err)
		}
	}
	return string(raw), nil
}

var _ Def = (*Tool[int, int])(nil)

// Registry collects declarations and compiles them into a Set.
type Registry struct {
	defs []Def
	seen map[string]struct{}
	errs []error
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{seen: make(map[string]struct{})}
}

// Register adds declarations and returns the registry, so calls chain. A nil
// Def is ignored; a declaration error or a duplicate name is kept for Build.
func (r *Registry) Register(defs ...Def) *Registry {
	for _, def := range defs {
		if def == nil {
			continue
		}
		if _, dup := r.seen[def.Name()]; dup {
			r.errs = append(r.errs, fmt.Errorf("tool: %s is registered twice", def.Name()))
			continue
		}
		r.seen[def.Name()] = struct{}{}
		r.defs = append(r.defs, def)
	}
	return r
}

// Build validates every declaration and returns the immutable set.
func (r *Registry) Build() (*Set, error) {
	errs := append([]error(nil), r.errs...)
	set := &Set{byName: make(map[string]Def, len(r.defs)), order: make([]string, 0, len(r.defs))}
	for _, def := range r.defs {
		if err := def.Err(); err != nil {
			errs = append(errs, err)
			continue
		}
		set.byName[def.Name()] = def
		set.order = append(set.order, def.Name())
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return set, nil
}

// Set is an immutable, compiled set of tools.
type Set struct {
	byName map[string]Def
	order  []string
}

// Get returns the definition for name, or nil.
func (s *Set) Get(name string) Def {
	if s == nil {
		return nil
	}
	return s.byName[name]
}

// All returns the definitions in registration order.
func (s *Set) All() []Def {
	if s == nil {
		return nil
	}
	out := make([]Def, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

// Len returns the number of tools.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}

// Names returns the tool names in lexical order.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := append([]string(nil), s.order...)
	sort.Strings(out)
	return out
}
