package importrules

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type imp struct {
	dir  string
	file string
	line int
	test bool
}

type rule struct {
	name string
	desc string
	// skipTests constrains only non-test files: a library's test dependency never
	// reaches consumers, and a core test may legitimately import an optional
	// package — kittest is the test kit for exactly that (D78), and the facade
	// cross-checks Generate against eval.
	skipTests bool
	applies   func(pkg string) bool
	allows    func(pkg, imported string) bool
}

var coreFamily = []string{"authz", "guard", "model", "embed", "chunk", "store", "trace"}

var coreFamilyExceptions = map[[2]string]bool{
	{"trace", "store"}: true,
}

var leafPackages = []string{
	"internal/core",
	"internal/id",
	"internal/httpjson",
	"internal/oauth",
	"internal/ort",
	"internal/ratelimit",
	"internal/cache",
	"internal/importrules",
}

var skipDirs = map[string]bool{
	"models":       true,
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
}

// optionalPackages are the non-core packages this repository ships: MCP (client
// and server), the webfetch tool, the flow primitive, the eval harness, the
// rerank adapter, the test kit and the tooling (cmd, demo, scripts). The core
// must not depend on them — the dependency runs the other way, or not at all
// (docs/internals/architecture.md, «Границы ядра»).
var optionalPackages = []string{
	"mcp", "webfetch", "flow", "eval", "kittest", "rerank",
	"cmd", "demo", "scripts",
}

// coreOptionalExceptions is the honest list of edges where the compiled core
// still depends on an optional package. It holds one edge: the façade exposes
// Config.Reranker and SearchRequest.Rerank, so it imports rerank. The core rule
// asserts this list is exactly the observed edges, so it cannot grow silently.
var coreOptionalExceptions = map[[2]string]bool{
	{"", "rerank"}: true,
}

var rules = []rule{
	{
		name:    "core-family",
		desc:    "authz, guard (with guard/**), model, embed, chunk, store and trace must not import each other; only trace → store is allowed",
		applies: func(pkg string) bool { return familyOf(pkg) != "" },
		allows: func(pkg, imported string) bool {
			from := familyOf(pkg)
			to := familyOf(imported)
			if to == "" || from == to {
				return true
			}
			return coreFamilyExceptions[[2]string{from, to}]
		},
	},
	{
		name:    "leaf-internal-only",
		desc:    "internal/core, internal/id, internal/httpjson, internal/oauth, internal/ort, internal/ratelimit, internal/cache and internal/importrules may import only internal/** packages",
		applies: func(pkg string) bool { return isLeaf(pkg) },
		allows:  func(pkg, imported string) bool { return isUnderInternal(imported) },
	},
	{
		name:    "eval-trace-and-internal-only",
		desc:    "eval may import only trace and internal/**, and must never import the root kit façade",
		applies: func(pkg string) bool { return rootOf(pkg) == "eval" },
		allows: func(pkg, imported string) bool {
			return imported == "trace" || isUnderInternal(imported)
		},
	},
	{
		name:    "flow-internal-leaves-only",
		desc:    "flow is an optional module (D85) that used to be a core primitive (D67): it may import only internal/** leaves — never the kit façade (import cycle), never internal/call, never the core family (authz/guard/model/embed/chunk/store/trace), never a sibling or transport package (eval/rerank/mcp/webfetch); stdlib and the two jsonschema libraries of D67 are outside this rule",
		applies: func(pkg string) bool { return rootOf(pkg) == "flow" },
		allows: func(pkg, imported string) bool {
			return isUnderInternal(imported) && !isCallPrimitive(imported)
		},
	},
	{
		name:    "internal-no-facade",
		desc:    "no file under internal/ may import the root kit façade (import-cycle risk)",
		applies: func(pkg string) bool { return isUnderInternal(pkg) },
		allows:  func(pkg, imported string) bool { return imported != "" },
	},
	{
		name:      "core-no-optional",
		desc:      "the compiled core (root façade, internal/**, authz, guard/**, model, embed, chunk, store, trace) must not import an optional package (mcp, webfetch, flow, eval, kittest, rerank, cmd, demo, scripts); the one recorded façade → rerank edge is in coreOptionalExceptions (docs/internals/architecture.md, «Границы ядра», D84)",
		skipTests: true,
		applies:   func(pkg string) bool { return isCorePackage(pkg) },
		allows: func(pkg, imported string) bool {
			if !isOptionalPackage(imported) {
				return true
			}
			return coreOptionalExceptions[[2]string{rootOf(pkg), rootOf(imported)}]
		},
	},
}

