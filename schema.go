package gobellim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxSchemaDepth  = 2
	defaultMaxItems = 100
	maxJSONDepth    = 16
)

// tagSpec is the parsed content of a `gobellim:"..."` struct tag.
type tagSpec struct {
	required       bool
	source         string
	oneof          []string
	min            *float64
	max            *float64
	sensitivity    Sensitivity
	hasSensitivity bool
	// ref makes a value addressable without labelling it confidential: the model
	// sees a handle, the approval card and the audit carry the value.
	ref    bool
	ground bool
	// groundSource names the input field a grounded handle must come from.
	// Empty means "the argument path itself"; "any" disables the check.
	groundSource string
}

func badTag(key string) error {
	return fmt.Errorf("%w: unknown or malformed tag %q", ErrInvalidConfig, key)
}

func parseTag(raw string) (tagSpec, error) {
	var s tagSpec
	if raw == "" {
		return s, nil
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, hasVal := strings.Cut(part, "=")
		switch key {
		case "required":
			if hasVal {
				return s, badTag(key)
			}
			s.required = true
		case "source":
			if !hasVal || (val != "principal" && val != "application") {
				return s, badTag(key)
			}
			s.source = val
		case "oneof":
			if !hasVal || val == "" {
				return s, badTag(key)
			}
			values := strings.Split(val, "|")
			if len(values) < 2 {
				return s, fmt.Errorf("%w: oneof needs at least two values", ErrInvalidConfig)
			}
			seen := make(map[string]struct{}, len(values))
			for _, v := range values {
				if v == "" {
					return s, fmt.Errorf("%w: oneof contains an empty value", ErrInvalidConfig)
				}
				if _, dup := seen[v]; dup {
					return s, fmt.Errorf("%w: oneof repeats %q", ErrInvalidConfig, v)
				}
				seen[v] = struct{}{}
			}
			s.oneof = values
		case "min", "max":
			if !hasVal {
				return s, badTag(key)
			}
			n, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return s, badTag(key)
			}
			if key == "min" {
				s.min = &n
			} else {
				s.max = &n
			}
		case "sensitivity":
			if !hasVal {
				return s, badTag(key)
			}
			v, ok := parseSensitivity(val)
			if !ok {
				return s, badTag(key)
			}
			s.sensitivity = v
			s.hasSensitivity = true
		case "ref":
			if hasVal {
				return s, badTag(key)
			}
			s.ref = true
		case "ground":
			if hasVal {
				if val == "" {
					return s, badTag(key)
				}
				s.groundSource = val
			}
			s.ground = true
		default:
			return s, badTag(key)
		}
	}
	if s.min != nil && s.max != nil && *s.min > *s.max {
		return s, fmt.Errorf("%w: min is greater than max", ErrInvalidConfig)
	}
	return s, nil
}

