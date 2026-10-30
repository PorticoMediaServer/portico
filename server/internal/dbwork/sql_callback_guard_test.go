package dbwork

import (
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Runtime complement to the source census: the newly audited callback runs on
// a query-only connection, preserves exact TEXT bytes, and writes nothing.
func TestSearchMatchPureSQLCallback(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := db.QueryRow("SELECT portico_search_match(?1,?2,?3)", "foo\x00bar", "k2 l1", `["bar","k2"]`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 512 {
		t.Fatalf("unbounded scalar delivery: %d", len(encoded))
	}
	var result sqlite.SearchMatchResult
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Match || result.SourceBytes != 12 || result.DocumentTokens != 4 || result.QueryBytes != 5 || result.QueryTokens != 2 || result.TokenBytes != 15 {
		t.Fatalf("unexpected scalar work receipt: %+v", result)
	}
	if err := db.QueryRow("SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatalf("pure callback changed database: before=%d after=%d err=%v", before, after, err)
	}
	for _, query := range []string{`null`, `{}`, `[1]`, `"a"`, `[`, strings.Repeat(" ", 500000)} {
		if err := db.QueryRow("SELECT portico_search_match('alpha','k2',?)", query).Scan(&encoded); err == nil {
			t.Fatalf("accepted invalid phrase array: %.32s", query)
		}
	}
	var capacity *sqlite.Error
	err = db.QueryRow("SELECT portico_search_match('alpha','k2',?)", strings.Repeat(" ", 500000)).Scan(&encoded)
	if !errors.As(err, &capacity) || capacity.Code() != sqlite3.SQLITE_TOOBIG {
		t.Fatalf("SQL lost typed capacity: %v", err)
	}
}

// A plain SELECT is exempted from the exact wake (statementHead.plainSelect)
// because nothing this server can run from SQL writes. That is a census, not a
// property of SQL: the vendored driver's arbitrary-callback registration would
// let a Go function write through another handle, and a connection hook, virtual
// table module or loaded extension could do the same. This guard keeps the
// census mechanical, so adding one fails here until someone re-reads the
// exemption and updates the design note.
//
// It reads source only. It inspects no database, no user data, no secret and no
// metadata, and it deliberately does not forbid SQL text in general: the point
// is the registration surface, and the pinned names.
const sqlCallbackDriverPath = "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"

// sqlFunctionCensus is every SQL-callable name this server registers outside the
// vendored driver, as of the exact-wake amendment. Adding a name here is a
// deliberate act: re-read statementHead.plainSelect first.
var sqlFunctionCensus = []string{
	"collation:portico_guide_name",
	"function:pid",
	"function:pid_blob",
	"function:portico_author_key",
	"function:portico_channel_number",
	"function:portico_fold_text",
	"function:portico_letter",
	"function:portico_metadata_files_revision",
	"function:portico_reading_revision",
	"function:portico_search_fold",
	"function:portico_search_match",
	"function:portico_search_prefixes",
	"function:portico_search_text",
	"function:portico_sort_title",
}

// sqlSurfaceForbidden names the driver entry points that hand arbitrary Go code
// to SQLite without the read-only callback shape the census above audited.
var sqlSurfaceForbidden = map[string]string{
	"RegisterFunction":           "arbitrary scalar callback",
	"MustRegisterFunction":       "arbitrary scalar callback",
	"RegisterScalarFunction":     "arbitrary scalar callback",
	"MustRegisterScalarFunction": "arbitrary scalar callback",
	"RegisterConnectionHook":     "per-connection hook",
	"MustRegisterConnectionHook": "per-connection hook",
	"RegisterModule":             "virtual table module",
	"MustRegisterModule":         "virtual table module",
}

// sqlSurfaceAudited names the registration calls whose callee is a deterministic
// text/JSON/hash transform, recorded in the census. Anything else that starts
// with Register or MustRegister is a new, unaudited surface.
var sqlSurfaceAudited = map[string]string{
	"MustRegisterDeterministicScalarFunction": "function",
	"MustRegisterCollationUtf8":               "collation",
}

func TestNoArbitrarySQLCallbackSurfaces(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var registrations, problems []string
	for _, tree := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path == filepath.Join(root, "internal", "thirdparty") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			regs, probs, scanErr := scanSQLSurfaceFile(path)
			if scanErr != nil {
				return scanErr
			}
			relative, _ := filepath.Rel(root, path)
			for _, problem := range probs {
				problems = append(problems, filepath.ToSlash(relative)+": "+problem)
			}
			registrations = append(registrations, regs...)
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("SQL registration surfaces outside the audited plain-SELECT exemption:\n%s", strings.Join(problems, "\n"))
	}
	sort.Strings(registrations)
	if strings.Join(registrations, "\n") != strings.Join(sqlFunctionCensus, "\n") {
		t.Fatalf("the SQL function/collation census changed; re-read statementHead.plainSelect before shipping:\ngot:\n%s\nwant:\n%s",
			strings.Join(registrations, "\n"), strings.Join(sqlFunctionCensus, "\n"))
	}
}

func scanSQLSurfaceFile(path string) (registrations, problems []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return scanSQLSurfaceSource(path, raw)
}

// scanSQLSurfaceSource reports the registrations a source file makes and every
// surface that would invalidate the plain-SELECT exemption. It takes source
// bytes so the negative controls below can exercise the same code path.
func scanSQLSurfaceSource(name string, src []byte) (registrations, problems []string, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, nil, err
	}
	alias := ""
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if path != sqlCallbackDriverPath {
			continue
		}
		alias = "sqlite"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				return true
			}
			lower := strings.ToLower(x.Value)
			if strings.Contains(lower, "load_extension") || strings.Contains(lower, "enable_load_extension") {
				problems = append(problems, "SQL text loads an extension: "+x.Value)
			}
		case *ast.CallExpr:
			selector, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || alias == "" || ident.Name != alias {
				return true
			}
			if reason, forbidden := sqlSurfaceForbidden[selector.Sel.Name]; forbidden {
				problems = append(problems, reason+": "+selector.Sel.Name)
				return true
			}
			kind, audited := sqlSurfaceAudited[selector.Sel.Name]
			if !audited {
				if strings.HasPrefix(selector.Sel.Name, "Register") || strings.HasPrefix(selector.Sel.Name, "MustRegister") {
					problems = append(problems, "unrecognised SQL registration surface: "+selector.Sel.Name)
				}
				return true
			}
			if len(x.Args) == 0 {
				problems = append(problems, kind+" registered without a name literal")
				return true
			}
			literal, ok := x.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				problems = append(problems, kind+" registered with a non-literal name: the census cannot pin it")
				return true
			}
			registrations = append(registrations, kind+":"+strings.Trim(literal.Value, `"`))
		}
		return true
	})
	return registrations, problems, nil
}

