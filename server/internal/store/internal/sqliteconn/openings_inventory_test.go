package sqliteconn

import (
	"errors"
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// These are the ONLY reviewed constrained store sources. All bodies must be
// type-resolved: host, Linux/non-Unix fixture variants, and GV's tagged audits.
var openingConstraints = map[string]string{
	"writer/crash_scan_issuer_preparation_linux_test.go": "", // implicit _linux suffix; actual issuer original input checkpoint
	"writer/crash_scan_issuer_oracle_linux_test.go":      "", // implicit _linux suffix; independent scan journal/digest model
	"writer/crash_scan_issuer_linux_test.go":             "", // implicit _linux suffix; real issuer SIGKILL/reopen controls

	"qual/scanbench/fixture_dir_unix.go":         "//go:build linux || darwin",
	"qual/scanbench/fixture_dir_other.go":        "//go:build !linux && !darwin",
	"qual/scanbench/fixture_dir_test.go":         "//go:build linux || darwin",
	"qual/namespace_linux.go":                    "",                 // implicit _linux filename constraint
	"writer/crash_passive_linux_test.go":         "",                 // implicit _linux suffix; native PASSIVE SIGKILL controls are Linux-only
	"writer/crash_oracle_linux_test.go":          "",                 // implicit _linux suffix; SIGKILL/native reopen harness is Linux-only
	"writer/crash_effective_steps_linux_test.go": "",                 // implicit _linux suffix; native-step SIGKILL/reopen controls are Linux-only
	"writer/crash_replay_linux_test.go":          "",                 // implicit _linux suffix; SIGKILL/native reopen harness is Linux-only
	"writer/crash_inventory_fresh_linux_test.go": "",                 // implicit _linux suffix; fresh SIGKILL component uses the Linux harness
	"writer/crash_scan_n1_driver_linux_test.go":  "",                 // implicit _linux suffix; merged N1 public-path SIGKILL/reopen controls
	"writer/race_on_test.go":                     "//go:build race",  // crash-child deadline scaled for the detector
	"writer/race_off_test.go":                    "//go:build !race", // established crash-child deadline
	"qual/namespace_other.go":                    "//go:build !linux",
	"governor/snapshot_binding_audit_test.go":    "//go:build libc.memgrind",
	"governor/snapshot_binding_counters_test.go": "//go:build memory.counters && !libc.memgrind",

	"internal/sqliteconn/ownership_lifetime_linux_test.go": "", // implicit _linux suffix; real SIGKILL/native lifetime witnesses are Linux-only

	"writer/crash_inventory_history_linux_test.go": "", // implicit _linux suffix; history SIGKILL/reopen controls are Linux-only
	"writer/crash_reclaim_linux_test.go":           "", // implicit _linux suffix; native cleanup SIGKILL/reopen witnesses
	"writer/scan_opening_falsifier_linux_test.go":  "", // implicit _linux suffix; native file replacement/SIGKILL witnesses are Linux-only
	"writer/crash_progress_linux_test.go":          "", // implicit _linux suffix; actual SIGKILL/reopen component witnesses are Linux-only
	"writer/crash_progress_oracle_linux_test.go":   "", // implicit _linux suffix; shares the native progress witness model
	"writer/crash_security_linux_test.go":          "", // implicit _linux suffix; native FULL SIGKILL/reopen witnesses
}

func openingPhysicalSources(root string) (map[string]bool, error) {
	sources := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("store source symlink: %s", path)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		var constraints []string
		for _, group := range f.Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//line ") || strings.HasPrefix(c.Text, "/*line ") {
					return fmt.Errorf("store source line directive: %s", rel)
				}
				if strings.HasPrefix(c.Text, "//go:build ") || strings.HasPrefix(c.Text, "// +build ") {
					constraints = append(constraints, c.Text)
				}
			}
		}
		// Neutral OS/arch rejects every filename suffix that this Go toolchain
		// recognizes, without maintaining a second copy of Go's suffix grammar.
		neutral := build.Default
		neutral.GOOS = ""
		neutral.GOARCH = ""
		unconstrained, err := neutral.MatchFile(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return err
		}
		expected, reviewed := openingConstraints[rel]
		if (!unconstrained || len(constraints) > 0) && !reviewed {
			return fmt.Errorf("unreviewed store build constraint: %s", rel)
		}
		if reviewed && strings.Join(constraints, "\n") != expected {
			return fmt.Errorf("changed reviewed store build constraint: %s", rel)
		}
		// The three explicitly reviewed fixture bodies are filesystem-only. An
		// alias/blank/dot import cannot hide SQL or a wrapper behind their tags.
		switch rel {
		case "qual/scanbench/fixture_dir_unix.go", "qual/scanbench/fixture_dir_other.go", "qual/scanbench/fixture_dir_test.go":
			for _, imp := range f.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				switch name {
				case "errors", "os", "path/filepath", "time", "testing", "golang.org/x/sys/unix":
				default:
					return fmt.Errorf("store fixture imports unreviewed dependency: %s: %s", rel, name)
				}
			}
		}
		sources[filepath.Clean(path)] = true
		return nil
	})
	return sources, err
}

