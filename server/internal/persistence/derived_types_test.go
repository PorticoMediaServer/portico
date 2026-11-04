package persistence

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Type-check source already collected by the census, using the real local
// package declarations and standard-library export data. We do not build or
// download dependencies. Missing imports, unsupported build variants and
// incomplete signatures remain unresolved and therefore unknown writers.
type derivedTypeLoader struct {
	root, module         string
	fset                 *token.FileSet
	groups               map[string][]*ast.File
	fallback             types.Importer
	packages             map[string]*types.Package
	infos                map[*ast.File]*types.Info
	checking             map[string]bool
	context              *build.Context
	active               map[*ast.File]bool
	exports              map[string]derivedExportPackage
	exportErr            error
	reflectionNeeded     map[*ast.File]bool
	reflectionNames      map[string]bool
	reflectionSignatures map[string][]*types.Signature
}

func newDerivedTypeLoader(root string, fset *token.FileSet, groups map[string][]*ast.File) *derivedTypeLoader {
	module := "github.com/PorticoMediaServer/portico/server"
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if match := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(data); len(match) > 0 {
			module = string(match[1])
		}
	}
	return &derivedTypeLoader{root: root, module: module, fset: fset, groups: groups, fallback: importer.Default(), packages: map[string]*types.Package{}, infos: map[*ast.File]*types.Info{}, checking: map[string]bool{}}
}
func (l *derivedTypeLoader) Import(path string) (*types.Package, error) {
	key := path
	if path == l.module {
		key = "."
	} else if strings.HasPrefix(path, l.module+"/") {
		key = strings.TrimPrefix(path, l.module+"/")
	}
	if _, ok := l.groups[filepath.FromSlash(key)]; !ok || (l.context != nil && path != l.module && !strings.HasPrefix(path, l.module+"/")) {
		if l.context != nil {
			return l.importDependency(path)
		}
		return l.fallback.Import(path)
	}
	return l.check(filepath.FromSlash(key), path)
}
func (l *derivedTypeLoader) check(key, path string) (*types.Package, error) {
	if pkg := l.packages[key]; pkg != nil {
		return pkg, nil
	}
	if l.checking[key] {
		return nil, fmt.Errorf("unresolved census import cycle: %s", path)
	}
	l.checking[key] = true
	defer delete(l.checking, key)
	var active []*ast.File
	for _, file := range l.groups[key] {
		match, err := l.matches(file)
		if err != nil {
			return nil, err
		}
		if match {
			checked := file
			if l.context != nil && !l.reflectionNeeded[file] {
				// Only declarations are needed from files with no unresolved
				// reflection candidates (or an already-unknown reflect import).
				// Function signatures remain the actual source declarations.
				copy := *file
				copy.Decls = append([]ast.Decl(nil), file.Decls...)
				for i, decl := range copy.Decls {
					if function, ok := decl.(*ast.FuncDecl); ok {
						functionCopy := *function
						functionCopy.Body = nil
						copy.Decls[i] = &functionCopy
					}
				}
				checked = &copy
			}
			active = append(active, checked)
		}
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("no census source for active build: %s", path)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	if l.context != nil {
		info.Defs = nil
	}
	conf := types.Config{Importer: l, DisableUnusedImportCheck: true, FakeImportC: true, Error: func(error) {}}
	if l.context != nil {
		conf.Sizes = types.SizesFor("gc", l.context.GOARCH)
	}
	pkg, err := conf.Check(path, l.fset, active, info)
	if pkg == nil {
		return nil, err
	}
	// Individual expressions are used only if their actual types/signatures
	// resolve completely. An unrelated body/import error grants no fallback to
	// guessed argument positions, and does not erase valid local declarations.
	l.packages[key] = pkg
	if l.context != nil {
		for node, value := range info.Types {
			if !value.IsType() {
				delete(info.Types, node)
			}
		}
		for node, object := range info.Uses {
			if !l.reflectionNames[node.Name] && (object.Pkg() == nil || object.Pkg().Path() != "reflect") {
				delete(info.Uses, node)
			}
		}
		for node, selection := range info.Selections {
			if !l.reflectionNames[node.Sel.Name] && (selection.Obj().Pkg() == nil || selection.Obj().Pkg().Path() != "reflect") {
				delete(info.Selections, node)
			}
		}
	}
	for _, file := range l.groups[key] {
		if match, _ := l.matches(file); match {
			l.infos[file] = info
		}
	}
	return pkg, nil
}

// Only declarations present across the supported OS/architecture superset are
// trusted for type resolution. Platform/tag-specific code remains unknown on
// every host, rather than changing the checked-in census with the runner OS.
func derivedUnconditionalSource(path string) bool {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			ctx := build.Default
			ctx.GOOS = goos
			ctx.GOARCH = goarch
			match, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil || !match {
				return false
			}
		}
	}
	return true
}

