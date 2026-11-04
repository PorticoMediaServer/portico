package persistence

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

type derivedCallableBinding struct {
	expr ast.Expr
	info *types.Info
}
type derivedCallableOrigins struct {
	writes  map[types.Object][]derivedCallableBinding
	exposed map[types.Object]bool
}

// Function parameters, fields, heap/global slots and function-producing calls
// cannot prove an ordinary target. Treat them as unknown writers. Only stable
// local aliases of declared free functions/literals are closed. This rule does
// not depend on finding a reflect import in the caller or guessing a method name.
func derivedCallableFiles(loader *derivedTypeLoader, _ map[string]bool) (map[string]bool, error) {
	origins := &derivedCallableOrigins{writes: map[types.Object][]derivedCallableBinding{}, exposed: map[types.Object]bool{}}
	type checkedFile struct {
		file *ast.File
		info *types.Info
		name string
	}
	var files []checkedFile
	for _, group := range loader.groups {
		for _, file := range group {
			match, err := loader.matches(file)
			if err != nil {
				return nil, err
			}
			if !match && loader.context != nil {
				continue
			}
			info := loader.forFile(file)
			if loader.exportErr != nil {
				return nil, loader.exportErr
			}
			if info == nil {
				continue
			}
			name, err := filepath.Rel(loader.root, loader.fset.Position(file.Pos()).Filename)
			if err != nil {
				return nil, err
			}
			files = append(files, checkedFile{file, info, filepath.ToSlash(name)})
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.UnaryExpr:
					if node.Op == token.AND {
						if object := derivedFlowObject(node.X, info); object != nil && derivedCallableType(object.Type()) {
							origins.exposed[object] = true
						}
					}
				case *ast.AssignStmt:
					for i, left := range node.Lhs {
						if object := derivedFlowObject(left, info); object != nil {
							var value ast.Expr
							if len(node.Rhs) == len(node.Lhs) {
								value = node.Rhs[i]
							}
							origins.writes[object] = append(origins.writes[object], derivedCallableBinding{value, info})
						}
					}
				case *ast.ValueSpec:
					for i, name := range node.Names {
						if object := info.Defs[name]; object != nil {
							var value ast.Expr
							if len(node.Values) == len(node.Names) {
								value = node.Values[i]
							}
							origins.writes[object] = append(origins.writes[object], derivedCallableBinding{value, info})
						}
					}
				case *ast.RangeStmt:
					for _, left := range []ast.Expr{node.Key, node.Value} {
						if object := derivedFlowObject(left, info); object != nil {
							origins.writes[object] = append(origins.writes[object], derivedCallableBinding{nil, info})
						}
					}
				}
				return true
			})
		}
	}
	// Taking a function slot's address invalidates the closed-local proof,
	// even when an interface, conversion or helper later obscures that address.
	out := map[string]bool{}
	for _, source := range files {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			info := source.info
			if info.Types[call.Fun].IsType() {
				return true
			}
			switch derivedFlowObject(call.Fun, info).(type) {
			case *types.Func, *types.Builtin, *types.TypeName:
				return true
			}
			typ := info.TypeOf(call.Fun)
			if (derivedCallableType(typ) || typ == nil || typ == types.Typ[types.Invalid]) && !origins.closed(call.Fun, info, map[types.Object]bool{}) {
				out[source.name] = true
			}
			return true
		})
	}
	return out, nil
}

func derivedFlowObject(expr ast.Expr, info *types.Info) types.Object {
	switch expr := expr.(type) {
	case *ast.Ident:
		return info.ObjectOf(expr)
	case *ast.SelectorExpr:
		if selection := info.Selections[expr]; selection != nil {
			return selection.Obj()
		}
		return info.Uses[expr.Sel]
	case *ast.ParenExpr:
		return derivedFlowObject(expr.X, info)
	case *ast.IndexExpr:
		if _, function := derivedFlowObject(expr.X, info).(*types.Func); function {
			return derivedFlowObject(expr.X, info)
		}
	case *ast.IndexListExpr:
		return derivedFlowObject(expr.X, info)
	}
	return nil
}
func (o *derivedCallableOrigins) closed(expr ast.Expr, info *types.Info, seen map[types.Object]bool) bool {
	switch expr := expr.(type) {
	case *ast.FuncLit:
		return true
	case *ast.ParenExpr:
		return o.closed(expr.X, info, seen)
	case *ast.StarExpr:
		return false // mutable function storage
	case *ast.CallExpr:
		if info.Types[expr.Fun].IsType() && derivedCallableType(info.TypeOf(expr)) && len(expr.Args) == 1 {
			return o.closed(expr.Args[0], info, seen)
		}
	default:
		return o.closedObject(derivedFlowObject(expr, info), seen)
	}
	return false
}
func (o *derivedCallableOrigins) closedObject(object types.Object, seen map[types.Object]bool) bool {
	switch object := object.(type) {
	case *types.Nil:
		return true // invoking nil cannot reach a driver
	case *types.Func:
		signature, _ := object.Type().(*types.Signature)
		return signature != nil && signature.Recv() == nil && (object.Pkg() == nil || object.Pkg().Path() != "reflect")
	case *types.Var:
		if object.IsField() || o.exposed[object] || seen[object] || len(o.writes[object]) == 0 || (object.Pkg() != nil && object.Parent() == object.Pkg().Scope()) {
			return false
		}
		seen[object] = true
		defer delete(seen, object)
		for _, binding := range o.writes[object] {
			if binding.expr == nil || !o.closed(binding.expr, binding.info, seen) {
				return false
			}
		}
		return true
	}
	return false
}
