package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type rail string

const (
	railInput      rail = "input"
	railOutput     rail = "output"
	railToolResult rail = "tool_result"
)

func (r rail) valid() bool {
	switch r {
	case railInput, railOutput, railToolResult:
		return true
	default:
		return false
	}
}

type layer string

const (
	layerL1 layer = "L1"
	layerL2 layer = "L2"
)

type testCase struct {
	ID             string   `json:"id"`
	Text           string   `json:"text"`
	Rail           rail     `json:"rail"`
	Layer          layer    `json:"layer,omitempty"`
	Expect         []string `json:"expect"`
	MustNotContain []string `json:"mustNotContain,omitempty"`
	Note           string   `json:"note,omitempty"`
	expectSet      map[string]bool
	forbidSet      map[string]bool
}

func (c testCase) expects(cat category) bool { return c.expectSet[string(cat)] }

func (c testCase) forbids(cat category) bool { return c.forbidSet[string(cat)] }

func (c testCase) wantsNothing() bool { return len(c.Expect) == 0 }

func loadCases(path string) ([]testCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		out  []testCase
		line int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	seen := map[string]int{}
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "//") {
			continue
		}
		var c testCase
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if c.ID == "" {
			return nil, fmt.Errorf("%s:%d: id is required", path, line)
		}
		if prev, dup := seen[c.ID]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate id %q (first seen on line %d)", path, line, c.ID, prev)
		}
		seen[c.ID] = line
		if c.Text == "" {
			return nil, fmt.Errorf("%s:%d: text is required", path, line)
		}
		if c.Rail == "" {
			c.Rail = railInput
		}
		if !c.Rail.valid() {
			return nil, fmt.Errorf("%s:%d: unknown rail %q", path, line, c.Rail)
		}
		if c.Layer == "" {
			c.Layer = layerL1
		}
		if c.Layer != layerL1 && c.Layer != layerL2 {
			return nil, fmt.Errorf("%s:%d: unknown layer %q", path, line, c.Layer)
		}
		if c.Layer == layerL2 && c.wantsNothing() {
			return nil, fmt.Errorf("%s:%d: a deferred case must expect something", path, line)
		}
		c.expectSet = toSet(c.Expect)
		c.forbidSet = toSet(c.MustNotContain)
		for cat := range c.forbidSet {
			if c.expectSet[cat] {
				return nil, fmt.Errorf("%s:%d: category %q is both expected and forbidden", path, line, cat)
			}
		}
		for cat := range c.expectSet {
			if !knownCategory(cat) {
				return nil, fmt.Errorf("%s:%d: unknown category %q", path, line, cat)
			}
		}
		for cat := range c.forbidSet {
			if !knownCategory(cat) {
				return nil, fmt.Errorf("%s:%d: unknown category %q", path, line, cat)
			}
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: the corpus is empty", path)
	}
	return out, nil
}

func toSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s] = true
	}
	return out
}

func knownCategory(s string) bool {
	switch category(s) {
	case catPII, catSecret, catInjection, catUnsafe:
		return true
	default:
		return false
	}
}

func sortStrings(in []string) { sort.Strings(in) }