func (l *derivedTypeLoader) forFile(file *ast.File) *types.Info {
	if info := l.infos[file]; info != nil {
		return info
	}
	path := l.fset.Position(file.Pos()).Filename
	relative, err := filepath.Rel(l.root, filepath.Dir(path))
	if err != nil {
		return nil
	}
	name := l.module
	if relative != "." {
		name += "/" + filepath.ToSlash(relative)
	}
	_, _ = l.check(relative, name)
	return l.infos[file]
}

// The signature, not the spelling of the receiver/context, identifies SQL.
// There must be exactly one non-variadic built-in string parameter. A named
// string implementing context.Context is not that parameter. Ambiguous or
// invalid signatures remain unknown. Selection.Obj describes the method with
// its receiver separate; a MethodExpr adds that receiver as argument zero.
func derivedSQLParameter(expr ast.Expr, info *types.Info) int {
	if info == nil {
		return -1
	}
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = p.X
	}
	signature, _ := info.TypeOf(expr).(*types.Signature)
	offset := 0
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if selected := info.Selections[selector]; selected != nil && (selected.Kind() == types.MethodExpr || selected.Kind() == types.MethodVal) {
			signature, _ = selected.Obj().Type().(*types.Signature)
			if selected.Kind() == types.MethodExpr {
				offset = 1
			}
		}
	}
	if signature == nil || signature.TypeParams().Len() != 0 || signature.RecvTypeParams().Len() != 0 {
		return -1
	}
	found := -1
	for i := 0; i < signature.Params().Len(); i++ {
		param := signature.Params().At(i).Type()
		if param == nil || param.Underlying() == types.Typ[types.Invalid] {
			return -1
		}
		if basic, ok := param.Underlying().(*types.Basic); ok && basic.Kind() == types.String && !types.Identical(param, types.Typ[types.String]) {
			return -1
		}
		if types.Identical(param, types.Typ[types.String]) {
			if found >= 0 {
				return -1
			}
			found = i + offset
		}
	}
	return found
}

// Enumerate the reflective API only for fail-closed diagnostics when a
// selection cannot be resolved. This list never classifies a resolved method.
func derivedReflectionMethodNames(importer types.Importer) (map[string]bool, error) {
	pkg, err := importer.Import("reflect")
	if err != nil {
		return nil, fmt.Errorf("resolve reflection API: %w", err)
	}
	names := map[string]bool{}
	for _, name := range pkg.Scope().Names() {
		obj, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || !obj.Exported() {
			continue
		}
		for _, typ := range []types.Type{obj.Type(), types.NewPointer(obj.Type())} {
			methods := types.NewMethodSet(typ)
			for i := 0; i < methods.Len(); i++ {
				if method := methods.At(i).Obj(); method.Exported() {
					names[method.Name()] = true
				}
			}
		}
	}
	return names, nil
}

