package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

func goodRepo() map[string]string {
	return map[string]string{
		"AGENTS.md": "# Agent\n\nInvariants `I-01`–`I-03` and rules BR-01 live in [design](docs/internals/design.md).\n",
		"docs/internals/design.md": "# Design\n\n## Invariants\n\n" +
			"| I-01 | Call every I/O. |\n" +
			"| I-02 | Canonicalisation. |\n" +
			"| I-03 | Offsets. |\n" +
			"\n## Decisions\n\n| D1 | First. |\n| D21 | Contract. |\n| D30 | Last of the first batch. |\n",
		"docs/internals/business-rules.md": "# Rules\n\n| BR-01 | All calls go through internal/call. |\n",
		"docs/internals/decisions.md":      "# Decisions\n\nD21…D30 live in the design log.\n",
		"docs/guides/security.md":          "# Security\n\nSee [design §1](../internals/design.md#invariants).\n",
	}
}

func runFixture(t *testing.T, files map[string]string) Report {
	t.Helper()
	rep, err := Run(fixture(t, files))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func hasRule(rep Report, rule string) (Finding, bool) {
	for _, f := range rep.Failed {
		if f.Rule == rule {
			return f, true
		}
	}
	return Finding{}, false
}

func TestRun(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(map[string]string)
		wantOK   bool
		wantRule string
		wantIn   string
	}{
		{
			name:   "good repository passes",
			mutate: func(map[string]string) {},
			wantOK: true,
		},
		{
			name: "broken relative link fails",
			mutate: func(f map[string]string) {
				f["docs/guides/security.md"] = "# Security\n\nSee [rules](../internals/nope.md).\n"
			},
			wantOK:   false,
			wantRule: ruleBrokenLink,
			wantIn:   "docs/guides/security.md:3",
		},
		{
			name: "missing anchor fails",
			mutate: func(f map[string]string) {
				f["docs/guides/security.md"] = "# Security\n\nSee [design](../internals/design.md#nope).\n"
			},
			wantOK:   false,
			wantRule: ruleBrokenLink,
			wantIn:   "docs/guides/security.md:3",
		},
		{
			name: "undefined reference fails",
			mutate: func(f map[string]string) {
				f["AGENTS.md"] = "# Agent\n\nRule BR-42 does not exist.\n"
			},
			wantOK:   false,
			wantRule: ruleUndefined,
			wantIn:   "AGENTS.md:3",
		},
		{
			name: "stale closed range fails",
			mutate: func(f map[string]string) {
				f["AGENTS.md"] = "# Agent\n\nInvariants `I-01`–`I-02`.\n"
			},
			wantOK:   false,
			wantRule: ruleStaleRange,
			wantIn:   "AGENTS.md:3",
		},
		{
			name: "stale adjacent range fails",
			mutate: func(f map[string]string) {
				f["AGENTS.md"] = "# Agent\n\nInvariants `I-01|I-02`.\n"
			},
			wantOK:   false,
			wantRule: ruleStaleRange,
			wantIn:   "AGENTS.md:3",
		},
		{
			name: "duplicate definition fails",
			mutate: func(f map[string]string) {
				f["docs/internals/business-rules.md"] = "# Rules\n\n| BR-01 | One. |\n| BR-01 | Also one. |\n"
			},
			wantOK:   false,
			wantRule: ruleDuplicate,
			wantIn:   "docs/internals/business-rules.md:4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := goodRepo()
			tc.mutate(files)
			rep := runFixture(t, files)
			if tc.wantOK {
				if !rep.OK() {
					t.Fatalf("want OK, got:\n%s", report(rep))
				}
				return
			}
			if rep.OK() {
				t.Fatalf("want failure %s, got OK", tc.wantRule)
			}
			f, ok := hasRule(rep, tc.wantRule)
			if !ok {
				t.Fatalf("want rule %s, got:\n%s", tc.wantRule, report(rep))
			}
			if loc := f.File + ":" + itoa(f.Line); loc != tc.wantIn {
				t.Fatalf("want finding at %s, got %s", tc.wantIn, loc)
			}
		})
	}
}

