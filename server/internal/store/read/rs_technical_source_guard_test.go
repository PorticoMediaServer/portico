package read

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRSTechnicalStreamDetailVersionMirrorsAssets reads the owner constant from
// its source instead of importing internal/assets. Importing that package links
// the owner's SQL functions through internal/dbwork and changes the closed
// function environment every read plan golden pins. An owner bump that did not
// move this mirror would silently turn every stored detail into "stale".
func TestRSTechnicalStreamDetailVersionMirrorsAssets(t *testing.T) {
	entries, err := os.ReadDir("../../assets")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := false
	var value int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join("../../assets", name), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, ident := range spec.Names {
				if ident.Name != "StreamDetailVersion" {
					continue
				}
				found = true
				if i >= len(spec.Values) {
					t.Fatalf("%s: StreamDetailVersion has no literal value", name)
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					t.Fatalf("%s: StreamDetailVersion is not an integer literal", name)
				}
				value, err = strconv.ParseInt(lit.Value, 0, 64)
				if err != nil {
					t.Fatalf("%s: StreamDetailVersion: %v", name, err)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("assets package has no StreamDetailVersion constant")
	}
	if value != rsTechnicalStreamDetailVersion {
		t.Fatalf("assets.StreamDetailVersion=%d, read mirror=%d: bump both together", value, rsTechnicalStreamDetailVersion)
	}
}
