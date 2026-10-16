package main

import "sort"

type category string

const (
	catPII       category = "PII"
	catSecret    category = "SECRET"
	catInjection category = "INJECTION"
	catUnsafe    category = "UNSAFE"
)

var allCategories = []category{catPII, catSecret, catInjection, catUnsafe}

type counts struct {
	Expected int
	Found    int
	Flagged  int
	Benign   int
	FalsePos int
}

func (c counts) add(o counts) counts {
	return counts{
		Expected: c.Expected + o.Expected,
		Found:    c.Found + o.Found,
		Flagged:  c.Flagged + o.Flagged,
		Benign:   c.Benign + o.Benign,
		FalsePos: c.FalsePos + o.FalsePos,
	}
}

func (c counts) Misses() int { return c.Expected - c.Found }

func (c counts) FalseNegatives() int { return c.Flagged - c.Found }

func recall(c counts) float64 {
	if c.Expected == 0 {
		return 1
	}
	return float64(c.Found) / float64(c.Expected)
}

func precision(c counts) float64 {
	if c.Flagged == 0 {
		return 1
	}
	return float64(c.Found) / float64(c.Flagged)
}

func fpr(c counts) float64 {
	if c.Benign == 0 {
		return 0
	}
	return float64(c.FalsePos) / float64(c.Benign)
}

type Scored struct {
	Name   string
	Counts counts
}

func (s Scored) HasRecall() bool { return s.Counts.Expected > 0 }

func (s Scored) HasPrecision() bool { return s.Counts.Flagged > 0 }

func (s Scored) HasFPR() bool { return s.Counts.Benign > 0 }

func (s Scored) Recall() float64    { return recall(s.Counts) }
func (s Scored) Precision() float64 { return precision(s.Counts) }
func (s Scored) FPR() float64       { return fpr(s.Counts) }

type thresholds struct {
	MinRecall    float64
	MinPrecision float64
	MaxFPR       float64
}

type gates struct {
	Overall   thresholds
	PerCat    map[category]thresholds
	MinBenign float64
	Rows      []string
}

func (g gates) Check(rows map[string]Scored) []Violation {
	var out []Violation
	if s, ok := rows["overall"]; ok {
		out = append(out, checkRow("overall", s, g.Overall)...)
		out = append(out, checkBenignDenominator("overall", s, g.MinBenign)...)
	}
	for _, name := range g.Rows {
		th, ok := g.PerCat[category(name)]
		if !ok {
			continue
		}
		out = append(out, checkRow(name, rows[name], th)...)
		out = append(out, checkBenignDenominator(name, rows[name], g.MinBenign)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Row < out[j].Row })
	return out
}

func checkBenignDenominator(name string, s Scored, min float64) []Violation {
	if min <= 0 || float64(s.Counts.Benign) >= min {
		return nil
	}
	return []Violation{{
		Row:      name,
		Metric:   "benign controls (FPR denominator)",
		Got:      float64(s.Counts.Benign),
		Limit:    min,
		Compares: "min",
	}}
}

type Violation struct {
	Row      string
	Metric   string
	Got      float64
	Limit    float64
	Compares string
}

func (v Violation) String() string {
	switch v.Compares {
	case "max":
		return v.Row + ": " + v.Metric + " " + format(v.Got) + " > limit " + format(v.Limit)
	case "min":
		return v.Row + ": " + v.Metric + " " + format(v.Got) + " < limit " + format(v.Limit)
	}
	return v.Row + ": " + v.Metric + " " + format(v.Got) + " vs limit " + format(v.Limit)
}

func checkRow(name string, s Scored, th thresholds) []Violation {
	var out []Violation
	if s.HasRecall() && th.MinRecall > 0 && s.Recall() < th.MinRecall {
		out = append(out, Violation{Row: name, Metric: "recall", Got: s.Recall(), Limit: th.MinRecall, Compares: "min"})
	}
	if s.HasPrecision() && th.MinPrecision > 0 && s.Precision() < th.MinPrecision {
		out = append(out, Violation{Row: name, Metric: "precision", Got: s.Precision(), Limit: th.MinPrecision, Compares: "min"})
	}
	if s.HasFPR() && th.MaxFPR > 0 && s.FPR() > th.MaxFPR {
		out = append(out, Violation{Row: name, Metric: "fpr", Got: s.FPR(), Limit: th.MaxFPR, Compares: "max"})
	}
	return out
}