func report(rep Report) string {
	var b strings.Builder
	for _, f := range rep.Failed {
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}

func TestNumberingGapIsInfoOnly(t *testing.T) {
	files := goodRepo()
	files["docs/internals/design.md"] = "# Design\n\n## Invariants\n\n| D1 | One. |\n| D20 | Twenty. |\n| D31 | Thirty-one. |\n"
	files["docs/internals/decisions.md"] = "# Decisions\n\n(empty)\n"
	files["AGENTS.md"] = "# Agent\n"
	rep := runFixture(t, files)
	if !rep.OK() {
		t.Fatalf("gap must not fail the gate:\n%s", report(rep))
	}
	var found bool
	for _, f := range rep.Infos {
		if f.Rule == "numbering-gap" && strings.Contains(f.Text, "D20 → D31") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an informational D20 → D31 gap, got %+v", rep.Infos)
	}
}

func TestStaleRangeDetailNamesRealMax(t *testing.T) {
	files := goodRepo()
	files["AGENTS.md"] = "# Agent\n\nDecisions `D1…D2`.\n"
	rep := runFixture(t, files)
	if rep.OK() {
		t.Fatal("want a stale-range failure")
	}
	f, ok := hasRule(rep, ruleStaleRange)
	if !ok {
		t.Fatalf("want stale-range, got:\n%s", report(rep))
	}
	if want := fmt.Sprintf("D%d is defined", rep.IDs.maxOf(seriesD)); !strings.Contains(f.Detail, want) {
		t.Fatalf("detail %q must name the real max %q", f.Detail, want)
	}
}

func TestRangeOverClaimIsInfo(t *testing.T) {
	files := goodRepo()
	files["AGENTS.md"] = "# Agent\n\nInvariants `I-01…I-99`.\n"
	rep := runFixture(t, files)
	if _, bad := hasRule(rep, ruleStaleRange); bad {
		t.Fatalf("over-claim must not be reported as stale-range:\n%s", report(rep))
	}
	var noted bool
	for _, f := range rep.Infos {
		if f.Rule == "range-over-claim" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("want an informational over-claim note, got %+v", rep.Infos)
	}
}

func TestHeadingRangeNotFrozen(t *testing.T) {
	files := goodRepo()
	files["docs/internals/decisions.md"] = "# Decisions\n\n### Продолжение журнала: D21…D30\n"
	rep := runFixture(t, files)
	if !rep.OK() {
		t.Fatalf("heading range must not fail:\n%s", report(rep))
	}
}

func TestExternalLinksSkipped(t *testing.T) {
	files := goodRepo()
	files["docs/guides/security.md"] = "[e](https://example.com/x) [m](mailto:a@b.c)\n"
	rep := runFixture(t, files)
	if !rep.OK() {
		t.Fatalf("external links must not fail:\n%s", report(rep))
	}
}

func TestCyrillicAnchors(t *testing.T) {
	files := goodRepo()
	files["docs/internals/design.md"] += "\n## Инварианты\n"
	files["docs/guides/security.md"] = "[i](../internals/design.md#инварианты)\n"
	rep := runFixture(t, files)
	if !rep.OK() {
		t.Fatalf("cyrillic anchor must resolve:\n%s", report(rep))
	}
}

func TestDefinitionLinesAreNotReferences(t *testing.T) {
	files := goodRepo()
	files["docs/internals/business-rules.md"] = "# Rules\n\n| BR-01 | Uses D99. |\n"
	rep := runFixture(t, files)
	if !rep.OK() {
		t.Fatalf("definition rows must not be scanned as references:\n%s", report(rep))
	}
}

func TestFindingsAreDeterministic(t *testing.T) {
	files := goodRepo()
	files["AGENTS.md"] = "b [b](docs/nope.md) `I-01|I-02`\n"
	files["README.md"] = "a [a](docs/nope.md) `I-01|I-02`\n"
	files["README.ru.md"] = "r [r](docs/nope.md) `I-01|I-02`\n"
	dir := fixture(t, files)
	first, err := Run(dir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range 3 {
		again, rerr := Run(dir)
		if rerr != nil {
			t.Fatalf("Run: %v", rerr)
		}
		if report(first) != report(again) {
			t.Fatalf("findings differ between runs:\n%s\nvs\n%s", report(first), report(again))
		}
	}
	got := report(first)
	want := []string{"AGENTS.md", "README.md", "README.ru.md", "docs/"}
	prev := -1
	for _, f := range strings.Split(strings.TrimSpace(got), "\n") {
		idx := strings.Index(f, ":")
		if idx < 0 {
			continue
		}
		file := f[:idx]
		at := sort.SearchStrings(want, file)
		if at >= len(want) || !strings.HasPrefix(file, want[at]) {
			t.Fatalf("unexpected file %q in sorted findings:\n%s", file, got)
		}
		if at < prev {
			t.Fatalf("findings not sorted by file:\n%s", got)
		}
		prev = at
	}
	if !strings.Contains(got, "duplicate-id") && !strings.Contains(got, "broken-link") {
		t.Fatalf("want findings:\n%s", got)
	}
}

func TestParseID(t *testing.T) {
	cases := []struct {
		raw    string
		want   ID
		wantOK bool
	}{
		{"I-01", ID{Series: seriesI, Num: 1}, true},
		{"I-21", ID{Series: seriesI, Num: 21}, true},
		{"BR-86", ID{Series: seriesBR, Num: 86}, true},
		{"BR-100", ID{Series: seriesBR, Num: 100}, true},
		{"D1", ID{Series: seriesD, Num: 1}, true},
		{"D66", ID{Series: seriesD, Num: 66}, true},
		{"D035", ID{Series: seriesD, Num: 35}, true},
		{"ISO", ID{}, false},
		{"MD5", ID{}, false},
	}
	for _, tc := range cases {
		got, ok := parseID(tc.raw)
		if ok != tc.wantOK || (ok && got != tc.want) {
			t.Fatalf("parseID(%q) = %v,%v want %v,%v", tc.raw, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Invariants":                "invariants",
		"## Decisions D21…D30":      "decisions-d21d30",
		"`internal/call`":           "internalcall",
		"Бизнес-правила (BR-01)":    "бизнес-правила-br-01",
		"Design.md: the frozen API": "designmd-the-frozen-api",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q) = %q want %q", in, got, want)
		}
	}
}
