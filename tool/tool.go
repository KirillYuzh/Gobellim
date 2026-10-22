package tool

import (
	"context"
	"encoding/json"
	"fmt"

	stjs "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KirillYuzh/Gobellim/internal/tool"
)

// Tool is a typed, policy-bound tool declaration. In is the argument type,
// Out is the result type. Create one with New and register it via Registry.
// Declaration is pure: it never touches an App.
type Tool[In, Out any] struct {
	name        string
	desc        string
	fn          func(context.Context, In) (Out, error)
	policy      tool.Policy
	view        tool.ViewApplier
	schema      json.RawMessage
	compiled    *stjs.Schema
	outCompiled *stjs.Schema
	err         error
	permission  string
}

// New declares a typed tool. The JSON Schema is derived from In, Out is
// serialized and checked against its schema, and View (if set) projects Out
// to what the model sees. No *App is needed: registration happens separately
// via Registry. Build errors surface at Registry.Build.
func New[In, Out any](name string, fn func(context.Context, In) (Out, error), opts ...Option) *Tool[In, Out] {
	t := &Tool[In, Out]{
		name:       name,
		fn:         fn,
		policy:     tool.Policy{},
		permission: "",
	}
	if name == "" {
		t.err = fmt.Errorf("tool: name is required")
		return t
	}
	if fn == nil {
		t.err = fmt.Errorf("tool: %s: handler is required", name)
		return t
	}

	o := options{
		policy:     tool.Policy{},
		permission: "",
	}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&o)
		}
	}
	t.desc = o.description
	t.policy = o.policy
	t.view = o.view
	t.permission = o.permission

	// Every tool is gated by its own permission unless the caller names
	// another one, so a policy grants access to one tool by naming it and a
	// forgotten Perm cannot widen access.
	if t.permission == "" {
		t.permission = tool.ToolPermission(name)
	}

	if err := t.policy.Validate(name); err != nil {
		t.err = err
		return t
	}

	schema, err := tool.SchemaFor[In]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema: %w", name, err)
		return t
	}
	compiled, err := tool.CompileSchema(name, schema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema compile: %w", name, err)
		return t
	}
	t.schema = schema
	t.compiled = compiled

	outSchema, err := tool.SchemaFor[Out]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema: %w", name, err)
		return t
	}
	outCompiled, err := tool.CompileSchema(name+"_out", outSchema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema compile: %w", name, err)
		return t
	}
	t.outCompiled = outCompiled

	return t
}

// View sets a typed projection from Out to ViewOut for the model. The model
// sees ViewOut; the application and audit log see the original Out. View
// cannot bypass the result guard: the guard runs after View.
func View[Out, ViewOut any](f func(context.Context, Out) (ViewOut, error)) Option {
	return viewOption[Out, ViewOut]{f: f}
}

// Desc sets the tool description shown to the model.
func Desc(s string) Option {
	return optionFunc(func(o *options) { o.description = s })
}

// Perm overrides the default tool:<name> permission.
func Perm(p string) Option {
	return optionFunc(func(o *options) { o.permission = p })
}

type options struct {
	description string
	permission  string
	policy      tool.Policy
	view        tool.ViewApplier
}

// Option configures a tool declaration.
type Option interface {
	apply(*options)
}

type optionFunc func(*options)

func (f optionFunc) apply(o *options) { f(o) }

// Name returns the tool name.
func (t *Tool[In, Out]) Name() string { return t.name }

// Description returns the description shown to the model.
func (t *Tool[In, Out]) Description() string { return t.desc }

// Policy returns the declared policy.
func (t *Tool[In, Out]) Policy() tool.Policy { return t.policy }

// Schema returns the input JSON Schema.
func (t *Tool[In, Out]) Schema() json.RawMessage { return t.schema }

// SetSchema overrides the input schema and recompiles it. On failure the error
// is stored in t.err and surfaces at Registry.Build.
func (t *Tool[In, Out]) SetSchema(schema json.RawMessage) {
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	compiled, err := tool.CompileSchema(t.name, schema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema compile: %w", t.name, err)
		return
	}
	t.schema = schema
	t.compiled = compiled
}

// Err returns any declaration error, or nil.
func (t *Tool[In, Out]) Err() error { return t.err }

// Invoke decodes JSON arguments, validates against the input schema, runs the
// typed function, validates the result against the output schema, applies View
// if set, and serializes the result. Args is already validated against Schema
// by the caller; Invoke validates again for defense in depth.
func (t *Tool[In, Out]) Invoke(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.err != nil {
		return nil, t.err
	}

	// Validate arguments against the input schema.
	if t.compiled != nil {
		var v any
		if err := json.Unmarshal(args, &v); err != nil {
			return nil, fmt.Errorf("tool: %s: invalid JSON arguments: %w: %w", t.name, err, tool.ErrInvalidConfig)
		}
		if err := t.compiled.Validate(v); err != nil {
			return nil, fmt.Errorf("tool: %s: arguments do not match schema: %w: %w", t.name, err, tool.ErrInvalidConfig)
		}
	}

	var in In
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("tool: %s: decode arguments: %w", t.name, err)
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("tool: %s: encode result: %w", t.name, err)
	}

	// Validate result against the output schema.
	if t.outCompiled != nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("tool: %s: result is not JSON: %w", t.name, err)
		}
		if err := t.outCompiled.Validate(v); err != nil {
			return nil, fmt.Errorf("tool: %s: result does not match output schema: %w", t.name, err)
		}
	}

	if t.view != nil {
		raw, err = t.view.Apply(ctx, raw)
		if err != nil {
			return nil, fmt.Errorf("tool: %s: view: %w", t.name, err)
		}
	}
	return raw, nil
}

// Key returns the canonical call key for the given arguments.
func (t *Tool[In, Out]) Key(args json.RawMessage) string {
	return tool.CanonicalArgs(args)
}

// Validate validates the arguments against the tool's input schema.
func (t *Tool[In, Out]) Validate(args json.RawMessage) error {
	if t.compiled == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return fmt.Errorf("tool: %s: invalid JSON arguments: %w", t.name, err)
	}
	if err := t.compiled.Validate(v); err != nil {
		return fmt.Errorf("tool: %s: arguments do not match schema: %w", t.name, err)
	}
	return nil
}

// ValidateResult validates the result against the tool's output schema.
func (t *Tool[In, Out]) ValidateResult(result string) error {
	if t.outCompiled == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(result), &v); err != nil {
		return fmt.Errorf("tool: %s: result is not JSON: %w", t.name, err)
	}
	if err := t.outCompiled.Validate(v); err != nil {
		return fmt.Errorf("tool: %s: result does not match output schema: %w", t.name, err)
	}
	return nil
}

// ViewFor applies the view projection to the result if a view is configured.
func (t *Tool[In, Out]) ViewFor(ctx context.Context, result string) (string, error) {
	if t.view == nil {
		return result, nil
	}
	raw, err := t.view.Apply(ctx, json.RawMessage(result))
	if err != nil {
		return "", fmt.Errorf("tool: %s: view: %w", t.name, err)
	}
	return string(raw), nil
}

// Permission returns the RBAC permission the tool is gated by: the
// tool:<name> default, or the override set with Perm.
func (t *Tool[In, Out]) Permission() string {
	return t.permission
}

var _ tool.Def = (*Tool[int, int])(nil)
