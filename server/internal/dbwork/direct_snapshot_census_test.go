package dbwork

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var updateDirectSnapshots = flag.Bool("update-direct-snapshot-census", false, "remove migrated sites from the direct snapshot baseline; never add sites")

type directSnapshotPackage struct {
	Dir, ImportPath                                   string
	GoFiles, CgoFiles, IgnoredGoFiles, InvalidGoFiles []string
	Module                                            *struct {
		Main    bool
		Replace *struct{ Dir, Version string }
	}
}

// Directory names choose initial package roots only. Go resolves every actual
// production import, including imports into testdata, node_modules, hidden
// directories, vendored code and locally replaced modules. Imports in inactive
// platform/tag files are followed too, so the host's build tags cannot hide one.
func directSnapshotProductionFiles(root string) ([]string, error) {
	seeds := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root {
				if name := info.Name(); name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			relative, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			seeds["./"+filepath.ToSlash(relative)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var queue []string
	for seed := range seeds {
		queue = append(queue, seed)
	}
	listed, scanned, files := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for len(queue) != 0 {
		sort.Strings(queue)
		cmd := exec.Command("go", append([]string{"list", "-deps", "-e", "-json"}, queue...)...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("resolve production imports: %w\n%s", err, stderr.String())
		}
		var packages []directSnapshotPackage
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var pkg directSnapshotPackage
			if err := decoder.Decode(&pkg); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			if pkg.Dir == "" {
				return nil, fmt.Errorf("cannot resolve production import %q", pkg.ImportPath)
			}
			listed[pkg.ImportPath] = true
			packages = append(packages, pkg)
		}
		pending := map[string]bool{}
		for _, pkg := range packages {
			relative, err := filepath.Rel(root, pkg.Dir)
			owned := err == nil && filepath.IsLocal(relative)
			localDependency := pkg.Module != nil && (pkg.Module.Main ||
				pkg.Module.Replace != nil && pkg.Module.Replace.Dir != "" && pkg.Module.Replace.Version == "")
			if (!owned && !localDependency) || scanned[pkg.Dir] {
				continue
			}
			scanned[pkg.Dir] = true
			names := append(append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...), pkg.IgnoredGoFiles...)
			names = append(names, pkg.InvalidGoFiles...)
			for _, name := range names {
				if strings.HasSuffix(name, "_test.go") {
					continue
				}
				path := filepath.Join(pkg.Dir, name)
				if owned {
					files[path] = true
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					return nil, err
				}
				for _, spec := range file.Imports {
					importPath, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						return nil, err
					}
					if importPath != "C" && !listed[importPath] {
						pending[importPath] = true
					}
				}
			}
		}
		queue = nil
		for importPath := range pending {
			queue = append(queue, importPath)
		}
	}
	var out []string
	for path := range files {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

// Direct BeginSnapshot is a legacy API: it does not take snapshot admission or
// request reuse. Some listed references are intentional borrows, but a new
// physical reader must use WithReadSnapshot (or its one-shot variant).
// The baseline is exact, so a migration must also remove its retired entries.
func TestDirectBeginSnapshotCensus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	total, functions, err := directSnapshotCensus(root, filepath.Join("testdata", "direct_snapshot_census.tsv"), *updateDirectSnapshots)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("direct snapshot references=%d functions=%d", total, functions)
}

