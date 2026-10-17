package kit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/internal/core"
	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

// Effect is the side-effect class of a tool, used by runtime policies such as
// the Rule of Two and egress host allowlists.
type Effect int

const (
	// EffectRead means the tool only reads state.
	EffectRead Effect = iota
	// EffectWrite means the tool mutates state.
	EffectWrite
	// EffectEgress means the tool sends data outside the trust boundary and is
	// subject to the host allowlist and Rule of Two.
	EffectEgress
)

// String returns "read", "write", "egress", or "unknown".
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

// IsEgress reports whether e is EffectEgress.
func (e Effect) IsEgress() bool { return e == EffectEgress }

// ToolCallInfo is passed to the before and after tool hooks; Fingerprint is the
// stable sha256 of the tool name and canonical arguments.
type ToolCallInfo struct {
	UserID string
	Tenant string
	Tool   string
	Args   string
	// Fingerprint is the stable key of this call: sha256 of the tool name and its
	// canonical arguments — the same value kit.IdempotencyKey exposes to the tool.
	Fingerprint string
	Effect      Effect
	CallIndex   int
}

type idempotencyKeyCtxKey struct{}

// withIdempotencyKey carries the call's idempotency key to the tool function.
func withIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtxKey{}, key)
}

// IdempotencyKey returns the stable key of the current tool call: sha256 of the
// tool name and its canonical arguments (the same value as
// ToolCallInfo.Fingerprint). It is stable across repeats of the same call, so an
// effectful tool can forward it downstream (e.g. as an "Idempotency-Key" header)
// and let the effect deduplicate instead of applying twice. It returns "" outside
// a kit.Tool call.
func IdempotencyKey(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyCtxKey{}).(string)
	return key
}

// BeforeToolHook runs after argument validation and before the tool executes;
// proceed=false substitutes output and skips the call.
type BeforeToolHook func(ctx context.Context, info ToolCallInfo) (proceed bool, output string, err error)

// AfterToolHook always runs after execution and may rewrite the result or error;
// the tool_result guard runs after both hooks.
type AfterToolHook func(ctx context.Context, info ToolCallInfo, result string, err error) (string, error)

