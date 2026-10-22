package tool

import (
	"errors"
	"fmt"
	"sort"
)

// Registry collects tool declarations and compiles them into a ToolSet.
// Declaration errors surface at Build.
type Registry struct {
	defs []Def
	seen map[string]struct{}
	errs []error
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{seen: make(map[string]struct{})}
}

// Register adds declarations to the registry and returns the same registry so
// calls chain. A nil Def is ignored; a declaration error or a duplicate name
// is kept and reported by Build.
func (r *Registry) Register(defs ...Def) *Registry {
	for _, def := range defs {
		if def == nil {
			continue
		}
		if _, dup := r.seen[def.Name()]; dup {
			r.errs = append(r.errs, fmt.Errorf("tool: %s is registered twice: %w", def.Name(), ErrInvalidConfig))
			continue
		}
		r.seen[def.Name()] = struct{}{}
		r.defs = append(r.defs, def)
	}
	return r
}

// Build validates every declaration and returns the immutable tool set.
// It returns ErrInvalidConfig when a declaration is invalid or a name repeats,
// reporting every error at once via errors.Join.
func (r *Registry) Build() (*ToolSet, error) {
	errs := append([]error(nil), r.errs...)
	set := &ToolSet{
		byName: make(map[string]Def, len(r.defs)),
		order:  make([]string, 0, len(r.defs)),
	}
	for _, def := range r.defs {
		if def == nil {
			continue
		}
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

// ToolSet is an immutable, compiled set of tools with deterministic ordering.
type ToolSet struct {
	byName map[string]Def
	order  []string
}

// Len returns the number of tools in the set.
func (s *ToolSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}

// Names returns the tool names in lexical order, so the model-facing catalog is
// deterministic regardless of registration order.
func (s *ToolSet) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.order))
	out = append(out, s.order...)
	sort.Strings(out)
	return out
}

// All returns the definitions in registration order.
func (s *ToolSet) All() []Def {
	if s == nil {
		return nil
	}
	out := make([]Def, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.byName[name])
	}
	return out
}

// Get returns the definition for name, or nil.
func (s *ToolSet) Get(name string) Def {
	if s == nil {
		return nil
	}
	return s.byName[name]
}
