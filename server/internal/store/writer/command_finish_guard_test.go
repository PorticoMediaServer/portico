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
	resultTypes := map[string]bool{"commandOutput": true, "SearchImageStageResult": true, "InventoryResult": true, "EffectiveStageResult": true, "AckResult": true, "EffectiveStageBeginResult": true, "inventoryReceiptLookup": true}
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
		needs := fn.Name.Name == "disclose" || strings.HasPrefix(fn.Name.Name, "checkInventory")
		for _, r := range fn.Type.Results.List {
			if typ, ok := r.Type.(*ast.Ident); ok && resultTypes[typ.Name] {
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
				ast.Inspect(d.Call, func(n ast.Node) bool {
					// Merely calling the helper and discarding its zeroed value is not a guard.
					assign, ok := n.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for _, rhs := range assign.Rhs {
						if call, ok := rhs.(*ast.CallExpr); ok {
							if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "finishCommand" {
								guarded = true
							}
						}
					}
					return true
				})
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
	for _, body := range []string{
		`func bypass(ctx context.Context)(InventoryResult,error){return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){if true{return InventoryResult{},nil};defer func(){finishCommand()}();return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){defer func(){}();return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){defer func(){finishCommand()}();return InventoryResult{},nil}`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "control.go", "package writer\n"+body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(unguardedCommandFinishes(file)) != 1 {
			t.Fatal("guard accepted bypass", body)
		}
	}
}
