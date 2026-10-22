// Package gobellim is the public façade of the Gobellim library: retrieval,
// tool calling and guardrails behind one permission-checked call path.
//
// The implementation lives in internal/app. This package re-exports its surface,
// so the import path and the package name stay stable while the code moves
// between internal packages.
package gobellim

import (
	"context"
	"encoding/json"

	"github.com/KirillYuzh/Gobellim/internal/app"
)

// Default limits applied by New when the matching Config field is zero.
const (
	// DefaultMaxSteps is the default limit on the model → tools → model loop.
	DefaultMaxSteps = app.DefaultMaxSteps
	// DefaultMaxCallsPerTool is the default per-turn call limit for one tool.
	DefaultMaxCallsPerTool = app.DefaultMaxCallsPerTool
	// DefaultToolTimeout is the default timeout for a single tool invocation.
	DefaultToolTimeout = app.DefaultToolTimeout
	// DefaultArgsMaxBytes is the default cap on serialized tool arguments.
	DefaultArgsMaxBytes = app.DefaultArgsMaxBytes
	// DefaultTopK is the default number of search hits.
	DefaultTopK = app.DefaultTopK
	// DefaultRerankMultiplier is the candidate multiplier applied to TopK when
	// Config.RerankCandidates is zero.
	DefaultRerankMultiplier = app.DefaultRerankMultiplier
)

// Kind constants for span kinds.
const (
	// KindSpan marks a plain span.
	KindSpan = app.KindSpan
	// KindGeneration marks a model generation.
	KindGeneration = app.KindGeneration
	// KindEvent marks a point-in-time event.
	KindEvent = app.KindEvent
)

// Defaults for thread memory.
const (
	// DefaultMemoryLimit is the number of recent thread messages loaded when
	// MemoryLoad is given a non-positive limit.
	DefaultMemoryLimit = app.DefaultMemoryLimit
	// DefaultMemoryTopK is the recall result count used when MemoryRecall is given
	// a non-positive topK and Config.MemoryRecall is zero.
	DefaultMemoryTopK = app.DefaultMemoryTopK
	// DefaultMemoryRecall is the zero value of Config.MemoryRecall, which disables
	// semantic recall.
	DefaultMemoryRecall = app.DefaultMemoryRecall
)

// DefaultRetrievalToolName is the tool name used when RetrievalTool is called
// with an empty name.
const DefaultRetrievalToolName = app.DefaultRetrievalToolName

// Defaults for tool search.
const (
	// DefaultToolSearchThreshold is the tool count above which tool search ranks
	// the catalog by query relevance.
	DefaultToolSearchThreshold = app.DefaultToolSearchThreshold
	// DefaultToolSearchTopK is the number of tools kept by tool search when
	// ToolSearch.TopK is zero.
	DefaultToolSearchTopK = app.DefaultToolSearchTopK
)

// Sentinel errors, re-exported for errors.Is and errors.As.
var (
	// ErrInvalidConfig signals a missing or unusable configuration.
	ErrInvalidConfig = app.ErrInvalidConfig
	// ErrUnauthorized signals a missing principal, a policy denial, or a blocked
	// egress attempt.
	ErrUnauthorized = app.ErrUnauthorized
	// ErrGuardReject signals that the guard rejected text on the input rail.
	ErrGuardReject = app.ErrGuardReject
	// ErrGuardUnavailable signals that a required guard layer was unavailable.
	ErrGuardUnavailable = app.ErrGuardUnavailable
	// ErrProvider wraps failures from the LLM, embedder, or Elasticsearch.
	ErrProvider = app.ErrProvider
	// ErrToolNotFound is returned by CallTool for an unregistered tool name.
	ErrToolNotFound = app.ErrToolNotFound
	// ErrInvalidArgs signals arguments that are not JSON or fail the schema.
	ErrInvalidArgs = app.ErrInvalidArgs
	// ErrMaxSteps signals that the Chat or ChatStream loop exhausted MaxSteps.
	ErrMaxSteps = app.ErrMaxSteps
	// ErrNotFound marks a missing object.
	ErrNotFound = app.ErrNotFound
	// ErrTool marks a non-fatal failure inside a tool.
	ErrTool = app.ErrTool
	// ErrBudget signals that a turn exceeded Config.MaxTotalTokens.
	ErrBudget = app.ErrBudget
	// ErrRateLimited signals that the tenant's rate-limit bucket is exhausted.
	ErrRateLimited = app.ErrRateLimited
	// ErrApprovalRequired signals missing, denied, or already-consumed approval.
	ErrApprovalRequired = app.ErrApprovalRequired
)

