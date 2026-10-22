package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// ErrInvalidConfig signals a missing or invalid tool declaration.
var ErrInvalidConfig = fmt.Errorf("tool: invalid configuration")

// Def is the internal tool declaration implemented by tool.Tool. It is the
// boundary between the typed public API and the registry/invocation engine.
type Def interface {
	// Name returns the tool name.
	Name() string
	// Description returns the description shown to the model.
	Description() string
	// Schema returns the raw JSON Schema for the tool's arguments.
	Schema() json.RawMessage
	// Invoke decodes JSON arguments, runs the tool, and returns the serialized
	// result. Args is already validated against Schema.
	Invoke(context.Context, json.RawMessage) (json.RawMessage, error)
	// Err returns any declaration error, or nil.
	Err() error
	// Policy returns the tool's policy.
	Policy() Policy
	// Permission returns the custom permission override, or empty string if not set.
	Permission() string
	// Key returns the canonical call key for the given arguments.
	Key(json.RawMessage) string
	// Validate validates the arguments against the tool's input schema.
	Validate(json.RawMessage) error
	// ValidateResult validates the result against the tool's output schema.
	ValidateResult(string) error
	// ViewFor applies the view projection to the result if a view is configured.
	ViewFor(context.Context, string) (string, error)
}

// ViewApplier applies a typed view projection to serialized output.
type ViewApplier interface {
	Apply(context.Context, json.RawMessage) (json.RawMessage, error)
}
