package trace

// Semantic attribute keys following OpenTelemetry GenAI semantic conventions
// and Gobellim-specific conventions.

const (
	// AttrModel is the model name used for the request.
	AttrModel = "gen_ai.request.model"
	// AttrProvider is the LLM provider name.
	AttrProvider = "gen_ai.provider.name"
	// AttrOperation is the operation name (chat, completion, etc.).
	AttrOperation = "gen_ai.operation.name"
	// AttrFinishReason is the reason the model stopped generating. The
	// semantic convention defines it as a list of strings.
	AttrFinishReason = "gen_ai.response.finish_reasons"
	// AttrInputTokens is the number of input tokens.
	AttrInputTokens = "gen_ai.usage.input_tokens"
	// AttrOutputTokens is the number of output tokens.
	AttrOutputTokens = "gen_ai.usage.output_tokens"

	// AttrTenant is the tenant identifier.
	AttrTenant = "gobellim.tenant"
	// AttrUserID is the user identifier.
	AttrUserID = "gobellim.user.id"
	// AttrTraceID is the identifier of the traced operation.
	AttrTraceID = "gobellim.trace.id"
	// AttrTags is the list of tags of the traced operation.
	AttrTags = "gobellim.tags"
	// AttrInput is the masked input of the operation.
	AttrInput = "gobellim.input"
	// AttrOutput is the output of the operation.
	AttrOutput = "gobellim.output"
	// AttrToolName is the tool name.
	AttrToolName = "gobellim.tool.name"
	// AttrToolEffect is the tool effect class (read, write, egress).
	AttrToolEffect = "gobellim.tool.effect"
	// AttrGuardRail is the guard rail the verdict applies to.
	AttrGuardRail = "gobellim.guard.rail"
	// AttrGuardAction is the guard action (allow, mask, reject, detect).
	AttrGuardAction = "gobellim.guard.action"
	// AttrGuardCategory is the guard finding category.
	AttrGuardCategory = "gobellim.guard.category"
	// AttrGuardVersion is the guard version string.
	AttrGuardVersion = "gobellim.guard.version"
	// AttrGuardModel is the guard model identifier.
	AttrGuardModel = "gobellim.guard.model"
	// AttrPolicyHash is the policy hash.
	AttrPolicyHash = "gobellim.policy.hash"
	// AttrWorkflowName is the workflow name.
	AttrWorkflowName = "gobellim.workflow.name"
	// AttrWorkflowRunID is the workflow run identifier.
	AttrWorkflowRunID = "gobellim.workflow.run_id"
	// AttrErrorType is the error type.
	AttrErrorType = "gobellim.error.type"
	// AttrErrorKind is the kind of the error, as classified by the call path.
	AttrErrorKind = "gobellim.error.kind"
	// AttrStatusMessage is the status message of the operation.
	AttrStatusMessage = "gobellim.status.message"
	// AttrCost is the monetary cost of the operation.
	AttrCost = "gobellim.cost"
	// AttrKind is the kind of the operation the span covers: a span, a
	// generation or an event.
	AttrKind = "gobellim.kind"
	// AttrCache is the cache state of a generation: hit or miss.
	AttrCache = "gobellim.cache"
	// AttrStatus is the HTTP status of a backend call.
	AttrStatus = "gobellim.status"
)

// Model creates an attribute for the model name.
func Model(name string) Attribute {
	return String(AttrModel, name)
}

// ProviderName creates an attribute for the provider name.
func ProviderName(name string) Attribute {
	return String(AttrProvider, name)
}

// Operation creates an attribute for the operation name.
func Operation(name string) Attribute {
	return String(AttrOperation, name)
}

// FinishReason creates an attribute for the finish reason. The semantic
// convention defines gen_ai.response.finish_reasons as a list of strings, so
// the reason is recorded as a single-element list.
func FinishReason(reason string) Attribute {
	return Attr(AttrFinishReason, []string{reason})
}

// Tokens creates the token usage attributes of a generation.
func Tokens(input, output int) []Attribute {
	return []Attribute{
		Int(AttrInputTokens, input),
		Int(AttrOutputTokens, output),
	}
}

// Tenant creates an attribute for the tenant.
func Tenant(tenant string) Attribute {
	return String(AttrTenant, tenant)
}

// UserID creates an attribute for the user ID.
func UserID(id string) Attribute {
	return String(AttrUserID, id)
}

// ToolName creates an attribute for the tool name.
func ToolName(name string) Attribute {
	return String(AttrToolName, name)
}

// ToolEffect creates an attribute for the tool effect.
func ToolEffect(effect string) Attribute {
	return String(AttrToolEffect, effect)
}

// GuardRail creates an attribute for the guard rail.
func GuardRail(rail string) Attribute {
	return String(AttrGuardRail, rail)
}

// GuardAction creates an attribute for the guard action.
func GuardAction(action string) Attribute {
	return String(AttrGuardAction, action)
}

// GuardCategory creates an attribute for the guard category.
func GuardCategory(category string) Attribute {
	return String(AttrGuardCategory, category)
}

// GuardVersion creates an attribute for the guard version.
func GuardVersion(version string) Attribute {
	return String(AttrGuardVersion, version)
}

// GuardModel creates an attribute for the guard model identifier.
func GuardModel(model string) Attribute {
	return String(AttrGuardModel, model)
}

// PolicyHash creates an attribute for the policy hash.
func PolicyHash(hash string) Attribute {
	return String(AttrPolicyHash, hash)
}

// WorkflowName creates an attribute for the workflow name.
func WorkflowName(name string) Attribute {
	return String(AttrWorkflowName, name)
}

// WorkflowRunID creates an attribute for the workflow run ID.
func WorkflowRunID(runID string) Attribute {
	return String(AttrWorkflowRunID, runID)
}

// ErrorType creates an attribute for the error type.
func ErrorType(errType string) Attribute {
	return String(AttrErrorType, errType)
}