func syntheticOpeningTestMain(p *packages.Package) bool {
	// go/packages leaves ForTest empty on synthetic main in this pinned version.
	// Its test-only internal import and single compiler-generated file identify
	// it; a directory/package merely named *.test does not qualify.
	return p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test") &&
		p.Imports["testing/internal/testdeps"] != nil && len(p.CompiledGoFiles) == 1
}

func validateOpeningLoads(pkgs []*packages.Package) error {
	var found error
	seen := map[*packages.Package]bool{}
	var visit func(*packages.Package)
	visit = func(p *packages.Package) {
		if p == nil {
			found = errors.Join(found, errors.New("nil loaded dependency"))
			return
		}
		if seen[p] {
			return
		}
		seen[p] = true
		if p.IllTyped {
			found = errors.Join(found, fmt.Errorf("ill-typed package: %s", p.ID))
		}
		for _, e := range p.Errors {
			found = errors.Join(found, fmt.Errorf("%s: %s", p.ID, e))
		}
		for _, e := range p.TypeErrors {
			found = errors.Join(found, fmt.Errorf("%s: %s", p.ID, e))
		}
		for _, dep := range p.Imports {
			visit(dep)
		}
	}
	for _, p := range pkgs {
		visit(p)
	}
	return found
}

func openingCoverage(sources, metadata, typed map[string]bool) error {
	var err error
	for file := range sources {
		if !metadata[file] {
			err = errors.Join(err, fmt.Errorf("physical store source omitted from compiled/ignored metadata: %s", file))
		}
		if !typed[file] {
			err = errors.Join(err, fmt.Errorf("physical store source not type-resolved: %s", file))
		}
	}
	return err
}

func openingEnv(goos string) []string {
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GOOS=") && !strings.HasPrefix(v, "GOARCH=") && !strings.HasPrefix(v, "CGO_ENABLED=") {
			env = append(env, v)
		}
	}
	arch := runtime.GOARCH
	if goos != runtime.GOOS {
		arch = "amd64" // supported representative target, never host arm64+Windows by accident
	}
	return append(env, "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0")
}

func openingOtherPlatforms(host string) []string {
	if host == "linux" {
		return []string{"windows"}
	}
	if host == "darwin" {
		// Linux and Darwin BOTH exclude !linux && !darwin bodies. Metadata
		// listing does not replace the Windows profile's real type resolution.
		return []string{"linux", "windows"}
	}
	return []string{"linux"}
}

func loadOpeningSourceUniverse(root string) ([]*packages.Package, error) {
	sources, err := openingPhysicalSources(root)
	if err != nil {
		return nil, err
	}
	metadata, typed := map[string]bool{}, map[string]bool{}
	var all []*packages.Package
	profiles := []struct {
		env     []string
		flags   []string
		pattern string
	}{
		{os.Environ(), []string{"-p=1"}, "./internal/store/..."},
		// Listing the audit as ignored is insufficient: load its actual body and
		// dependencies with the exact reviewed tag, then scan the same references.
		{os.Environ(), []string{"-p=1", "-tags=libc.memgrind"}, "./internal/store/governor"},
		{os.Environ(), []string{"-p=1", "-tags=memory.counters"}, "./internal/store/governor"},
	}
	for _, target := range openingOtherPlatforms(runtime.GOOS) {
		profiles = append(profiles, struct {
			env     []string
			flags   []string
			pattern string
		}{openingEnv(target), []string{"-p=1"}, "./internal/store/..."})
	}
	for _, profile := range profiles {
		pkgs, err := packages.Load(&packages.Config{Dir: filepath.Join(root, "../.."), Tests: true, Env: profile.env, BuildFlags: profile.flags,
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedForTest | packages.NeedImports | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		}, profile.pattern)
		if err != nil {
			return nil, err
		}
		if err := validateOpeningLoads(pkgs); err != nil {
			return nil, err
		}
		for _, p := range pkgs {
			if syntheticOpeningTestMain(p) {
				continue
			}
			if p.Fset == nil || p.Types == nil || p.TypesInfo == nil {
				return nil, fmt.Errorf("missing store type information: %s", p.ID)
			}
			for _, files := range [][]string{p.CompiledGoFiles, p.IgnoredFiles} {
				for _, file := range files {
					if sources[filepath.Clean(file)] {
						metadata[filepath.Clean(file)] = true
					}
				}
			}
			for _, f := range p.Syntax {
				file := filepath.Clean(p.Fset.PositionFor(f.Pos(), false).Filename)
				if !sources[file] {
					return nil, fmt.Errorf("unmapped physical store syntax: %s (%s)", file, p.ID)
				}
				typed[file] = true
			}
		}
		// Dependency errors were checked above. Their AST/type-use maps are not
		// store inputs to scan; release those before loading the next profile.
		packages.Visit(pkgs, nil, func(p *packages.Package) {
			if !strings.HasPrefix(p.PkgPath, openingStore) {
				p.Syntax = nil
				p.TypesInfo = nil
			}
		})
		all = append(all, pkgs...)
	}
	if err := openingCoverage(sources, metadata, typed); err != nil {
		return nil, err
	}
	return all, nil
}

