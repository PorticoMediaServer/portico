package sqliteconn

import (
	"go/ast"
	"go/token"
	"go/types"
)

func openingOwner(file *ast.File, info *types.Info, pos token.Pos) string {
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && pos >= f.Pos() && pos <= f.End() {
			return openingObject(info.Defs[f.Name])
		}
	}
	return ""
}

// This is a lexical, type-resolved rule, not data-flow analysis: local Driver
// and Raw interfaces are forbidden, as is putting an actual SQL DB/Conn into
// an empty interface or one exposing Driver/Raw, regardless of its package.
func interfaceOpeningSites(file string, syntax *ast.File, info *types.Info) []openingSite {
	var sites []openingSite
	add := func(pos token.Pos, object string) {
		sites = append(sites, openingSite{file, openingOwner(syntax, info, pos), object})
	}
	transfer := func(pos token.Pos, from, to types.Type) {
		if from == nil || to == nil {
			return
		}
		p, ok := types.Unalias(from).(*types.Pointer)
		if !ok {
			return
		}
		n, ok := types.Unalias(p.Elem()).(*types.Named)
		if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "database/sql" || (n.Obj().Name() != "DB" && n.Obj().Name() != "Conn") {
			return
		}
		to = types.Unalias(to)
		iface, ok := to.Underlying().(*types.Interface)
		if !ok || !types.AssignableTo(from, to) {
			return
		}
		iface.Complete()
		risky := iface.NumMethods() == 0
		for i := 0; i < iface.NumMethods(); i++ {
			risky = risky || iface.Method(i).Name() == "Driver" || iface.Method(i).Name() == "Raw"
		}
		if !risky {
			return
		}
		add(pos, "interface transfer to "+types.TypeString(to, func(p *types.Package) string { return p.Path() }))
	}
	values := func(pos token.Pos, rhs []ast.Expr, targets []types.Type) {
		if len(rhs) == 1 {
			if tuple, ok := info.TypeOf(rhs[0]).(*types.Tuple); ok {
				for i, to := range targets {
					if i < tuple.Len() {
						transfer(pos, tuple.At(i).Type(), to)
					}
				}
				return
			}
		}
		for i, from := range rhs {
			if i < len(targets) {
				transfer(pos, info.TypeOf(from), targets[i])
			}
		}
	}
	var stack []ast.Node
	ast.Inspect(syntax, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		switch x := node.(type) {
		case *ast.InterfaceType:
			if typ := info.TypeOf(x); typ != nil {
				iface := typ.Underlying().(*types.Interface).Complete()
				for i := 0; i < iface.NumMethods(); i++ {
					name := iface.Method(i).Name()
					if name == "Driver" || name == "Raw" {
						add(x.Pos(), "interface declaration "+name)
					}
				}
			}
		case *ast.AssignStmt:
			var targets []types.Type
			for _, lhs := range x.Lhs {
				targets = append(targets, info.TypeOf(lhs))
			}
			values(x.Pos(), x.Rhs, targets)
		case *ast.ValueSpec:
			var targets []types.Type
			for _, name := range x.Names {
				if obj := info.Defs[name]; obj != nil {
					targets = append(targets, obj.Type())
				}
			}
			values(x.Pos(), x.Values, targets)
		case *ast.CallExpr:
			if info.Types[x.Fun].IsType() {
				if len(x.Args) == 1 {
					transfer(x.Pos(), info.TypeOf(x.Args[0]), info.TypeOf(x.Fun))
				}
				break
			}
			if sig, ok := info.TypeOf(x.Fun).(*types.Signature); ok {
				for i, arg := range x.Args {
					j := i
					if sig.Variadic() && j >= sig.Params().Len()-1 {
						j = sig.Params().Len() - 1
					}
					if j >= sig.Params().Len() {
						continue
					}
					to := sig.Params().At(j).Type()
					if sig.Variadic() && j == sig.Params().Len()-1 && !x.Ellipsis.IsValid() {
						to = to.(*types.Slice).Elem()
					}
					transfer(arg.Pos(), info.TypeOf(arg), to)
				}
			}
		case *ast.ReturnStmt:
			for i := len(stack) - 2; i >= 0; i-- {
				var typ types.Type
				switch f := stack[i].(type) {
				case *ast.FuncDecl:
					typ = info.Defs[f.Name].Type()
				case *ast.FuncLit:
					typ = info.TypeOf(f)
				}
				if sig, ok := typ.(*types.Signature); ok {
					var targets []types.Type
					for j := 0; j < sig.Results().Len(); j++ {
						targets = append(targets, sig.Results().At(j).Type())
					}
					values(x.Pos(), x.Results, targets)
					break
				}
			}
		case *ast.CompositeLit:
			for i, element := range x.Elts {
				value := element.(ast.Expr)
				key, keyed := value.(*ast.KeyValueExpr)
				if keyed {
					value = key.Value
				}
				var to types.Type
				switch typ := info.TypeOf(x).Underlying().(type) {
				case *types.Array:
					to = typ.Elem()
				case *types.Slice:
					to = typ.Elem()
				case *types.Map:
					to = typ.Elem()
					transfer(key.Pos(), info.TypeOf(key.Key), typ.Key())
				case *types.Struct:
					if keyed {
						for j := 0; j < typ.NumFields(); j++ {
							if typ.Field(j).Name() == key.Key.(*ast.Ident).Name {
								to = typ.Field(j).Type()
							}
						}
					} else if i < typ.NumFields() {
						to = typ.Field(i).Type()
					}
				}
				transfer(value.Pos(), info.TypeOf(value), to)
			}
		case *ast.SendStmt:
			if ch, ok := info.TypeOf(x.Chan).Underlying().(*types.Chan); ok {
				transfer(x.Pos(), info.TypeOf(x.Value), ch.Elem())
			}
		}
		return true
	})
	return sites
}