func directSnapshotCensus(root, path string, update bool) (int, int, error) {
	actual := map[string]int{}
	files, err := directSnapshotProductionFiles(root)
	if err != nil {
		return 0, 0, err
	}
	for _, path := range files {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return 0, 0, err
		}
		relative = filepath.ToSlash(relative)
		if filepath.ToSlash(filepath.Dir(relative)) == "internal/dbwork" {
			continue
		}
		found, err := directSnapshotReferences(path, nil)
		if err != nil {
			return 0, 0, err
		}
		for function, count := range found {
			actual[relative+"\t"+function] += count
		}
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	expected := map[string]int{}
	for _, line := range strings.Split(string(previous), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return 0, 0, fmt.Errorf("bad direct snapshot baseline row: %q", line)
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 1 {
			return 0, 0, fmt.Errorf("bad direct snapshot count: %q", line)
		}
		key := parts[0] + "\t" + parts[1]
		if expected[key] != 0 {
			return 0, 0, fmt.Errorf("duplicate baseline key: %s", key)
		}
		expected[key] = n
	}
	keys := make([]string, 0, len(actual))
	total := 0
	for key, n := range actual {
		keys = append(keys, key)
		total += n
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString("# file\tfunction\tcount; existing direct references only, remove as migrated\n")
	for _, key := range keys {
		fmt.Fprintf(&out, "%s\t%d\n", key, actual[key])
	}
	if update {
		if len(expected) == 0 {
			return 0, 0, fmt.Errorf("the snapshot ratchet requires a nonempty previous baseline")
		}
		if added := directSnapshotAdditions(expected, actual); len(added) > 0 {
			return 0, 0, fmt.Errorf("the snapshot ratchet cannot add references:\n%s", strings.Join(added, "\n"))
		}
		if err := os.WriteFile(path, []byte(out.String()), 0644); err != nil {
			return 0, 0, err
		}
	} else if string(previous) != out.String() {
		return 0, 0, fmt.Errorf("direct snapshot census changed; new references are forbidden. After migrating sites, run go test ./internal/dbwork -run '^TestDirectBeginSnapshotCensus$' -update-direct-snapshot-census:\n%s", censusDiff(string(previous), out.String()))
	}
	return total, len(actual), nil
}

func directSnapshotAdditions(previous, actual map[string]int) []string {
	var added []string
	for key, n := range actual {
		if n > previous[key] {
			added = append(added, fmt.Sprintf("%s: %d -> %d", key, previous[key], n))
		}
	}
	sort.Strings(added)
	return added
}

func directSnapshotReferences(path string, src any) (map[string]int, error) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	locals := map[string]bool{}
	for _, spec := range file.Imports {
		if value, _ := strconv.Unquote(spec.Path.Value); value == dbworkImport {
			local := "dbwork"
			if spec.Name != nil {
				local = spec.Name.Name
			}
			if local != "_" {
				locals[local] = true
			}
		}
	}
	found := map[string]int{}
	if len(locals) == 0 {
		return found, nil
	}
	scan := func(node ast.Node, function string) {
		selectors := map[*ast.Ident]bool{}
		ast.Inspect(node, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				selectors[s.Sel] = true
			}
			return true
		})
		ast.Inspect(node, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				if x, ok := s.X.(*ast.Ident); ok && locals[x.Name] && s.Sel.Name == "BeginSnapshot" {
					found[function]++
				}
			}
			if id, ok := n.(*ast.Ident); ok && locals["."] && id.Name == "BeginSnapshot" && !selectors[id] {
				found[function]++
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			name := fn.Name.Name
			if fn.Recv != nil {
				name = "(" + render(fs, fn.Recv.List[0].Type) + ")." + name
			}
			if fn.Body != nil {
				scan(fn.Body, name)
			}
		} else {
			scan(decl, "<package>")
		}
	}
	return found, nil
}

