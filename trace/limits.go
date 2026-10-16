package trace

import "unicode/utf8"

const (
	DefaultMaxStringLength = 131072
	DefaultMaxDepth        = 8
	DefaultMaxArrayLength  = 50
	DefaultMaxObjectKeys   = 50
)

type exportConfig struct {
	maxStringLength int
	maxDepth        int
	maxArrayLength  int
	maxObjectKeys   int
	hideInput       bool
	hideOutput      bool
}

func (c exportConfig) clip(s string) string {
	if c.maxStringLength <= 0 || len(s) <= c.maxStringLength {
		return s
	}
	n := c.maxStringLength
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (c exportConfig) truncate(v any) any { return c.truncateDepth(v, 0) }

func (c exportConfig) truncateDepth(v any, depth int) any {
	switch x := v.(type) {
	case string:
		return c.clip(x)
	case []string:
		if depth >= c.maxDepth {
			return x
		}
		return c.clipStrings(x)
	case []any:
		if depth >= c.maxDepth {
			return x
		}
		n := len(x)
		if c.maxArrayLength > 0 && n > c.maxArrayLength {
			n = c.maxArrayLength
		}
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = c.truncateDepth(x[i], depth+1)
		}
		return out
	case map[string]any:
		if depth >= c.maxDepth {
			return x
		}
		out := make(map[string]any, len(x))
		n := 0
		for k, val := range x {
			if c.maxObjectKeys > 0 && n >= c.maxObjectKeys {
				break
			}
			out[k] = c.truncateDepth(val, depth+1)
			n++
		}
		return out
	default:
		return v
	}
}

func (c exportConfig) clipStrings(in []string) []string {
	n := len(in)
	if c.maxArrayLength > 0 && n > c.maxArrayLength {
		n = c.maxArrayLength
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = c.clip(in[i])
	}
	return out
}