func TestStoreOpeningsPlatformProfilesCoverFixtureBodies(t *testing.T) {
	dir := filepath.Join("..", "..", "qual", "scanbench")
	for _, host := range []string{"linux", "darwin", "windows", "freebsd"} {
		t.Run(host, func(t *testing.T) {
			for _, file := range []string{"fixture_dir_unix.go", "fixture_dir_other.go", "fixture_dir_test.go"} {
				covered := false
				for _, target := range append([]string{host}, openingOtherPlatforms(host)...) {
					c := build.Default
					c.GOOS, c.GOARCH, c.CgoEnabled = target, "amd64", false
					match, err := c.MatchFile(dir, file)
					if err != nil {
						t.Fatal(err)
					}
					covered = covered || match
				}
				if !covered {
					t.Fatalf("%s host leaves %s untyped", host, file)
				}
			}
		})
	}
}

func TestStoreOpeningsForeignProfilePinsSupportedArchitecture(t *testing.T) {
	t.Setenv("GOOS", "plan9")
	t.Setenv("GOARCH", "not-a-supported-target")
	t.Setenv("CGO_ENABLED", "1")
	target := "windows"
	if runtime.GOOS == target {
		target = "linux"
	}
	counts := map[string]int{}
	for _, value := range openingEnv(target) {
		for _, want := range []string{"GOOS=" + target, "GOARCH=amd64", "CGO_ENABLED=0"} {
			if value == want {
				counts[want]++
			}
		}
		if value == "GOOS=plan9" || value == "GOARCH=not-a-supported-target" || value == "CGO_ENABLED=1" {
			t.Fatal("inherited target escaped normalization", value)
		}
	}
	if len(counts) != 3 {
		t.Fatal("incomplete explicit cross-target", counts)
	}
	for key, n := range counts {
		if n != 1 {
			t.Fatal("ambiguous cross-target", key, n)
		}
	}
}

func TestStoreOpeningsInventoryFailsClosed(t *testing.T) {
	for name, source := range map[string]string{
		"inactive_tag":   "//go:build recheck_dqs\n\npackage future\n",
		"inactive_cgo":   "//go:build !cgo\n\npackage future\n",
		"line_directive": "//line /outside/store/recheck.go:1\npackage future\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "future")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := openingPhysicalSources(root); err == nil {
				t.Fatal("excluded/directive future package accepted")
			}
		})
	}
	if err := openingCoverage(map[string]bool{"future.go": true}, map[string]bool{}, map[string]bool{}); err == nil {
		t.Fatal("missing physical source accepted")
	}
	if err := openingCoverage(map[string]bool{"future.go": true}, map[string]bool{"future.go": true}, map[string]bool{}); err == nil {
		t.Fatal("metadata without type resolution accepted")
	}
	// Physical positions remain inside the store despite adjusted diagnostic names.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "physical.go", "//line /outside/store/recheck.go:1\npackage future", 0)
	if err != nil || fset.PositionFor(f.Pos(), false).Filename != "physical.go" || fset.Position(f.Pos()).Filename == "physical.go" {
		t.Fatal("line position control invalid", err)
	}
}

func TestStoreOpeningsTransitiveTypeErrorFailsClosed(t *testing.T) {
	root := t.TempDir()
	for file, source := range map[string]string{
		"go.mod":                         "module guard.example\n\ngo 1.26\n",
		"internal/store/future/probe.go": "package future\nimport _ \"guard.example/bad\"\n",
		"bad/probe.go":                   "package bad\nvar x=undefined\n",
	} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(openingEnv(runtime.GOOS), "GOWORK=off", "GOPROXY=off")
	pkgs, err := packages.Load(&packages.Config{Dir: root, Env: env, BuildFlags: []string{"-p=1"}, Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedTypes}, "./internal/store/...")
	if err != nil {
		t.Fatal("loader transport failed instead of dependency type-check", err)
	}
	if err := validateOpeningLoads(pkgs); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatal("transitive ill-typed graph accepted", err)
	}
}

func TestStoreOpeningsFixtureImportsFailClosed(t *testing.T) {
	for _, rel := range []string{"qual/scanbench/fixture_dir_unix.go", "qual/scanbench/fixture_dir_other.go", "qual/scanbench/fixture_dir_test.go"} {
		for _, imp := range []string{`_ "os"`, `db "database/sql"`, `_ "database/sql/driver"`, `. "modernc.org/sqlite"`, `_ "github.com/PorticoMediaServer/portico/server/internal/store/writer"`} {
			t.Run(rel+imp, func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				source := openingConstraints[rel] + "\n\npackage scanbench\nimport " + imp + "\n"
				if err := os.WriteFile(target, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				_, err := openingPhysicalSources(root)
				if imp == `_ "os"` {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "unreviewed dependency") {
					t.Fatal("fixture DB/wrapper import accepted", err)
				}
			})
		}
	}
}
