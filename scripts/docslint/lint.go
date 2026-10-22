package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	ruleBrokenLink = "broken-link"
	ruleUndefined  = "undefined-ref"
	ruleStaleRange = "stale-range"
	ruleDuplicate  = "duplicate-id"
)

type Finding struct {
	Rule   string
	File   string
	Line   int
	Text   string
	Detail string
	Info   bool
}

func (f Finding) String() string {
	kind := f.Rule
	if f.Info {
		kind = "info"
	}
	return fmt.Sprintf("%s:%d: %s: %s: %s", f.File, f.Line, kind, f.Text, f.Detail)
}

type Report struct {
	Files  []string
	IDs    idSet
	Failed []Finding
	Infos  []Finding
}

func (r Report) OK() bool { return len(r.Failed) == 0 }

var definitionSources = []string{
	filepath.Join("docs", "internals", "design.md"),
	filepath.Join("docs", "internals", "business-rules.md"),
	filepath.Join("docs", "internals", "decisions.md"),
}

var rootDocs = []string{"AGENTS.md", "README.md", "README.ru.md"}

func Run(root string) (Report, error) {
	var rep Report

	files, err := docFiles(root)
	if err != nil {
		return rep, err
	}
	rep.Files = files

	contents := make(map[string][]string, len(files))
	for _, f := range files {
		raw, rerr := os.ReadFile(filepath.Join(root, f))
		if rerr != nil {
			return rep, rerr
		}
		contents[f] = splitLines(string(raw))
	}

	defined := newIDSet()
	sites := map[string][]site{}
	for _, src := range definitionSources {
		lines, ok := contents[src]
		if !ok {
			continue
		}
		for i, line := range lines {
			if !isDefinitionLine(line) {
				continue
			}
			id, ok := definedID(line)
			if !ok {
				continue
			}
			defined.add(id)
			sites[id.String()] = append(sites[id.String()], site{File: src, Line: i + 1})
		}
	}
	rep.IDs = defined

	for _, f := range files {
		lines := contents[f]
		inFence := false
		for i, line := range lines {
			n := i + 1
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			if !isDefinitionLine(line) && len(defined) > 0 {
				for _, id := range idsIn(line) {
					if defined.has(id) {
						continue
					}
					rep.Failed = append(rep.Failed, Finding{
						Rule:   ruleUndefined,
						File:   f,
						Line:   n,
						Text:   id.String(),
						Detail: fmt.Sprintf("referenced but never defined in %s", strings.Join(relSourceNames(), ", ")),
					})
				}
			}
			for _, l := range linksIn(stripInlineCode(line)) {
				if bad, detail := checkLink(root, f, contents, l); bad {
					rep.Failed = append(rep.Failed, Finding{
						Rule:   ruleBrokenLink,
						File:   f,
						Line:   n,
						Text:   l.text,
						Detail: detail,
					})
				}
			}
			for _, r := range staleRanges(line, defined) {
				rep.Failed = append(rep.Failed, Finding{
					Rule:   ruleStaleRange,
					File:   f,
					Line:   n,
					Text:   r.Text,
					Detail: r.Detail,
				})
			}
			rep.Infos = append(rep.Infos, overClaims(line, defined)...)
		}
	}

	rep.Failed = append(rep.Failed, duplicateNormative(files, contents)...)

	for _, key := range sortedKeys(sites) {
		s := sites[key]
		if len(s) > 1 {
			rep.Failed = append(rep.Failed, Finding{
				Rule:   ruleDuplicate,
				File:   s[1].File,
				Line:   s[1].Line,
				Text:   key,
				Detail: fmt.Sprintf("defined %d times, first at %s:%d", len(s), s[0].File, s[0].Line),
			})
		}
	}
	for _, series := range []string{seriesI, seriesBR, seriesD} {
		nums := defined.nums(series)
		if len(nums) == 0 {
			continue
		}
		for i := 1; i < len(nums); i++ {
			if nums[i] != nums[i-1]+1 {
				rep.Infos = append(rep.Infos, Finding{
					Rule: "numbering-gap",
					File: relSourceNameForSeries(series),
					Line: 1,
					Text: fmt.Sprintf("%s%d → %s%d", series, nums[i-1], series, nums[i]),
					Info: true,
					Detail: "numbering gap " +
						"(accepted where documented; not an error)",
				})
			}
		}
	}

	sortFindings(rep.Failed)
	sortFindings(rep.Infos)
	return rep, nil
}

type site struct {
	File string
	Line int
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].File != fs[j].File {
			return fs[i].File < fs[j].File
		}
		if fs[i].Line != fs[j].Line {
			return fs[i].Line < fs[j].Line
		}
		if fs[i].Rule != fs[j].Rule {
			return fs[i].Rule < fs[j].Rule
		}
		return fs[i].Text < fs[j].Text
	})
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

