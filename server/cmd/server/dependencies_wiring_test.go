package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
)

// dependenciesLeftToDefaults are the httpapi.Dependencies fields the served
// handler may leave unset, each because a nil value has a working default.
// Every other field must be wired: a nil service turns its routes into 503s
// (the G2 merge on 16 Sep dropped Downloads this way, and every server answered
// "Offline downloads are not configured" until 23 Sep).
var dependenciesLeftToDefaults = map[string]string{
	"TrustedProxies": "the owner settings document is the runtime authority; nil is an empty embedding fallback",
	"PlaybackV1":     "nil builds the Playback Protocol v1 service from the handler's own dependencies",
}

func TestMainWiresEveryHTTPDependency(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	served, err := servedHTTPDependencyFields(file)
	if err != nil {
		t.Fatal(err)
	}
	kind := reflect.TypeOf(httpapi.Dependencies{})
	for _, wired := range served {
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			if !field.IsExported() || wired[field.Name] {
				continue
			}
			if _, defaulted := dependenciesLeftToDefaults[field.Name]; defaulted {
				continue
			}
			t.Errorf("main.go serves httpapi.Dependencies without %s (%s); wire it, or list it in dependenciesLeftToDefaults with the reason nil is safe", field.Name, field.Type)
		}
	}
	for name := range dependenciesLeftToDefaults {
		if _, ok := kind.FieldByName(name); !ok {
			t.Errorf("dependenciesLeftToDefaults names %s, which httpapi.Dependencies no longer has", name)
		}
	}
}

// Follow straight-line construction up to each actual httpapi.New call. Reading
// needs the completed dependencies to construct its shared service, so it is
// wired after the literal. Assignments after New, to another value, or inside a
// conditional/closure do not establish the served handler's dependency.
func servedHTTPDependencyFields(file *ast.File) ([]map[string]bool, error) {
	var served []map[string]bool
	var unresolved bool
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		values := map[string]map[string]bool{}
		for _, statement := range block.List {
			// RHS calls use the value before this statement's assignments.
			ast.Inspect(statement, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.BlockStmt, *ast.FuncLit:
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok || !httpapiSelector(call.Fun, "New") {
					return true
				}
				if len(call.Args) != 1 {
					unresolved = true
					return false
				}
				fields := httpDependencyFields(call.Args[0], values)
				if fields == nil {
					unresolved = true
				} else {
					served = append(served, fields)
				}
				return false
			})
			assignment, ok := statement.(*ast.AssignStmt)
			if !ok || len(assignment.Lhs) != len(assignment.Rhs) {
				continue
			}
			// Evaluate all RHS values first, preserving Go's copy semantics.
			rhs := make([]map[string]bool, len(assignment.Rhs))
			for i, value := range assignment.Rhs {
				rhs[i] = httpDependencyFields(value, values)
			}
			for i, target := range assignment.Lhs {
				switch target := target.(type) {
				case *ast.Ident:
					values[target.Name] = rhs[i]
				case *ast.SelectorExpr:
					if variable, ok := target.X.(*ast.Ident); ok && values[variable.Name] != nil {
						values[variable.Name][target.Sel.Name] = !explicitNil(assignment.Rhs[i])
					}
				}
			}
		}
		return true
	})
	if unresolved || len(served) == 0 {
		return nil, fmt.Errorf("cannot resolve every served httpapi.New dependency value from straight-line construction")
	}
	return served, nil
}

func httpDependencyFields(expression ast.Expr, values map[string]map[string]bool) map[string]bool {
	expression = ast.Unparen(expression)
	if variable, ok := expression.(*ast.Ident); ok {
		original := values[variable.Name]
		if original == nil {
			return nil
		}
		fields := map[string]bool{}
		for key, value := range original {
			fields[key] = value
		}
		return fields
	}
	literal, ok := expression.(*ast.CompositeLit)
	if !ok || !httpapiSelector(literal.Type, "Dependencies") {
		return nil
	}
	fields := map[string]bool{}
	for _, element := range literal.Elts {
		if kv, ok := element.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				fields[key.Name] = !explicitNil(kv.Value)
			}
		}
	}
	return fields
}

func httpapiSelector(expression ast.Expr, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "httpapi"
}

func explicitNil(expression ast.Expr) bool {
	name, ok := ast.Unparen(expression).(*ast.Ident)
	return ok && name.Name == "nil"
}

func TestHTTPDependencyWiringFollowsServedValue(t *testing.T) {
	for _, tc := range []struct {
		name, statements  string
		wired, unresolved bool
	}{
		{"inline", `httpapi.New(httpapi.Dependencies{Reading: reader})`, true, false},
		{"post literal", `deps := httpapi.Dependencies{DB: db}; reader := httpapi.NewReadingService(deps); defer reader.Close(); deps.Reading = reader; httpapi.New(deps)`, true, false},
		{"absent", `deps := httpapi.Dependencies{DB: db}; httpapi.New(deps)`, false, false},
		{"after constructor", `deps := httpapi.Dependencies{DB: db}; httpapi.New(deps); deps.Reading = reader`, false, false},
		{"different value", `deps := httpapi.Dependencies{DB: db}; other := httpapi.Dependencies{DB: db}; other.Reading = reader; httpapi.New(deps)`, false, false},
		{"conditional", `deps := httpapi.Dependencies{DB: db}; if enabled { deps.Reading = reader }; httpapi.New(deps)`, false, false},
		{"closure", `deps := httpapi.Dependencies{DB: db}; run := func(){ deps.Reading = reader }; httpapi.New(deps)`, false, false},
		{"larger unused literal", `unused := httpapi.Dependencies{DB: db, Reading: reader, Storage: storage}; deps := httpapi.Dependencies{DB: db}; httpapi.New(deps)`, false, false},
		{"literal nil", `deps := httpapi.Dependencies{Reading: nil}; httpapi.New(deps)`, false, false},
		{"later nil", `deps := httpapi.Dependencies{Reading: reader}; deps.Reading = (nil); httpapi.New(deps)`, false, false},
		{"replacement", `deps := httpapi.Dependencies{Reading: reader}; deps = httpapi.Dependencies{DB: db}; httpapi.New(deps)`, false, false},
		{"copy before wiring", `deps := httpapi.Dependencies{DB: db}; copied := deps; deps.Reading = reader; httpapi.New(copied)`, false, false},
		{"copy after wiring", `deps := httpapi.Dependencies{Reading: reader}; copied := deps; deps.Reading = nil; httpapi.New(copied)`, true, false},
		{"shadow", `deps := httpapi.Dependencies{DB: db}; { deps := httpapi.Dependencies{Reading: reader} }; httpapi.New(deps)`, false, false},
		{"unknown replacement", `deps := httpapi.Dependencies{Reading: reader}; deps = unknown(); httpapi.New(deps)`, false, true},
		{"unserved", `deps := httpapi.Dependencies{Reading: reader}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package main; func main(){"+tc.statements+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			served, err := servedHTTPDependencyFields(file)
			if (err != nil) != tc.unresolved {
				t.Fatal("unexpected resolution", err)
			}
			if err == nil && (len(served) != 1 || served[0]["Reading"] != tc.wired) {
				t.Fatalf("served fields=%v, want Reading=%v", served, tc.wired)
			}
		})
	}
}
