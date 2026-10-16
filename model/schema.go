package model

import (
	"encoding/json"
	"fmt"
)

const schemaInstruction = "Return only JSON that validates against this JSON Schema:\n"

func schemaPlan(req Request) (msgs []Message, native bool) {
	if len(req.JSONSchema) == 0 {
		return req.Messages, false
	}
	switch req.SchemaMode {
	case SchemaInline:
		return injectInline(req.Messages, req.JSONSchema), false
	case SchemaSystem:
		return injectSystem(req.Messages, req.JSONSchema), false
	default:
		return req.Messages, true
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

func applyProviderOptions(req Request, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("model: encode request: %w", err)
	}
	protected := []string{"model", "messages", "stream"}
	required := make(map[string]any, len(protected))
	for _, k := range protected {
		if val, ok := m[k]; ok {
			required[k] = val
		}
	}
	for k, val := range req.ProviderOptions {
		m[k] = val
	}
	for _, k := range protected {
		if val, ok := required[k]; ok {
			m[k] = val
		} else {
			delete(m, k)
		}
	}
	return m, nil
}