func familyOf(dir string) string {
	if name := rootOf(dir); name == "guard" || isCoreFamily(name) {
		return name
	}
	return ""
}

func rootOf(dir string) string {
	if i := strings.IndexByte(dir, '/'); i >= 0 {
		return dir[:i]
	}
	return dir
}

func isCoreFamily(dir string) bool {
	for _, name := range coreFamily {
		if dir == name {
			return true
		}
	}
	return false
}

func isLeaf(dir string) bool {
	for _, name := range leafPackages {
		if dir == name || strings.HasPrefix(dir, name+"/") {
			return true
		}
	}
	return false
}

func isUnderInternal(dir string) bool {
	return dir == "internal" || strings.HasPrefix(dir, "internal/")
}

func isCallPrimitive(dir string) bool {
	return dir == "internal/call" || strings.HasPrefix(dir, "internal/call/")
}

// isCorePackage reports whether dir belongs to the core: the root façade, any
// internal/** package, or a subsystem of the core family (authz, guard/**,
// model, embed, chunk, store, trace).
func isCorePackage(dir string) bool {
	return dir == "" || isUnderInternal(dir) || familyOf(dir) != ""
}

// isOptionalPackage reports whether dir is one of the repository's optional
// packages. It matches on the root of the path, so mcp/stdio is still mcp.
func isOptionalPackage(dir string) bool {
	name := rootOf(dir)
	for _, o := range optionalPackages {
		if name == o {
			return true
		}
	}
	return false
}

