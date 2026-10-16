package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/gliner"
	"github.com/KirillYuzh/Gobellim/guard/rules"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

type detector interface {
	Check(ctx context.Context, rail guard.Rail, text string) (guard.Verdict, error)
}

type outcome struct {
	case_  testCase
	found  map[category]bool
	labels []string
	missed []category
	extra  []category
	runErr error
}

func evaluate(ctx context.Context, d detector, c testCase) outcome {
	o := outcome{case_: c, found: map[category]bool{}}
	v, err := d.Check(ctx, guard.Rail(c.Rail), c.Text)
	if err != nil {
		o.runErr = err
		return o
	}
	for _, f := range v.Findings {
		o.found[category(f.Category)] = true
		o.labels = append(o.labels, fmt.Sprintf("%s:%s [%d:%d]", f.Layer, f.Label, f.Start, f.End))
	}
	sort.Strings(o.labels)
	o.classify()
	return o
}

func (o *outcome) classify() {
	for cat := range o.case_.expectSet {
		if cat := category(cat); !o.found[cat] {
			o.missed = append(o.missed, cat)
		}
	}
	for cat := range o.found {
		if cat := category(cat); !o.case_.expects(cat) {
			o.extra = append(o.extra, cat)
		}
	}
	sortCategories(o.missed)
	sortCategories(o.extra)
}

func sortCategories(in []category) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

type result struct {
	Rows      []Scored
	Overall   Scored
	Deferred  int
	Benign    int
	Total     int
	Missed    map[string]findingReport
	FalsePos  map[string]findingReport
	RunErrors map[string]error
}

func newResult() *result {
	return &result{
		Missed:    map[string]findingReport{},
		FalsePos:  map[string]findingReport{},
		RunErrors: map[string]error{},
	}
}

func score(outcomes []outcome) *result {
	r := newResult()
	per := map[category]counts{}
	var overall counts

	for _, o := range outcomes {
		o := o
		r.Total++
		if o.runErr != nil {
			r.RunErrors[o.case_.ID] = o.runErr
			continue
		}
		if o.missed == nil && o.extra == nil {
			o.classify()
		}
		if o.case_.Layer == layerL2 {
			r.Deferred++
			continue
		}
		if o.case_.wantsNothing() {
			r.Benign++
			overall.Benign++
			for _, cat := range allCategories {
				c := per[cat]
				c.Benign++
				if o.found[cat] {
					c.FalsePos++
				}
				per[cat] = c
			}
			if len(o.found) > 0 {
				overall.FalsePos++
				var cats []category
				for cat := range o.found {
					cats = append(cats, cat)
				}
				sortCategories(cats)
				r.FalsePos[o.case_.ID] = findingReport{
					Unexpected: describe(cats),
					Missed:     describeForbidden(o),
				}
				continue
			}
			continue
		}

		for cat := range o.found {
			c := per[cat]
			c.Flagged++
			if o.case_.expects(cat) {
				c.Found++
			}
			per[cat] = c
			overall.Flagged++
			if o.case_.expects(cat) {
				overall.Found++
			}
		}
		for cat := range o.case_.expectSet {
			c := per[category(cat)]
			c.Expected++
			per[category(cat)] = c
			overall.Expected++
		}
		if len(o.missed) > 0 || len(o.extra) > 0 {
			r.Missed[o.case_.ID] = findingReport{Missed: describe(o.missed), Unexpected: describe(o.extra)}
		}
	}

	r.Overall = Scored{Name: "overall", Counts: overall}
	r.Rows = append(r.Rows, r.Overall)
	for _, cat := range allCategories {
		r.Rows = append(r.Rows, Scored{Name: string(cat), Counts: per[cat]})
	}
	return r
}

func describe(cats []category) []string {
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		out = append(out, string(c))
	}
	return out
}

func describeForbidden(o outcome) []string {
	var out []string
	for _, cat := range o.extra {
		if o.case_.forbids(cat) {
			out = append(out, string(cat)+" (forbidden)")
		}
	}
	return out
}

type thresholdFile struct {
	Rationale map[string]string             `json:"rationale"`
	Overall   map[string]float64            `json:"overall"`
	PerCat    map[string]map[string]float64 `json:"per_category"`
	MinBenign float64                       `json:"min_benign_controls"`
}

func loadThresholds(path string) (gates, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return gates{}, err
	}
	var tf thresholdFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return gates{}, fmt.Errorf("%s: %w", path, err)
	}
	g := gates{PerCat: map[category]thresholds{}, MinBenign: tf.MinBenign}
	g.Overall = thresholds{
		MinRecall:    tf.Overall["min_recall"],
		MinPrecision: tf.Overall["min_precision"],
		MaxFPR:       tf.Overall["max_fpr"],
	}
	if g.Overall == (thresholds{}) {
		return gates{}, fmt.Errorf("%s: overall thresholds are missing or all zero", path)
	}
	for name, m := range tf.PerCat {
		if !knownCategory(name) {
			return gates{}, fmt.Errorf("%s: unknown category %q", path, name)
		}
		g.PerCat[category(name)] = thresholds{
			MinRecall:    m["min_recall"],
			MinPrecision: m["min_precision"],
			MaxFPR:       m["max_fpr"],
		}
		g.Rows = append(g.Rows, name)
	}
	sortStrings(g.Rows)
	return g, nil
}

func l1Detector() (detector, error) {
	p, err := guard.New(guard.Options{Layers: []guard.Detector{rules.New()}})
	if err != nil {
		return nil, err
	}
	return p, nil
}

type parityOutcome struct {
	Ran    bool
	Reason string
	Report gliner.Report
	Err    error
}

func runParity(modelsDir, fixturesDir string, minAgreement float64) parityOutcome {
	if _, err := os.Stat(filepath.Join(fixturesDir, "fixtures.json")); err != nil {
		return parityOutcome{
			Reason: fmt.Sprintf("skipped: no parity fixtures at %s — export them with tools/gliner "+
				"(the weights are not in git, so this is expected on a fresh clone)", fixturesDir),
			Err: err,
		}
	}
	report, err := gliner.CheckParity(modelsDir, fixturesDir, minAgreement)
	if err != nil {
		reason := fmt.Sprintf("skipped: %v", err)
		if isUnavailable(err) {
			reason = "skipped: the L2 parity gate needs the ort build and the ONNX runtime " +
				"(go run -tags ort ./scripts/guardqual). The numbers below measure L1 only."
		}
		return parityOutcome{Reason: reason, Err: err}
	}
	return parityOutcome{Ran: true, Report: report}
}

func isUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), core.ErrGuardUnavailable.Error())
}
