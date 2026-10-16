package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	ratelimitNormative = "Каждая операция проходит через token bucket на тенанта " +
		"(`Config.RateLimit{PerSecond, Burst}`); исчерпание — `ErrRateLimited` до I/O. " +
		"Нулевой `PerSecond` отключает лимит."
	ruleOfTwoNormative = "«Rule of Two» запрещает эгресс в ходе, где уже был " +
		"недоверенный ввод; правило включено по умолчанию."
	guardL1Normative = "Детерминированный L1 входит в сборку по умолчанию, " +
		"а L2 — плагин, не входящий в неё."
)

func normativeFixture(t *testing.T, consumer string) string {
	t.Helper()
	return fixture(t, map[string]string{
		"docs/internals/design.md": "# Design\n\n## Конфигурация\n\n```go\n" +
			"type RateLimit struct {\n\tPerSecond float64\n\tBurst     int\n}\n```\n",
		"docs/internals/business-rules.md": "# Правила\n\n| BR-51 | " + ruleOfTwoNormative + " |\n" +
			"| BR-26 | " + guardL1Normative + " |\n" +
			"| BR-86 | " + ratelimitNormative + " |\n",
		"docs/reference/configuration.md": consumer,
	})
}

func runNormative(t *testing.T, root string) []Finding {
	t.Helper()
	rep, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out []Finding
	for _, f := range rep.Failed {
		if f.Rule == ruleDuplicateNormative {
			out = append(out, f)
		}
	}
	return out
}

func TestDuplicateNormative(t *testing.T) {
	cases := []struct {
		name     string
		consumer string
		wantIDs  []string
	}{
		{
			name:     "owner file is silent",
			consumer: "# Config\n\nПроизводные поля.\n",
			wantIDs:  nil,
		},
		{
			name: "derived-marked restatement passes",
			consumer: "# Config\n\n## Rate limit\n\n<!-- derived: BR-86 -->\n\n" +
				"Лимит — token bucket на тенанта; нулевой `PerSecond` отключает лимит.\n",
			wantIDs: nil,
		},
		{
			name: "blockquote-derived restatement passes",
			consumer: "# Config\n\n## Egress\n\n" + ruleOfTwoNormative + "\n\n" +
				"> Производное от BR-51\n",
			wantIDs: nil,
		},
		{
			name: "unmarked Rule of Two restatement fails",
			consumer: "# Config\n\n## Egress\n\nПравило Rule of Two включено по " +
				"умолчанию.\n",
			wantIDs: []string{"rule-of-two-default"},
		},
		{
			name: "unmarked RateLimit restatement fails",
			consumer: "# Config\n\n## Rate limit\n\nЛимит — token bucket на " +
				"тенанта.\n",
			wantIDs: []string{"ratelimit-scope"},
		},
		{
			name: "unmarked L1/L2 restatement fails",
			consumer: "# Config\n\n## Guard\n\nL1 — детерминированный слой, " +
				"L2 — плагин.\n",
			wantIDs: []string{"guard-l1-default"},
		},
		{
			name: "marker for another assertion does not license this one",
			consumer: "# Config\n\n## Egress\n\n<!-- derived: BR-86 -->\n\n" +
				"Правило Rule of Two включено по умолчанию.\n",
			wantIDs: []string{"rule-of-two-default"},
		},
		{
			name: "near-miss phrase does not false-positive",
			consumer: "# Config\n\n## Egress\n\nПоле `AllowEgressAfterUntrusted` — " +
				"opt-out; см. `Rule of Two`.\n",
			wantIDs: nil,
		},
		{
			name: "partial phrase does not false-positive",
			consumer: "# Config\n\n## Guard\n\nУровни L0 → L1 → L2 соединены " +
				"последовательно.\n",
			wantIDs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runNormative(t, normativeFixture(t, tc.consumer))
			if len(got) != len(tc.wantIDs) {
				for _, f := range got {
					t.Logf("unexpected finding: %s", f)
				}
				t.Fatalf("got %d findings, want %d", len(got), len(tc.wantIDs))
			}
			for i, want := range tc.wantIDs {
				if got[i].Text != want {
					t.Errorf("finding[%d].Text = %q, want %q", i, got[i].Text, want)
				}
				if got[i].File != "docs/reference/configuration.md" {
					t.Errorf("finding[%d].File = %q", i, got[i].File)
				}
				if got[i].Line < 1 {
					t.Errorf("finding[%d].Line = %d, want >= 1", i, got[i].Line)
				}
			}
		})
	}
}

func TestNormativeMarkerSpansSection(t *testing.T) {
	consumer := "# Config\n\n" +
		"## Egress\n\n" +
		"<!-- derived: BR-51 -->\n\n" +
		"Правило Rule of Two включено по умолчанию.\n" +
		"\n" +
		"## Лимит\n\n" +
		"Лимит — token bucket на тенанта.\n"
	got := runNormative(t, normativeFixture(t, consumer))
	if len(got) != 1 || got[0].Text != "ratelimit-scope" {
		for _, f := range got {
			t.Logf("finding: %s", f)
		}
		t.Fatalf("want exactly one ratelimit-scope finding, got %d", len(got))
	}
}

func TestRealDocsHaveNoUnmarkedRestatement(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "docs", "internals", "business-rules.md")); serr != nil {
		t.Skipf("repository docs not reachable from %s: %v", root, serr)
	}
	files, err := docFiles(root)
	if err != nil {
		t.Fatalf("docFiles: %v", err)
	}
	contents := map[string][]string{}
	for _, f := range files {
		raw, rerr := os.ReadFile(filepath.Join(root, f))
		if rerr != nil {
			t.Fatalf("read %s: %v", f, rerr)
		}
		contents[f] = splitLines(string(raw))
	}
	for _, f := range duplicateNormative(files, contents) {
		t.Errorf("unmarked normative restatement: %s", f)
	}
}

func TestEveryAssertionHasOwnerAndRule(t *testing.T) {
	if len(normativeAssertions) == 0 {
		t.Fatal("registry is empty")
	}
	seen := map[string]bool{}
	for _, a := range normativeAssertions {
		if a.ID == "" || a.RuleID == "" {
			t.Errorf("assertion %q missing ID or RuleID", a.ID)
		}
		if seen[a.ID] {
			t.Errorf("duplicate assertion id %q", a.ID)
		}
		seen[a.ID] = true
		if len(a.Owner) == 0 {
			t.Errorf("assertion %q has no owner", a.ID)
		}
		if len(a.Patterns) == 0 {
			t.Errorf("assertion %q has no patterns", a.ID)
		}
		for _, re := range a.Patterns {
			if re == nil {
				t.Errorf("assertion %q has a nil pattern", a.ID)
				continue
			}
			if !strings.Contains(re.String(), "(?i)") {
				t.Errorf("assertion %q pattern is not case-insensitive: %s", a.ID, re)
			}
		}
	}
}