func TestDirectSnapshotReferenceCensusUsesImports(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int
	}{
		{"alias-and-function-value", `package p; import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"; var open = work.BeginSnapshot; func f(){ work.BeginSnapshot(nil,nil); _ = "work.BeginSnapshot(nil,nil)" }`, 2},
		{"dot-import", `package p; import . "github.com/PorticoMediaServer/portico/server/internal/dbwork"; func f(){ BeginSnapshot(nil,nil) }`, 1},
		{"two-aliases", `package p; import (first "github.com/PorticoMediaServer/portico/server/internal/dbwork"; second "github.com/PorticoMediaServer/portico/server/internal/dbwork"); var Open = first.BeginSnapshot; var _ = second.WithReadSnapshot`, 1},
		{"named-and-dot", `package p; import (first "github.com/PorticoMediaServer/portico/server/internal/dbwork"; . "github.com/PorticoMediaServer/portico/server/internal/dbwork"); var Open = first.BeginSnapshot; var OpenDot = BeginSnapshot`, 2},
		{"named-and-blank", `package p; import (first "github.com/PorticoMediaServer/portico/server/internal/dbwork"; _ "github.com/PorticoMediaServer/portico/server/internal/dbwork"); var Open = first.BeginSnapshot`, 1},
		{"unrelated-method", `package p; import dbwork "example.com/other"; func f(){ dbwork.BeginSnapshot(nil,nil) }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := directSnapshotReferences("fixture.go", tc.source)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, count := range found {
				n += count
			}
			if n != tc.want {
				t.Fatalf("references=%d want=%d: %v", n, tc.want, found)
			}
		})
	}
}

func TestDirectSnapshotBaselineCanOnlyShrink(t *testing.T) {
	before := map[string]int{"file.go\tf": 2}
	for _, after := range []map[string]int{{"file.go\tf": 3}, {"file.go\tg": 1}} {
		if len(directSnapshotAdditions(before, after)) == 0 {
			t.Fatal("ratchet accepted a new reference", after)
		}
	}
	if added := directSnapshotAdditions(before, map[string]int{"file.go\tf": 1}); len(added) != 0 {
		t.Fatal("ratchet rejected a migration", added)
	}
}

func directSnapshotCensusFixture(t *testing.T) (root, baseline string) {
	t.Helper()
	t.Setenv("GOWORK", "off")
	root = t.TempDir()
	for _, dir := range []string{"internal/legacy", "internal/dbwork", "cmd"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/PorticoMediaServer/portico/server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stub := `package dbwork; import ("context"; "database/sql"); type Write struct{}; func BeginSnapshot(context.Context,*sql.DB)(*Write,error){return nil,nil}; func WithReadSnapshot(){}`
	if err := os.WriteFile(filepath.Join(root, "internal/dbwork/stub.go"), []byte(stub), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := `package legacy; import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"; var Open = work.BeginSnapshot`
	if err := os.WriteFile(filepath.Join(root, "internal/legacy/legacy.go"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	baseline = filepath.Join(root, "baseline.tsv")
	initial := "# file\tfunction\tcount; existing direct references only, remove as migrated\ninternal/legacy/legacy.go\t<package>\t1\n"
	if err := os.WriteFile(baseline, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	return root, baseline
}

func TestDirectSnapshotCensusRejectsReviewerWitnesses(t *testing.T) {
	for _, tc := range []struct{ name, path, source string }{
		{"two-import-aliases", "internal/witness/witness.go", `package witness; import (first "github.com/PorticoMediaServer/portico/server/internal/dbwork"; second "github.com/PorticoMediaServer/portico/server/internal/dbwork"); var Open = first.BeginSnapshot; var _ = second.WithReadSnapshot`},
		{"server-module-wrapper", "review738wrapper/wrapper.go", `package wrapper; import ("context"; "database/sql"; work "github.com/PorticoMediaServer/portico/server/internal/dbwork"); func Open(ctx context.Context, db *sql.DB) (*work.Write,error) { return work.BeginSnapshot(ctx,db) }`},
	} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%t", tc.name, update), func(t *testing.T) {
				root, baseline := directSnapshotCensusFixture(t)
				path := filepath.Join(root, tc.path)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(baseline)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := directSnapshotCensus(root, baseline, update); err == nil {
					t.Fatal("new direct snapshot reference evaded the census")
				}
				after, err := os.ReadFile(baseline)
				if err != nil || string(after) != string(before) {
					t.Fatal("refused reference changed the baseline", err)
				}
			})
		}
	}
}