type schemaNode struct {
	Type                 string                 `json:"type"`
	Properties           map[string]*schemaNode `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Minimum              *float64               `json:"minimum,omitempty"`
	Maximum              *float64               `json:"maximum,omitempty"`
	MinItems             *int                   `json:"minItems,omitempty"`
	MaxItems             *int                   `json:"maxItems,omitempty"`
	Items                *schemaNode            `json:"items,omitempty"`
	AdditionalProperties *bool                  `json:"additionalProperties,omitempty"`
}

type fieldSchema struct {
	goName   string
	jsonName string
	index    int
	typ      reflect.Type
	spec     tagSpec
	nested   *structSchema
}

func (f *fieldSchema) sensitivity() Sensitivity {
	if f.spec.hasSensitivity {
		return f.spec.sensitivity
	}
	return Public
}

// redact hides a value according to its declared sensitivity. A patterns field is masked by
// the narrow regex set; a secret is replaced by one opaque handle. A trusted
// source keeps its trusted label, so a model may reference the value through a
// handle.
func (f *fieldSchema) redact(value string, l Label, hs *handleSet) string {
	if value == "" || hs == nil {
		return value
	}
	if f.spec.ref {
		// Addressable, not confidential: the model gets a handle and the value
		// stays public in the card and the audit.
		return hs.put(value, l)
	}
	switch f.sensitivity() {
	case Secret:
		return hs.put(value, l)
	case Patterns:
		return maskPatterns(value, l, hs)
	default:
		return value
	}
}

type structSchema struct {
	typ    reflect.Type
	fields []*fieldSchema
	node   *schemaNode
	raw    json.RawMessage
}

type nodeResult struct {
	node   *schemaNode
	nested *structSchema
}

func jsonFieldName(sf reflect.StructField) (string, bool) {
	name := sf.Name
	if tag := sf.Tag.Get("json"); tag != "" {
		part, _, _ := strings.Cut(tag, ",")
		if part == "-" {
			return "", true
		}
		if part != "" {
			name = part
		}
	}
	return name, false
}

// hasExplicitJSONName reports whether the field carries a non-empty json name.
// encoding/json flattens anonymous structs without an explicit name; this
// schema generator does not, so it rejects them instead of lying to the model.
func hasExplicitJSONName(sf reflect.StructField) bool {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return false
	}
	name, _, _ := strings.Cut(tag, ",")
	return name != "" && name != "-"
}

func newStructSchema(t reflect.Type) (*structSchema, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s must be a struct", ErrUnsupportedType, t)
	}
	return buildStruct(t, 0)
}

func buildStruct(t reflect.Type, depth int) (*structSchema, error) {
	if depth > maxSchemaDepth {
		return nil, fmt.Errorf("%w: struct nesting exceeds %d levels", ErrUnsupportedType, maxSchemaDepth)
	}
	s := &structSchema{
		typ: t,
		node: &schemaNode{
			Type:                 "object",
			Properties:           make(map[string]*schemaNode),
			AdditionalProperties: boolPtr(false),
		},
	}
	seen := make(map[string]struct{})
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		jsonName, skip := jsonFieldName(sf)
		if skip {
			continue
		}
		if opts := strings.Split(sf.Tag.Get("json"), ","); len(opts) > 1 {
			for _, o := range opts[1:] {
				if o == "string" {
					// The generated schema says number or boolean, so the model sends an
					// unquoted value and decodeStrict would reject it. Differing here
					// would surface as a runtime argument error on every call.
					return nil, fmt.Errorf("%w: %s.%s uses json \",string\", which conflicts with the generated schema", ErrUnsupportedType, t, sf.Name)
				}
			}
		}
		if sf.Anonymous && !hasExplicitJSONName(sf) {
			return nil, fmt.Errorf("%w: %s embeds %s; declare an explicit field", ErrUnsupportedType, t, sf.Type)
		}
		if _, dup := seen[jsonName]; dup {
			return nil, fmt.Errorf("%w: %s has duplicate JSON field %q", ErrInvalidConfig, t, jsonName)
		}
		seen[jsonName] = struct{}{}
		spec, err := parseTag(sf.Tag.Get("gobellim"))
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name(), sf.Name, err)
		}
		res, err := nodeForType(sf.Type, spec, depth)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name(), sf.Name, err)
		}
		f := &fieldSchema{
			goName:   sf.Name,
			jsonName: jsonName,
			index:    i,
			typ:      sf.Type,
			spec:     spec,
			nested:   res.nested,
		}
		s.fields = append(s.fields, f)
		s.node.Properties[jsonName] = res.node
		if spec.required {
			s.node.Required = append(s.node.Required, jsonName)
		}
	}
	raw, err := json.Marshal(s.node)
	if err != nil {
		return nil, fmt.Errorf("%w: encode schema for %s: %w", ErrInvalidConfig, t, err)
	}
	s.raw = raw
	return s, nil
}

func nodeForType(t reflect.Type, spec tagSpec, depth int) (nodeResult, error) {
	if t == reflect.TypeOf(time.Time{}) {
		return nodeResult{}, fmt.Errorf("%w: time.Time encodes as an RFC3339 string; use string", ErrUnsupportedType)
	}
	switch t.Kind() {
	case reflect.String:
		// source, ground and sensitivity apply to strings.
	case reflect.Slice, reflect.Array:
		if spec.source != "" || spec.ground || spec.ref {
			return nodeResult{}, fmt.Errorf("%w: source, ground and ref apply to string fields only", ErrInvalidConfig)
		}
		if t.Elem().Kind() == reflect.Uint8 {
			return nodeResult{}, fmt.Errorf("%w: []byte encodes as base64; use string", ErrUnsupportedType)
		}
		if t.Elem().Kind() != reflect.String && spec.sensitivity != Public {
			return nodeResult{}, fmt.Errorf("%w: sensitivity applies to strings and slices of strings", ErrInvalidConfig)
		}
	default:
		if spec.source != "" || spec.ground || spec.ref || spec.sensitivity != Public {
			return nodeResult{}, fmt.Errorf("%w: source, ground, ref and sensitivity apply to strings only", ErrInvalidConfig)
		}
	}

	switch t.Kind() {
	case reflect.String:
		if spec.min != nil || spec.max != nil {
			return nodeResult{}, fmt.Errorf("%w: min and max apply to numbers and slices", ErrInvalidConfig)
		}
		return nodeResult{node: &schemaNode{Type: "string", Enum: spec.oneof}}, nil

	case reflect.Bool:
		if spec.required {
			return nodeResult{}, fmt.Errorf("%w: required is ambiguous on bool", ErrInvalidConfig)
		}
		if len(spec.oneof) > 0 || spec.min != nil || spec.max != nil {
			return nodeResult{}, badTag("bool")
		}
		return nodeResult{node: &schemaNode{Type: "boolean"}}, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if len(spec.oneof) > 0 {
			return nodeResult{}, badTag("oneof")
		}
		return nodeResult{node: &schemaNode{Type: "integer", Minimum: spec.min, Maximum: spec.max}}, nil

	case reflect.Float32, reflect.Float64:
		if len(spec.oneof) > 0 {
			return nodeResult{}, badTag("oneof")
		}
		return nodeResult{node: &schemaNode{Type: "number", Minimum: spec.min, Maximum: spec.max}}, nil

	case reflect.Slice, reflect.Array:
		if len(spec.oneof) > 0 {
			return nodeResult{}, badTag("oneof")
		}
		elem := t.Elem()
		if elem.Kind() == reflect.Struct || elem.Kind() == reflect.Pointer || elem.Kind() == reflect.Slice || elem.Kind() == reflect.Array {
			return nodeResult{}, fmt.Errorf("%w: only slices of scalars are supported", ErrUnsupportedType)
		}
		items, err := nodeForType(elem, tagSpec{}, depth+1)
		if err != nil {
			return nodeResult{}, err
		}
		minItems := 0
		maxItems := defaultMaxItems
		if spec.min != nil {
			if *spec.min < 0 || *spec.min != math.Trunc(*spec.min) {
				return nodeResult{}, fmt.Errorf("%w: slice min must be a non-negative integer", ErrInvalidConfig)
			}
			minItems = int(*spec.min)
		}
		if spec.max != nil {
			if *spec.max < 0 || *spec.max != math.Trunc(*spec.max) {
				return nodeResult{}, fmt.Errorf("%w: slice max must be a non-negative integer", ErrInvalidConfig)
			}
			maxItems = int(*spec.max)
		}
		if minItems > maxItems {
			return nodeResult{}, fmt.Errorf("%w: slice min is greater than max", ErrInvalidConfig)
		}
		return nodeResult{node: &schemaNode{
			Type:     "array",
			Items:    items.node,
			MinItems: &minItems,
			MaxItems: &maxItems,
		}}, nil

	case reflect.Struct:
		if len(spec.oneof) > 0 || spec.min != nil || spec.max != nil {
			return nodeResult{}, fmt.Errorf("%w: oneof, min and max do not apply to structs", ErrInvalidConfig)
		}
		nested, err := buildStruct(t, depth+1)
		if err != nil {
			return nodeResult{}, err
		}
		return nodeResult{node: nested.node, nested: nested}, nil

	default:
		return nodeResult{}, fmt.Errorf("%w: %s", ErrUnsupportedType, t)
	}
}

func boolPtr(b bool) *bool { return &b }

var schemaCache sync.Map // reflect.Type -> cachedSchema

type cachedSchema struct {
	schema *structSchema
	err    error
}

func schemaFor[T any]() (*structSchema, error) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	if v, ok := schemaCache.Load(t); ok {
		c := v.(cachedSchema)
		return c.schema, c.err
	}
	s, err := newStructSchema(t)
	schemaCache.Store(t, cachedSchema{schema: s, err: err})
	return s, err
}

// validate checks the declared constraints on a decoded value.
func (s *structSchema) validate(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	for _, f := range s.fields {
		if err := f.validate(v.Field(f.index)); err != nil {
			return err
		}
	}
	return nil
}

func (f *fieldSchema) validate(fv reflect.Value) error {
	if f.spec.required && fv.IsZero() {
		return fmt.Errorf("%w: %s is required", ErrInvalidArgs, f.jsonName)
	}
	if f.nested != nil {
		return f.nested.validate(fv)
	}
	switch fv.Kind() {
	case reflect.String:
		if len(f.spec.oneof) == 0 {
			return nil
		}
		got := fv.String()
		for _, v := range f.spec.oneof {
			if got == v {
				return nil
			}
		}
		return fmt.Errorf("%w: %s=%q is not one of %v", ErrInvalidArgs, f.jsonName, got, f.spec.oneof)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return checkRange(f.jsonName, float64(fv.Int()), f.spec)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return checkRange(f.jsonName, float64(fv.Uint()), f.spec)
	case reflect.Float32, reflect.Float64:
		return checkRange(f.jsonName, fv.Float(), f.spec)
	case reflect.Slice, reflect.Array:
		n := float64(fv.Len())
		if f.spec.min != nil && n < *f.spec.min {
			return fmt.Errorf("%w: %s has %d items, min is %v", ErrInvalidArgs, f.jsonName, fv.Len(), *f.spec.min)
		}
		if f.spec.max != nil && n > *f.spec.max {
			return fmt.Errorf("%w: %s has %d items, max is %v", ErrInvalidArgs, f.jsonName, fv.Len(), *f.spec.max)
		}
		if fv.Len() > defaultMaxItems {
			return fmt.Errorf("%w: %s has %d items, cap is %d", ErrInvalidArgs, f.jsonName, fv.Len(), defaultMaxItems)
		}
	}
	return nil
}

func checkRange(name string, got float64, spec tagSpec) error {
	if spec.min != nil && got < *spec.min {
		return fmt.Errorf("%w: %s=%v is below min %v", ErrInvalidArgs, name, got, *spec.min)
	}
	if spec.max != nil && got > *spec.max {
		return fmt.Errorf("%w: %s=%v is above max %v", ErrInvalidArgs, name, got, *spec.max)
	}
	return nil
}

// decodeStrict decodes one JSON object into T. It rejects unknown fields,
// duplicate keys, trailing data, excessive nesting and oversized input.
func decodeStrict[T any](raw []byte, maxBytes int) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, fmt.Errorf("%w: empty JSON", ErrInvalidArgs)
	}
	if len(raw) > maxBytes {
		return out, fmt.Errorf("%w: %d bytes exceeds %d", ErrBudget, len(raw), maxBytes)
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return out, fmt.Errorf("%w: null is not a valid value", ErrInvalidArgs)
	}
	if err := scanJSON(raw, maxJSONDepth); err != nil {
		return out, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return out, fmt.Errorf("%w: trailing data", ErrInvalidArgs)
	}
	return out, nil
}

func scanJSON(raw []byte, maxDepth int) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := scanValue(dec, 0, maxDepth); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing data", ErrInvalidArgs)
	}
	return nil
}

func scanValue(dec *json.Decoder, depth, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: JSON nesting exceeds %d", ErrInvalidArgs, maxDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidArgs, err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("%w: object key is not a string", ErrInvalidArgs)
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("%w: duplicate key %q", ErrInvalidArgs, key)
			}
			seen[key] = struct{}{}
			if err := scanValue(dec, depth+1, maxDepth); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgs, err)
		}
	case '[':
		for dec.More() {
			if err := scanValue(dec, depth+1, maxDepth); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgs, err)
		}
	}
	return nil
}

// resolveHandles replaces every exact handle reference in a JSON value with
// the stored value and returns the labels of the resolved paths.
func resolveHandles(raw []byte, hs *handleSet) ([]byte, map[string]Label, error) {
	if hs == nil || len(raw) == 0 {
		return raw, nil, nil
	}
	if err := scanJSON(raw, maxJSONDepth); err != nil {
		return nil, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	labels := make(map[string]Label)
	resolved, err := resolveValue(v, "", hs, labels)
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	return out, labels, nil
}

func resolveValue(v any, path string, hs *handleSet, labels map[string]Label) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			resolved, err := resolveValue(val, joinPath(path, k), hs, labels)
			if err != nil {
				return nil, err
			}
			x[k] = resolved
		}
		return x, nil
	case []any:
		for i, val := range x {
			resolved, err := resolveValue(val, fmt.Sprintf("%s[%d]", path, i), hs, labels)
			if err != nil {
				return nil, err
			}
			x[i] = resolved
		}
		return x, nil
	case string:
		if !isHandleRef(x) {
			return x, nil
		}
		e, ok := hs.get(x)
		if !ok {
			return nil, fmt.Errorf("%w: unknown handle %s", ErrUngrounded, x)
		}
		labels[path] = e.label
		return e.value, nil
	default:
		return v, nil
	}
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// labels gives every field of a decoded argument the supplied base label,
// raised to the field's declared sensitivity.
func (s *structSchema) labels(base Label) map[string]Label {
	out := make(map[string]Label, len(s.fields))
	for _, f := range s.fields {
		l := base
		l.Sensitivity = maxSensitivity(l.Sensitivity, f.sensitivity())
		out[f.jsonName] = l
		if f.nested != nil {
			for k, v := range f.nested.labels(l) {
				out[f.jsonName+"."+k] = v
			}
		}
	}
	return out
}

// checkInputTags rejects a tag that only makes sense on a tool argument.
// Accepting it here would be a silent no-op on a security tag: the developer
// declares "this value must be an existing handle" on a field the runtime never
// grounds, and reads the result as a protection that does not exist.
func (s *structSchema) checkInputTags() error {
	for _, f := range s.fields {
		if f.spec.ground {
			return fmt.Errorf("%w: %s.%s: ground applies to a tool argument, not to a task input field", ErrInvalidConfig, s.typ, f.goName)
		}
		if f.spec.ref && f.spec.hasSensitivity {
			// ref means "public but addressable"; sensitivity means "limit
			// disclosure". A value cannot be both, and picking one silently would
			// leave the developer with a protection that does not exist.
			return fmt.Errorf("%w: %s.%s: ref and sensitivity are mutually exclusive", ErrInvalidConfig, s.typ, f.goName)
		}
		if f.nested != nil {
			if err := f.nested.checkInputTags(); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkArgTags rejects a tag that only makes sense on a task input field. The
// runtime labels a tool argument from the model as Derived no matter what the
// declaration says, so source=application on an argument is a declaration the
// Gate would never honour.
func (s *structSchema) checkArgTags() error {
	for _, f := range s.fields {
		if f.spec.source != "" {
			return fmt.Errorf("%w: %s.%s: source applies to a task input field, not to a tool argument", ErrInvalidConfig, s.typ, f.goName)
		}
		if f.spec.ref {
			return fmt.Errorf("%w: %s.%s: ref applies to a task input field, not to a tool argument", ErrInvalidConfig, s.typ, f.goName)
		}
		if f.nested != nil {
			if err := f.nested.checkArgTags(); err != nil {
				return err
			}
		}
	}
	return nil
}

// paths returns every JSON path a schema declares. NewTask uses it to reject a
// ground argument that binds to an input field the task does not have: a
// renamed input field would otherwise fail at run time, as a Deny the model
// cannot explain.
func (s *structSchema) paths() map[string]struct{} {
	out := make(map[string]struct{}, len(s.fields))
	var walk func(*structSchema, string)
	walk = func(sc *structSchema, prefix string) {
		for _, f := range sc.fields {
			path := joinPath(prefix, f.jsonName)
			out[path] = struct{}{}
			if f.nested != nil {
				walk(f.nested, path)
			}
		}
	}
	walk(s, "")
	return out
}

// groundBinding is one ground argument and the input field it binds to.
type groundBinding struct{ arg, source string }

// groundBindings lists the input fields the ground arguments of this schema
// expect. A bare ground binds to the argument's own path; ground=any opts out
// and is not listed.
func (s *structSchema) groundBindings() []groundBinding {
	var out []groundBinding
	var walk func(*structSchema, string)
	walk = func(sc *structSchema, prefix string) {
		for _, f := range sc.fields {
			path := joinPath(prefix, f.jsonName)
			if f.spec.ground && f.spec.groundSource != "any" {
				source := f.spec.groundSource
				if source == "" {
					source = path
				}
				out = append(out, groundBinding{arg: path, source: source})
			}
			if f.nested != nil {
				walk(f.nested, path)
			}
		}
	}
	walk(s, "")
	return out
}

// checkGrounding walks the argument schema and the handles the model used.
// A handle may appear only in a field tagged ground. A ground field must
// contain a handle, and that handle must come from the declared source (the
// argument path by default). A raw string never grounds an argument.
func (s *structSchema) checkGrounding(handleLabels map[string]Label) (bool, error) {
	for path, l := range handleLabels {
		f, ok := s.fieldByPath(path)
		if !ok || !f.spec.ground {
			return false, fmt.Errorf("%w: handle used in non-handle field %s", ErrUngrounded, path)
		}
		if err := groundSource(f, path, l); err != nil {
			return false, err
		}
	}
	grounded := true
	var walk func(*structSchema, string)
	walk = func(sc *structSchema, prefix string) {
		for _, f := range sc.fields {
			path := joinPath(prefix, f.jsonName)
			if f.spec.ground {
				if _, ok := handleLabels[path]; !ok {
					grounded = false
				}
			}
			if f.nested != nil {
				walk(f.nested, path)
			}
		}
	}
	walk(s, "")
	return grounded, nil
}

func (s *structSchema) fieldByPath(path string) (*fieldSchema, bool) {
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	cur := s
	for i, part := range parts {
		var next *fieldSchema
		for _, f := range cur.fields {
			if f.jsonName == part {
				next = f
				break
			}
		}
		if next == nil {
			return nil, false
		}
		if i == len(parts)-1 {
			return next, true
		}
		if next.nested == nil {
			return nil, false
		}
		cur = next.nested
	}
	return nil, false
}

func groundSource(f *fieldSchema, path string, l Label) error {
	if f.spec.groundSource == "any" {
		return nil
	}
	want := f.spec.groundSource
	if want == "" {
		want = path
	}
	if len(l.Sources) == 0 || l.Sources[0].ID != want {
		return fmt.Errorf("%w: handle for %s came from %q, want %q", ErrUngrounded, path, sourceID(l), want)
	}
	return nil
}

func sourceID(l Label) string {
	if len(l.Sources) == 0 {
		return ""
	}
	return l.Sources[0].ID
}

// marshalNoEscape encodes JSON without HTML escaping so a handle stays
// readable as <h1> in the prompt instead of \u003ch1\u003e.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// prepareInput validates In, redacts what must be redacted and returns the JSON
// the model may see plus the provenance of each field. Run does not need the
// provenance itself, but it is the only observable record of how an input value
// was labelled before masking.
func (s *structSchema) prepareInput(in any, p Principal, hs *handleSet) (map[string]any, map[string]Label, error) {
	rv := reflect.ValueOf(in)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, nil, fmt.Errorf("%w: nil input", ErrInvalidArgs)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("%w: input must be a struct", ErrInvalidConfig)
	}
	if err := s.validate(rv); err != nil {
		return nil, nil, err
	}
	labels := make(map[string]Label)
	out, err := s.prepareFields(rv, "", p, hs, labels)
	if err != nil {
		return nil, nil, err
	}
	return out, labels, nil
}

func (s *structSchema) prepareFields(v reflect.Value, prefix string, p Principal, hs *handleSet, labels map[string]Label) (map[string]any, error) {
	out := make(map[string]any, len(s.fields))
	for _, f := range s.fields {
		val, err := f.prepareValue(v.Field(f.index), joinPath(prefix, f.jsonName), p, hs, labels)
		if err != nil {
			return nil, err
		}
		out[f.jsonName] = val
	}
	return out, nil
}

func (f *fieldSchema) prepareValue(fv reflect.Value, path string, p Principal, hs *handleSet, labels map[string]Label) (any, error) {
	if f.nested != nil {
		return f.nested.prepareFields(fv, path, p, hs, labels)
	}

	switch fv.Kind() {
	case reflect.String:
		value := fv.String()
		var l Label
		switch f.spec.source {
		case "principal":
			name, ok := principalField(f.goName)
			if !ok {
				return nil, fmt.Errorf("%w: field %s cannot be taken from a principal", ErrInvalidConfig, f.goName)
			}
			want, _ := principalValue(p, name)
			if value != want {
				return nil, fmt.Errorf("%w: %s does not match the principal", ErrInvalidArgs, f.jsonName)
			}
			l = Label{
				Integrity:   Trusted,
				Sources:     []SourceRef{{Kind: "principal", ID: path}},
				Sensitivity: f.sensitivity(),
			}
		case "application":
			l = Label{
				Integrity:   Trusted,
				Sources:     []SourceRef{{Kind: "application", ID: path}},
				Sensitivity: f.sensitivity(),
			}
		default:
			l = Label{
				Integrity:   Untrusted,
				Sources:     []SourceRef{{Kind: "input", ID: path}},
				Sensitivity: f.sensitivity(),
			}
		}
		labels[path] = l
		return f.redact(value, l, hs), nil

	case reflect.Slice, reflect.Array:
		l := Label{
			Integrity:   Untrusted,
			Sources:     []SourceRef{{Kind: "input", ID: path}},
			Sensitivity: f.sensitivity(),
		}
		labels[path] = l
		out := make([]any, 0, fv.Len())
		for i := 0; i < fv.Len(); i++ {
			ev := fv.Index(i)
			if ev.Kind() == reflect.String {
				out = append(out, f.redact(ev.String(), l, hs))
				continue
			}
			out = append(out, ev.Interface())
		}
		return out, nil

	default:
		labels[path] = Label{
			Integrity:   Untrusted,
			Sources:     []SourceRef{{Kind: "input", ID: path}},
			Sensitivity: f.sensitivity(),
		}
		return fv.Interface(), nil
	}
}
