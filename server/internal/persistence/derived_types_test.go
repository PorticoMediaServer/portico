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
	root, module string
	fset         *token.FileSet
	groups       map[string][]*ast.File
	fallback     types.Importer
	packages     map[string]*types.Package
	infos        map[*ast.File]*types.Info
	checking     map[string]bool
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
	if _, ok := l.groups[filepath.FromSlash(key)]; !ok {
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
		name := l.fset.Position(file.Pos()).Filename
		if derivedUnconditionalSource(name) {
			active = append(active, file)
		}
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("no census source for active build: %s", path)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: l, DisableUnusedImportCheck: true, Error: func(error) {}}
	pkg, err := conf.Check(path, l.fset, active, info)
	if pkg == nil {
		return nil, err
	}
	// Individual expressions are used only if their actual types/signatures
	// resolve completely. An unrelated body/import error grants no fallback to
	// guessed argument positions, and does not erase valid local declarations.
	l.packages[key] = pkg
	for _, file := range active {
		l.infos[file] = info
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