func TestDirectSnapshotUpdateRequiresPreviousBaseline(t *testing.T) {
	for _, state := range []string{"missing", "empty", "header-only"} {
		t.Run(state, func(t *testing.T) {
			root, baseline := directSnapshotCensusFixture(t)
			added := `package legacy; import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"; var NewOpen = work.BeginSnapshot`
			if err := os.WriteFile(filepath.Join(root, "internal/legacy/new.go"), []byte(added), 0600); err != nil {
				t.Fatal(err)
			}
			var previous []byte
			if state == "missing" {
				if err := os.Remove(baseline); err != nil {
					t.Fatal(err)
				}
			} else {
				if state == "header-only" {
					previous = []byte("# file\tfunction\tcount\n")
				}
				if err := os.WriteFile(baseline, previous, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := directSnapshotCensus(root, baseline, true); err == nil {
				t.Fatal("baseline update laundered a new reference without a previous baseline")
			}
			after, err := os.ReadFile(baseline)
			if state == "missing" {
				if !os.IsNotExist(err) {
					t.Fatal("refused update created the missing baseline", err)
				}
			} else if err != nil || string(after) != string(previous) {
				t.Fatal("refused update changed the empty baseline", err)
			}
		})
	}
}

func TestDirectSnapshotCensusFollowsProductionImports(t *testing.T) {
	for _, tc := range []struct {
		parent, directory string
		tagged            bool
	}{
		{"internal/review755area", "node_modules", false},
		{"internal/review755area", "testdata", false},
		{"internal/review755area", ".hidden", false},
		{"internal/review755area", ".hidden", true},
		{"internal/dbwork", "nested", false},
	} {
		t.Run(fmt.Sprintf("%s/%s/tagged=%t", tc.parent, tc.directory, tc.tagged), func(t *testing.T) {
			root, baseline := directSnapshotCensusFixture(t)
			bridgePath := filepath.ToSlash(filepath.Join(tc.parent, tc.directory, "bridge"))
			bridgeDir := filepath.Join(root, bridgePath)
			consumerDir := filepath.Join(root, "internal/review755consumer")
			for _, dir := range []string{bridgeDir, consumerDir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			bridge := `package bridge; import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"; var Open = work.BeginSnapshot`
			consumer := fmt.Sprintf(`package consumer; import bridge "github.com/PorticoMediaServer/portico/server/%s"; var Open = bridge.Open`, bridgePath)
			if tc.tagged {
				consumer = "//go:build snapshot_census_witness\n\n" + consumer
				if err := os.WriteFile(filepath.Join(consumerDir, "base.go"), []byte("package consumer\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for path, source := range map[string]string{filepath.Join(bridgeDir, "bridge.go"): bridge, filepath.Join(consumerDir, "consumer.go"): consumer} {
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("go", "test", "-p", "1", "-tags", "snapshot_census_witness", "-run", "^$", "./internal/review755consumer")
			cmd.Dir = root
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("production witness does not compile: %v\n%s", err, output)
			}
			for _, update := range []bool{false, true} {
				t.Run(fmt.Sprintf("update=%t", update), func(t *testing.T) {
					before, err := os.ReadFile(baseline)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := directSnapshotCensus(root, baseline, update); err == nil {
						t.Fatal("imported production package escaped the census", bridgePath)
					}
					after, err := os.ReadFile(baseline)
					if err != nil || string(after) != string(before) {
						t.Fatal("refused production import changed the baseline", err)
					}
				})
			}
		})
	}
}

func TestDirectSnapshotCensusIgnoresUnimportedFixtures(t *testing.T) {
	root, baseline := directSnapshotCensusFixture(t)
	fixture := filepath.Join(root, "testdata", "not-a-package")
	if err := os.MkdirAll(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "invalid.go"), []byte("deliberately invalid Go fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, update := range []bool{false, true} {
		if total, functions, err := directSnapshotCensus(root, baseline, update); err != nil || total != 1 || functions != 1 {
			t.Fatalf("unimported test fixture became production: references=%d functions=%d err=%v", total, functions, err)
		}
	}
}

func TestDirectSnapshotCensusFollowsWorkspaceImports(t *testing.T) {
	root, baseline := directSnapshotCensusFixture(t)
	helper := t.TempDir()
	work := filepath.Join(t.TempDir(), "go.work")
	bridgeDir := filepath.Join(root, ".hidden", "bridge")
	consumerDir := filepath.Join(root, "internal", "consumer")
	for _, dir := range []string{bridgeDir, consumerDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, source := range map[string]string{
		work:                             fmt.Sprintf("go 1.26.10\nuse (\n%q\n%q\n)\n", root, helper),
		filepath.Join(helper, "go.mod"):  "module example.com/helper\ngo 1.26.10\n",
		filepath.Join(helper, "base.go"): "package helper; const Base = 1\n",
		filepath.Join(helper, "tagged.go"): `//go:build snapshot_census_witness

package helper
import bridge "github.com/PorticoMediaServer/portico/server/.hidden/bridge"
var Open = bridge.Open
`,
		filepath.Join(bridgeDir, "bridge.go"):     `package bridge; import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"; var Open = work.BeginSnapshot`,
		filepath.Join(consumerDir, "consumer.go"): `package consumer; import "example.com/helper"; var _ = helper.Base`,
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", work)
	cmd := exec.Command("go", "test", "-p", "1", "-tags", "snapshot_census_witness", "-run", "^$", "./internal/consumer")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("workspace witness does not compile: %v\n%s", err, output)
	}
	for _, update := range []bool{false, true} {
		if _, _, err := directSnapshotCensus(root, baseline, update); err == nil {
			t.Errorf("inactive workspace import escaped the census: update=%t", update)
		}
	}
}
