package store

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Filter is a metadata filter language compiled to an Elasticsearch query by
// CompileFilter.
type Filter map[string]any

var allowedFilterFields = map[string]struct{}{
	"doc_id":      {},
	"source":      {},
	"position":    {},
	"created_at":  {},
	"quarantined": {},
}

var rangeOperators = map[string]string{
	"$gt":  "gt",
	"$gte": "gte",
	"$lt":  "lt",
	"$lte": "lte",
}

// CompileFilter translates Filter into an Elasticsearch query, accepting the
// fields doc_id, source, position, created_at, and quarantined and the operators
// $eq, $ne, $gt, $gte, $lt, $lte, $in, $nin, $exists, $and, $or, and $not.
// An empty filter yields nil, and invalid fields, operators, or values wrap
// core.ErrInvalidArgs.
func CompileFilter(f Filter) (map[string]any, error) {
	if len(f) == 0 {
		return nil, nil
	}
	clauses := make([]any, 0, len(f))
	for _, key := range sortedKeys(f) {
		val := f[key]
		switch key {
		case "$and", "$or", "$not":
			clause, err := compileLogical(key, val)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, clause)
		default:
			if _, ok := allowedFilterFields[key]; !ok {
				return nil, invalidFilter("unknown field %q", key)
			}
			fieldClauses, err := compileField(key, val)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, fieldClauses...)
		}
	}
	return andClauses(clauses), nil
}

func compileField(field string, val any) ([]any, error) {
	ops, ok := asStringMap(val)
	if !ok {
		return []any{equalityClause(field, val)}, nil
	}
	if len(ops) == 0 {
		return nil, invalidFilter("field %q has an empty operator set", field)
	}
	rangeBody := map[string]any{}
	clauses := make([]any, 0, len(ops))
	for _, op := range sortedKeys(ops) {
		v := ops[op]
		switch {
		case op == "$eq":
			clauses = append(clauses, equalityClause(field, v))
		case op == "$ne":
			clauses = append(clauses, negate(equalityClause(field, v)))
		case rangeOperators[op] != "":
			rangeBody[rangeOperators[op]] = v
		case op == "$in":
			terms, err := asSlice(v)
			if err != nil {
				return nil, invalidFilter("$in for field %q: %v", field, err)
			}
			clauses = append(clauses, termsClause(field, terms))
		case op == "$nin":
			terms, err := asSlice(v)
			if err != nil {
				return nil, invalidFilter("$nin for field %q: %v", field, err)
			}
			clauses = append(clauses, negate(termsClause(field, terms)))
		case op == "$exists":
			b, ok := v.(bool)
			if !ok {
				return nil, invalidFilter("$exists for field %q must be a boolean", field)
			}
			exists := map[string]any{"exists": map[string]any{"field": field}}
			if b {
				clauses = append(clauses, exists)
			} else {
				clauses = append(clauses, negate(exists))
			}
		case op == "$and" || op == "$or" || op == "$not":
			return nil, invalidFilter("logical operator %q cannot be nested inside field %q", op, field)
		default:
			return nil, invalidFilter("unknown operator %q for field %q", op, field)
		}
	}
	if len(rangeBody) > 0 {
		clauses = append(clauses, map[string]any{"range": map[string]any{field: rangeBody}})
	}
	return clauses, nil
}

func compileLogical(op string, val any) (map[string]any, error) {
	switch op {
	case "$and", "$or":
		filters, err := asFilterList(val)
		if err != nil {
			return nil, invalidFilter("%s: %v", op, err)
		}
		subs := make([]any, 0, len(filters))
		for _, sub := range filters {
			clause, err := CompileFilter(sub)
			if err != nil {
				return nil, err
			}
			if clause == nil {
				continue
			}
			subs = append(subs, clause)
		}
		if op == "$and" {
			return map[string]any{"bool": map[string]any{"filter": subs}}, nil
		}
		return map[string]any{"bool": map[string]any{"should": subs, "minimum_should_match": 1}}, nil
	case "$not":
		sub, err := asFilter(val)
		if err != nil {
			return nil, invalidFilter("$not: %v", err)
		}
		clause, err := CompileFilter(sub)
		if err != nil {
			return nil, err
		}
		if clause == nil {
			return nil, invalidFilter("$not requires a non-empty filter")
		}
		return negate(clause), nil
	}
	return nil, invalidFilter("unknown logical operator %q", op)
}

func equalityClause(field string, v any) map[string]any {
	return map[string]any{"term": map[string]any{field: v}}
}

func termsClause(field string, terms []any) map[string]any {
	return map[string]any{"terms": map[string]any{field: terms}}
}

func negate(clause map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"must_not": []any{clause}}}
}

func andClauses(clauses []any) map[string]any {
	switch len(clauses) {
	case 0:
		return nil
	case 1:
		return clauses[0].(map[string]any)
	default:
		return map[string]any{"bool": map[string]any{"filter": clauses}}
	}
}

func asFilter(v any) (Filter, error) {
	switch t := v.(type) {
	case Filter:
		return t, nil
	case map[string]any:
		return Filter(t), nil
	}
	return nil, fmt.Errorf("expected a filter object, got %T", v)
}

func asStringMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case Filter:
		return map[string]any(t), true
	}
	return nil, false
}

func asFilterList(v any) ([]Filter, error) {
	if t, ok := v.([]Filter); ok {
		return t, nil
	}
	items, err := asSlice(v)
	if err != nil {
		return nil, fmt.Errorf("expected an array of filter objects, got %T", v)
	}
	out := make([]Filter, 0, len(items))
	for i, e := range items {
		f, err := asFilter(e)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func asSlice(v any) ([]any, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("expected an array, got %T", v)
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out = append(out, rv.Index(i).Interface())
	}
	return out, nil
}

func sortedKeys[M ~map[string]any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func invalidFilter(format string, args ...any) error {
	return fmt.Errorf("store: filter: "+format+": %w", append(args, core.ErrInvalidArgs)...)
}
