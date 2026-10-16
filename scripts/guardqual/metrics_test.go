package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecallPrecisionAndFPR(t *testing.T) {
	c := counts{Expected: 10, Found: 8, Flagged: 12, Benign: 20, FalsePos: 1}
	if got, want := recall(c), 0.8; math.Abs(got-want) > 1e-9 {
		t.Errorf("recall = %v, want %v", got, want)
	}
	if got, want := precision(c), 8.0/12.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("precision = %v, want %v", got, want)
	}
	if got, want := fpr(c), 0.05; math.Abs(got-want) > 1e-9 {
		t.Errorf("fpr = %v, want %v", got, want)
	}
}

func TestPerfectRunIsOne(t *testing.T) {
	c := counts{Expected: 7, Found: 7, Flagged: 7, Benign: 30, FalsePos: 0}
	if recall(c) != 1 || precision(c) != 1 || fpr(c) != 0 {
		t.Fatalf("recall=%v precision=%v fpr=%v, want 1/1/0", recall(c), precision(c), fpr(c))
	}
}

func TestZeroFoundIsPrecisionOneNotZero(t *testing.T) {
	c := counts{Expected: 5, Found: 0, Flagged: 0, Benign: 10, FalsePos: 0}
	if got := precision(c); got != 1 {
		t.Fatalf("precision with nothing flagged = %v, want 1", got)
	}
}

func TestZeroExpectedIsRecallOne(t *testing.T) {
	c := counts{}
	if got := recall(c); got != 1 {
		t.Fatalf("recall with nothing expected = %v, want 1", got)
	}
	if got := fpr(c); got != 0 {
		t.Fatalf("fpr with no benign cases = %v, want 0", got)
	}
}

func TestEmptyCountsReportNoDenominator(t *testing.T) {
	s := Scored{Name: "UNSAFE"}
	if s.HasRecall() || s.HasPrecision() || s.HasFPR() {
		t.Fatal("an empty row must report no denominator for any metric")
	}
	if got := ratio(s.Recall(), s.HasRecall()); got != "n/a" {
		t.Fatalf("ratio = %q, want n/a", got)
	}
	full := Scored{Name: "PII", Counts: counts{Expected: 1, Found: 1, Flagged: 1, Benign: 1}}
	if !full.HasRecall() || !full.HasPrecision() || !full.HasFPR() {
		t.Fatal("a row with counts must report all three denominators")
	}
}

func TestCountsAdd(t *testing.T) {
	a := counts{Expected: 1, Found: 1, Flagged: 2, Benign: 3, FalsePos: 1}
	b := counts{Expected: 4, Found: 2, Flagged: 5, Benign: 7, FalsePos: 2}
	got := a.add(b)
	want := counts{Expected: 5, Found: 3, Flagged: 7, Benign: 10, FalsePos: 3}
	if got != want {
		t.Fatalf("add = %+v, want %+v", got, want)
	}
}

func TestMissesAndFalseNegatives(t *testing.T) {
	c := counts{Expected: 10, Found: 8, Flagged: 13}
	if c.Misses() != 2 {
		t.Errorf("Misses = %d, want 2", c.Misses())
	}
	if c.FalseNegatives() != 5 {
		t.Errorf("FalseNegatives = %d, want 5", c.FalseNegatives())
	}
}

func TestCheckSkipsMetricsWithoutADenominator(t *testing.T) {
	g := gates{
		Overall: thresholds{MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05},
		PerCat:  map[category]thresholds{catUnsafe: {MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05}},
		Rows:    []string{"UNSAFE"},
	}
	rows := map[string]Scored{
		"overall": {Name: "overall", Counts: counts{Expected: 0, Found: 0, Flagged: 0, Benign: 0}},
		"UNSAFE":  {Name: "UNSAFE", Counts: counts{Expected: 0, Found: 0, Flagged: 0, Benign: 0}},
	}
	if v := g.Check(rows); len(v) != 0 {
		t.Fatalf("an empty corpus must not fail the gate: %v", v)
	}
}

