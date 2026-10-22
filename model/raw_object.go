package model

import (
	"encoding/json"
)

// RawObject parses a JSON string into a map; an empty or invalid string yields
// an empty map. Providers use it to encode tool-call arguments for APIs that
// require an object rather than a JSON string.
func RawObject(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}