// ToolDef is a registered tool's descriptor and invocation closure; arguments
// are validated against its schema with additionalProperties:false.
type ToolDef struct {
	name            string
	description     string
	permission      string
	effect          Effect
	egressHosts     []string
	egressAny       bool
	allowPII        bool
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

// Name returns the tool name.
func (t *ToolDef) Name() string { return t.name }

// Description returns the description shown to the model.
func (t *ToolDef) Description() string { return t.description }

// Permission returns the RBAC permission checked before the tool runs.
func (t *ToolDef) Permission() string { return t.permission }

// Effect returns the tool's side-effect class.
func (t *ToolDef) Effect() Effect { return t.effect }

// EgressHosts returns a copy of the host allowlist used for egress tools.
func (t *ToolDef) EgressHosts() []string { return append([]string(nil), t.egressHosts...) }

// EgressAny reports whether the tool explicitly opted out of the host allowlist.
func (t *ToolDef) EgressAny() bool { return t.egressAny }

// AllowPII reports whether the tool may receive rehydrated real PII values
// instead of placeholders.
func (t *ToolDef) AllowPII() bool { return t.allowPII }

// RequireApproval reports whether each distinct call must be approved by
// Config.Approver.
func (t *ToolDef) RequireApproval() bool { return t.requireApproval }

// ValidateOutput reports whether the result is checked against the output type's
// schema.
func (t *ToolDef) ValidateOutput() bool { return t.validateOutput }

// Schema returns the raw JSON Schema for the tool's arguments.
func (t *ToolDef) Schema() json.RawMessage { return t.schema }

// ModelView maps the tool result to what the model sees; it returns the result
// unchanged when no view function is configured.
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

// Fingerprint returns the stable sha256 of the tool name and canonical
// arguments; approvals and idempotency keys are bound to it.
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

// ToolOption configures a tool registered by Tool or RawTool.
type ToolOption func(*toolOptions)

type toolOptions struct {
	description     string
	permission      string
	effect          Effect
	egressHosts     []string
	egressAny       bool
	allowPII        bool
	requireApproval bool
	validateOutput  bool
	modelView       func(string) string
	beforeTool      BeforeToolHook
	afterTool       AfterToolHook
}

// Desc sets the tool description shown to the model.
func Desc(s string) ToolOption { return func(o *toolOptions) { o.description = s } }

// Perm overrides the default tool:<name> RBAC permission.
func Perm(p string) ToolOption { return func(o *toolOptions) { o.permission = p } }

// Effects sets the tool's side-effect class.
func Effects(e Effect) ToolOption { return func(o *toolOptions) { o.effect = e } }

// EgressHosts sets the host allowlist checked for URLs in an egress tool's
// arguments.
func EgressHosts(hosts ...string) ToolOption {
	return func(o *toolOptions) { o.egressHosts = append([]string(nil), hosts...) }
}

// EgressAny allows an egress tool to target any host; without it an empty
// allowlist is a denial.
func EgressAny() ToolOption { return func(o *toolOptions) { o.egressAny = true } }

// AllowPII permits rehydrating real PII values into arguments when masking is not
// mandatory.
func AllowPII(b bool) ToolOption { return func(o *toolOptions) { o.allowPII = b } }

// RequireApproval requires Config.Approver to approve each distinct call
// fingerprint.
func RequireApproval(b bool) ToolOption { return func(o *toolOptions) { o.requireApproval = b } }

// ValidateOutput validates the result against the JSON Schema of Out.
func ValidateOutput(b bool) ToolOption { return func(o *toolOptions) { o.validateOutput = b } }

// ModelView sets a function that rewrites the result before the model sees it.
func ModelView(f func(string) string) ToolOption {
	return func(o *toolOptions) { o.modelView = f }
}

// BeforeToolCall sets a per-tool hook that replaces Config.BeforeToolCall.
func BeforeToolCall(h BeforeToolHook) ToolOption {
	return func(o *toolOptions) { o.beforeTool = h }
}

// AfterToolCall sets a per-tool hook that replaces Config.AfterToolCall.
func AfterToolCall(h AfterToolHook) ToolOption {
	return func(o *toolOptions) { o.afterTool = h }
}

// Tool registers a typed tool, deriving the argument schema from In and
// serializing Out for the model. It returns ErrInvalidConfig when app, name, or
// fn is missing, and rejects a schema that cannot be derived or compiled.
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
		egressAny:       o.egressAny,
		allowPII:        o.allowPII,
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

// RawTool registers a tool whose JSON Schema is supplied verbatim, as with MCP
// tools; an empty schema becomes {"type":"object"}. It returns ErrInvalidConfig
// when app, name, or fn is missing, and rejects a schema that cannot be compiled.
func RawTool(app *App, name string, schema json.RawMessage, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...ToolOption) (*ToolDef, error) {
	if app == nil {
		return nil, fmt.Errorf("kit: app is required: %w", core.ErrInvalidConfig)
	}
	if name == "" {
		return nil, fmt.Errorf("kit: tool name is required: %w", core.ErrInvalidConfig)
	}
	if fn == nil {
		return nil, fmt.Errorf("kit: tool %s: handler is required: %w", name, core.ErrInvalidConfig)
	}
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}

	o := toolOptions{permission: authz.ToolPermission(name)}
	for _, opt := range opts {
		opt(&o)
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
		egressAny:       o.egressAny,
		allowPII:        o.allowPII,
		requireApproval: o.requireApproval,
		validateOutput:  o.validateOutput,
		modelView:       o.modelView,
		beforeTool:      o.beforeTool,
		afterTool:       o.afterTool,
		schema:          schema,
		compiled:        compiled,
		invoke: func(ctx context.Context, args string) (string, error) {
			if args == "" {
				args = "{}"
			}
			return fn(ctx, json.RawMessage(args))
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
	if !reflect.ValueOf(v).IsValid() {
		return json.RawMessage(`{}`), nil
	}
	return reflectSchema(v)
}

func reflectSchema(v any) (json.RawMessage, error) {
	if !reflect.ValueOf(v).IsValid() {
		return json.RawMessage(`{}`), nil
	}
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

func decodeAnswer[T any](text string, compiled *stjs.Schema, dst *T) error {
	trimmed := strings.TrimSpace(text)
	var raw any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return err
	}
	if err := compiled.Validate(raw); err != nil {
		return fmt.Errorf("answer does not match the schema: %w", err)
	}
	return json.Unmarshal([]byte(trimmed), dst)
}
