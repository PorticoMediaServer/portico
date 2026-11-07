package writer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A typed command/protocol result with a caller lifetime is a release boundary,
// even if it only delegates to another guarded entry. Adding a success return
// cannot silently opt out. Pure unimplemented stubs are the only exception.
func unguardedCommandFinishes(file *ast.File) []string {
	var bad []string
	resultTypes := commandFinishGuardResultTypes()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Type.Results == nil {
			continue
		}
		hasContext := false
		for _, p := range fn.Type.Params.List {
			if typ, ok := p.Type.(*ast.SelectorExpr); ok {
				if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "context" && typ.Sel.Name == "Context" {
					hasContext = true
				}
			}
		}
		if !hasContext {
			continue
		}
		needs := commandFinishGuardNeedsName(fn.Name.Name)
		for _, r := range fn.Type.Results.List {
			typ := r.Type
			if pointer, ok := typ.(*ast.StarExpr); ok {
				typ = pointer.X
			}
			if id, ok := typ.(*ast.Ident); ok && resultTypes[id.Name] {
				needs = true
			}
		}
		if !needs {
			continue
		}
		if len(fn.Body.List) == 1 {
			if ret, ok := fn.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 2 {
				zero, zok := ret.Results[0].(*ast.CompositeLit)
				err, eok := ret.Results[1].(*ast.Ident)
				if zok && len(zero.Elts) == 0 && eok && err.Name == "ErrNotImplemented" {
					continue
				}
			}
		}
		guarded := false
		for _, s := range fn.Body.List {
			if d, ok := s.(*ast.DeferStmt); ok {
				guarded = guarded || guardedFinishDefer(fn, d)
			}
			// The deferred rule must be installed before ANY reachable return, not
			// merely somewhere in a later branch of the function.
			returns := false
			ast.Inspect(s, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if _, ok := n.(*ast.ReturnStmt); ok {
					returns = true
				}
				return true
			})
			if returns && !guarded {
				break
			}
		}
		if !guarded {
			bad = append(bad, fn.Name.Name)
		}
	}
	return bad
}

// Accept only the two straight-line shapes used by real releases: assignment
// directly to named results, or a finish pair immediately projected into ALL
// named results. Identifier-object identity distinguishes actual named returns
// from unrelated or same-spelled shadow variables.
func guardedFinishDefer(fn *ast.FuncDecl, deferred *ast.DeferStmt) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	literal, ok := deferred.Call.Fun.(*ast.FuncLit)
	if !ok || len(deferred.Call.Args) != 0 || literal.Type.Params.NumFields() != 0 || len(literal.Body.List) == 0 {
		return false
	}
	var named []*ast.Ident
	for _, field := range fn.Type.Results.List {
		if len(field.Names) == 0 {
			return false
		}
		for _, id := range field.Names {
			if id.Name == "_" || id.Obj == nil {
				return false
			}
			named = append(named, id)
		}
	}
	lastType, ok := fn.Type.Results.List[len(fn.Type.Results.List)-1].Type.(*ast.Ident)
	if !ok || lastType.Name != "error" || len(named) == 0 {
		return false
	}
	// An early return inside the deferred closure would skip the finish.
	earlyReturn := false
	for _, stmt := range literal.Body.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if _, ok := n.(*ast.ReturnStmt); ok {
				earlyReturn = true
			}
			return true
		})
	}
	if earlyReturn {
		return false
	}
	last, ok := literal.Body.List[len(literal.Body.List)-1].(*ast.AssignStmt)
	if !ok || last.Tok != token.ASSIGN {
		return false
	}
	if call := finishAssignment(last); call != nil {
		// Error-only native scope checks discard ONLY a literal empty struct, not
		// a payload. Their sole named result must receive the finish error.
		if len(named) == 1 {
			blank, ok := last.Lhs[0].(*ast.Ident)
			if !ok || blank.Name != "_" || resultObject(last.Lhs[1]) != named[0].Obj {
				return false
			}
			zero, ok := call.Args[2].(*ast.CompositeLit)
			if !ok || len(zero.Elts) != 0 {
				return false
			}
			shape, ok := zero.Type.(*ast.StructType)
			return ok && shape.Fields.NumFields() == 0
		}
		return namedFinishTargets(last.Lhs, named)
	}
	if len(named) < 2 || len(literal.Body.List) < 2 || !namedFinishTargets(last.Lhs, named) || len(last.Rhs) != len(named) {
		return false
	}
	pair, ok := literal.Body.List[len(literal.Body.List)-2].(*ast.AssignStmt)
	if !ok || finishAssignment(pair) == nil {
		return false
	}
	value, valueOK := pair.Lhs[0].(*ast.Ident)
	failure, failureOK := pair.Lhs[1].(*ast.Ident)
	if !valueOK || !failureOK || value.Name == "_" || failure.Name == "_" || value.Obj == nil || failure.Obj == nil || value.Obj == failure.Obj {
		return false
	}
	for i, expr := range last.Rhs {
		want := value.Obj
		if i == len(named)-1 {
			want = failure.Obj
		}
		if resultObject(expr) != want {
			return false
		}
	}
	return true
}