// App is the configured façade: it holds the policy, guard pipeline, trace
// writer, chunker, rate limiter, response cache, and the registered tool set.
type App = app.App

// Config carries all dependencies and tunables for New.
type Config = app.Config

// EgressPolicy controls the Rule of Two for egress tools.
type EgressPolicy = app.EgressPolicy

// RateLimit is a token bucket on a tenant.
type RateLimit = app.RateLimit

// ResponseCache configures the model response cache.
type ResponseCache = app.ResponseCache

// SpotlightMode manages untrusted-content marking.
type SpotlightMode = app.SpotlightMode

// ToolSearch configures lexical ranking of the tool catalog.
type ToolSearch = app.ToolSearch

// ApprovalRequest is what an Approver decides on.
type ApprovalRequest = app.ApprovalRequest

// Approver decides whether an approval-gated tool call may run.
type Approver = app.Approver

// ApproverFunc adapts a function to Approver.
type ApproverFunc = app.ApproverFunc

// Decision is the approver's tri-state answer.
type Decision = app.Decision

// Call is the immutable runtime view of one tool invocation.
type Call = app.Call

// Middleware wraps one tool call.
type Middleware = app.Middleware

// Next continues a middleware chain.
type Next = app.Next

// Option configures a tool declaration.
type Option = app.Option

// Policy is a tool's declared behaviour.
type Policy = app.Policy

// Effect is the side-effect class of a tool.
type Effect = app.Effect

// ToolDef is a validated tool declaration.
type ToolDef = app.ToolDef

// ToolSet is an immutable, compiled set of tools.
type ToolSet = app.ToolSet

// Registry collects tool declarations and compiles them into a ToolSet.
type Registry = app.Registry

// ChatRequest is one Chat turn.
type ChatRequest = app.ChatRequest

// Response is the result of Chat.
type Response = app.Response

// DecisionReport is the auditable part of a turn.
type DecisionReport = app.DecisionReport

// StreamChunk is one stream event from ChatStream.
type StreamChunk = app.StreamChunk

// Doc is one document to ingest.
type Doc = app.Doc

// SearchRequest is one hybrid search.
type SearchRequest = app.SearchRequest

// SearchHit is one fused search result.
type SearchHit = app.SearchHit

// GenerateRequest is one structured generation.
type GenerateRequest[T any] = app.GenerateRequest[T]

// Generated is the result of Generate.
type Generated[T any] = app.Generated[T]

// ErrorStrategy selects what Generate does when the answer cannot be decoded.
type ErrorStrategy = app.ErrorStrategy

// RetrievalIn is the typed input of the retrieval tool.
type RetrievalIn = app.RetrievalIn

// RetrievalOut is the retrieval tool's result.
type RetrievalOut = app.RetrievalOut

// RetrievalHit is one passage returned by the retrieval tool.
type RetrievalHit = app.RetrievalHit

// MemoryHit is one recalled memory message.
type MemoryHit = app.MemoryHit

// Score is an evaluation score attached to a trace.
type Score = app.Score

// Trace is one recorded operation.
type Trace = app.Trace

// Usage is token and cost usage.
type Usage = app.Usage

// Health is a runtime snapshot of the app's components.
type Health = app.Health

// ComponentHealth is one component's snapshot inside Health.
type ComponentHealth = app.ComponentHealth

// Metrics is the counter surface a dashboard reads.
type Metrics = app.Metrics

// Principal identifies the caller for RBAC and data scoping.
type Principal = app.Principal

