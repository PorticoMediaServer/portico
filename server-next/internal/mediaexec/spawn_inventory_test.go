package mediaexec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// processSpawns is every place in the server that starts a process other than
// through this package, with why it is not a media tool on untrusted input.
// FFmpeg and ffprobe start only through mediaexec. A
// new spawn anywhere fails this test until it is either routed through
// mediaexec or added here with a reason a reviewer accepts.
var processSpawns = map[string]string{
	// The decoder's own Linux sandbox, already inside mediaexec's policy.
	"internal/decoder/confinement_linux.go:ProbeConfinement":         "bubblewrap probe of the server's own helper; no media",
	"internal/decoder/confinement_linux.go:runOuter":                 "the trusted outer helper starting bubblewrap; the helper itself was started by mediaexec",
	"internal/decoder/confinement_linux.go:runInner":                 "the tool inside the bubblewrap sandbox the outer helper built",
	"internal/decoder/prepared_file.go:PreparedFileSandboxAvailable": "bubblewrap --help, to read its capabilities",
	// Not media.
	"internal/decoder/hardware_nvidia.go:runNVIDIADiscovery": "fixed read-only nvidia-smi UUID/PCI/index/name/driver query; 5-second context, bounded output, minimal environment and " +
		"childprocess group retirement; no media input or codec availability inference",
	"internal/supervise/process.go:Process": "the server starting itself as its own worker, so a restart stays inside the service",
	"cmd/portico-crash/main.go:command":     "a test tool starting the server binary, and itself to run an import, so it can kill them; no media",
}

type spawn struct{ file, function string }

func TestEveryProcessSpawnIsMediaexecOrListed(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	var unlisted []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/mediaexec/") {
			return nil
		}
		for _, s := range spawnsIn(t, path) {
			key := rel + ":" + s.function
			found[key] = true
			if _, ok := processSpawns[key]; !ok {
				unlisted = append(unlisted, key+" ("+s.file+")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(unlisted)
	for _, key := range unlisted {
		t.Errorf("process started outside mediaexec: %s — route FFmpeg/ffprobe through mediaexec, or list a non-media spawn in processSpawns with its reason", key)
	}
	for key := range processSpawns {
		if !found[key] {
			t.Errorf("processSpawns lists %s, which no longer starts a process; remove it", key)
		}
	}
}

// spawnsIn finds exec.Command, exec.CommandContext, exec.Cmd literals,
// os.StartProcess and syscall/unix Exec, ForkExec and StartProcess, by the
// import path of the package they belong to.
func spawnsIn(t *testing.T, path string) []spawn {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	imports := map[string]string{}
	for _, spec := range file.Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		name := p[strings.LastIndex(p, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = p
	}
	starts := map[string][]string{
		"os/exec":               {"Command", "CommandContext"},
		"os":                    {"StartProcess"},
		"syscall":               {"Exec", "ForkExec", "StartProcess"},
		"golang.org/x/sys/unix": {"Exec"},
	}
	var out []spawn
	visit := func(owner string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				// exec.Cmd{...} built by hand.
				if sel, ok := n.Type.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && imports[pkg.Name] == "os/exec" && sel.Sel.Name == "Cmd" {
						out = append(out, spawn{file: "exec.Cmd{}", function: owner})
					}
				}
			case *ast.SelectorExpr:
				pkg, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				for _, name := range starts[imports[pkg.Name]] {
					if n.Sel.Name == name {
						out = append(out, spawn{file: pkg.Name + "." + name, function: owner})
					}
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			visit(fn.Name.Name, fn)
		} else {
			visit("<package>", decl)
		}
	}
	return out
}
