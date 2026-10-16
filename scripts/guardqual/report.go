package main

import (
	"fmt"
	"strings"
)

func format(v float64) string { return fmt.Sprintf("%.3f", v) }

func ratio(v float64, ok bool) string {
	if !ok {
		return "n/a"
	}
	return format(v)
}

func markdownTable(rows []Scored, g gates) string {
	limits := map[string]thresholds{"overall": g.Overall}
	for _, name := range g.Rows {
		if th, ok := g.PerCat[category(name)]; ok {
			limits[name] = th
		}
	}

	var b []byte
	b = append(b, "| Scope | Cases | Expected | Found | Flagged | Benign | Recall | Precision | FPR | Threshold (recall / precision / fpr) | Verdict |\n"...)
	b = append(b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | :--- |\n"...)
	for _, row := range rows {
		th := limits[row.Name]
		violations := checkRow(row.Name, row, th)
		violations = append(violations, checkBenignDenominator(row.Name, row, g.MinBenign)...)
		verdict := "PASS"
		if len(violations) > 0 {
			verdict = "**FAIL**"
		}
		cases := row.Counts.Benign + row.Counts.Expected
		b = append(b, fmt.Sprintf(
			"| %s | %d | %d | %d | %d | %d | %s | %s | %s | %s / %s / %s | %s |\n",
			row.Name, cases, row.Counts.Expected, row.Counts.Found, row.Counts.Flagged,
			row.Counts.Benign,
			ratio(row.Recall(), row.HasRecall()),
			ratio(row.Precision(), row.HasPrecision()),
			ratio(row.FPR(), row.HasFPR()),
			thresholdCell(th.MinRecall), thresholdCell(th.MinPrecision), thresholdCell(th.MaxFPR),
			verdict)...)
	}
	return string(b)
}

func thresholdCell(v float64) string {
	if v <= 0 {
		return "-"
	}
	return format(v)
}

type findingReport struct {
	Missed     []string
	Unexpected []string
}

func (r findingReport) String() string {
	s := ""
	for _, m := range r.Missed {
		s += "  missed " + m + "\n"
	}
	for _, u := range r.Unexpected {
		s += "  unexpected " + u + "\n"
	}
	return s
}

func listFindings(title string, lines map[string]findingReport) string {
	ids := make([]string, 0, len(lines))
	for id := range lines {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return "## " + title + "\n\nnone\n"
	}
	sortStrings(ids)
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)
	for _, id := range ids {
		fmt.Fprintf(&b, "- `%s`\n%s", id, lines[id].String())
	}
	return b.String()
}
