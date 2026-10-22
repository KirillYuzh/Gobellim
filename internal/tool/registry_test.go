package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type defStub struct {
	name      string
	desc      string
	schema    json.RawMessage
	invokeErr error
}

func (d *defStub) Name() string            { return d.name }
func (d *defStub) Description() string     { return d.desc }
func (d *defStub) Schema() json.RawMessage { return d.schema }
func (d *defStub) Invoke(context.Context, json.RawMessage) (json.RawMessage, error) {
	if d.invokeErr != nil {
		return nil, d.invokeErr
	}
	return json.RawMessage(`{}`), nil
}
func (d *defStub) Err() error                     { return nil }
func (d *defStub) Policy() Policy                 { return Policy{} }
func (d *defStub) Permission() string             { return "" }
func (d *defStub) Key(json.RawMessage) string     { return "" }
func (d *defStub) Validate(json.RawMessage) error { return nil }
func (d *defStub) ValidateResult(string) error    { return nil }
func (d *defStub) ViewFor(context.Context, string) (string, error) {
	return "", nil
}

func TestRegistryBuild(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(&defStub{name: "a", schema: json.RawMessage(`{}`)})
	r.Register(&defStub{name: "b", schema: json.RawMessage(`{}`)})

	set, err := r.Build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set.Len() != 2 {
		t.Errorf("Len() = %d, want 2", set.Len())
	}
	names := set.Names()
	if len(names) != 2 {
		t.Errorf("Names() = %d, want 2", len(names))
	}
	if set.Get("a") == nil {
		t.Error("Get(a) = nil")
	}
	if set.Get("c") != nil {
		t.Error("Get(c) != nil")
	}
}

func TestRegistryDuplicate(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(&defStub{name: "a", schema: json.RawMessage(`{}`)})
	r.Register(&defStub{name: "a", schema: json.RawMessage(`{}`)})

	_, err := r.Build()
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestRegistryNilDef(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register(nil)

	set, err := r.Build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set.Len() != 0 {
		t.Errorf("Len() = %d, want 0", set.Len())
	}
}

var _ = errors.New
