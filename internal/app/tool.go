package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	internaltool "github.com/KirillYuzh/Gobellim/internal/tool"
	gobellimtool "github.com/KirillYuzh/Gobellim/tool"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

// Effect is the side-effect class of a tool, used by runtime policies such as
// the Rule of Two and egress host allowlists.
type Effect = internaltool.Effect

const (
	// EffectRead means the tool only reads state.
	EffectRead Effect = internaltool.EffectRead
	// EffectWrite means the tool mutates state.
	EffectWrite Effect = internaltool.EffectWrite
	// EffectEgress means the tool sends data outside the trust boundary and is
	// subject to the host allowlist and Rule of Two.
	EffectEgress Effect = internaltool.EffectEgress
)

// Policy is a tool's declared behaviour: its side-effect class, the egress host
// allowlist, and whether each distinct call needs approval. It is an Option,
// so it can be handed straight to Tool or RawTool.
type Policy = gobellimtool.Policy

// Read returns a read-only policy, the zero value.
func Read() Policy { return gobellimtool.Read() }

// Write returns a policy for a tool that mutates state.
func Write() Policy { return gobellimtool.Write() }

// Egress returns an egress policy with the given host allowlist. An egress tool
// must name hosts, or opt out explicitly with AnyHost, or Build fails.
func Egress(hosts ...string) Policy { return gobellimtool.Egress(hosts...) }

// Call is the immutable runtime view of one tool invocation, passed to every
// Middleware: who calls, what, with which validated arguments, and the stable
// key of the call. Args is already validated against the tool schema.
type Call struct {
	UserID string
	Tenant string
	Tool   string
	Args   json.RawMessage
	// Key is the stable identity of this call: sha256 of the tool name and its
	// canonical arguments. Approvals and downstream idempotency keys bind to it.
	Key string
	// Index is the 1-based call counter for this tool within the turn.
	Index  int
	Policy Policy
}

// Next continues a middleware chain: it runs the rest of the chain and returns
// the tool result, or the error the chain stopped with.
type Next func(ctx context.Context) (json.RawMessage, error)

// Middleware wraps one tool call. It may observe the call, stop it by returning
// without calling next, or rewrite the result of next. Whatever it returns still
// passes the tool_result guard and the output schema.
type Middleware func(ctx context.Context, call Call, next Next) (json.RawMessage, error)

type callKeyCtxKey struct{}

// withCallKey carries the call's identity to the tool function.
func withCallKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, callKeyCtxKey{}, key)
}

// CallKey returns the stable identity of the current tool call: sha256 of the
// tool name and its canonical arguments, the same value as Call.Key. It is
// stable across repeats of the same call, so an effectful tool can forward it
// downstream (e.g. as an "Idempotency-Key" header) and let the effect
// deduplicate instead of applying twice. It returns "" outside a gobellim.Tool call.
func CallKey(ctx context.Context) string {
	key, _ := ctx.Value(callKeyCtxKey{}).(string)
	return key
}

// IdempotencyKey is the former name of CallKey.
//
// Deprecated: use CallKey.
func IdempotencyKey(ctx context.Context) string { return CallKey(ctx) }

type appCtxKey struct{}

// withApp carries the App to the tools that need its own operations.
func withApp(ctx context.Context, a *App) context.Context {
	return context.WithValue(ctx, appCtxKey{}, a)
}

func appFromContext(ctx context.Context) *App {
	a, _ := ctx.Value(appCtxKey{}).(*App)
	return a
}

// Option configures a tool declaration.
type Option = gobellimtool.Option

// Desc sets the tool description shown to the model.
func Desc(s string) Option { return gobellimtool.Desc(s) }

// Perm overrides the default tool:<name> RBAC permission.
func Perm(p string) Option { return gobellimtool.Perm(p) }

// View sets what the model sees instead of the serialized result. It receives
// the typed Out, so the mapping is checked by the compiler and cannot change the
// JSON the application or the trace sees.
func View[Out any](f func(context.Context, Out) string) Option {
	return gobellimtool.View(func(ctx context.Context, out Out) (string, error) {
		return f(ctx, out), nil
	})
}

// ToolDef is an opaque, validated tool declaration. Build it with Tool or
// RawTool and register it in a Registry; the declaration never touches an App.
type ToolDef = internaltool.Def

// Tool declares a typed tool. The argument schema is derived from In, the
// result is serialized from Out and checked against Out's schema, and View, if
// set, maps it to what the model sees. Declaration is pure: it returns a
// ToolDef and never touches an App, so validation errors surface at
// Registry.Build.
func Tool[In, Out any](name string, fn func(ctx context.Context, in In) (Out, error), opts ...Option) ToolDef {
	return gobellimtool.New(name, fn, opts...)
}

// RawTool declares a tool whose JSON Schema is supplied verbatim, as with MCP
// tools; an empty schema becomes {"type":"object"}. The arguments are handed to
// the handler as raw JSON, so a schema with object arguments works. The result
// is text, not a typed Out, so it is not checked against an output schema.
func RawTool(name string, schema json.RawMessage, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...Option) ToolDef {
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	t := gobellimtool.New[json.RawMessage, string](name, fn, opts...)
	t.SetSchema(schema)
	return t
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
