package tool

import (
	"encoding/json"
	"testing"
)

type schemaInput struct {
	Name   string `json:"name"`
	Age    int    `json:"age"`
	Active bool   `json:"active"`
}

func TestSchemaFor(t *testing.T) {
	t.Parallel()

	t.Run("struct produces object schema", func(t *testing.T) {
		t.Parallel()
		schema, err := SchemaFor[schemaInput]()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(schema, &m); err != nil {
			t.Fatalf("unmarshal schema: %v", err)
		}
		if m["type"] != "object" {
			t.Errorf("type = %v, want object", m["type"])
		}
		if m["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", m["additionalProperties"])
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			t.Fatal("properties is not an object")
		}
		if _, ok := props["name"]; !ok {
			t.Error("missing name property")
		}
		if _, ok := props["age"]; !ok {
			t.Error("missing age property")
		}
	})

	t.Run("schema is deterministic", func(t *testing.T) {
		t.Parallel()
		s1, err := SchemaFor[schemaInput]()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		s2, err := SchemaFor[schemaInput]()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(s1) != string(s2) {
			t.Error("schema is not deterministic")
		}
	})
}

func TestCompileSchema(t *testing.T) {
	t.Parallel()

	t.Run("valid schema compiles", func(t *testing.T) {
		t.Parallel()
		schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"additionalProperties":false}`)
		comp, err := CompileSchema("test", schema)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if comp == nil {
			t.Error("compiled schema is nil")
		}
	})

	t.Run("invalid schema returns error", func(t *testing.T) {
		t.Parallel()
		_, err := CompileSchema("bad", json.RawMessage(`{invalid}`))
		if err == nil {
			t.Error("expected error for invalid schema")
		}
	})
}