func TestCheckReportsEveryMissedThreshold(t *testing.T) {
	g := gates{
		Overall: thresholds{MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05},
		PerCat:  map[category]thresholds{catInjection: {MinRecall: 0.8, MinPrecision: 0.85, MaxFPR: 0.05}},
		Rows:    []string{"INJECTION"},
	}
	rows := map[string]Scored{
		"overall":   {Name: "overall", Counts: counts{Expected: 10, Found: 7, Flagged: 14, Benign: 20, FalsePos: 4}},
		"INJECTION": {Name: "INJECTION", Counts: counts{Expected: 8, Found: 6, Flagged: 8, Benign: 20, FalsePos: 1}},
	}
	v := g.Check(rows)
	if len(v) != 5 {
		t.Fatalf("violations = %v, want 5 (overall recall/precision/fpr, injection recall/precision)", v)
	}
	seen := map[string]string{}
	for _, x := range v {
		seen[x.Row+"/"+x.Metric] = x.String()
	}
	for _, want := range []string{
		"overall/recall", "overall/precision", "overall/fpr",
		"INJECTION/recall", "INJECTION/precision",
	} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing violation %s in %v", want, seen)
		}
	}
	if !strings.Contains(seen["overall/fpr"], "> limit") {
		t.Errorf("an over-limit fpr must compare with max: %s", seen["overall/fpr"])
	}
	if !strings.Contains(seen["overall/recall"], "< limit") {
		t.Errorf("an under-limit recall must compare with min: %s", seen["overall/recall"])
	}
}

func TestCheckIgnoresZeroThresholds(t *testing.T) {
	g := gates{Overall: thresholds{MinRecall: 0}}
	rows := map[string]Scored{"overall": {Name: "overall", Counts: counts{Expected: 10, Found: 1}}}
	if v := g.Check(rows); len(v) != 0 {
		t.Fatalf("a zero threshold is not a gate: %v", v)
	}
}

func TestMarkdownTableMarksUngatedCells(t *testing.T) {
	g := gates{Overall: thresholds{MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05}}
	rows := []Scored{
		{Name: "overall", Counts: counts{Expected: 10, Found: 10, Flagged: 10, Benign: 20}},
		{Name: "UNSAFE"},
	}
	table := markdownTable(rows, g)
	for _, want := range []string{"| overall |", "n/a", "0.900", "PASS", "| UNSAFE |"} {
		if !strings.Contains(table, want) {
			t.Fatalf("table is missing %q:\n%s", want, table)
		}
	}
}

func TestScoreFoldsOutcomes(t *testing.T) {
	found := func(cats ...category) map[category]bool {
		m := map[category]bool{}
		for _, c := range cats {
			m[c] = true
		}
		return m
	}
	outcomes := []outcome{
		{
			case_: testCase{ID: "a", expectSet: map[string]bool{"PII": true}, Expect: []string{"PII"}},
			found: found(catPII),
		},
		{
			case_: testCase{ID: "b", expectSet: map[string]bool{"PII": true, "SECRET": true}, Expect: []string{"PII", "SECRET"}},
			found: found(catPII),
		},
		{
			case_: testCase{ID: "c", expectSet: map[string]bool{"PII": true}, Expect: []string{"PII"}},
			found: found(catPII, catInjection), extra: []category{catInjection},
		},
		{case_: testCase{ID: "clean"}},
		{case_: testCase{ID: "noisy"}, found: found(catSecret)},
		{
			case_: testCase{ID: "l2", Layer: layerL2, expectSet: map[string]bool{"PII": true}, Expect: []string{"PII"}},
			found: found(),
		},
		{case_: testCase{ID: "broken"}, runErr: os.ErrDeadlineExceeded},
	}
	res := score(outcomes)

	if res.Total != 7 {
		t.Fatalf("Total = %d, want 7", res.Total)
	}
	if res.Deferred != 1 {
		t.Errorf("Deferred = %d, want 1", res.Deferred)
	}
	if res.Benign != 2 {
		t.Errorf("Benign = %d, want 2", res.Benign)
	}
	if len(res.RunErrors) != 1 {
		t.Errorf("RunErrors = %v, want one entry", res.RunErrors)
	}

	ov := res.Overall.Counts
	if ov.Expected != 4 || ov.Found != 3 || ov.Flagged != 4 || ov.Benign != 2 || ov.FalsePos != 1 {
		t.Fatalf("overall counts = %+v", ov)
	}
	if _, ok := res.Missed["b"]; !ok {
		t.Error("case b missed SECRET and must be reported")
	}
	if _, ok := res.FalsePos["noisy"]; !ok {
		t.Error("the benign case that fired must be reported as a false positive")
	}
	if _, ok := res.Missed["l2"]; ok {
		t.Error("a deferred case must not be reported as an L1 miss")
	}
}