func TestImportRules(t *testing.T) {
	root, modulePath := moduleRoot(t)
	imports := collectImports(t, root, modulePath)

	for _, r := range rules {
		t.Logf("rule %-30s %s", r.name, r.desc)
	}

	type violation struct {
		rules map[string]bool
		files map[string]bool
	}
	violations := map[[2]string]*violation{}

	for _, pkg := range sortedPkgDirs(imports) {
		for _, im := range imports[pkg] {
			var hit []string
			for _, r := range rules {
				if r.skipTests && im.test {
					continue
				}
				if !r.applies(pkg) || r.allows(pkg, im.dir) {
					continue
				}
				hit = append(hit, r.name)
			}
			if len(hit) == 0 {
				continue
			}
			key := [2]string{pkg, im.dir}
			v := violations[key]
			if v == nil {
				v = &violation{rules: map[string]bool{}, files: map[string]bool{}}
				violations[key] = v
			}
			for _, name := range hit {
				v.rules[name] = true
			}
			v.files[fmt.Sprintf("%s:%d", im.file, im.line)] = true
		}
	}

	if len(violations) == 0 {
		return
	}

	keys := make([][2]string, 0, len(violations))
	for k := range violations {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d import-rule violation(s) — see docs/internals/architecture.md §«Пакеты» (I-13)", len(keys))
	for _, k := range keys {
		v := violations[k]
		fmt.Fprintf(&b, "\n\n  %s must not import %s", display(modulePath, k[0]), display(modulePath, k[1]))
		fmt.Fprintf(&b, "\n    rule(s): %s", strings.Join(sortedSet(v.rules), ", "))
		fmt.Fprintf(&b, "\n    file(s): %s", strings.Join(sortedSet(v.files), ", "))
	}
	t.Error(b.String())
}

func TestFlowInternalLeavesOnly(t *testing.T) {
	r := ruleByName(t, "flow-internal-leaves-only")

	t.Run("applies to flow only", func(t *testing.T) {
		for _, pkg := range []string{"flow", "flow/step"} {
			if !r.applies(pkg) {
				t.Errorf("rule %s must apply to %q", r.name, pkg)
			}
		}
		for _, pkg := range []string{
			"", "authz", "guard", "store", "trace", "eval",
			"internal/core", "internal/call", "rerank", "mcp", "webfetch",
			"cmd/kitctl", "demo/cmd/bot",
		} {
			if r.applies(pkg) {
				t.Errorf("rule %s must not apply to %q", r.name, pkg)
			}
		}
	})

	t.Run("allows internal leaves", func(t *testing.T) {
		for _, dir := range []string{
			"internal/core", "internal/id", "internal/httpjson",
			"internal/oauth", "internal/cache", "internal/ratelimit",
			"internal/id/sub",
		} {
			if !r.allows("flow", dir) {
				t.Errorf("flow may import %s", dir)
			}
		}
	})

	t.Run("rejects every forbidden edge", func(t *testing.T) {
		forbidden := []struct {
			what, dir string
		}{
			{"the kit façade", ""},
			{"the authz core package", "authz"},
			{"the guard core package", "guard"},
			{"a guard subpackage", "guard/rules"},
			{"the guard test detector", "guard/guardtest"},
			{"the model package", "model"},
			{"the embed package", "embed"},
			{"the chunk package", "chunk"},
			{"the store package", "store"},
			{"the trace package", "trace"},
			{"the call primitive", "internal/call"},
			{"a call subpackage", "internal/call/x"},
			{"the eval package", "eval"},
			{"the rerank plugin", "rerank"},
			{"the mcp client", "mcp"},
			{"the webfetch tool", "webfetch"},
			{"itself", "flow"},
			{"the CLI", "cmd/kitctl"},
			{"the demo", "demo/cmd/bot"},
		}
		for _, c := range forbidden {
			if r.allows("flow", c.dir) {
				t.Errorf("flow must not import %q (%s)", c.dir, c.what)
			}
		}
	})

	t.Run("current graph is inside the boundary", func(t *testing.T) {
		root, modulePath := moduleRoot(t)
		imports := collectImports(t, root, modulePath)
		if len(imports["flow"]) == 0 {
			t.Fatal("no imports collected for flow: the boundary would pass vacuously")
		}
		for _, pkg := range sortedPkgDirs(imports) {
			if !r.applies(pkg) {
				continue
			}
			for _, im := range imports[pkg] {
				if !r.allows(pkg, im.dir) {
					t.Errorf("%s:%d imports %s, which %s forbids", im.file, im.line, display(modulePath, im.dir), r.name)
				}
			}
		}
	})
}

// TestCoreHasNoOptionalDependency: the core is a point solution only if it does
// not lean on the optional packages. The rule also pins the recorded edge
// (façade → rerank), so the coupling is visible and cannot grow silently.
func TestCoreHasNoOptionalDependency(t *testing.T) {
	r := ruleByName(t, "core-no-optional")

	t.Run("applies to the core only", func(t *testing.T) {
		for _, pkg := range []string{
			"", "internal/call", "internal/core", "authz",
			"guard", "guard/rules", "guard/gliner", "model", "embed", "chunk", "store", "trace",
		} {
			if !r.applies(pkg) {
				t.Errorf("rule %s must apply to %q", r.name, pkg)
			}
		}
		for _, pkg := range []string{
			"mcp", "mcp/stdio", "webfetch", "flow", "eval", "kittest", "rerank",
			"cmd/kitctl", "demo/cmd/bot", "scripts/docslint",
		} {
			if r.applies(pkg) {
				t.Errorf("rule %s must not apply to %q", r.name, pkg)
			}
		}
	})

	t.Run("allows internal and family edges, forbids optionals", func(t *testing.T) {
		if !r.allows("", "internal/call") || !r.allows("trace", "store") {
			t.Error("core-internal edges must be allowed")
		}
		for _, dir := range []string{
			"mcp", "webfetch", "flow", "eval", "kittest", "cmd/kitctl", "demo/cmd/bot", "scripts/docslint",
		} {
			if r.allows("store", dir) {
				t.Errorf("the core must not import %q", dir)
			}
		}
		if !r.allows("", "rerank") {
			t.Error("the recorded façade → rerank edge must be allowed")
		}
		if r.allows("store", "rerank") || r.allows("model", "rerank") {
			t.Error("rerank is optional: only the façade may import it")
		}
	})

	t.Run("current graph matches the recorded edges exactly", func(t *testing.T) {
		root, modulePath := moduleRoot(t)
		imports := collectImports(t, root, modulePath)
		if len(imports[""]) == 0 {
			t.Fatal("no imports collected for the façade: the boundary would pass vacuously")
		}
		observed := map[[2]string]bool{}
		for _, pkg := range sortedPkgDirs(imports) {
			if !r.applies(pkg) {
				continue
			}
			for _, im := range imports[pkg] {
				if r.skipTests && im.test {
					continue
				}
				if !r.allows(pkg, im.dir) {
					t.Errorf("%s:%d imports %s, which %s forbids", im.file, im.line, display(modulePath, im.dir), r.name)
				}
				if isOptionalPackage(im.dir) {
					observed[[2]string{rootOf(pkg), rootOf(im.dir)}] = true
				}
			}
		}
		for edge := range coreOptionalExceptions {
			if !observed[edge] {
				t.Errorf("stale exception %q → %q: no such core→optional edge in the graph", edge[0], edge[1])
			}
		}
	})
}

func TestRuleNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range rules {
		if r.name == "" {
			t.Error("rule with an empty name")
		}
		if seen[r.name] {
			t.Errorf("duplicate rule name %q", r.name)
		}
		seen[r.name] = true
		if r.desc == "" {
			t.Errorf("rule %q has an empty desc", r.name)
		}
	}
}

