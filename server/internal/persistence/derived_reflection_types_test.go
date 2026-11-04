package persistence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SQL's original census deliberately uses only unconditional declarations.
// Reflection needs complete method identities instead: type-check each build
// family, with real module-aware dependency export data. No unmatched source
// is silently omitted; a new build constraint must have a covered profile.
func derivedReflectionLoaders(base *derivedTypeLoader, names map[string]bool) ([]*derivedTypeLoader, error) {
	if _, err := os.Stat(filepath.Join(base.root, "go.mod")); os.IsNotExist(err) {
		// Standalone synthetic packages have no module/dependency graph.
		return []*derivedTypeLoader{base}, nil
	}
	needed := map[*ast.File]bool{}
	for _, group := range base.groups {
		for _, file := range group {
			importsReflect := false
			for _, spec := range file.Imports {
				path, _ := strconv.Unquote(spec.Path.Value)
				importsReflect = importsReflect || path == "reflect"
			}
			if importsReflect {
				continue // the explicit import already requires an unknown pin
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok && names[selector.Sel.Name] {
					needed[file] = true
				}
				return true
			})
		}
	}
	var loaders []*derivedTypeLoader
	for _, target := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"freebsd", "amd64"}} {
		for _, tags := range [][]string{nil, {"devtrust", "portico_cb7", "portico_rar7z", "race"}, {"release", "portico_cb7", "portico_rar7z", "race"}} {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = target[0], target[1], false
			ctx.BuildTags = tags
			loader := newDerivedTypeLoader(base.root, base.fset, base.groups)
			loader.context = &ctx
			loader.reflectionNeeded, loader.reflectionNames = needed, names
			loaders = append(loaders, loader)
		}
	}
	// The Darwin watcher imports C, but its Go method identities are checked
	// without linking a foreign framework. FakeImportC preserves C boundaries;
	// actual Go dependency declarations/exports still supply their own methods.
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = "darwin", "arm64", true
	loader := newDerivedTypeLoader(base.root, base.fset, base.groups)
	loader.context = &ctx
	loader.reflectionNeeded, loader.reflectionNames = needed, names
	loaders = append(loaders, loader)
	for _, group := range base.groups {
		for _, file := range group {
			covered := false
			for _, loader := range loaders {
				match, err := loader.matches(file)
				if err != nil {
					return nil, err
				}
				covered = covered || match
			}
			if !covered {
				return nil, fmt.Errorf("no reflection type-check build covers %s", base.fset.Position(file.Pos()).Filename)
			}
		}
	}
	return loaders, nil
}

func (l *derivedTypeLoader) matches(file *ast.File) (bool, error) {
	if l.context == nil {
		return derivedUnconditionalSource(l.fset.Position(file.Pos()).Filename), nil
	}
	if l.active == nil {
		l.active = map[*ast.File]bool{}
	}
	if match, ok := l.active[file]; ok {
		return match, nil
	}
	path := l.fset.Position(file.Pos()).Filename
	match, err := l.context.MatchFile(filepath.Dir(path), filepath.Base(path))
	l.active[file] = match
	return match, err
}

type derivedExportPackage struct {
	ImportPath, Dir, Export string
	GoFiles, CgoFiles       []string
	Error                   *struct{ Err string }
}

// Only dependency packages are exported, not the application binaries. The Go
// command supplies module/workspace resolution, build tags and cache validity;
// one bounded compiler process at a time avoids a nested parallel build.
func (l *derivedTypeLoader) prepareExports() error {
	if l.exports != nil {
		return l.exportErr
	}
	l.exports = map[string]derivedExportPackage{}
	imports := map[string]bool{"reflect": true}
	for _, group := range l.groups {
		for _, file := range group {
			match, err := l.matches(file)
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				key := strings.TrimPrefix(path, l.module+"/")
				if path == "C" || ((path == l.module || key != path) && len(l.groups[filepath.FromSlash(key)]) > 0) {
					continue
				}
				imports[path] = true
			}
		}
	}
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	args := []string{"list", "-e", "-deps", "-export", "-p=1", "-json=ImportPath,Dir,Export,GoFiles,CgoFiles,Error"}
	if len(l.context.BuildTags) != 0 {
		args = append(args, "-tags="+strings.Join(l.context.BuildTags, ","))
	}
	cmd := exec.Command("go", append(args, paths...)...)
	cmd.Dir = l.root
	// Cross-platform dependency exports do not link native C code. A source
	// fallback below reads real Go declarations for packages without exports.
	cmd.Env = append(os.Environ(), "GOOS="+l.context.GOOS, "GOARCH="+l.context.GOARCH, "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		l.exportErr = fmt.Errorf("dependency types for %s/%s %v: %w: %s", l.context.GOOS, l.context.GOARCH, l.context.BuildTags, err, strings.TrimSpace(stderr.String()))
		return l.exportErr
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg derivedExportPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			l.exportErr = err
			return err
		}
		l.exports[pkg.ImportPath] = pkg
	}
	l.fallback = importer.ForCompiler(l.fset, "gc", func(path string) (io.ReadCloser, error) {
		if pkg, ok := l.exports[path]; ok && pkg.Export != "" {
			return os.Open(pkg.Export)
		}
		return nil, fmt.Errorf("no dependency export for %s", path)
	})
	return nil
}

func (l *derivedTypeLoader) importDependency(path string) (*types.Package, error) {
	if err := l.prepareExports(); err != nil {
		return nil, err
	}
	if pkg, ok := l.exports[path]; ok && pkg.Export == "" && len(pkg.GoFiles) != 0 {
		// Cgo/foreign exports may be unavailable on this host. Read the actual
		// selected Go declarations; never invent a method signature. Any later
		// selection still unresolved is a non-waivable census failure.
		if checked := l.packages[path]; checked != nil {
			return checked, nil
		}
		var files []*ast.File
		for _, name := range pkg.GoFiles {
			file, err := parser.ParseFile(l.fset, filepath.Join(pkg.Dir, name), nil, 0)
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
		conf := types.Config{Importer: l, IgnoreFuncBodies: true, FakeImportC: true, Sizes: types.SizesFor("gc", l.context.GOARCH), Error: func(error) {}}
		checked, err := conf.Check(path, l.fset, files, nil)
		if checked != nil {
			l.packages[path] = checked
			return checked, nil
		}
		return nil, err
	}
	return l.fallback.Import(path)
}
