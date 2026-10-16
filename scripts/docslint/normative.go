package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const ruleDuplicateNormative = "duplicate-normative"

type normativeAssertion struct {
	ID       string
	Owner    []string
	RuleID   string
	Patterns []*regexp.Regexp
}

func (a normativeAssertion) owns(f string) bool {
	for _, o := range a.Owner {
		if f == o {
			return true
		}
	}
	return false
}

func (a normativeAssertion) matches(line string) bool {
	for _, re := range a.Patterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func (a normativeAssertion) accepts(target string) bool {
	t := strings.ToLower(target)
	rule := strings.ToLower(a.RuleID)
	if rule != "" && strings.Contains(t, rule) {
		return true
	}
	for _, o := range a.Owner {
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(o), ".md"))
		if base != "" && strings.Contains(t, base) {
			return true
		}
	}
	return false
}

var (
	businessRules = filepath.Join("docs", "internals", "business-rules.md")
	designDoc     = filepath.Join("docs", "internals", "design.md")
)

var normativeAssertions = []normativeAssertion{
	{
		ID:     "ratelimit-scope",
		Owner:  []string{businessRules, designDoc},
		RuleID: "BR-86",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)token bucket[^\n]{0,80}тенант`),
			regexp.MustCompile(`(?i)тенант[^\n]{0,80}token bucket`),
			regexp.MustCompile(`(?i)(нулевой|нулевом|нулевое)[^\n]{0,40}PerSecond[^\n]{0,60}отключа`),
			regexp.MustCompile(`(?i)PerSecond[^\n]{0,80}0[^\n]{0,40}отключа`),
		},
	},
	{
		ID:     "guard-l1-default",
		Owner:  []string{businessRules, designDoc},
		RuleID: "BR-26",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)L1[^\n]{0,140}(входит в сборку по умолчанию|часть(?:ю)? сборки по умолчанию|default build|в сборке по умолчанию)`),
			regexp.MustCompile(`(?i)(сборк\w+ по умолчанию|default build)[^\n]{0,140}\bL1\b`),
			regexp.MustCompile(`(?i)L2[^\n]{0,140}плагин`),
			regexp.MustCompile(`(?i)плагин[^\n]{0,80}\bL2\b`),
		},
	},
	{
		ID:     "rule-of-two-default",
		Owner:  []string{businessRules, designDoc},
		RuleID: "BR-51",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(Rule of Two|RuleOfTwo|AllowEgressAfterUntrusted)[^\n]{0,140}(включ[её]н|по умолчанию|enforced)`),
			regexp.MustCompile(`(?i)(включ[её]н|по умолчанию|enforced)[^\n]{0,140}(Rule of Two|RuleOfTwo|AllowEgressAfterUntrusted)`),
		},
	},
}

var (
	reDerivedHTML  = regexp.MustCompile(`<!--\s*derived:\s*([^>]+?)\s*-->`)
	reDerivedQuote = regexp.MustCompile(`^>\s*Производное от\s+(\S.*)$`)
)

type derivedRange struct {
	lo, hi int
	target string
}

func derivedRanges(lines []string) []derivedRange {
	starts := headingIndices(lines)
	out := make([]derivedRange, 0, len(lines))
	for i, line := range lines {
		target := markerTarget(line)
		if target == "" {
			continue
		}
		lo := 0
		for _, h := range starts {
			if h <= i {
				lo = h
			}
		}
		hi := len(lines)
		for _, h := range starts {
			if h > i {
				hi = h
				break
			}
		}
		out = append(out, derivedRange{lo: lo, hi: hi, target: target})
	}
	return out
}

func headingIndices(lines []string) []int {
	var out []int
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			out = append(out, i)
		}
	}
	return out
}

func markerTarget(line string) string {
	if m := reDerivedHTML.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := reDerivedQuote.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func licensed(ranges []derivedRange, n int, a normativeAssertion) bool {
	for _, r := range ranges {
		if n < r.lo || n >= r.hi {
			continue
		}
		if a.accepts(r.target) {
			return true
		}
	}
	return false
}

func duplicateNormative(files []string, contents map[string][]string) []Finding {
	var out []Finding
	for _, f := range files {
		lines, ok := contents[f]
		if !ok {
			continue
		}
		ranges := derivedRanges(lines)
		for i, line := range lines {
			if isDefinitionLine(line) {
				continue
			}
			for _, a := range normativeAssertions {
				if a.owns(f) || !a.matches(line) {
					continue
				}
				if licensed(ranges, i, a) {
					continue
				}
				out = append(out, Finding{
					Rule: ruleDuplicateNormative,
					File: f,
					Line: i + 1,
					Text: a.ID,
					Detail: fmt.Sprintf(
						"normative restatement of %s (owner %s, %s); mark it as derived with "+
							"`<!-- derived: %s -->` or a `> Производное от %s` note",
						a.ID, strings.Join(a.Owner, " + "), a.RuleID, a.RuleID, a.RuleID),
				})
			}
		}
	}
	return out
}
