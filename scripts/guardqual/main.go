package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

type options struct {
	cases       string
	thresholds  string
	parityModel string
	parityFix   string
	minAgree    float64
	skipParity  bool
	explain     string
}

func run(args []string, out *os.File) int {
	var o options
	fs := flag.NewFlagSet("guardqual", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&o.cases, "cases", defaultPath("testdata/guard/cases.jsonl"), "path to the JSONL case set")
	fs.StringVar(&o.thresholds, "thresholds", defaultPath("scripts/guardqual/thresholds.json"), "path to the thresholds file")
	fs.StringVar(&o.parityModel, "parity-models", envOr("KIT_MODELS_DIR", "./models"), "models directory for the L2 parity gate")
	fs.StringVar(&o.parityFix, "parity-fixtures", defaultPath("guard/gliner/testdata"), "directory with the L2 parity fixtures (the same default kitctl verify parity uses)")
	fs.Float64Var(&o.minAgree, "parity-min-agreement", 1.0, "minimum L2 parity agreement")
	fs.BoolVar(&o.skipParity, "skip-parity", false, "do not attempt the L2 parity gate")
	fs.StringVar(&o.explain, "explain", "", "print the findings of one case id and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cases, err := loadCases(o.cases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guardqual: corpus: %v\n", err)
		return 2
	}

	d, err := l1Detector()
	if err != nil {
		fmt.Fprintf(os.Stderr, "guardqual: build the L1 layer: %v\n", err)
		return 2
	}

	outcomes := make([]outcome, 0, len(cases))
	ctx := context.Background()
	for _, c := range cases {
		outcomes = append(outcomes, evaluate(ctx, d, c))
	}

	if o.explain != "" {
		return explain(out, outcomes, o.explain)
	}

	g, err := loadThresholds(o.thresholds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "guardqual: thresholds: %v\n", err)
		return 2
	}
	res := score(outcomes)

	fmt.Fprint(out, "# Guard quality (L1, guard/rules)\n\n")
	fmt.Fprintf(out, "Corpus `%s`: %d cases — %d scored, %d benign controls, %d deferred to L2.\n\n",
		o.cases, res.Total, res.Total-res.Benign-res.Deferred, res.Benign, res.Deferred)
	fmt.Fprint(out, markdownTable(res.Rows, g))
	fmt.Fprint(out, "\n")

	var parity parityOutcome
	if o.skipParity {
		parity = parityOutcome{Reason: "skipped: -skip-parity"}
	} else {
		parity = runParity(o.parityModel, o.parityFix, o.minAgree)
	}
	fmt.Fprint(out, "\n## L2 parity\n\n")
	if parity.Ran {
		fmt.Fprintf(out, "%d/%d fixtures agree (%.1f%%).\n", parity.Report.Agreed, parity.Report.Compared, parity.Report.Agreement()*100)
		for _, f := range parity.Report.Failures {
			fmt.Fprintf(out, "- %s\n", f)
		}
	} else {
		fmt.Fprintf(out, "%s\n", parity.Reason)
	}

	fmt.Fprint(out, "\n")
	fmt.Fprint(out, listFindings("Missed detections", res.Missed))
	fmt.Fprint(out, "\n")
	fmt.Fprint(out, listFindings("False positives", res.FalsePos))
	fmt.Fprint(out, "\n")

	violations := g.Check(rowsByName(res.Rows))
	if len(res.RunErrors) > 0 {
		fmt.Fprintf(out, "## Cases that could not run\n\n")
		ids := make([]string, 0, len(res.RunErrors))
		for id := range res.RunErrors {
			ids = append(ids, id)
		}
		sortStrings(ids)
		for _, id := range ids {
			fmt.Fprintf(out, "- `%s`: %v\n", id, res.RunErrors[id])
		}
		fmt.Fprint(out, "\n")
		violations = append(violations, Violation{
			Row: "corpus", Metric: "unrunnable cases",
			Got: float64(len(res.RunErrors)), Limit: 0, Compares: "max",
		})
	}

	if len(violations) == 0 {
		fmt.Fprintln(out, "**PASS** — every gated metric is within thresholds.json.")
		return 0
	}
	fmt.Fprintf(out, "**FAIL** — %d threshold(s) missed:\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(out, "- %s\n", v)
	}
	return 1
}

func rowsByName(rows []Scored) map[string]Scored {
	out := make(map[string]Scored, len(rows))
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

func explain(out *os.File, outcomes []outcome, id string) int {
	for _, o := range outcomes {
		if o.case_.ID != id {
			continue
		}
		fmt.Fprintf(out, "case %s\n", o.case_.ID)
		fmt.Fprintf(out, "  rail:      %s\n", o.case_.Rail)
		fmt.Fprintf(out, "  layer:     %s\n", o.case_.Layer)
		fmt.Fprintf(out, "  text:      %s\n", o.case_.Text)
		fmt.Fprintf(out, "  expect:    %v\n", o.case_.Expect)
		if len(o.case_.MustNotContain) > 0 {
			fmt.Fprintf(out, "  forbid:    %v\n", o.case_.MustNotContain)
		}
		if o.runErr != nil {
			fmt.Fprintf(out, "  error:     %v\n", o.runErr)
			return 1
		}
		if len(o.labels) == 0 {
			fmt.Fprintln(out, "  findings:  none")
		} else {
			for _, l := range o.labels {
				fmt.Fprintf(out, "  finding:   %s\n", l)
			}
		}
		if len(o.missed) > 0 {
			fmt.Fprintf(out, "  missed:    %v\n", describe(o.missed))
		}
		if len(o.extra) > 0 {
			fmt.Fprintf(out, "  unexpected:%v\n", describe(o.extra))
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "guardqual: no case with id %q\n", id)
	return 2
}

func defaultPath(p string) string {
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat(filepath.Join("..", "..", p)); err == nil {
		return filepath.Join("..", "..", p)
	}
	return p
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
