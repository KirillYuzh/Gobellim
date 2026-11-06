package gobellim

import (
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
// sensitivity=secret becomes a handle, a field tagged sensitivity=pii is
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
			if base.Sensitivity == Secret || base.Sensitivity == PII {
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
	case PII:
		return maskPII(s, l, hs)
	default:
		return s
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
