package gobellim

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// Tool is a typed action. The model only ever returns a name and JSON
// arguments; the runtime finds the tool in a closed registry and routes it
// through the Gate.
//
// A tool value is sealed: the ToolDef interface has unexported methods, so only
// Tool[A, R] values can be passed to a Task.
type Tool[Args, Result any] struct {
	Name        string
	Description string
	Effect      Effect
	// Permission is the RBAC permission checked by the Authorizer. An empty
	// value defaults to "tool:<Name>".
	Permission string
	// Resource binds the action to an object and tenant. It is called before
	// authorization.
	Resource func(p Principal, a Args) Resource
	// Validate is the application's business validation. An error becomes Deny.
	Validate func(ctx context.Context, p Principal, a Args) error
	// Run is the existing business logic.
	Run func(ctx context.Context, p Principal, a Args) (Result, error)
}

type toolSpec struct {
	name        string
	description string
	effect      Effect
	permission  string
	schema      json.RawMessage
}

type prepared struct {
	tool        ToolDef
	spec        toolSpec
	args        any
	labels      map[string]Label
	resource    Resource
	digest      string
	grounded    bool
	worst       Integrity
	sensitivity Sensitivity
}

// ToolDef is the sealed, type-erased form of Tool[A, R].
type ToolDef interface {
	check() error
	toolSpec() toolSpec
	prepare(ctx context.Context, p Principal, raw json.RawMessage, hs *handleSet, maxArgBytes int) (prepared, error)
	validate(ctx context.Context, p Principal, args any) error
	run(ctx context.Context, p Principal, args any) (any, error)
}

func (t Tool[A, R]) check() error {
	if t.Name == "" {
		return fmt.Errorf("%w: tool name is required", ErrInvalidConfig)
	}
	if t.Run == nil {
		return fmt.Errorf("%w: tool %s: run is required", ErrInvalidConfig, t.Name)
	}
	if t.Effect == EffectUnknown || t.Effect > EffectDestructive {
		return fmt.Errorf("%w: tool %s: declare read, write, egress or destructive", ErrInvalidConfig, t.Name)
	}
	_, err := schemaFor[A]()
	return err
}

func (t Tool[A, R]) toolSpec() toolSpec {
	var raw json.RawMessage
	if s, err := schemaFor[A](); err == nil {
		raw = s.raw
	}
	return toolSpec{
		name:        t.Name,
		description: t.Description,
		effect:      t.Effect,
		permission:  t.Permission,
		schema:      raw,
	}
}

func (t Tool[A, R]) prepare(_ context.Context, p Principal, raw json.RawMessage, hs *handleSet, maxArgBytes int) (prepared, error) {
	s, err := schemaFor[A]()
	if err != nil {
		return prepared{}, err
	}
	if len(raw) > maxArgBytes {
		return prepared{}, fmt.Errorf("%w: %d argument bytes exceed %d", ErrBudget, len(raw), maxArgBytes)
	}
	resolved, handleLabels, err := resolveHandles(raw, hs)
	if err != nil {
		return prepared{}, err
	}
	args, err := decodeStrict[A](resolved, maxArgBytes)
	if err != nil {
		return prepared{}, err
	}
	if err := s.validate(reflect.ValueOf(args)); err != nil {
		return prepared{}, err
	}
	labels := s.labels(Label{Integrity: Derived, Sources: []SourceRef{{Kind: "model"}}})
	for path, l := range handleLabels {
		labels[path] = l
	}
	resource := Resource{}
	if t.Resource != nil {
		resource = t.Resource(p, args)
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return prepared{}, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	worst := Trusted
	sensitivity := Public
	for _, l := range labels {
		worst = worseIntegrity(worst, l.Integrity)
		sensitivity = maxSensitivity(sensitivity, l.Sensitivity)
	}
	if len(labels) == 0 {
		worst = Untrusted
	}
	return prepared{
		tool:        t,
		spec:        t.toolSpec(),
		args:        args,
		labels:      labels,
		resource:    resource,
		digest:      actionDigest(t.Name, argsJSON),
		grounded:    groundedArgs(s, args, handleLabels),
		worst:       worst,
		sensitivity: sensitivity,
	}, nil
}

func (t Tool[A, R]) validate(ctx context.Context, p Principal, args any) error {
	a, ok := args.(A)
	if !ok {
		return fmt.Errorf("%w: tool %s: argument type mismatch", ErrInvalidConfig, t.Name)
	}
	if t.Validate == nil {
		return nil
	}
	return t.Validate(ctx, p, a)
}

func (t Tool[A, R]) run(ctx context.Context, p Principal, args any) (any, error) {
	a, ok := args.(A)
	if !ok {
		return nil, fmt.Errorf("%w: tool %s: argument type mismatch", ErrInvalidConfig, t.Name)
	}
	return t.Run(ctx, p, a)
}

// groundedArgs checks the fields marked `ground`. A grounded field must contain
// a handle minted for this run: the runtime never accepts a raw value from the
// model, so a model cannot invent an ID or copy an unrelated trusted string.
func groundedArgs(s *structSchema, args any, handleLabels map[string]Label) bool {
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return true
	}
	return groundedStruct(s, v, "", handleLabels)
}

func groundedStruct(s *structSchema, v reflect.Value, prefix string, handleLabels map[string]Label) bool {
	for _, f := range s.fields {
		path := joinPath(prefix, f.jsonName)
		if f.spec.ground {
			if _, ok := handleLabels[path]; !ok {
				return false
			}
		}
		if f.nested != nil && !groundedStruct(f.nested, v.Field(f.index), path, handleLabels) {
			return false
		}
	}
	return true
}
