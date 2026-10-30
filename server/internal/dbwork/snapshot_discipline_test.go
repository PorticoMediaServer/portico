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

// A read snapshot cannot write: the transaction is read-only and Commit reports
// ErrSnapshotWrite. That is a runtime refusal, and twice (live channel starts,
// 17-23 Sep; source re-verification, B80) the refused write sat behind a
// "best effort" error path and failed silently for days. This guardrail finds
// the shape statically: an Exec on a transaction that came from BeginSnapshot,
// directly or through a helper that hands its snapshot to a callback.
//
// It is a syntactic check. It sees `tx.Exec*` on the snapshot's own variable and
// on a callback's *sql.Tx parameter, and one level of indirection: a call that
// passes that transaction to a function which Execs on its own *sql.Tx
// parameter. Deeper chains are not followed.

// knownSnapshotWrites are open findings (INT — Review.md), listed so the
// guardrail stays green while they are routed. Remove each entry with its fix.
// Keyed by file and message (not line), with the number of sites allowed.
var knownSnapshotWrites = map[string]int{}

func TestNoWritesThroughReadSnapshots(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	type parsed struct {
		rel  string
		dir  string
		file *ast.File
	}
	var files []parsed
	fset := token.NewFileSet()
	walkErr := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/dbwork/") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		files = append(files, parsed{rel: rel, dir: filepath.ToSlash(filepath.Dir(rel)), file: f})
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}

	// Pass 1: helpers whose body opens a snapshot and whose parameters include
	// a callback taking *sql.Tx, and which never open a gated write.
	helpers := map[string]bool{}
	key := func(dir, name string) string {
		if ast.IsExported(name) {
			return "*." + name
		}
		return dir + "." + name
	}
	for _, p := range files {
		for _, decl := range p.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			snap, write := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "dbwork" {
							switch sel.Sel.Name {
							case "BeginSnapshot":
								snap = true
							case "Begin", "ExecWrite", "BeginConn":
								write = true
							}
						}
					}
				}
				return true
			})
			if snap && !write && takesTxCallback(fn.Type) {
				helpers[key(p.dir, fn.Name.Name)] = true
			}
		}
	}

	// Pass 2: one level of indirection — functions that take a *sql.Tx and
	// Exec on it. Passing a snapshot transaction to one of them is a write too.
	writers := map[string]bool{}
	for _, p := range files {
		for _, decl := range p.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			tx := txParam(fn.Type)
			if tx == "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && isExecOn(c, tx) {
					writers[key(p.dir, fn.Name.Name)] = true
				}
				return true
			})
		}
	}
	passesTo := func(p parsed, c *ast.CallExpr, tx string) string {
		name := calledName(c)
		if name == "" || !(writers[key(p.dir, name)] || writers["*."+name]) {
			return ""
		}
		for _, a := range c.Args {
			if id, ok := a.(*ast.Ident); ok && id.Name == tx {
				return name
			}
		}
		return ""
	}

	var offenders []string
	report := func(p parsed, pos token.Pos, what string) {
		if k := p.rel + "|" + what; knownSnapshotWrites[k] > 0 {
			knownSnapshotWrites[k]--
			return
		}
		offenders = append(offenders, p.rel+":"+itoa(fset.Position(pos).Line)+": "+what)
	}
	for _, p := range files {
		ast.Inspect(p.file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			}
			if body == nil {
				// Callbacks handed to snapshot helpers.
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := calledName(call)
				if name == "" || !(helpers[key(p.dir, name)] || helpers["*."+name]) {
					return true
				}
				for _, arg := range call.Args {
					lit, ok := arg.(*ast.FuncLit)
					if !ok {
						continue
					}
					tx := txParam(lit.Type)
					if tx == "" {
						continue
					}
					ast.Inspect(lit.Body, func(m ast.Node) bool {
						if c, ok := m.(*ast.CallExpr); ok && isExecOn(c, tx) {
							report(p, c.Pos(), "write inside a "+name+" snapshot callback")
						} else if ok {
							if w := passesTo(p, c, tx); w != "" {
								report(p, c.Pos(), "writer "+w+" called inside a "+name+" snapshot callback")
							}
						}
						return true
					})
				}
				return true
			}
			// Direct use: w := dbwork.BeginSnapshot(...); tx := w.Tx(); tx.Exec...
			snaps, txs := map[string]bool{}, map[string]bool{}
			ast.Inspect(body, func(m ast.Node) bool {
				as, ok := m.(*ast.AssignStmt)
				if !ok || len(as.Rhs) != 1 {
					return true
				}
				call, ok := as.Rhs[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				lhs, ok := as.Lhs[0].(*ast.Ident)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok {
					if x.Name == "dbwork" && sel.Sel.Name == "BeginSnapshot" {
						snaps[lhs.Name] = true
					} else if sel.Sel.Name == "Tx" && snaps[x.Name] {
						txs[lhs.Name] = true
					}
				}
				return true
			})
			if len(txs) == 0 && len(snaps) == 0 {
				return true
			}
			ast.Inspect(body, func(m ast.Node) bool {
				c, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				for tx := range txs {
					if isExecOn(c, tx) {
						report(p, c.Pos(), "write through read snapshot "+tx)
					} else if w := passesTo(p, c, tx); w != "" {
						report(p, c.Pos(), "writer "+w+" called with read snapshot "+tx)
					}
				}
				// w.Tx().Exec...
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "Exec") {
					if inner, ok := sel.X.(*ast.CallExpr); ok {
						if is, ok := inner.Fun.(*ast.SelectorExpr); ok && is.Sel.Name == "Tx" {
							if x, ok := is.X.(*ast.Ident); ok && snaps[x.Name] {
								report(p, c.Pos(), "write through read snapshot "+x.Name+".Tx()")
							}
						}
					}
				}
				return true
			})
			return true
		})
	}
	sort.Strings(offenders)
	offenders = uniqueStrings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("writes through a read snapshot always fail (ErrSnapshotWrite / read-only transaction); open the transaction with dbwork.Begin:\n%s", strings.Join(offenders, "\n"))
	}
}

func takesTxCallback(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	for _, field := range ft.Params.List {
		if fn, ok := field.Type.(*ast.FuncType); ok && txParam(fn) != "" || ok && hasTxType(fn) {
			return true
		}
	}
	return false
}

func hasTxType(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	for _, field := range ft.Params.List {
		if isSQLTx(field.Type) {
			return true
		}
	}
	return false
}

func txParam(ft *ast.FuncType) string {
	if ft.Params == nil {
		return ""
	}
	for _, field := range ft.Params.List {
		if isSQLTx(field.Type) && len(field.Names) > 0 && field.Names[0].Name != "_" {
			return field.Names[0].Name
		}
	}
	return ""
}

func isSQLTx(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "sql" && sel.Sel.Name == "Tx"
}

func isExecOn(c *ast.CallExpr, tx string) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, "Exec") {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == tx
}

func calledName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	}
	return ""
}

func uniqueStrings(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
