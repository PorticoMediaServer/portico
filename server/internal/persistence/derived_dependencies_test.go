package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type derivedSourceDependency struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// A proof is recomputed, not a reviewer-supplied dependency allowlist. The
// first grammar admits string constants, concatenation and single-return free
// builders with closed arguments. Unsupported constructs stay unknown. Whole
// package source is bound so sibling declarations, init code, policy changes
// and membership cannot inherit an old proof. No production pin uses it yet.
type derivedSQLProof struct {
	Kind         string                    `json:"kind"`
	Dependencies []derivedSourceDependency `json:"dependencies"`
	Inventory    string                    `json:"package_inventory_sha256"`
	Schema       string                    `json:"schema_sha256"`
	Toolchain    string                    `json:"toolchain"`
	Callees      []string                  `json:"callees"`
}

type derivedProofScan struct {
	graph    *derivedEffectGraph
	pins     map[string]derivedSQLPin
	problems []string
	report   *derivedClearanceReport
}

func derivedWildcard(pin derivedSQLPin) bool {
	return reflect.DeepEqual(pin.Effects, []derivedSQLEffect{{Table: "*", Verb: "*"}})
}

// Verify the whole file, not one selected statement. This runs in the census's
// existing SQL type view; reflection/callable profile analysis remains shared.
func (scan *derivedProofScan) verify(loader *derivedTypeLoader, file *ast.File, relative string, actual derivedSQLPin) derivedSQLPin {
	pin, present := scan.pins[relative]
	if !present || actual.SHA256 != pin.SHA256 {
		return actual
	}
	if derivedWildcard(pin) && pin.Proof == nil {
		actual.Effects = pin.Effects
		return actual
	}
	proof, effects, err := scan.prove(loader, file, relative, pin)
	if err == nil && (!reflect.DeepEqual(pin.Proof, proof) || !reflect.DeepEqual(pin.Effects, effects)) {
		err = fmt.Errorf("stale/incomplete dependency proof or effect union")
	}
	if err != nil {
		scan.problems = append(scan.problems, relative+": "+err.Error())
		// Invalid proof data can never be used for ownership, even in update mode.
		actual.Effects = []derivedSQLEffect{{Table: "*", Verb: "*"}}
		return actual
	}
	actual.Effects, actual.Proof = effects, proof
	actual.verified = true
	return actual
}