func derivedReflectionSurface(expr ast.Expr, info *types.Info, names map[string]bool, signatures map[string][]*types.Signature) (reflective, unresolved bool) {
	var object types.Object
	switch expr := expr.(type) {
	case *ast.SelectorExpr:
		if info != nil {
			if selection := info.Selections[expr]; selection != nil {
				if selection.Kind() == types.MethodVal || selection.Kind() == types.MethodExpr {
					return derivedReflectiveMethod(selection.Obj(), selection.Recv(), signatures), false
				}
				return false, false // resolved field, not a reflective method
			}
			object = info.Uses[expr.Sel]
		}
		if object == nil {
			// Cgo operands are intentionally opaque to go/types. A conversion
			// still has an explicitly resolved target type; use its real method
			// declaration rather than guessing from the selector's name.
			if call, ok := expr.X.(*ast.CallExpr); ok && len(call.Args) == 1 && info != nil {
				if target := info.Types[call.Fun]; target.IsType() {
					if method, _, _ := types.LookupFieldOrMethod(target.Type, true, nil, expr.Sel.Name); method != nil {
						return derivedReflectiveMethod(method, target.Type, signatures), false
					}
				}
			}
			return names[expr.Sel.Name], names[expr.Sel.Name]
		}
	case *ast.Ident:
		if info != nil {
			object = info.Uses[expr]
		}
	}
	if function, ok := object.(*types.Func); ok {
		pkg := function.Pkg()
		return pkg != nil && pkg.Path() == "reflect", false
	}
	return false, false
}

// An interface can dispatch a promoted reflect method even when its own method
// declaration belongs to the application's package. Compare actual method
// signatures against the reflection value containers; this is not a name
// fallback and does not classify ordinary Error() or Close() error methods.
func derivedReflectiveMethod(object types.Object, receiver types.Type, signatures map[string][]*types.Signature) bool {
	method, ok := object.(*types.Func)
	if !ok {
		return false
	}
	if pkg := method.Pkg(); pkg != nil && pkg.Path() == "reflect" {
		return true
	}
	signature, _ := method.Type().(*types.Signature)
	if signature == nil {
		return false
	}
	if signature.Recv() != nil {
		receiver = signature.Recv().Type()
	}
	if receiver == nil {
		return false
	}
	if _, ok := receiver.Underlying().(*types.Interface); !ok {
		return false
	}
	for _, candidate := range signatures[method.Name()] {
		if types.Identical(signature, candidate) {
			return true
		}
	}
	return false
}

func (l *derivedTypeLoader) reflectiveSignatures() (map[string][]*types.Signature, error) {
	if l.reflectionSignatures != nil {
		return l.reflectionSignatures, nil
	}
	if l.context != nil {
		if err := l.prepareExports(); err != nil {
			return nil, err
		}
	}
	pkg, err := l.fallback.Import("reflect")
	if err != nil {
		return nil, err
	}
	l.reflectionSignatures = map[string][]*types.Signature{}
	for _, name := range []string{"Value", "Type", "MapIter"} {
		object := pkg.Scope().Lookup(name)
		if object == nil {
			return nil, fmt.Errorf("missing reflective container %s", name)
		}
		typ := object.Type()
		if name == "MapIter" {
			typ = types.NewPointer(typ)
		}
		methods := types.NewMethodSet(typ)
		for i := 0; i < methods.Len(); i++ {
			method := methods.At(i).Obj()
			if signature, ok := method.Type().(*types.Signature); ok && derivedReflectionEscape(method.Name()) {
				l.reflectionSignatures[method.Name()] = append(l.reflectionSignatures[method.Name()], signature)
			}
		}
	}
	return l.reflectionSignatures, nil
}

// Structural-interface dispatch needs the execution/escape signatures even
// when its declaration is outside reflect. Other resolved ordinary methods
// stay ordinary; direct reflect selections are covered by their package above.
func derivedReflectionEscape(name string) bool {
	switch name {
	case "Call", "CallSlice", "Method", "MethodByName", "Interface":
		return true
	}
	return false
}
