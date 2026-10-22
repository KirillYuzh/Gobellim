package tools

import (
	"encoding/json"
	"fmt"
	"reflect"

	invopop "github.com/invopop/jsonschema"
	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaFor выводит JSON Schema из типа Go.
func SchemaFor[T any]() (json.RawMessage, error) {
	var v T
	if !reflect.ValueOf(v).IsValid() {
		return json.RawMessage(`{}`), nil
	}
	return reflectSchema(v)
}

func reflectSchema(v any) (json.RawMessage, error) {
	r := &invopop.Reflector{DoNotReference: true, ExpandedStruct: true}
	raw, err := json.Marshal(r.Reflect(v))
	if err != nil {
		return nil, fmt.Errorf("reflect schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("normalize schema: %w", err)
	}
	delete(m, "$schema")
	delete(m, "$id")
	// Для структуры схема закрытая; у map остаётся схема значений.
	if m["type"] == "object" {
		hasProps := false
		if props, ok := m["properties"].(map[string]any); ok && len(props) > 0 {
			hasProps = true
		}
		_, isMap := m["additionalProperties"].(map[string]any)
		if hasProps && !isMap {
			m["additionalProperties"] = false
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	return out, nil
}

// CompileSchema компилирует сырую JSON Schema под именем name.
func CompileSchema(name string, raw json.RawMessage) (*stjs.Schema, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	c := stjs.NewCompiler()
	if err := c.AddResource(name, doc); err != nil {
		return nil, fmt.Errorf("add schema: %w", err)
	}
	comp, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return comp, nil
}