func TestPerCategoryFPRHasEveryBenignControlAsDenominator(t *testing.T) {
	found := func(cats ...category) map[category]bool {
		m := map[category]bool{}
		for _, c := range cats {
			m[c] = true
		}
		return m
	}
	outcomes := []outcome{
		{case_: testCase{ID: "clean-1"}},
		{case_: testCase{ID: "clean-2"}},
		{case_: testCase{ID: "clean-3"}},
		{case_: testCase{ID: "noisy"}, found: found(catUnsafe)},
		{
			case_: testCase{ID: "pos", expectSet: map[string]bool{"PII": true}, Expect: []string{"PII"}},
			found: found(catPII),
		},
	}
	res := score(outcomes)
	rows := rowsByName(res.Rows)

	for _, cat := range allCategories {
		row := rows[string(cat)]
		if row.Counts.Benign != 4 {
			t.Errorf("%s: benign denominator = %d, want all 4 benign controls", cat, row.Counts.Benign)
		}
		if !row.HasFPR() {
			t.Errorf("%s: FPR must have a denominator once the corpus has benign controls", cat)
		}
		want := 0.0
		if cat == catUnsafe {
			want = 0.25
		}
		if got := row.FPR(); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: FPR = %v, want %v", cat, got, want)
		}
	}
	if ov := res.Overall; ov.FPR() != 0.25 {
		t.Errorf("overall FPR = %v, want 0.25", ov.FPR())
	}
}

func TestDeferringACaseLeavesTheFPRDenominatorAlone(t *testing.T) {
	outcomes := []outcome{
		{case_: testCase{ID: "clean"}},
		{
			case_:  testCase{ID: "deferred", Layer: layerL2, expectSet: map[string]bool{"UNSAFE": true}, Expect: []string{"UNSAFE"}},
			found:  map[category]bool{catUnsafe: true},
			labels: []string{"L2:unsafe"},
		},
	}
	res := score(outcomes)
	for _, cat := range allCategories {
		if got := res.Rows[1+indexOfCategory(cat)].Counts.Benign; got != 1 {
			t.Errorf("%s: benign denominator = %d, want 1 (the deferred case must not count)", cat, got)
		}
	}
}

func indexOfCategory(cat category) int {
	for i, c := range allCategories {
		if c == cat {
			return i
		}
	}
	return -1
}

func TestScoreOnAnEmptyCorpusProducesNoRowsWithDenominators(t *testing.T) {
	res := score(nil)
	if res.Total != 0 || res.Overall.HasRecall() || res.Overall.HasFPR() {
		t.Fatalf("empty corpus produced %+v", res.Overall)
	}
	if len(res.Rows) != 1+len(allCategories) {
		t.Fatalf("rows = %d, want the overall row plus every category", len(res.Rows))
	}
}

