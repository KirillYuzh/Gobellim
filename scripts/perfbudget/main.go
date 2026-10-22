// Command perfbudget gates the "small immutable core" promise with numbers: the
// number of direct dependencies, the size of go.sum, and the module closure of
// the default build. It is a hard gate in CI (.github/workflows/ci.yml, job
// `perf`), so a new dependency or a fattened dependency graph cannot land
// silently. The budgets are the measured values with headroom; raise one
// deliberately, in this file, when a change genuinely needs it.
//
// It measures the core module: run it from this module (`go -C scripts run
// ./perfbudget`) or pass the core directory as the first argument.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// 4 in the default build + 2 behind `-tags ort` (onnxruntime, tokenizers).
	maxDirectRequires = 6
	maxGoSumBytes     = 4096
	// External modules reachable from the default build (no `-tags ort`).
	maxDefaultModules = 8
)

func main() {
	root := ".."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	modPath, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		fatal(err)
	}
	direct, err := directRequires(filepath.Join(root, "go.mod"))
	if err != nil {
		fatal(err)
	}
	size, err := fileSize(filepath.Join(root, "go.sum"))
	if err != nil {
		fatal(err)
	}
	modules, err := defaultBuildModules(root, modPath)
	if err != nil {
		fatal(err)
	}

	over := false
	over = report("direct requires in go.mod", direct, maxDirectRequires) || over
	over = report("go.sum bytes", size, maxGoSumBytes) || over
	over = report("modules in the default build", modules, maxDefaultModules) || over

	if over {
		fmt.Fprintln(os.Stderr,
			"perfbudget: FAILED — a dependency budget above was exceeded; "+
				"raise it in scripts/perfbudget/main.go only if the change justifies it")
		os.Exit(1)
	}
	fmt.Println("perfbudget: ok")
}

func report(name string, got, limit int) bool {
	status := "ok"
	if got > limit {
		status = "OVER"
	}
	fmt.Printf("%-34s %6d / %-6d %s\n", name, got, limit, status)
	return got > limit
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "perfbudget: %v\n", err)
	os.Exit(2)
}

func fileSize(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return int(info.Size()), nil
}

func modulePath(goMod string) (string, error) {
	raw, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path), nil
		}
	}
	return "", fmt.Errorf("%s: no module line", goMod)
}

// directRequires counts requirements that are not marked `// indirect` — the
// dependencies a reader can actually see at the top of go.mod.
func directRequires(goMod string) (int, error) {
	raw, err := os.ReadFile(goMod)
	if err != nil {
		return 0, err
	}
	count := 0
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require ") && !strings.Contains(line, "("):
			if !strings.Contains(line, "// indirect") {
				count++
			}
			continue
		}
		if inBlock && line != "" && !strings.HasPrefix(line, "//") && !strings.Contains(line, "// indirect") {
			count++
		}
	}
	return count, nil
}

// defaultBuildModules counts the distinct external modules the default build
// (`go build`/`go test` without `-tags ort`) reaches.
func defaultBuildModules(root, modPath string) (int, error) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("go list -deps: %w", err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == modPath {
			continue
		}
		seen[line] = true
	}
	return len(seen), nil
}
