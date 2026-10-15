package kit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/internal/core"
	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

type ToolDef struct {
	name        string
	description string
	permission  string
	schema      json.RawMessage
	compiled    *stjs.Schema
	invoke      func(ctx context.Context, args string) (string, error)
}

func (t *ToolDef) Name() string { return t.name }

func (t *ToolDef) Description() string { return t.description }

func (t *ToolDef) Permission() string { return t.permission }

func (t *ToolDef) Schema() json.RawMessage { return t.schema }

func (t *ToolDef) validate(args string) error {
	if args == "" {
		args = "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return fmt.Errorf("kit: tool %s: invalid JSON arguments: %w: %w", t.name, err, core.ErrInvalidArgs)
	}
	if err := t.compiled.Validate(v); err != nil {
		return fmt.Errorf("kit: tool %s: %w: %w", t.name, err, core.ErrInvalidArgs)
	}
	return nil
}

type ToolOption func(*toolOptions)

type toolOptions struct {
	description string
	permission  string
}

func Desc(s string) ToolOption { return func(o *toolOptions) { o.description = s } }

func Perm(p string) ToolOption { return func(o *toolOptions) { o.permission = p } }

// Tool registers a typed tool on the app. The JSON Schema is derived from the
// input type In; the result Out is serialized for the model.
func Tool[In, Out any](app *App, name string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) (*ToolDef, error) {
	if app == nil {
		return nil, fmt.Errorf("kit: app is required: %w", core.ErrInvalidConfig)
	}
	if name == "" {
		return nil, fmt.Errorf("kit: tool name is required: %w", core.ErrInvalidConfig)
	}
	if fn == nil {
		return nil, fmt.Errorf("kit: tool %s: handler is required: %w", name, core.ErrInvalidConfig)
	}

	o := toolOptions{permission: authz.ToolPermission(name)}
	for _, opt := range opts {
		opt(&o)
	}

	schema, err := schemaFor[In]()
	if err != nil {
		return nil, fmt.Errorf("kit: tool %s: %w", name, err)
	}
	compiled, err := compileSchema(name, schema)
	if err != nil {
		return nil, fmt.Errorf("kit: tool %s: %w", name, err)
	}

	def := &ToolDef{
		name:        name,
		description: o.description,
		permission:  o.permission,
		schema:      schema,
		compiled:    compiled,
		invoke: func(ctx context.Context, args string) (string, error) {
			var in In
			if args == "" {
				args = "{}"
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", fmt.Errorf("kit: tool %s: decode arguments: %w: %w", name, err, core.ErrInvalidArgs)
			}
			out, err := fn(ctx, in)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(out)
			if err != nil {
				return "", fmt.Errorf("kit: tool %s: encode result: %w", name, err)
			}
			return string(b), nil
		},
	}

	app.register(def)
	return def, nil
}

func (a *App) register(def *ToolDef) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.tools[def.name]; !ok {
		a.order = append(a.order, def.name)
	}
	a.tools[def.name] = def
}

func schemaFor[T any]() (json.RawMessage, error) {
	var v T
	return reflectSchema(v)
}

func reflectSchema(v any) (json.RawMessage, error) {
	r := &invopop.Reflector{DoNotReference: true, ExpandedStruct: true}
	raw, err := json.Marshal(r.Reflect(v))
	if err != nil {
		return nil, fmt.Errorf("reflect schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("normalize schema: %w", err)
	}
	delete(m, "$schema")
	delete(m, "$id")
	m["additionalProperties"] = false
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	return out, nil
}

func compileSchema(name string, raw json.RawMessage) (*stjs.Schema, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	c := stjs.NewCompiler()
	if err := c.AddResource(name, doc); err != nil {
		return nil, fmt.Errorf("add schema: %w", err)
	}
	comp, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return comp, nil
}