func TestLoadCasesRejectsMalformedCorpora(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if _, err := loadCases(write("empty.jsonl", "\n# a comment\n\n")); err == nil ||
		!strings.Contains(err.Error(), "empty") {
		t.Errorf("empty corpus err = %v", err)
	}
	if _, err := loadCases(write("noid.jsonl", `{"text":"x","rail":"input","expect":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "id is required") {
		t.Errorf("missing id err = %v", err)
	}
	if _, err := loadCases(write("rail.jsonl", `{"id":"a","text":"x","rail":"nope","expect":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown rail") {
		t.Errorf("bad rail err = %v", err)
	}
	if _, err := loadCases(write("cat.jsonl", `{"id":"a","text":"x","rail":"input","expect":["NOPE"]}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown category") {
		t.Errorf("bad category err = %v", err)
	}
	if _, err := loadCases(write("field.jsonl", `{"id":"a","text":"x","rail":"input","expect":[],"typo":1}`)); err == nil {
		t.Error("an unknown field must be rejected, not ignored")
	}
	if _, err := loadCases(write("dup.jsonl",
		"{\"id\":\"a\",\"text\":\"x\",\"rail\":\"input\",\"expect\":[]}\n{\"id\":\"a\",\"text\":\"y\",\"rail\":\"input\",\"expect\":[]}\n")); err == nil ||
		!strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("duplicate id err = %v", err)
	}
	if _, err := loadCases(write("conflict.jsonl",
		`{"id":"a","text":"x","rail":"input","expect":["PII"],"mustNotContain":["PII"]}`)); err == nil ||
		!strings.Contains(err.Error(), "both expected and forbidden") {
		t.Errorf("contradiction err = %v", err)
	}
	if _, err := loadCases(write("deferred.jsonl",
		`{"id":"a","text":"x","rail":"input","layer":"L2","expect":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "must expect something") {
		t.Errorf("deferred control err = %v", err)
	}
	if _, err := loadCases(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a missing corpus must be an error")
	}
}

func TestLoadCasesAcceptsAValidCorpus(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cases.jsonl")
	body := "# comment\n" +
		`{"id":"a","text":"x","rail":"input","expect":["PII"],"mustNotContain":["UNSAFE"]}` + "\n" +
		`{"id":"b","text":"y","rail":"tool_result","expect":[]}` + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cases, err := loadCases(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(cases))
	}
	if !cases[0].expects(catPII) || !cases[0].forbids(catUnsafe) {
		t.Error("expect and mustNotContain must both be indexed")
	}
	if cases[1].Rail != railToolResult || !cases[1].wantsNothing() {
		t.Errorf("case b = %+v", cases[1])
	}
	if cases[0].Layer != layerL1 {
		t.Errorf("layer default = %q, want L1", cases[0].Layer)
	}
}

func TestLoadThresholdsRequiresAnOverallGate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := loadThresholds(write("empty.json", `{}`)); err == nil {
		t.Error("thresholds without an overall gate must be rejected")
	}
	if _, err := loadThresholds(write("bad.json", `{`)); err == nil {
		t.Error("malformed JSON must be rejected")
	}
	if _, err := loadThresholds(write("cat.json", `{"overall":{"min_recall":0.9},"per_category":{"NOPE":{}}}`)); err == nil {
		t.Error("an unknown category must be rejected")
	}
	g, err := loadThresholds(write("ok.json",
		`{"overall":{"min_recall":0.9,"min_precision":0.8,"max_fpr":0.05},"per_category":{"PII":{"min_recall":0.9}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if g.Overall.MinPrecision != 0.8 || g.PerCat[catPII].MinRecall != 0.9 {
		t.Fatalf("thresholds = %+v %+v", g.Overall, g.PerCat)
	}
	if len(g.Rows) != 1 || g.Rows[0] != "PII" {
		t.Fatalf("Rows = %v", g.Rows)
	}
}

func TestShippedCorpusIsLargeEnoughAndPassesItsOwnLoader(t *testing.T) {
	cases, err := loadCases(defaultPath("testdata/guard/cases.jsonl"))
	if err != nil {
		t.Skipf("corpus not present: %v", err)
	}
	if len(cases) < 200 {
		t.Fatalf("corpus has %d cases, want at least 200", len(cases))
	}
	benign, deferred := 0, 0
	for _, c := range cases {
		if c.Layer == layerL2 {
			deferred++
		}
		if c.wantsNothing() {
			benign++
		}
	}
	if benign < 30 {
		t.Errorf("corpus has %d benign controls; precision and FPR need them", benign)
	}
	if deferred == 0 {
		t.Error("the corpus must record what L1 does not cover, or the gap is invisible")
	}
}

func TestShippedThresholdsGateEveryCategoryTheCorpusCanMeasure(t *testing.T) {
	g, err := loadThresholds(defaultPath("scripts/guardqual/thresholds.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range allCategories {
		th, ok := g.PerCat[cat]
		if !ok {
			t.Errorf("%s has no gate at all", cat)
			continue
		}
		if th.MaxFPR <= 0 {
			t.Errorf("%s: max_fpr must be gated — every category is scored over the benign set", cat)
		}
		if th.MinPrecision <= 0 {
			t.Errorf("%s: min_precision must be gated", cat)
		}
	}
	if th := g.PerCat[catUnsafe]; th.MinRecall != 0 {
		t.Errorf("UNSAFE: min_recall = %v, want no gate — L1 has no UNSAFE detector to measure (D28)", th.MinRecall)
	}
	if th := g.PerCat[catUnsafe]; th.MinPrecision != 1.0 {
		t.Errorf("UNSAFE: min_precision = %v, want 1.0 — L1 is asked for no UNSAFE finding", th.MinPrecision)
	}
}

func TestCorpusRecordsTheUnsafeGap(t *testing.T) {
	cases, err := loadCases(defaultPath("testdata/guard/cases.jsonl"))
	if err != nil {
		t.Skipf("corpus not present: %v", err)
	}
	unsafeDeferred, unsafeControls := 0, 0
	for _, c := range cases {
		switch {
		case c.Layer == layerL2 && c.expects(catUnsafe):
			unsafeDeferred++
		case c.forbids(catUnsafe):
			unsafeControls++
		}
	}
	if unsafeDeferred < 10 {
		t.Errorf("corpus records %d deferred UNSAFE cases, want at least 10", unsafeDeferred)
	}
	if unsafeControls < 10 {
		t.Errorf("corpus has %d benign controls that forbid UNSAFE, want at least 10 — "+
			"an L1 unsafe keyword list must have something to over-fire on", unsafeControls)
	}
}

func TestShippedThresholdsLoad(t *testing.T) {
	g, err := loadThresholds(defaultPath("scripts/guardqual/thresholds.json"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Overall.MinRecall <= 0 {
		t.Fatal("the overall gate must have a recall limit")
	}
	if g.MinBenign <= 0 {
		t.Fatal("the gate must declare a minimum benign denominator: a rate over a " +
			"handful of controls is a sample, and this corpus shipped one PII control " +
			"while the detector masked 80% of bare ten-digit numbers")
	}
}

func TestBenignDenominatorGateFiresOnASmallCorpus(t *testing.T) {
	rows := map[string]Scored{
		"overall": {Name: "overall", Counts: counts{Expected: 10, Found: 10, Flagged: 10, Benign: 1}},
		"PII":     {Name: "PII", Counts: counts{Expected: 10, Found: 10, Flagged: 10, Benign: 1}},
	}
	g := gates{
		Overall:   thresholds{MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05},
		PerCat:    map[category]thresholds{catPII: {MinRecall: 0.9, MinPrecision: 0.9, MaxFPR: 0.05}},
		MinBenign: 60,
		Rows:      []string{"PII"},
	}
	violations := g.Check(rows)
	if len(violations) != 2 {
		t.Fatalf("got %d violations, want one per row with a small denominator: %v", len(violations), violations)
	}
	for _, v := range violations {
		if v.Metric != "benign controls (FPR denominator)" {
			t.Errorf("violation = %q, want the benign denominator", v.Metric)
		}
		if v.Compares != "min" {
			t.Errorf("compares = %q, want min", v.Compares)
		}
	}

	rows["PII"] = Scored{Name: "PII", Counts: counts{Expected: 10, Found: 10, Flagged: 10, Benign: 60}}
	delete(rows, "overall")
	if got := g.Check(rows); len(got) != 0 {
		t.Errorf("got %v with a denominator over the limit, want no violations", got)
	}
}

func TestShippedCorpusClearsTheDeclaredBenignDenominator(t *testing.T) {
	g, err := loadThresholds(defaultPath("scripts/guardqual/thresholds.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := loadCases(defaultPath("testdata/guard/cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	benign := 0
	for _, c := range cases {
		if c.Layer == layerL1 && c.wantsNothing() {
			benign++
		}
	}
	if float64(benign) < g.MinBenign {
		t.Errorf("corpus has %d benign controls, thresholds.json requires %v", benign, g.MinBenign)
	}
	if !strings.Contains(markdownTable([]Scored{
		{Name: "PII", Counts: counts{Expected: 1, Found: 1, Flagged: 1, Benign: 205, FalsePos: 5}},
	}, g), "| 205 |") {
		t.Error("the table must print the benign denominator next to the FPR")
	}
}
