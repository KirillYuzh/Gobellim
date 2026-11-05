package gobellim

import (
	"context"
	"log/slog"
	"time"
)

// EventKind names an audit event.
type EventKind string

const (
	// EventTaskStarted marks the start of a task run.
	EventTaskStarted EventKind = "task.started"
	// EventTaskCompleted marks a task that produced a valid output.
	EventTaskCompleted EventKind = "task.completed"
	// EventTaskSuspended marks a task that stopped for human approval.
	EventTaskSuspended EventKind = "task.suspended"
	// EventTaskFailed marks a task that returned an error.
	EventTaskFailed EventKind = "task.failed"
	// EventModelCompleted marks one completed model call.
	EventModelCompleted EventKind = "model.completed"
	// EventGateAllowed marks an allowed action.
	EventGateAllowed EventKind = "gate.allowed"
	// EventGateDenied marks a denied action.
	EventGateDenied EventKind = "gate.denied"
	// EventGateEscalated marks an escalated action.
	EventGateEscalated EventKind = "gate.escalated"
	// EventToolCompleted marks a completed tool call.
	EventToolCompleted EventKind = "tool.completed"
	// EventWorkflowSuspended marks a workflow that needs approval.
	EventWorkflowSuspended EventKind = "workflow.suspended"
	// EventApprovalRequested marks an approval card issued to a human.
	EventApprovalRequested EventKind = "approval.requested"
	// EventApprovalGranted marks an approved action.
	EventApprovalGranted EventKind = "approval.granted"
	// EventApprovalDenied marks a rejected action.
	EventApprovalDenied EventKind = "approval.denied"
	// EventWorkflowResumed marks a resumed workflow.
	EventWorkflowResumed EventKind = "workflow.resumed"
	// EventWorkflowExpired marks an expired approval.
	EventWorkflowExpired EventKind = "workflow.expired"
	// EventWorkflowFailed marks a failed workflow.
	EventWorkflowFailed EventKind = "workflow.failed"
)

// Event is one audit record. It never carries prompt text, raw arguments,
// secrets or continuation tokens.
type Event struct {
	Kind             EventKind
	Time             time.Time
	RunID            string
	Task             string
	Flow             string
	Step             string
	PrincipalID      string
	Tenant           string
	Tool             string
	Effect           Effect
	Decision         DecisionKind
	RuleIDs          []string
	PolicyVersion    string
	ArgsDigest       string
	Integrity        Integrity
	Sensitivity      Sensitivity
	Latency          time.Duration
	Model            string
	ModelCalls       int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ToolCalls        int
	Error            string
}

// Sink receives audit events. Implementations must not block the caller for
// long and must not log the values an event deliberately omits.
type Sink interface {
	Emit(ctx context.Context, e Event)
}

// SlogSink writes events through log/slog.
type SlogSink struct {
	Logger *slog.Logger
}

// Emit writes one structured record.
func (s SlogSink) Emit(ctx context.Context, e Event) {
	l := s.Logger
	if l == nil {
		l = slog.Default()
	}
	attrs := make([]slog.Attr, 0, 20)
	add := func(k, v string) {
		if v != "" {
			attrs = append(attrs, slog.String(k, v))
		}
	}
	add("kind", string(e.Kind))
	add("run_id", e.RunID)
	add("task", e.Task)
	add("flow", e.Flow)
	add("step", e.Step)
	add("principal", e.PrincipalID)
	add("tenant", e.Tenant)
	add("tool", e.Tool)
	if e.Effect != EffectUnknown {
		add("effect", e.Effect.String())
	}
	if len(e.RuleIDs) > 0 || e.Decision != Deny {
		add("decision", e.Decision.String())
	}
	if len(e.RuleIDs) > 0 {
		attrs = append(attrs, slog.Any("rules", e.RuleIDs))
	}
	add("policy_version", e.PolicyVersion)
	add("args_digest", e.ArgsDigest)
	if e.Tool != "" {
		add("integrity", e.Integrity.String())
		add("sensitivity", e.Sensitivity.String())
	}
	if e.Latency > 0 {
		attrs = append(attrs, slog.Duration("latency", e.Latency))
	}
	add("model", e.Model)
	if e.ModelCalls > 0 {
		attrs = append(attrs, slog.Int("model_calls", e.ModelCalls))
	}
	if e.PromptTokens > 0 {
		attrs = append(attrs, slog.Int("prompt_tokens", e.PromptTokens))
	}
	if e.CompletionTokens > 0 {
		attrs = append(attrs, slog.Int("completion_tokens", e.CompletionTokens))
	}
	if e.TotalTokens > 0 {
		attrs = append(attrs, slog.Int("total_tokens", e.TotalTokens))
	}
	if e.ToolCalls > 0 {
		attrs = append(attrs, slog.Int("tool_calls", e.ToolCalls))
	}
	add("error", e.Error)
	l.LogAttrs(ctx, slog.LevelInfo, "gobellim.audit", attrs...)
}