func ruleByName(t *testing.T, name string) rule {
	t.Helper()
	for _, r := range rules {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("rule %q not found in the rules table", name)
	return rule{}
}

func moduleRoot(t *testing.T) (root, modulePath string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate the module root")
	}
	root = filepath.Dir(filepath.Dir(filepath.Dir(file)))
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod under %s: %v", root, err)
	}
	modulePath = modulePathFromGoMod(string(b))
	if modulePath == "" {
		t.Fatalf("could not parse a module path from %s/go.mod", root)
	}
	return root, modulePath
}

func modulePathFromGoMod(gomod string) string {
	sc := bufio.NewScanner(strings.NewReader(gomod))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

func collectImports(t *testing.T, root, modulePath string) map[string][]imp {
	t.Helper()
	byPkg := map[string][]imp{}
	prefix := modulePath + "/"

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		if pkgDir == "." {
			pkgDir = ""
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		for _, im := range fileImports(f, fset, modulePath, prefix) {
			im.file = rel
			im.test = strings.HasSuffix(rel, "_test.go")
			byPkg[pkgDir] = append(byPkg[pkgDir], im)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return byPkg
}

func fileImports(f *ast.File, fset *token.FileSet, modulePath, prefix string) []imp {
	var out []imp
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if p != modulePath && !strings.HasPrefix(p, prefix) {
			continue
		}
		dir := strings.TrimPrefix(strings.TrimPrefix(p, modulePath), "/")
		out = append(out, imp{dir: dir, line: fset.Position(spec.Pos()).Line})
	}
	return out
}

func sortedPkgDirs(m map[string][]imp) []string {
	dirs := make([]string, 0, len(m))
	for d := range m {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func display(modulePath, dir string) string {
	if dir == "" {
		return modulePath
	}
	return modulePath + "/" + dir
}