func (scan *derivedProofScan) prove(loader *derivedTypeLoader, file *ast.File, relative string, pin derivedSQLPin) (*derivedSQLProof, []derivedSQLEffect, error) {
	if pin.Proof == nil || scan.graph == nil {
		return nil, nil, fmt.Errorf("narrowed effects require a closed source/schema proof")
	}
	proof := &derivedSQLProof{Kind: pin.Proof.Kind, Schema: scan.graph.schema, Toolchain: runtime.Version(), Callees: []string{}}
	group := filepath.Dir(filepath.FromSlash(relative))
	var members []string
	// Bind the complete local import closure as well as the execution package.
	// A constant imported from another package can change a WHERE policy without
	// changing table/verb effects. Comparing effects alone cannot catch that.
	seen, pending := map[string]bool{}, []string{group}
	for len(pending) != 0 {
		pkg := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		for _, sibling := range loader.groups[pkg] {
			path := loader.fset.Position(sibling.Pos()).Filename
			name, err := filepath.Rel(loader.root, path)
			if err != nil {
				return nil, nil, err
			}
			members = append(members, filepath.ToSlash(name))
			for _, spec := range sibling.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return nil, nil, err
				}
				if strings.HasPrefix(path, loader.module+"/") {
					key := filepath.FromSlash(strings.TrimPrefix(path, loader.module+"/"))
					if len(loader.groups[key]) == 0 {
						return nil, nil, fmt.Errorf("missing local dependency package %s", path)
					}
					pending = append(pending, key)
				}
			}
		}
	}
	// apikit is the other repository workspace module. Bind its full source
	// inventory conservatively; external modules are bound by go.sum/toolchain.
	apikit := filepath.Join(loader.root, "..", "apikit")
	if _, err := os.Stat(filepath.Join(apikit, "go.mod")); err == nil {
		err = filepath.WalkDir(apikit, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				name, err := filepath.Rel(loader.root, path)
				if err != nil {
					return err
				}
				members = append(members, filepath.ToSlash(name))
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	sort.Strings(members)
	inventory := sha256.Sum256([]byte(strings.Join(members, "\n")))
	proof.Inventory = hex.EncodeToString(inventory[:])
	// Type resolution also depends on module/workspace declarations. Bind the
	// full bytes when present; no path from a submitted proof is opened.
	dependencies := append([]string{}, members...)
	for _, name := range []string{"go.mod", "go.sum", "../go.work", "../go.work.sum", "../apikit/go.mod", "../apikit/go.sum", "internal/dbwork/sql_callback_guard_test.go"} {
		if _, err := os.Stat(filepath.Join(loader.root, name)); err == nil {
			dependencies = append(dependencies, name)
		} else if !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
	sort.Strings(dependencies)
	for _, name := range dependencies {
		data, err := os.ReadFile(filepath.Join(loader.root, name))
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(data)
		proof.Dependencies = append(proof.Dependencies, derivedSourceDependency{name, hex.EncodeToString(sum[:])})
	}
	if proof.Kind == "test_fixture" {
		if filepath.ToSlash(group) != "internal/catalogtest" {
			return nil, nil, fmt.Errorf("fixture identity must use the existing catalogtest boundary")
		}
		importers, err := catalogFixtureRuntimeImports(loader.root)
		if err != nil || len(importers) != 0 {
			return nil, nil, fmt.Errorf("fixture runtime-import proof failed: %v %v", importers, err)
		}
		return proof, []derivedSQLEffect{{Table: "*", Verb: "*"}}, nil
	}
	if proof.Kind != "sql" && proof.Kind != "resolved_call" {
		return nil, nil, fmt.Errorf("unsupported proof kind %q", proof.Kind)
	}
	info := loader.forFile(file)
	if info == nil || !derivedUnconditionalSource(loader.fset.Position(file.Pos()).Filename) {
		return nil, nil, fmt.Errorf("proof requires complete unconditional types")
	}
	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err != nil || path == "reflect" || path == "unsafe" {
			return nil, nil, fmt.Errorf("reflect/unsafe import cannot have a finite proof")
		}
	}
	builders := map[types.Object]*ast.FuncDecl{}
	for _, sibling := range loader.groups[group] {
		siblingInfo := loader.forFile(sibling)
		if siblingInfo == nil || !derivedUnconditionalSource(loader.fset.Position(sibling.Pos()).Filename) {
			continue
		}
		for _, decl := range sibling.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				builders[siblingInfo.Defs[fn.Name]] = fn
			}
		}
	}
	var effects []derivedSQLEffect
	callees := map[string]bool{}
	sites := 0
	reflectionMethods, err := derivedReflectionMethodNames(loader.fallback)
	if err != nil {
		return nil, nil, err
	}
	signatures, err := loader.reflectiveSignatures()
	if err != nil {
		return nil, nil, err
	}
	var failure error
	ast.Inspect(file, func(node ast.Node) bool {
		if failure != nil {
			return false
		}
		if expr, ok := node.(ast.Expr); ok {
			if reflective, _ := derivedReflectionSurface(expr, info, reflectionMethods, signatures); reflective {
				failure = fmt.Errorf("reflect-derived selection remains unknown")
				return false
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if info.Types[call.Fun].IsType() {
			return true
		}
		object := derivedFlowObject(call.Fun, info)
		function, declared := object.(*types.Func)
		if !declared {
			if _, builtin := object.(*types.Builtin); !builtin {
				failure = fmt.Errorf("indirect/unresolved callable remains unknown")
			}
			return true
		}
		if function.Pkg() == nil || function.Pkg().Path() == "reflect" {
			failure = fmt.Errorf("unresolved/reflect-derived function remains unknown")
			return false
		}
		identity := function.FullName()
		callees[identity] = true
		index, surface := derivedSQLSurface(call.Fun, info)
		if !surface {
			return true // ordinary direct calls have their own source census.
		}
		sites++
		if index < 0 || index >= len(call.Args) {
			// A resolved non-SQL API is a bound identity, with no invented
			// SELECT effect. Interface/generic methods cannot establish it.
			signature, _ := function.Type().(*types.Signature)
			localOther := strings.HasPrefix(function.Pkg().Path(), loader.module+"/") && function.Pkg().Path() != loader.module+"/"+filepath.ToSlash(group)
			if proof.Kind != "resolved_call" || signature == nil || signature.TypeParams().Len() != 0 || signature.RecvTypeParams().Len() != 0 || localOther || !derivedNonSQLSelection(call.Fun, info) {
				failure = fmt.Errorf("SQL parameter/callee cannot be closed")
			}
			return true
		}
		if proof.Kind != "sql" {
			failure = fmt.Errorf("SQL execution cannot be labelled non-SQL")
			return false
		}
		signature, _ := function.Type().(*types.Signature)
		native := function.Pkg().Path() == "database/sql" && signature != nil && signature.Recv() != nil
		wrapper := function.Pkg().Path() == loader.module+"/internal/dbwork" && signature != nil && signature.Recv() == nil
		if !native && !wrapper {
			failure = fmt.Errorf("unbound executor implementation remains unknown")
			return false
		}
		text, err := derivedBuilderValue(call.Args[index], info, builders, map[types.Object]constant.Value{}, map[types.Object]bool{})
		if err != nil || text.Kind() != constant.String {
			failure = fmt.Errorf("open SQL builder: %v", err)
			return false
		}
		direct, err := scan.graph.direct(constant.StringVal(text))
		if err == nil {
			direct, err = scan.graph.expand(direct)
		}
		if err != nil {
			failure = err
			return false
		}
		effects = append(effects, direct...)
		return true
	})
	if failure != nil {
		return nil, nil, failure
	}
	// Escaped SQL methods/functions, string fragments and indirect execution
	// are conservatively rejected even if another call in the file was proved.
	var escaped bool
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		if expr, ok := node.(ast.Expr); ok {
			if _, surface := derivedSQLSurface(expr, info); surface {
				current := ast.Node(expr)
				for {
					if paren, ok := parents[current].(*ast.ParenExpr); ok {
						current = paren
						continue
					}
					break
				}
				parent, direct := parents[current].(*ast.CallExpr)
				declaration := false
				if selector, ok := parents[current].(*ast.SelectorExpr); ok && selector.Sel == current {
					declaration = true
				}
				if fn, ok := parents[current].(*ast.FuncDecl); ok && fn.Name == current {
					declaration = true
				}
				if !declaration && (!direct || parent.Fun != current) {
					escaped = true
				}
			}
		}
		return true
	})
	if escaped || sites == 0 {
		return nil, nil, fmt.Errorf("escaped SQL/non-SQL method remains unknown")
	}
	// Preserve the census's conservative builder-fragment coverage. One proved
	// SELECT must not clear a different, unbound DML constructor in the file.
	ast.Inspect(file, func(node ast.Node) bool {
		expr, ok := node.(ast.Expr)
		if !ok || failure != nil {
			return failure == nil
		}
		typ := info.TypeOf(expr)
		if typ == nil || typ.Underlying() != types.Typ[types.String] {
			return true
		}
		if parent, ok := parents[node].(ast.Expr); ok {
			if parentType := info.TypeOf(parent); parentType != nil && parentType.Underlying() == types.Typ[types.String] {
				return true
			}
		}
		candidate := false
		ast.Inspect(expr, func(child ast.Node) bool {
			if value, ok := child.(*ast.BasicLit); ok && value.Kind == token.STRING {
				if text, err := strconv.Unquote(value.Value); err == nil {
					targets, unknown := derivedWriteTargets(text)
					candidate = candidate || unknown || len(targets) != 0
				}
			}
			return true
		})
		if !candidate {
			return false
		}
		value, err := derivedBuilderValue(expr, info, builders, map[types.Object]constant.Value{}, map[types.Object]bool{})
		if err != nil || value.Kind() != constant.String {
			failure = fmt.Errorf("unbound SQL fragment remains unknown")
			return false
		}
		direct, err := scan.graph.direct(constant.StringVal(value))
		if err == nil {
			direct, err = scan.graph.expand(direct)
		}
		if err != nil || proof.Kind != "sql" {
			failure = fmt.Errorf("unproved SQL fragment/effect: %v", err)
			return false
		}
		effects = append(effects, direct...)
		return false
	})
	if failure != nil {
		return nil, nil, failure
	}
	for identity := range callees {
		proof.Callees = append(proof.Callees, identity)
	}
	sort.Strings(proof.Callees)
	if proof.Kind == "sql" && len(effects) == 0 {
		effects = []derivedSQLEffect{{Table: "*", Verb: "SELECT"}}
	}
	return proof, derivedSortedEffects(effects), nil
}

