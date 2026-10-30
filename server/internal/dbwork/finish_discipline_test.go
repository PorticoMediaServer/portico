package dbwork

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A Write holds the process-wide write gate until Commit or Rollback. A handle
// that is used (w.Tx()) but never finished in its function keeps the gate for
// the life of the process: every later write in the server waits behind it.
// That happened when a second transaction (gated2) was opened and the code went
// on finishing the first one, which is already done and ignores the call.
//
// The rule, checked where it is written: in every function, a handle whose Tx()
// is used must also be committed or rolled back there, unless the handle leaves
// the function (returned, passed on, or stored), in which case its receiver owns
// it. Prefer WithWriteTx, which cannot leak.
func TestEveryWriteHandleIsFinishedWhereItIsUsed(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, tree := range []string{"internal", "cmd"} {
		walkErr := filepath.Walk(filepath.Join(root, tree), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			relative, _ := filepath.Rel(root, path)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				for _, name := range unfinishedHandles(fn.Body) {
					offenders = append(offenders, filepath.ToSlash(relative)+": "+fn.Name.Name+" uses "+name+".Tx() but never commits or rolls back "+name)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("write handles that can leak the write gate (finish them, or use WithWriteTx):\n%s", strings.Join(offenders, "\n"))
	}
}

// unfinishedHandles returns identifiers v with v.Tx() in body but no v.Commit()
// or v.Rollback(), and that never escape the function.
func unfinishedHandles(body *ast.BlockStmt) []string {
	used := map[string]bool{}
	finished := map[string]bool{}
	escapes := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					switch sel.Sel.Name {
					case "Tx":
						if len(x.Args) == 0 {
							used[id.Name] = true
						}
					case "Commit", "Rollback":
						finished[id.Name] = true
					}
				}
			}
			for _, arg := range x.Args {
				if id, ok := arg.(*ast.Ident); ok {
					escapes[id.Name] = true
				}
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				if id, ok := r.(*ast.Ident); ok {
					escapes[id.Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, r := range x.Rhs {
				if id, ok := r.(*ast.Ident); ok && i < len(x.Lhs) {
					if _, plain := x.Lhs[i].(*ast.Ident); !plain {
						escapes[id.Name] = true
					}
				}
			}
		case *ast.CompositeLit:
			for _, e := range x.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					e = kv.Value
				}
				if id, ok := e.(*ast.Ident); ok {
					escapes[id.Name] = true
				}
			}
		}
		return true
	})
	var out []string
	for name := range used {
		if !finished[name] && !escapes[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func TestUnfinishedHandlesCatchesTheSecondTransactionMistake(t *testing.T) {
	src := `package p
func f() error {
	gated, e := begin()
	tx := gated.Tx()
	use(tx)
	if e = gated.Commit(); e != nil {
		return e
	}
	gated2, e := begin()
	tx = gated2.Tx()
	defer gated.Rollback()
	use(tx)
	return gated.Commit()
}`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := unfinishedHandles(file.Decls[0].(*ast.FuncDecl).Body)
	if len(got) != 1 || got[0] != "gated2" {
		t.Fatalf("unfinishedHandles = %v, want [gated2]", got)
	}
}
