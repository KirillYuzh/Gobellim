// Package tools — небольшой типизированный движок инструментов: объявление
// выводит JSON Schema из типа аргументов, реестр собирает детерминированный
// набор, а каждый вызов проверяет аргументы и результат.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	stjs "github.com/santhosh-tekuri/jsonschema/v6"
)

// Effect — класс побочного эффекта инструмента. Демо пишет его в лог;
// реально вызов ограничивает разрешение из файла политики.
type Effect int

const (
	// EffectRead — инструмент только читает состояние.
	EffectRead Effect = iota
	// EffectWrite — инструмент меняет состояние.
	EffectWrite
	// EffectEgress — инструмент отправляет данные за пределы сервиса.
	EffectEgress
)

// String возвращает имя эффекта.
func (e Effect) String() string {
	switch e {
	case EffectRead:
		return "read"
	case EffectWrite:
		return "write"
	case EffectEgress:
		return "egress"
	default:
		return "unknown"
	}
}

// Policy объявляет эффект инструмента. Это Option, поэтому передаётся прямо в
// New. Нулевое значение — только чтение.
type Policy struct{ effect Effect }

// Effect возвращает объявленный эффект.
func (p Policy) Effect() Effect { return p.effect }

// apply реализует Option.
func (p Policy) apply(o *options) { o.effect = p.effect }

// Read — политика только для чтения, нулевое значение.
func Read() Policy { return Policy{} }

// Write — политика для инструмента, который меняет состояние.
func Write() Policy { return Policy{effect: EffectWrite} }

// Egress — политика для инструмента, который ходит в другой сервис.
func Egress() Policy { return Policy{effect: EffectEgress} }

type options struct {
	description string
	permission  string
	effect      Effect
}

// Option настраивает объявление инструмента.
type Option interface{ apply(*options) }

type optionFunc func(*options)

func (f optionFunc) apply(o *options) { f(o) }

// Desc задаёт описание, которое видит модель.
func Desc(s string) Option { return optionFunc(func(o *options) { o.description = s }) }

// Perm переопределяет разрешение по умолчанию tool:<name>.
func Perm(p string) Option { return optionFunc(func(o *options) { o.permission = p }) }

// ToolPermission возвращает разрешение RBAC по умолчанию для инструмента.
func ToolPermission(name string) string { return "tool:" + name }

// Def — контракт объявленного инструмента во время работы: его хватает,
// чтобы описать инструмент модели, ограничить вызов разрешением и выполнить его.
type Def interface {
	Name() string
	Description() string
	Permission() string
	Effect() Effect
	Schema() json.RawMessage
	Invoke(ctx context.Context, args json.RawMessage) (string, error)
	Err() error
}

// Tool — типизированное объявление инструмента. In — тип аргументов, Out — тип
// результата. Создаётся через New, регистрируется через Registry.
type Tool[In, Out any] struct {
	name        string
	desc        string
	permission  string
	effect      Effect
	fn          func(context.Context, In) (Out, error)
	schema      json.RawMessage
	compiled    *stjs.Schema
	outCompiled *stjs.Schema
	err         error
}

// New объявляет типизированный инструмент. JSON Schema выводится из In,
// результат сериализуется из Out и сверяется со схемой Out. Ошибки объявления
// всплывают в Registry.Build.
func New[In, Out any](name string, fn func(context.Context, In) (Out, error), opts ...Option) *Tool[In, Out] {
	t := &Tool[In, Out]{name: name, fn: fn}
	if name == "" {
		t.err = fmt.Errorf("tool: name is required")
		return t
	}
	if fn == nil {
		t.err = fmt.Errorf("tool: %s: handler is required", name)
		return t
	}
	o := options{permission: ToolPermission(name)}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&o)
		}
	}
	t.desc = o.description
	t.permission = o.permission
	t.effect = o.effect

	schema, err := SchemaFor[In]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema: %w", name, err)
		return t
	}
	compiled, err := CompileSchema(name, schema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: input schema: %w", name, err)
		return t
	}
	outSchema, err := SchemaFor[Out]()
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema: %w", name, err)
		return t
	}
	outCompiled, err := CompileSchema(name+"_out", outSchema)
	if err != nil {
		t.err = fmt.Errorf("tool: %s: output schema: %w", name, err)
		return t
	}
	t.schema = schema
	t.compiled = compiled
	t.outCompiled = outCompiled
	return t
}

