package sqliteconn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStoreOpeningsUseSharedPolicy(t *testing.T) {
	// Exact function/API exceptions: the shared implementation and wrappers
	// around its connector, plus ONE intentionally enabled-DQS negative control.
	allow := map[string]string{
		"internal/sqliteconn/connection.go":      "|Connector:NewConnector|Open:OpenDB|",
		"internal/sqliteconn/connection_test.go": "|TestReplacementRestoresEveryConnectionSetting:OpenDB|TestCancelledReadReopensWithPolicy:OpenDB|",
		"internal/sqliteconn/limits_test.go":     "|limitedReader:OpenDB|",
		"internal/sqliteconn/dqs_test.go":        "|TestDQSAssertRejectsFlagsAndMissingGetter:OpenDB|TestDQSAllRolesReplacementAndReaderGrowth:OpenDB|TestDQSActualEnabledDriverRefused:NewConnector|TestDQSActualEnabledDriverRefused:OpenDB|",
		"writer/transaction.go":                  "|openPool:OpenDB|",
		"writer/transaction_test.go":             "|installFaultPool:OpenDB|", // held-source fault overlay
	}
	root := filepath.Clean("../..")
	// fs.FS telemetry uses the same method spelling, but opens no database.
	allow["qual/host.go"] = "|recordFile:Open|telemetryDirectory:Open|"
	allow["qual/host_guards_test.go"] = "|Open:Open|"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		imports := map[string]string{}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = p
			if name == "." && (p == "database/sql" || strings.HasSuffix(p, "/sqlite")) {
				t.Errorf("%s: dot import bypasses shared opener", path)
			}
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			s, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch s.Sel.Name {
			case "Open", "OpenDB", "NewConnector", "NewDriver", "OpenConnector":
			default:
				return true
			}
			id, _ := s.X.(*ast.Ident)
			if id != nil {
				if p := imports[id.Name]; p != "" && p != "database/sql" && !strings.HasSuffix(p, "/sqlite") {
					return true
				}
			}
			fn := ""
			for _, decl := range f.Decls {
				if f, ok := decl.(*ast.FuncDecl); ok && s.Pos() >= f.Pos() && s.End() <= f.End() {
					fn = f.Name.Name
				}
			}
			if !strings.Contains(allow[filepath.ToSlash(rel)], "|"+fn+":"+s.Sel.Name+"|") {
				t.Errorf("%s#%s: %s bypasses shared opener", rel, fn, s.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
