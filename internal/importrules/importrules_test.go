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
}

type rule struct {
	name    string
	desc    string
	applies func(pkg string) bool
	allows  func(pkg, imported string) bool
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
	"internal/importrules",
}

var skipDirs = map[string]bool{
	"models":       true,
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
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
		desc:    "internal/core, internal/id, internal/httpjson, internal/oauth, internal/ort and internal/importrules may import only internal/** packages",
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
		name:    "internal-no-facade",
		desc:    "no file under internal/ may import the root kit façade (import-cycle risk)",
		applies: func(pkg string) bool { return isUnderInternal(pkg) },
		allows:  func(pkg, imported string) bool { return imported != "" },
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