// HealthStatus is the per-component result returned by a HealthChecker.
type HealthStatus = app.HealthStatus

// HealthChecker is implemented by dependencies that App.Health can probe.
type HealthChecker = app.HealthChecker

// GuardRejectError is the typed guard rejection available through errors.As.
type GuardRejectError = app.GuardRejectError

// Constants for SpotlightMode.
const (
	// SpotlightOn marks tool results as untrusted data; it is the default.
	SpotlightOn = app.SpotlightOn
	// SpotlightOff disables untrusted-content marking.
	SpotlightOff = app.SpotlightOff
)

// Constants for Decision.
const (
	// Defer means the approver did not decide; the call is refused.
	Defer = app.Defer
	// Approve lets the call run.
	Approve = app.Approve
	// Deny refuses the call.
	Deny = app.Deny
)

// Constants for ErrorStrategy.
const (
	// Strict returns an error when decoding fails; it is the default.
	Strict = app.Strict
	// Warn logs a warning and returns the raw answer.
	Warn = app.Warn
	// Fallback returns the caller-supplied fallback value.
	Fallback = app.Fallback
)

// Constants for Effect.
const (
	// EffectRead means the tool only reads state.
	EffectRead = app.EffectRead
	// EffectWrite means the tool mutates state.
	EffectWrite = app.EffectWrite
	// EffectEgress means the tool sends data outside the trust boundary.
	EffectEgress = app.EffectEgress
)

// New validates cfg and builds the App.
func New(ctx context.Context, cfg Config) (*App, error) { return app.New(ctx, cfg) }

// Generate asks the model for a value of type T and decodes it after validating
// it against T's JSON Schema.
func Generate[T any](ctx context.Context, a *App, req GenerateRequest[T]) (Generated[T], error) {
	return app.Generate(ctx, a, req)
}

// WithPrincipal returns a context carrying the caller's identity.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return app.WithPrincipal(ctx, p)
}

// PrincipalFrom returns the principal stored in ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) { return app.PrincipalFrom(ctx) }

// CallKey returns the stable identity of the current tool call.
func CallKey(ctx context.Context) string { return app.CallKey(ctx) }

// IdempotencyKey is the former name of CallKey.
//
// Deprecated: use CallKey.
func IdempotencyKey(ctx context.Context) string { return app.IdempotencyKey(ctx) }

// WithTraceMetadata attaches request-scoped metadata to every trace.
func WithTraceMetadata(ctx context.Context, md map[string]any) context.Context {
	return app.WithTraceMetadata(ctx, md)
}

// TraceMetadataFrom returns the request-scoped trace metadata.
func TraceMetadataFrom(ctx context.Context) map[string]any { return app.TraceMetadataFrom(ctx) }

// NewRegistry returns an empty tool registry.
func NewRegistry() *Registry { return app.NewRegistry() }

// Tool declares a typed tool.
func Tool[In, Out any](name string, fn func(ctx context.Context, in In) (Out, error), opts ...Option) ToolDef {
	return app.Tool(name, fn, opts...)
}

// RawTool declares a tool whose JSON Schema is supplied verbatim.
func RawTool(name string, schema json.RawMessage, fn func(ctx context.Context, args json.RawMessage) (string, error), opts ...Option) ToolDef {
	return app.RawTool(name, schema, fn, opts...)
}

// RetrievalTool declares a typed tool that calls App.Search.
func RetrievalTool(name string, opts ...Option) ToolDef { return app.RetrievalTool(name, opts...) }

// Desc sets the tool description shown to the model.
func Desc(s string) Option { return app.Desc(s) }

// Perm overrides the default tool:<name> permission.
func Perm(p string) Option { return app.Perm(p) }

// View sets what the model sees instead of the serialized result.
func View[Out any](f func(ctx context.Context, out Out) string) Option { return app.View(f) }

// Read returns a read-only tool policy.
func Read() Policy { return app.Read() }

// Write returns a policy for a tool that mutates state.
func Write() Policy { return app.Write() }

// Egress returns an egress policy with the given host allowlist.
func Egress(hosts ...string) Policy { return app.Egress(hosts...) }