// TestSQLCallbackGuardCatchesSyntheticViolations is the negative control: each
// shape the guard is meant to stop is injected as source and must be reported.
func TestSQLCallbackGuardCatchesSyntheticViolations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		contains string
	}{
		{
			name: "arbitrary scalar callback",
			source: `package p
import "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { sqlite.MustRegisterFunction("evil", 1, nil) }`,
			contains: "arbitrary scalar callback: MustRegisterFunction",
		},
		{
			name: "connection hook",
			source: `package p
import "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { sqlite.RegisterConnectionHook(nil) }`,
			contains: "per-connection hook: RegisterConnectionHook",
		},
		{
			name: "virtual table module",
			source: `package p
import "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { _ = sqlite.RegisterModule("evil", nil) }`,
			contains: "virtual table module: RegisterModule",
		},
		{
			name: "extension in SQL text",
			source: `package p
const dsn = "?_pragma=load_extension(1)"`,
			contains: "loads an extension",
		},
		{
			name: "aliased import",
			source: `package p
import driver "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { driver.MustRegisterScalarFunction("evil", 1, nil) }`,
			contains: "arbitrary scalar callback: MustRegisterScalarFunction",
		},
		{
			name: "non-literal function name",
			source: `package p
import "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { sqlite.MustRegisterDeterministicScalarFunction(dynamicName, 1, nil) }`,
			contains: "non-literal name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems, err := scanSQLSurfaceSource("synthetic.go", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.contains) {
				t.Fatalf("guard missed %q; reported:\n%s", tc.contains, joined)
			}
		})
	}
	// The same scanner accepts the audited shape and records it in the census,
	// so the guard's silence above is not silence for every registration.
	registrations, problems, err := scanSQLSurfaceSource("synthetic.go", []byte(`package p
import "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
func init() { sqlite.MustRegisterDeterministicScalarFunction("portico_new_text", 1, nil) }`))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 || len(registrations) != 1 || registrations[0] != "function:portico_new_text" {
		t.Fatalf("audited registration shape: %v %v", registrations, problems)
	}
	// A new name changes the census, which the comparison above reports.
	if strings.Join(registrations, "\n") == strings.Join(sqlFunctionCensus, "\n") {
		t.Fatal("a new registered name did not change the census")
	}
}
