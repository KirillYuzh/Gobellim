package tool

import (
	"bytes"
	"encoding/json"
)

// CanonicalArgs returns the canonical JSON encoding of args: sorted keys,
// no whitespace, no HTML escaping. It is used for deterministic call keys.
func CanonicalArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return string(args)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return string(args)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}
