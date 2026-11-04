package dbwork

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var updateDirectSnapshots = flag.Bool("update-direct-snapshot-census", false, "remove migrated sites from the direct snapshot baseline; never add sites")

// Direct BeginSnapshot is a legacy API: it does not take snapshot admission or
// request reuse. Some listed references are intentional borrows, but a new
// physical reader must use WithReadSnapshot (or its one-shot variant).
// The baseline is exact, so a migration must also remove its retired entries.
func TestDirectBeginSnapshotCensus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]int{}
	for _, dir := range []string{"internal", "cmd"} {
		err = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if info.IsDir() {
				if relative == "internal/dbwork" || info.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			found, err := directSnapshotReferences(path, nil)
			if err != nil {
				return err
			}
			for function, count := range found {
				actual[relative+"\t"+function] += count
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join("testdata", "direct_snapshot_census.tsv")
	previous, err := os.ReadFile(path)
	if err != nil && (!*updateDirectSnapshots || !os.IsNotExist(err)) {
		t.Fatal(err)
	}
	expected := map[string]int{}
	for _, line := range strings.Split(string(previous), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("bad direct snapshot baseline row: %q", line)
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 1 {
			t.Fatalf("bad direct snapshot count: %q", line)
		}
		key := parts[0] + "\t" + parts[1]
		if expected[key] != 0 {
			t.Fatalf("duplicate baseline key: %s", key)
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
	if *updateDirectSnapshots {
		if len(previous) > 0 {
			if added := directSnapshotAdditions(expected, actual); len(added) > 0 {
				t.Fatalf("the snapshot ratchet cannot add references:\n%s", strings.Join(added, "\n"))
			}
		}
		if err := os.WriteFile(path, []byte(out.String()), 0644); err != nil {
			t.Fatal(err)
		}
	} else if string(previous) != out.String() {
		t.Fatalf("direct snapshot census changed; new references are forbidden. After migrating sites, run go test ./internal/dbwork -run '^TestDirectBeginSnapshotCensus$' -update-direct-snapshot-census:\n%s", censusDiff(string(previous), out.String()))
	}
	t.Logf("direct snapshot references=%d functions=%d", total, len(actual))
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
	local := ""
	for _, spec := range file.Imports {
		if value, _ := strconv.Unquote(spec.Path.Value); value == dbworkImport {
			local = "dbwork"
			if spec.Name != nil {
				local = spec.Name.Name
			}
		}
	}
	found := map[string]int{}
	if local == "" || local == "_" {
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
				if x, ok := s.X.(*ast.Ident); ok && x.Name == local && s.Sel.Name == "BeginSnapshot" {
					found[function]++
				}
			}
			if id, ok := n.(*ast.Ident); ok && local == "." && id.Name == "BeginSnapshot" && !selectors[id] {
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