func derivedNonSQLSelection(expr ast.Expr, info *types.Info) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	selection := info.Selections[selector]
	if selection == nil {
		return false
	}
	method := selection.Obj()
	if method.Pkg() == nil {
		return false
	}
	// The initial non-SQL grammar admits only a concrete standard-library
	// method over closed data. Local/external implementations may use globals
	// or callbacks: their spelling/signature cannot prove a non-SQL body.
	standard := filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(method.Pkg().Path()))
	if relative, err := filepath.Rel(filepath.Join(runtime.GOROOT(), "src"), standard); err != nil || strings.HasPrefix(relative, "..") {
		return false
	}
	if entry, err := os.Stat(standard); err != nil || !entry.IsDir() {
		return false
	}
	typ := selection.Recv()
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, ok := types.Unalias(typ).(*types.Named)
	if !ok {
		return false
	}
	if _, iface := named.Underlying().(*types.Interface); iface {
		return false
	}
	// No string SQL parameter and no executor-shaped receiver methods. The
	// concrete callee identity is part of the pin; a lookalike cannot inherit it.
	for _, typ := range []types.Type{typ, types.NewPointer(typ)} {
		methods := types.NewMethodSet(typ)
		for i := 0; i < methods.Len(); i++ {
			name := methods.At(i).Obj().Name()
			if name == "Exec" || name == "ExecContext" || name == "Prepare" || name == "PrepareContext" || name == "QueryContext" || name == "QueryRowContext" {
				return false
			}
		}
	}
	return derivedClosedData(selection.Recv(), map[types.Type]bool{})
}

