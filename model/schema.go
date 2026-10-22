package model

import (
	"encoding/json"
	"fmt"
)

const schemaInstruction = "Return only JSON that validates against this JSON Schema:\n"

// SchemaPlan applies Request.SchemaMode to the messages: SchemaInline and
// SchemaSystem inject the schema as text and report native == false, while the
// default leaves the messages untouched and reports native == true so the
// provider can use its own structured-output field. A provider implementation
// must call it before sending the request.
func SchemaPlan(req Request) (msgs []Message, native bool) {
	if len(req.JSONSchema) == 0 {
		return append([]Message(nil), req.Messages...), false
	}
	switch req.SchemaMode {
	case SchemaInline:
		return injectInline(req.Messages, req.JSONSchema), false
	case SchemaSystem:
		return injectSystem(req.Messages, req.JSONSchema), false
	default:
		return append([]Message(nil), req.Messages...), true
	}
}

func injectInline(msgs []Message, schema json.RawMessage) []Message {
	out := append([]Message(nil), msgs...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == RoleUser {
			out[i].Content += "\n\n" + schemaInstruction + string(schema)
			return out
		}
	}
	return append(out, Message{Role: RoleUser, Content: schemaInstruction + string(schema)})
}

func injectSystem(msgs []Message, schema json.RawMessage) []Message {
	out := append([]Message(nil), msgs...)
	for i := range out {
		if out[i].Role == RoleSystem {
			out[i].Content += "\n\n" + schemaInstruction + string(schema)
			return out
		}
	}
	return append([]Message{{Role: RoleSystem, Content: schemaInstruction + string(schema)}}, out...)
}

// ApplyProviderOptions encodes v as the request object and merges
// Request.ProviderOptions into it, except for the protected contract keys
// (model, messages, stream). A provider implementation uses it so arbitrary
// per-provider options do not overwrite the request contract.
func ApplyProviderOptions(req Request, v any) (map[string]any, error) {
	m, err := toMap(v)
	if err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	// ProviderOptions override any field except protected contract keys.
	protected := map[string]bool{
		"model": true, "messages": true, "stream": true,
		"tools": true, "tool_choice": true, "response_format": true,
		"format": true, "functions": true, "function_call": true,
	}
	for k, val := range req.ProviderOptions {
		if !protected[k] {
			m[k] = val
		}
	}
	return m, nil
}

func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	return m, nil
}
