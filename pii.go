package kit

import (
	"strings"

	"github.com/gobellim/kit/guard"
)

func rehydrateArgs(args string, allow bool, subs []guard.Substitution) (string, bool) {
	if !allow || len(subs) == 0 {
		return args, false
	}
	values, ok := unambiguous(subs)
	if !ok || len(values) == 0 {
		return args, false
	}
	out := args
	replaced := false
	for placeholder, value := range values {
		if strings.Contains(out, placeholder) {
			out = strings.ReplaceAll(out, placeholder, value)
			replaced = true
		}
	}
	return out, replaced
}

func unambiguous(subs []guard.Substitution) (map[string]string, bool) {
	values := make(map[string]string, len(subs))
	for _, s := range subs {
		if s.Placeholder == "" || s.Value == "" {
			return nil, false
		}
		if prev, seen := values[s.Placeholder]; seen && prev != s.Value {
			return nil, false
		}
		values[s.Placeholder] = s.Value
	}
	return values, true
}

func maskValues(text string, subs []guard.Substitution) string {
	for _, s := range subs {
		if s.Value != "" {
			text = strings.ReplaceAll(text, s.Value, s.Placeholder)
		}
	}
	return text
}