func derivedClosedData(typ types.Type, seen map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if seen[typ] {
		return true
	}
	seen[typ] = true
	switch typ := typ.(type) {
	case *types.Basic:
		return typ.Kind() != types.Invalid && typ.Kind() != types.UnsafePointer && typ.Kind() != types.Uintptr
	case *types.Pointer:
		return derivedClosedData(typ.Elem(), seen)
	case *types.Named:
		if typ.Obj().Pkg() == nil || typ.Obj().Pkg().Path() == "reflect" {
			return false
		}
		return derivedClosedData(typ.Underlying(), seen)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if !derivedClosedData(typ.Field(i).Type(), seen) {
				return false
			}
		}
		return true
	case *types.Map:
		return derivedClosedData(typ.Key(), seen) && derivedClosedData(typ.Elem(), seen)
	case *types.Slice:
		return derivedClosedData(typ.Elem(), seen)
	case *types.Array:
		return derivedClosedData(typ.Elem(), seen)
	}
	return false // interfaces, function slots, channels and type parameters
}

func derivedBuilderValue(expr ast.Expr, info *types.Info, builders map[types.Object]*ast.FuncDecl, inputs map[types.Object]constant.Value, visiting map[types.Object]bool) (constant.Value, error) {
	if value := info.Types[expr].Value; value != nil {
		return value, nil
	}
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return derivedBuilderValue(expr.X, info, builders, inputs, visiting)
	case *ast.Ident:
		if value := inputs[info.ObjectOf(expr)]; value != nil {
			return value, nil
		}
	case *ast.BinaryExpr:
		left, err := derivedBuilderValue(expr.X, info, builders, inputs, visiting)
		if err != nil {
			return nil, err
		}
		right, err := derivedBuilderValue(expr.Y, info, builders, inputs, visiting)
		if err == nil && expr.Op == token.ADD && left.Kind() == constant.String && right.Kind() == constant.String {
			return constant.BinaryOp(left, token.ADD, right), nil
		}
	case *ast.CallExpr:
		object := derivedFlowObject(expr.Fun, info)
		fn := builders[object]
		if fn == nil || visiting[object] || fn.Recv != nil || fn.Body == nil || len(fn.Body.List) != 1 {
			break
		}
		result, ok := fn.Body.List[0].(*ast.ReturnStmt)
		signature, okSig := object.Type().(*types.Signature)
		if !ok || len(result.Results) != 1 || !okSig || signature.Variadic() || signature.TypeParams().Len() != 0 || signature.Params().Len() != 0 || len(expr.Args) != 0 {
			break
		}
		arguments := map[types.Object]constant.Value{}
		for i, arg := range expr.Args {
			value, err := derivedBuilderValue(arg, info, builders, inputs, visiting)
			if err != nil {
				return nil, err
			}
			arguments[signature.Params().At(i)] = value
		}
		visiting[object] = true
		value, err := derivedBuilderValue(result.Results[0], info, builders, arguments, visiting)
		delete(visiting, object)
		return value, err
	}
	return nil, fmt.Errorf("unsupported, mutable or unbound expression")
}
