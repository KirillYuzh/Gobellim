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
	// Operation is what the tool does: read, write, send, delete or compute.
	// The zero value is rejected by NewTask.
	Operation Operation
	// Permission is the RBAC permission checked by the Authorizer. An empty
	// value defaults to "tool:<Name>".
	Permission string
	// Target binds the action to an object and tenant. It is required for
	// Read, Write, Send and Delete, because the authorizer needs the object to
	// check the ACL. Compute may leave it nil. A returned Resource must carry a
	// non-empty Type, ID and TenantID: the Gate denies an unbound target instead
	// of filling TenantID from the principal.
	Target func(p Principal, a Args) Resource
	// Validate is the application's business check. An error becomes Deny. A
	// nil result does not raise trust: it only means the rule did not object.
	Validate func(ctx context.Context, p Principal, a Args) error
	// Run is the existing business logic.
	Run func(ctx context.Context, p Principal, a Args) (Result, error)
}

type toolSpec struct {
	name        string
	description string
	operation   Operation
	permission  string
	schema      json.RawMessage
}

type prepared struct {
	tool   ToolDef
	spec   toolSpec
	args   any
	labels map[string]Label
	target Resource
	digest string
	// raw is the resolved argument object the handler receives; masked is the
	// same object with secret and PII fields replaced, and it is what an
	// approver is shown.
	raw         json.RawMessage
	masked      json.RawMessage
	grounded    bool
	worst       Integrity
	sensitivity Sensitivity
}

// ToolDef is the sealed, type-erased form of Tool[A, R].
type ToolDef interface {
	check() error
	checkGroundSources(inputPaths map[string]struct{}) error
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
	if t.Operation == OperationUnknown || t.Operation > OperationCompute {
		return fmt.Errorf("%w: tool %s: declare read, write, send, delete or compute", ErrInvalidConfig, t.Name)
	}
	if t.Operation != OperationCompute && t.Target == nil {
		// A data operation without a target cannot be checked against the
		// application's ACL, so it would silently fall back to RBAC only.
		return fmt.Errorf("%w: tool %s: Target is required for a %s operation", ErrInvalidConfig, t.Name, t.Operation)
	}
	s, err := schemaFor[A]()
	if err != nil {
		return err
	}
	if err := s.checkArgTags(); err != nil {
		return fmt.Errorf("tool %s: %w", t.Name, err)
	}
	return checkResultTags(reflect.TypeOf((*R)(nil)).Elem())
}

// checkGroundSources rejects a ground argument that binds to an input field the
// task does not declare. Without this, renaming an input field turns every call
// into a runtime Deny instead of a startup error.
func (t Tool[A, R]) checkGroundSources(inputPaths map[string]struct{}) error {
	s, err := schemaFor[A]()
	if err != nil {
		return err
	}
	for _, b := range s.groundBindings() {
		if _, ok := inputPaths[b.source]; !ok {
			return fmt.Errorf("%w: tool %s: ground on %s expects input field %q, which the task input does not declare", ErrInvalidConfig, t.Name, b.arg, b.source)
		}
	}
	return nil
}

func (t Tool[A, R]) toolSpec() toolSpec {
	var raw json.RawMessage
	if s, err := schemaFor[A](); err == nil {
		raw = s.raw
	}
	return toolSpec{
		name:        t.Name,
		description: t.Description,
		operation:   t.Operation,
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
	grounded, err := s.checkGrounding(handleLabels)
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
	target := Resource{}
	if t.Target != nil {
		target = t.Target(p, args)
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
		args:        args,
		labels:      labels,
		target:      target,
		digest:      actionDigest(t.Name, argsJSON),
		raw:         argsJSON,
		masked:      maskArgsJSON(argsJSON, labels, hs),
		grounded:    grounded,
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

// checkResultTags rejects a malformed or unusable gobellim tag on a tool
// result. Result tags drive redaction only, so the shape stays free-form
// (maps, pointers, json.Marshaler are allowed), but a tag that cannot be
// honoured must fail at NewTask instead of silently disabling masking.
func checkResultTags(t reflect.Type) error {
	return checkResultType(t, tagSpec{}, 0, make(map[reflect.Type]bool))
}

func checkResultType(t reflect.Type, spec tagSpec, depth int, seen map[reflect.Type]bool) error {
	if depth > 8 {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if seen[t] {
		return nil
	}
	if spec.hasSensitivity {
		if t == timeType || t.Implements(jsonMarshaler) {
			return fmt.Errorf("%w: a self-marshalling result type cannot be redacted; remove sensitivity from %s", ErrUnsupportedType, t)
		}
		if !sensitivityAllowed(t) {
			return fmt.Errorf("%w: sensitivity applies to string, []string, []byte and map[string]string result fields, not %s", ErrUnsupportedType, t)
		}
	}
	seen[t] = true
	defer delete(seen, t)

	switch t.Kind() {
	case reflect.Struct:
		if t == timeType {
			return nil
		}
		if t.Implements(jsonMarshaler) {
			// Opaque: redactResult passes it through unchanged. Do not recurse
			// into fields the runtime will not inspect.
			return nil
		}
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if sf.PkgPath != "" {
				continue
			}
			ts, err := parseTag(sf.Tag.Get("gobellim"))
			if err != nil {
				return fmt.Errorf("%w: result %s.%s", err, t.Name(), sf.Name)
			}
			if ts.ref || ts.ground || ts.source != "" || ts.resolve {
				return fmt.Errorf("%w: result %s.%s: ref, ground, resolve and source apply to a task field, not to a tool result", ErrInvalidConfig, t.Name(), sf.Name)
			}
			if err := checkResultType(sf.Type, ts, depth+1, seen); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		return checkResultType(t.Elem(), tagSpec{}, depth+1, seen)
	}
	return nil
}

func sensitivityAllowed(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.String || t.Elem().Kind() == reflect.Uint8
	case reflect.Map:
		return t.Key().Kind() == reflect.String && t.Elem().Kind() == reflect.String
	default:
		return false
	}
}