// stripInlineCode blanks the text between backticks, so Go generics such as
// View[Out](f) are not mistaken for Markdown links.
func stripInlineCode(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '`' {
			in = !in
			b.WriteRune(r)
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func docFiles(root string) ([]string, error) {
	var out []string
	docs := filepath.Join(root, "docs")
	if st, err := os.Stat(docs); err == nil && st.IsDir() {
		err := filepath.WalkDir(docs, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".md") {
				rel, rerr := filepath.Rel(root, p)
				if rerr != nil {
					return rerr
				}
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, name := range rootDocs {
		if st, err := os.Stat(filepath.Join(root, name)); err == nil && !st.IsDir() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func relSourceNames() []string { return definitionSources }

func relSourceNameForSeries(series string) string {
	switch series {
	case seriesI, seriesBR:
		return filepath.ToSlash(filepath.Join("docs", "internals", "design.md"))
	default:
		return filepath.ToSlash(filepath.Join("docs", "internals", "decisions.md"))
	}
}

func checkLink(root, from string, contents map[string][]string, l link) (bool, string) {
	if external(l.target) {
		return false, ""
	}
	file, anchor := splitTarget(l.target)
	if file == "" {
		if anchor == "" {
			return false, ""
		}
		if !has(anchorsOf(join(contents[from])), decodeAnchor(anchor)) {
			return true, fmt.Sprintf("anchor #%s not found in %s", anchor, from)
		}
		return false, ""
	}
	if strings.HasPrefix(file, "/") {
		return false, ""
	}
	target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(from), file)))
	if target != from {
		if _, ok := contents[target]; !ok {
			if _, err := os.Stat(filepath.Join(root, target)); err != nil {
				return true, fmt.Sprintf("target %s does not exist", l.target)
			}
		}
	}
	if anchor == "" {
		return false, ""
	}
	lines, ok := contents[target]
	if !ok {
		return false, ""
	}
	want := decodeAnchor(anchor)
	if anchorsOf(join(lines))[want] {
		return false, ""
	}
	for a := range anchorsOf(join(lines)) {
		if decodeAnchor(a) == want {
			return false, ""
		}
	}
	return true, fmt.Sprintf("anchor #%s not found in %s", anchor, target)
}

func has(m map[string]bool, k string) bool { return m[k] }

func join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

var (
	reRangeDash   = regexp.MustCompile("(BR-\\d{1,3}|I-\\d{1,3}|D\\d{1,3})`?\\s*(?:…|\\.\\.\\.|–|—|-)\\s*`?(BR-\\d{1,3}|I-\\d{1,3}|D\\d{1,3})")
	reRangeInline = regexp.MustCompile("(BR-\\d{1,3}|I-\\d{1,3}|D\\d{1,3})`?\\s*\\|\\s*`?(BR-\\d{1,3}|I-\\d{1,3}|D\\d{1,3})")
)

type rangeClaim struct {
	Text   string
	Detail string
}

func seriesClaims(line string) map[string]rawPair {
	if isDefinitionLine(line) {
		return nil
	}
	pairs := allRangePairs(line)
	if len(pairs) == 0 {
		return nil
	}
	out := map[string]rawPair{}
	for _, p := range pairs {
		lo, okLo := parseID(p.lo)
		hi, okHi := parseID(p.hi)
		if !okLo || !okHi || lo.Series != hi.Series || lo.Num > hi.Num {
			continue
		}
		if _, dup := out[lo.Series]; dup {
			return nil
		}
		out[lo.Series] = p
	}
	return out
}

func staleRanges(line string, defined idSet) []rangeClaim {
	var out []rangeClaim
	for series, p := range seriesClaims(line) {
		lo, okLo := parseID(p.lo)
		hi, okHi := parseID(p.hi)
		if !okLo || !okHi {
			continue
		}
		nums := defined.nums(series)
		if len(nums) == 0 || lo.Num != nums[0] {
			continue
		}
		extra := false
		for _, id := range idsIn(line) {
			if id.Series == series && id.Num != lo.Num && id.Num != hi.Num {
				extra = true
			}
		}
		if extra {
			continue
		}
		max := defined.maxOf(series)
		if max < 0 || hi.Num >= max {
			continue
		}
		out = append(out, rangeClaim{
			Text:   p.text,
			Detail: fmt.Sprintf("claims the series ends at %s, but %s is defined", hi, ID{Series: series, Num: max}),
		})
	}
	return out
}

func overClaims(line string, defined idSet) []Finding {
	var out []Finding
	for series, p := range seriesClaims(line) {
		hi, ok := parseID(p.hi)
		if !ok {
			continue
		}
		max := defined.maxOf(series)
		if max >= 0 && hi.Num > max {
			out = append(out, Finding{
				Rule:   "range-over-claim",
				Text:   p.text,
				Info:   true,
				Detail: fmt.Sprintf("claims %s but the highest defined is %s", p.text, ID{Series: series, Num: max}),
			})
		}
	}
	return out
}

type rawPair struct {
	lo, hi, text string
}

func allRangePairs(line string) []rawPair {
	spans := codeSpans(line)
	if len(spans) == 0 {
		return nil
	}
	var out []rawPair
	for _, re := range []*regexp.Regexp{reRangeDash, reRangeInline} {
		for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
			if !withinSpans(spans, m[2], m[3]) && !withinSpans(spans, m[4], m[5]) {
				continue
			}
			out = append(out, rawPair{
				lo:   line[m[2]:m[3]],
				hi:   line[m[4]:m[5]],
				text: strings.TrimSpace(line[m[0]:m[1]]),
			})
		}
	}
	return out
}

type span struct{ start, end int }

func codeSpans(line string) []span {
	var out []span
	for i := 0; i < len(line); i++ {
		if line[i] != '`' {
			continue
		}
		j := strings.IndexByte(line[i+1:], '`')
		if j < 0 {
			break
		}
		out = append(out, span{start: i, end: i + 1 + j + 1})
		i += j + 1
	}
	return out
}

func withinSpans(spans []span, start, end int) bool {
	for _, s := range spans {
		if start >= s.start && end <= s.end {
			return true
		}
	}
	return false
}
