package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", "", "repository root to lint (default: the nearest directory with docs/internals/design.md)")
	quiet := flag.Bool("quiet", false, "suppress informational findings")
	flag.Parse()

	if *root == "" {
		*root = defaultRoot()
	}

	rep, err := Run(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docs-lint: %v\n", err)
		os.Exit(2)
	}

	for _, f := range rep.Failed {
		fmt.Printf("%s\n", f)
	}
	if !*quiet {
		for _, f := range rep.Infos {
			fmt.Printf("%s\n", f)
		}
	}

	fmt.Printf("docs-lint: %d file(s), %d failure(s), %d note(s)\n",
		len(rep.Files), len(rep.Failed), len(rep.Infos))

	if !rep.OK() {
		fmt.Fprintf(os.Stderr, "docs-lint: FAILED — %s\n", summarise(rep))
		os.Exit(1)
	}
}

func defaultRoot() string {
	for _, dir := range []string{".", ".."} {
		if _, err := os.Stat(filepath.Join(dir, "docs", "README.md")); err == nil {
			return dir
		}
	}
	return "."
}

func summarise(rep Report) string {
	counts := map[string]int{}
	for _, f := range rep.Failed {
		counts[f.Rule]++
	}
	parts := make([]string, 0, len(counts))
	for _, rule := range []string{ruleBrokenLink, ruleUndefined, ruleStaleRange, ruleDuplicate, ruleDuplicateNormative} {
		if counts[rule] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[rule], rule))
		}
	}
	return strings.Join(parts, ", ")
}
