package app

import (
	"encoding/json"
	"strings"

	"github.com/KirillYuzh/Gobellim/guard"
)

// rehydrateArgs replaces placeholders with original PII values in decoded JSON
// arguments. The substitution is performed on decoded string values to avoid
// breaking JSON structure when values contain quotes or backslashes.
func rehydrateArgs(args string, allow bool, subs []guard.Substitution) (string, bool) {
	if !allow || len(subs) == 0 {
		return args, false
	}
	values, ok := unambiguous(subs)
	if !ok || len(values) == 0 {
		return args, false
	}

	// First try fast path: if no placeholders appear in the raw text, skip parsing.
	hasPlaceholder := false
	for placeholder := range values {
		if strings.Contains(args, placeholder) {
			hasPlaceholder = true
			break
		}
	}
	if !hasPlaceholder {
		return args, false
	}

	// Parse JSON, substitute in decoded string values, re-marshal.
	var raw any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		// Fall back to raw string replacement if JSON is invalid.
		out := args
		replaced := false
		for placeholder, value := range values {
			if strings.Contains(out, placeholder) {
				out = strings.ReplaceAll(out, placeholder, value)
				replaced = true
			}
		}
		return out, replaced
	}

	raw = substituteInJSON(raw, values)
	out, err := json.Marshal(raw)
	if err != nil {
		return args, false
	}
	return string(out), true
}

// substituteInJSON recursively walks a decoded JSON value and replaces placeholders
// with original values in all string nodes. Returns the modified node.
func substituteInJSON(node any, values map[string]string) any {
	switch v := node.(type) {
	case string:
		result := v
		for placeholder, value := range values {
			if strings.Contains(result, placeholder) {
				result = strings.ReplaceAll(result, placeholder, value)
			}
		}
		return result
	case []any:
		for i := range v {
			v[i] = substituteInJSON(v[i], values)
		}
		return v
	case map[string]any:
		for k := range v {
			v[k] = substituteInJSON(v[k], values)
		}
		return v
	default:
		return node
	}
}

// unambiguous checks that each placeholder maps to a single value.
func unambiguous(subs []guard.Substitution) (map[string]string, bool) {
	values := make(map[string]string, len(subs))
	for _, s := range subs {
		if s.Placeholder == "" || s.Value == "" {
			return nil, false
		}
		if prev, seen := values[s.Placeholder]; seen && prev != s.Value {
			return nil, false
		}
		values[s.Placeholder] = s.Value
	}
	return values, true
}

// maskValues replaces original PII values with placeholders in decoded JSON
// text. The substitution is performed on decoded string values to avoid
// breaking JSON structure.
func maskValues(text string, subs []guard.Substitution) string {
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		// Fall back to raw string replacement if JSON is invalid.
		for _, s := range subs {
			if s.Value != "" {
				text = strings.ReplaceAll(text, s.Value, s.Placeholder)
			}
		}
		return text
	}

	raw = maskInJSON(raw, subs)
	out, err := json.Marshal(raw)
	if err != nil {
		return text
	}
	return string(out)
}

// maskInJSON recursively walks a decoded JSON value and replaces original values
// with placeholders in all string nodes. Returns the modified node.
func maskInJSON(node any, subs []guard.Substitution) any {
	switch v := node.(type) {
	case string:
		result := v
		for _, s := range subs {
			if s.Value != "" && strings.Contains(result, s.Value) {
				result = strings.ReplaceAll(result, s.Value, s.Placeholder)
			}
		}
		return result
	case []any:
		for i := range v {
			v[i] = maskInJSON(v[i], subs)
		}
		return v
	case map[string]any:
		for k := range v {
			v[k] = maskInJSON(v[k], subs)
		}
		return v
	default:
		return node
	}
}
