package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/embed"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/gliner"
)

const (
	defaultGuardModel  = "hivetrace__gliner-guard-omni"
	defaultEmbedModel  = "intfloat/multilingual-e5-small"
	defaultFixturesDir = "guard/gliner/testdata"
)

func runVerify(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kitctl verify: expected 'window', 'models', 'parity' or 'latency'")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "window":
		return runVerifyWindow(rest)
	case "models":
		return runVerifyModels(rest)
	case "parity":
		return runVerifyParity(rest)
	case "latency":
		return runVerifyLatency(rest)
	default:
		fmt.Fprintf(os.Stderr, "kitctl verify: unknown check %q\n", sub)
		return 2
	}
}

func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func runVerifyWindow(args []string) int {
	fs := flag.NewFlagSet("verify window", flag.ContinueOnError)
	modelContext := fs.Int("model-context", 0, "model context window in tokens; 0 skips the check")
	guardMax := fs.Int("guard-max-chars", 0, "guard window in runes; 0 uses guard.DefaultMaxChars")
	policyFile := fs.String("policy", "", "policy.yaml to validate")
	if _, err := parseInterleaved(fs, args); err != nil {
		return 2
	}

	effective := *guardMax
	if effective <= 0 {
		effective = guard.DefaultMaxChars
	}

	if *policyFile != "" {
		if _, err := authz.Load(*policyFile); err != nil {
			fmt.Fprintf(os.Stderr, "kitctl verify window: %v\n", err)
			return 1
		}
		fmt.Printf("policy %s: ok\n", *policyFile)
	}

	if *modelContext <= 0 {
		fmt.Printf("guard window %d chars: I-06 context check skipped (no --model-context; the client reports its context window at runtime)\n", effective)
		return 0
	}
	// GuardMaxChars is in characters; the model window is in tokens. Convert
	// with a conservative chars/3 estimate so the check is not fooled by the
	// unit mismatch (8000 chars ~= 2700 tokens).
	estimatedTokens := effective / 3
	if estimatedTokens > *modelContext {
		fmt.Fprintf(os.Stderr, "kitctl verify window: guard window %d chars (~%d tokens) exceeds the model context window %d tokens (I-06); set GuardMaxChars <= %d or use a model with a wider context\n", effective, estimatedTokens, *modelContext, *modelContext*3)
		return 1
	}
	fmt.Printf("guard window %d chars (~%d tokens) fits the model context window %d tokens: ok\n", effective, estimatedTokens, *modelContext)
	return 0
}

func runVerifyModels(args []string) int {
	fs := flag.NewFlagSet("verify models", flag.ContinueOnError)
	dir := fs.String("dir", defaultModelsDir(), "models directory")
	guardModel := fs.String("guard-model", defaultGuardModel, "guard model directory name")
	embedModel := fs.String("embed-model", defaultEmbedModel, "embedder model name")
	if _, err := parseInterleaved(fs, args); err != nil {
		return 2
	}

	code := 0
	if err := gliner.VerifyDir(*dir, *guardModel); err != nil {
		fmt.Fprintf(os.Stderr, "kitctl verify models: guard %s: %v\n", *guardModel, err)
		code = 1
	} else {
		fmt.Printf("guard model %s: ok\n", *guardModel)
	}
	if err := embed.VerifyDir(*dir, *embedModel); err != nil {
		fmt.Fprintf(os.Stderr, "kitctl verify models: embedder %s: %v\n", *embedModel, err)
		code = 1
	} else {
		fmt.Printf("embedder model %s: ok\n", *embedModel)
	}
	return code
}

func runVerifyParity(args []string) int {
	fs := flag.NewFlagSet("verify parity", flag.ContinueOnError)
	dir := fs.String("dir", defaultModelsDir(), "models directory")
	fixtures := fs.String("fixtures", defaultFixturesDir, "fixtures directory")
	minAgreement := fs.Float64("min-agreement", 1.0, "minimum agreement ratio in [0,1]")
	if _, err := parseInterleaved(fs, args); err != nil {
		return 2
	}

	report, err := gliner.CheckParity(*dir, *fixtures, *minAgreement)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kitctl verify parity: %v\n", err)
		for _, f := range report.Failures {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		return 1
	}
	fmt.Printf("guard parity: %d/%d fixtures agreed (%.4f)\n", report.Agreed, report.Compared, report.Agreement())
	return 0
}
