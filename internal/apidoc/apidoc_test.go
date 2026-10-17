package apidoc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var skipDirs = map[string]bool{
	"models":       true,
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
	"site":         true,
}

// TestExportedAPIIsDocumented keeps pkg.go.dev populated: every exported
// declaration in a published package carries a doc comment (BR-75).
// Internal packages, the demo, the CLI and the scripts are out of scope — they
// are not imported by consumers.
func TestExportedAPIIsDocumented(t *testing.T) {
	root := moduleRoot(t)
	var missing []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return filepath.SkipDir
			}
			if unpublishedRoot(rootOf(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", rel, perr)
		}
		report := func(pos token.Pos, kind, name string) {
			missing = append(missing, fmt.Sprintf("%s:%d %s %s", rel, fset.Position(pos).Line, kind, name))
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() || d.Doc != nil {
					continue
				}
				// A method on an unexported type is not part of the exported API.
				if d.Recv != nil && !receiverExported(d.Recv) {
					continue
				}
				report(d.Pos(), "func", d.Name.Name)
			case *ast.GenDecl:
				if d.Doc != nil {
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() && s.Doc == nil {
							report(s.Pos(), "type", s.Name.Name)
						}
					case *ast.ValueSpec:
						if s.Doc != nil {
							continue
						}
						for _, n := range s.Names {
							if n.IsExported() {
								report(n.Pos(), "value", n.Name)
							}
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Fatalf("%d exported declaration(s) without a doc comment — pkg.go.dev stays empty for them (BR-75):\n  %s",
		len(missing), strings.Join(missing, "\n  "))
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate the module root")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func rootOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

func unpublishedRoot(name string) bool {
	switch name {
	case "internal", "demo", "scripts", "cmd":
		return true
	}
	return false
}

// receiverExported reports whether a method's receiver type is exported.
func receiverExported(recv *ast.FieldList) bool {
	if len(recv.List) == 0 {
		return false
	}
	expr := recv.List[0].Type
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.IsExported()
		default:
			return false
		}
	}
}
