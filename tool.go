package kit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/internal/core"
	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

type Effect int

const (
	EffectRead Effect = iota
	EffectWrite
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

func (e Effect) IsEgress() bool { return e == EffectEgress }

type ToolCallInfo struct {
	UserID      string
	Tenant      string
	Tool        string
	Args        string
	Fingerprint string
	Effect      Effect
	CallIndex   int
}

type BeforeToolHook func(ctx context.Context, info ToolCallInfo) (proceed bool, output string, err error)

type AfterToolHook func(ctx context.Context, info ToolCallInfo, result string, err error) (string, error)

type ToolDef struct {
	name            string
	description     string
	permission      string
	effect          Effect
	egressHosts     []string
	requireApproval bool
	validateOutput  bool
	modelView       func(string) string
	beforeTool      BeforeToolHook
	afterTool       AfterToolHook
	schema          json.RawMessage
	compiled        *stjs.Schema
	outputSchema    json.RawMessage
	outputCompiled  *stjs.Schema
	invoke          func(ctx context.Context, args string) (string, error)
}

func (t *ToolDef) Name() string { return t.name }

func (t *ToolDef) Description() string { return t.description }

func (t *ToolDef) Permission() string { return t.permission }

func (t *ToolDef) Effect() Effect { return t.effect }

func (t *ToolDef) EgressHosts() []string { return append([]string(nil), t.egressHosts...) }

func (t *ToolDef) RequireApproval() bool { return t.requireApproval }

func (t *ToolDef) ValidateOutput() bool { return t.validateOutput }

func (t *ToolDef) Schema() json.RawMessage { return t.schema }

func (t *ToolDef) ModelView(result string) string {
	if t.modelView == nil {
		return result
	}
	return t.modelView(result)
}

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

func (t *ToolDef) validateResult(result string) error {
	if t.outputCompiled == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(result), &v); err != nil {
		return fmt.Errorf("kit: tool %s: result is not JSON: %w", t.name, err)
	}
	if err := t.outputCompiled.Validate(v); err != nil {
		return fmt.Errorf("kit: tool %s: result does not match the output schema: %w", t.name, err)
	}
	return nil
}

func (t *ToolDef) Fingerprint(args string) string {
	sum := sha256.Sum256([]byte(t.name + "\x00" + canonicalArgs(args)))
	return hex.EncodeToString(sum[:])
}

func canonicalArgs(args string) string {
	if args == "" {
		return "{}"
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	out, err := json.Marshal(v)
	if err != nil {
		return args
	}
	return string(out)
}

type ToolOption func(*toolOptions)

type toolOptions struct {
	description     string
	permission      string
	effect          Effect
	egressHosts     []string
	requireApproval bool
	validateOutput  bool
	modelView       func(string) string
	beforeTool      BeforeToolHook
	afterTool       AfterToolHook
}

func Desc(s string) ToolOption { return func(o *toolOptions) { o.description = s } }

func Perm(p string) ToolOption { return func(o *toolOptions) { o.permission = p } }

func Effects(e Effect) ToolOption { return func(o *toolOptions) { o.effect = e } }

func EgressHosts(hosts ...string) ToolOption {
	return func(o *toolOptions) { o.egressHosts = append([]string(nil), hosts...) }
}

func RequireApproval(b bool) ToolOption { return func(o *toolOptions) { o.requireApproval = b } }

func ValidateOutput(b bool) ToolOption { return func(o *toolOptions) { o.validateOutput = b } }

func ModelView(f func(string) string) ToolOption {
	return func(o *toolOptions) { o.modelView = f }
}

func BeforeToolCall(h BeforeToolHook) ToolOption {
	return func(o *toolOptions) { o.beforeTool = h }
}

func AfterToolCall(h AfterToolHook) ToolOption {
	return func(o *toolOptions) { o.afterTool = h }
}

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
		name:            name,
		description:     o.description,
		permission:      o.permission,
		effect:          o.effect,
		egressHosts:     o.egressHosts,
		requireApproval: o.requireApproval,
		validateOutput:  o.validateOutput,
		modelView:       o.modelView,
		beforeTool:      o.beforeTool,
		afterTool:       o.afterTool,
		schema:          schema,
		compiled:        compiled,
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

	if o.validateOutput {
		outSchema, err := schemaFor[Out]()
		if err != nil {
			return nil, fmt.Errorf("kit: tool %s: output schema: %w", name, err)
		}
		outCompiled, err := compileSchema(name+"_out", outSchema)
		if err != nil {
			return nil, fmt.Errorf("kit: tool %s: output schema: %w", name, err)
		}
		def.outputSchema = outSchema
		def.outputCompiled = outCompiled
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
