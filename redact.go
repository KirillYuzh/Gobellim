package gobellim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var (
	jsonMarshaler = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	timeType      = reflect.TypeOf(time.Time{})
)

// redactResult prepares a tool result for the model context. A field tagged
// sensitivity=secret becomes a handle, a field tagged sensitivity=patterns is
// masked, and every other value is passed through unchanged.
//
// Without this, a single read tool would put the same secrets back into the
// prompt that the input side carefully masked.
//
// Redaction understands structs, slices, arrays, maps and pointers. A type
// that marshals itself (time.Time, json.Marshaler) is opaque: the runtime
// cannot inspect its fields, so NewTask rejects a sensitivity tag on one.
func redactResult(v any, tool string, hs *handleSet) any {
	if hs == nil {
		return v
	}
	base := Label{
		Integrity:   Untrusted,
		Sensitivity: Public,
		Sources:     []SourceRef{{Kind: "tool", ID: tool}},
	}
	return redactValue(reflect.ValueOf(v), base, hs)
}

func redactValue(v reflect.Value, base Label, hs *handleSet) any {
	if !v.IsValid() {
		return nil
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Type() == timeType || v.Type().Implements(jsonMarshaler) {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Struct:
		out := make(map[string]any, v.NumField())
		for i := 0; i < v.NumField(); i++ {
			sf := v.Type().Field(i)
			if sf.PkgPath != "" {
				continue
			}
			name, omit, skip := jsonTag(sf)
			if skip {
				continue
			}
			fv := v.Field(i)
			if omit && fv.IsZero() {
				continue
			}
			if sf.Anonymous && !hasExplicitJSONName(sf) {
				// encoding/json flattens an embedded struct; keep the wire shape.
				if inner, ok := redactValue(fv, base, hs).(map[string]any); ok {
					for k, val := range inner {
						out[k] = val
					}
					continue
				}
			}
			out[name] = redactField(fv, base, sf, hs)
		}
		return out
	case reflect.Map:
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = redactValue(iter.Value(), base, hs)
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if base.Sensitivity == Secret || base.Sensitivity == Patterns {
				return redactString(string(v.Bytes()), base, hs)
			}
			return v.Interface()
		}
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, redactValue(v.Index(i), base, hs))
		}
		return out
	default:
		return v.Interface()
	}
}

func redactField(v reflect.Value, base Label, sf reflect.StructField, hs *handleSet) any {
	spec, err := parseTag(sf.Tag.Get("gobellim"))
	if err != nil {
		spec = tagSpec{}
	}
	if spec.hasSensitivity {
		base.Sensitivity = spec.sensitivity
	}
	switch {
	case v.Kind() == reflect.String:
		return redactString(v.String(), base, hs)
	case spec.hasSensitivity && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Type().Elem().Kind() == reflect.String:
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, redactString(v.Index(i).String(), base, hs))
		}
		return out
	case spec.hasSensitivity && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Type().Elem().Kind() == reflect.Uint8:
		return redactString(string(v.Bytes()), base, hs)
	case spec.hasSensitivity && v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.String:
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = redactString(iter.Value().String(), base, hs)
		}
		return out
	}
	return redactValue(v, base, hs)
}

func redactString(s string, l Label, hs *handleSet) string {
	if s == "" {
		return s
	}
	switch l.Sensitivity {
	case Secret:
		return hs.put(s, l)
	case Patterns:
		return maskPatterns(s, l, hs)
	default:
		return s
	}
}

// restoreValue replaces every handle inside a decoded output with the value it
// was minted for. A handle this run did not mint is removed: the model can echo
// one, and a caller must never receive an internal placeholder.
//
// Masking without restoration is half a feature: the customer would read
// "order <h1> is refunded" instead of the order number.
func restoreValue(v reflect.Value, hs *handleSet) {
	if hs == nil || !v.IsValid() {
		return
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(restoreString(v.String(), hs))
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := 0; i < v.Len(); i++ {
			restoreValue(v.Index(i), hs)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			restoreValue(v.Field(i), hs)
		}
	}
}

func restoreString(s string, hs *handleSet) string {
	if !strings.Contains(s, "<h") {
		return s
	}
	return handleTokenRE.ReplaceAllStringFunc(s, func(tok string) string {
		if e, ok := hs.get(tok); ok {
			return e.value
		}
		return ""
	})
}

// maskArgsJSON returns the argument object with patterns and secret values replaced
// the same way the model saw them. An approver reads this copy; the executable
// arguments stay in PendingAction.Args on the server.
//
// Numbers are decoded with UseNumber, so an untouched value keeps its literal
// and the whole object is returned unchanged when nothing needs masking.
func maskArgsJSON(raw []byte, labels map[string]Label, hs *handleSet) json.RawMessage {
	if len(labels) == 0 || hs == nil || len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	masked, changed := maskJSONValue(v, "", labels, hs)
	if !changed {
		return raw
	}
	// No HTML escaping: the card is read by a person, and <h1> in the stored
	// JSON is easier to spot than \u003ch1\u003e.
	out, err := marshalNoEscape(masked)
	if err != nil {
		return raw
	}
	return out
}

func maskJSONValue(v any, path string, labels map[string]Label, hs *handleSet) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		changed := false
		for k, val := range x {
			nv, c := maskJSONValue(val, joinPath(path, k), labels, hs)
			x[k] = nv
			changed = changed || c
		}
		return x, changed
	case []any:
		changed := false
		for i := range x {
			// Labels are keyed by field path, so every element shares one.
			nv, c := maskJSONValue(x[i], path, labels, hs)
			x[i] = nv
			changed = changed || c
		}
		return x, changed
	case string:
		l, ok := labels[path]
		if !ok || l.Sensitivity < Patterns || x == "" {
			return x, false
		}
		m := redactString(x, l, hs)
		return m, m != x
	default:
		return v, false
	}
}

func jsonTag(sf reflect.StructField) (name string, omit, skip bool) {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return sf.Name, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, true
	}
	name = parts[0]
	if name == "" {
		name = sf.Name
	}
	for _, o := range parts[1:] {
		if o == "omitempty" {
			omit = true
		}
	}
	return name, omit, false
}