func finishAssignment(assign *ast.AssignStmt) *ast.CallExpr {
	if len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 4 {
		return nil
	}
	name, ok := call.Fun.(*ast.Ident)
	if !ok || name.Name != "finishCommand" {
		return nil
	}
	return call
}

func namedFinishTargets(targets []ast.Expr, named []*ast.Ident) bool {
	if len(targets) != len(named) {
		return false
	}
	for i, target := range targets {
		id, ok := target.(*ast.Ident)
		if !ok || id.Name == "_" || id.Obj != named[i].Obj {
			return false
		}
	}
	return true
}

func resultObject(expr ast.Expr) *ast.Object {
	switch node := expr.(type) {
	case *ast.Ident:
		if node.Name != "_" {
			return node.Obj
		}
	case *ast.SelectorExpr:
		return resultObject(node.X)
	case *ast.ParenExpr:
		return resultObject(node.X)
	}
	return nil
}

func TestEveryCommandReleaseUsesSharedFinish(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bad := unguardedCommandFinishes(file); len(bad) > 0 {
			t.Errorf("%s unguarded releases: %v", name, bad)
		}
	}
	// The physical inventory is deliberate: new source files must participate,
	// not just a closed list of today's dispatchers.
	if len(files) == 0 {
		t.Fatal("empty physical source inventory")
	}
}
func TestCommandReleaseGuardRejectsBypasses(t *testing.T) {
	for _, family := range commandFinishGuardFamilies {
		for _, body := range family.bypasses {
			file, err := parser.ParseFile(token.NewFileSet(), "control.go", "package writer\n"+body, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(unguardedCommandFinishes(file)) != 1 {
				t.Fatal("guard accepted bypass", body)
			}
		}
	}
}

func TestCommandReleaseGuardAcceptsNamedPublication(t *testing.T) {
	for _, body := range []string{
		`func release(ctx context.Context)(out InventoryResult,err error){defer func(){out,err=finishCommand(ctx,ctx,out,err)}();n,err:=work();_=n;return InventoryResult{},err}`,
		`func release(ctx context.Context)(out InventoryResult,err error){defer func(){out,err=finishCommand(ctx,ctx,out,err)}();return InventoryResult{},nil}`,
		`func release(ctx context.Context)(out InventoryResult,ack AckResult,err error){defer func(){v,e:=finishCommand(ctx,ctx,packet{out,ack},err);out,ack,err=v.output,v.ack,e}();return InventoryResult{},AckResult{},nil}`,
		`func checkInventoryScope(ctx context.Context)(err error){defer func(){_,err=finishCommand(ctx,ctx,struct{}{},err)}();return nil}`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "positive.go", "package writer\n"+body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bad := unguardedCommandFinishes(file); len(bad) != 0 {
			t.Fatal("valid named publication refused", body, bad)
		}
	}
}