// Name возвращает имя инструмента.
func (t *Tool[In, Out]) Name() string { return t.name }

// Description возвращает описание для модели.
func (t *Tool[In, Out]) Description() string { return t.desc }

// Permission возвращает разрешение RBAC, которым ограничен инструмент.
func (t *Tool[In, Out]) Permission() string { return t.permission }

// Effect возвращает объявленный эффект.
func (t *Tool[In, Out]) Effect() Effect { return t.effect }

// Schema возвращает JSON Schema аргументов.
func (t *Tool[In, Out]) Schema() json.RawMessage { return t.schema }

// Err возвращает ошибку объявления или nil.
func (t *Tool[In, Out]) Err() error { return t.err }

// Invoke проверяет аргументы по входной схеме, вызывает типизированную функцию,
// проверяет результат по выходной схеме и возвращает его как JSON.
func (t *Tool[In, Out]) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if t.compiled != nil {
		var v any
		if err := json.Unmarshal(args, &v); err != nil {
			return "", fmt.Errorf("tool: %s: invalid JSON arguments: %w", t.name, err)
		}
		if err := t.compiled.Validate(v); err != nil {
			return "", fmt.Errorf("tool: %s: arguments do not match schema: %w", t.name, err)
		}
	}
	var in In
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("tool: %s: decode arguments: %w", t.name, err)
	}
	out, err := t.fn(ctx, in)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("tool: %s: encode result: %w", t.name, err)
	}
	if t.outCompiled != nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", fmt.Errorf("tool: %s: result is not JSON: %w", t.name, err)
		}
		if err := t.outCompiled.Validate(v); err != nil {
			return "", fmt.Errorf("tool: %s: result does not match schema: %w", t.name, err)
		}
	}
	return string(raw), nil
}

var _ Def = (*Tool[int, int])(nil)

// Registry собирает объявления и компилирует их в Set.
type Registry struct {
	defs []Def
	seen map[string]struct{}
	errs []error
}

// NewRegistry возвращает пустой реестр.
func NewRegistry() *Registry {
	return &Registry{seen: make(map[string]struct{})}
}

// Register добавляет объявления и возвращает реестр, чтобы вызовы сцеплялись.
// nil Def пропускается; ошибка объявления или повтор имени копятся до Build.
func (r *Registry) Register(defs ...Def) *Registry {
	for _, def := range defs {
		if def == nil {
			continue
		}
		if _, dup := r.seen[def.Name()]; dup {
			r.errs = append(r.errs, fmt.Errorf("tool: %s is registered twice", def.Name()))
			continue
		}
		r.seen[def.Name()] = struct{}{}
		r.defs = append(r.defs, def)
	}
	return r
}

// Build проверяет все объявления и возвращает неизменяемый набор.
func (r *Registry) Build() (*Set, error) {
	errs := append([]error(nil), r.errs...)
	set := &Set{byName: make(map[string]Def, len(r.defs)), order: make([]string, 0, len(r.defs))}
	for _, def := range r.defs {
		if err := def.Err(); err != nil {
			errs = append(errs, err)
			continue
		}
		set.byName[def.Name()] = def
		set.order = append(set.order, def.Name())
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return set, nil
}

// Set — неизменяемый скомпилированный набор инструментов.
type Set struct {
	byName map[string]Def
	order  []string
}

// Get возвращает объявление по имени или nil.
func (s *Set) Get(name string) Def {
	if s == nil {
		return nil
	}
	return s.byName[name]
}

// All возвращает объявления в порядке регистрации.
func (s *Set) All() []Def {
	if s == nil {
		return nil
	}
	out := make([]Def, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

// Len возвращает число инструментов.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}

// Names возвращает имена инструментов в лексикографическом порядке.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := append([]string(nil), s.order...)
	sort.Strings(out)
	return out
}
