package persistence

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type derivedStringContext string

func (derivedStringContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (derivedStringContext) Done() <-chan struct{}       { return nil }
func (derivedStringContext) Err() error                  { return nil }
func (derivedStringContext) Value(any) any               { return nil }

var _ context.Context = derivedStringContext("")

type derivedSignatureExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// A string constant can implement context.Context. Reading this argument as
// SQL silently misses the actual dynamic UPDATE after an explicit receiver.
func TestDerivedWriterGuardNamedInterfaceMethodExpression(t *testing.T) {
	const contextValue derivedStringContext = "constant context"
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE catalog_browse_rows(title TEXT); INSERT INTO catalog_browse_rows VALUES('before')`); err != nil {
		t.Fatal(err)
	}
	var executor derivedSignatureExecutor = db
	build := func(verb, table string) string { return verb + " " + table + " SET title=?" }
	if _, err = derivedSignatureExecutor.ExecContext(executor, contextValue, build("UPDATE", "catalog_browse_rows"), "wrong"); err != nil {
		t.Fatal(err)
	}
	var title string
	if err = db.QueryRow(`SELECT title FROM catalog_browse_rows`).Scan(&title); err != nil || title != "wrong" {
		t.Fatal(title, err)
	}

	root := t.TempDir()
	writeDerivedSignatureFixture(t, root, `owner.Executor.ExecContext(db, contextValue, build("UPDATE", "catalog_browse_rows"), "wrong")`)
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"owner"}) {
		t.Fatal(got.Tables)
	}
	if got.Dynamic["bypass/b.go"].SHA256 == "" {
		t.Fatalf("named interface method expression hid the dynamic UPDATE: %+v", got)
	}
	pinUnknownDerivedSQL(&got)
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("named interface method expression bypassed migrated owner")
	}
}

func TestDerivedWriterGuardInterfaceBoundMethodValue(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE catalog_browse_rows(title TEXT); INSERT INTO catalog_browse_rows VALUES('before')`); err != nil {
		t.Fatal(err)
	}
	var executor derivedSignatureExecutor = db
	execute := executor.ExecContext
	build := func(verb, table string) string { return verb + " " + table + " SET title=?" }
	const contextValue derivedStringContext = "constant context"
	if _, err = execute(contextValue, build("UPDATE", "catalog_browse_rows"), "wrong"); err != nil {
		t.Fatal(err)
	}
	var title string
	if err = db.QueryRow(`SELECT title FROM catalog_browse_rows`).Scan(&title); err != nil || title != "wrong" {
		t.Fatal(title, err)
	}

	root := t.TempDir()
	writeDerivedSignatureFixture(t, root, `execute := db.ExecContext; execute(contextValue, build("UPDATE", "catalog_browse_rows"), "wrong")`)
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dynamic["bypass/b.go"].SHA256 == "" {
		t.Fatalf("bound interface execution hid the dynamic UPDATE: %+v", got)
	}
	pinUnknownDerivedSQL(&got)
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("bound interface method bypassed migrated owner")
	}
}

func writeDerivedSignatureFixture(t *testing.T, root, call string) {
	t.Helper()
	files := map[string]string{
		"owner/a.go": `package owner
import("context";"database/sql")
type Executor interface{ExecContext(context.Context,string,...any)(sql.Result,error)}
const owned="UPDATE catalog_browse_rows SET title=?"
`,
		"bypass/b.go": `package bypass
import("owner";"time")
type constantContext string
func(constantContext)Deadline()(time.Time,bool){return time.Time{},false}
func(constantContext)Done()<-chan struct{}{return nil}
func(constantContext)Err()error{return nil}
func(constantContext)Value(any)any{return nil}
const contextValue constantContext="constant context"
func build(verb,table string)string{return verb+" "+table+" SET title=?"}
func run(db owner.Executor){` + call + `}
`,
	}
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// Typed declarations for the old constant-SQL matrix: a missing database or
// wrapper signature is deliberately no longer treated as a constant call.
func writeDerivedTypedFixture(t *testing.T, root, pkg string) {
	t.Helper()
	declarations := `package ` + pkg + `
import("context";"database/sql")
var db *sql.DB
var ctx context.Context
const class=0
`
	wrappers := `package dbwork
import("context";"database/sql")
func ExecWrite(context.Context,*sql.DB,int,string,...any){}
func Query(context.Context,*sql.DB,string,...any){}
func QueryRow(context.Context,*sql.DB,string,...any){}
`
	if pkg == "dbwork" {
		declarations += `func ExecWrite(context.Context,*sql.DB,int,string,...any){}
func Query(context.Context,*sql.DB,string,...any){}
func QueryRow(context.Context,*sql.DB,string,...any){}
`
	}
	if err := os.WriteFile(filepath.Join(root, "typed.go"), []byte(declarations), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal", "dbwork")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "wrappers.go"), []byte(wrappers), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedWriterGuardSQLSignaturePositions(t *testing.T) {
	root := t.TempDir()
	writeDerivedSignatureFixture(t, root, `owner.Executor.ExecContext(db,contextValue,build("UPDATE","catalog_browse_rows")); db.ExecContext(contextValue,build("UPDATE","catalog_browse_rows")); execute:=db.ExecContext; execute(contextValue,build("UPDATE","catalog_browse_rows"))`)
	groups := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		groups[rel] = append(groups[rel], f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loader := newDerivedTypeLoader(root, fset, groups)
	resolved := 0
	for _, file := range groups["bypass"] {
		info := loader.forFile(file)
		if info == nil {
			t.Fatal("fixture type information missing")
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			expected := -1
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fn.Sel.Name != "ExecContext" {
					return true
				}
				selection := info.Selections[fn]
				if selection == nil {
					t.Fatal("SQL method selection missing")
				}
				switch selection.Kind() {
				case types.MethodExpr:
					expected = 2
				case types.MethodVal:
					expected = 1
				default:
					t.Fatal(selection.Kind())
				}
			case *ast.Ident:
				if fn.Name == "execute" {
					expected = 1
				}
			}
			if expected >= 0 {
				if got := derivedSQLParameter(call.Fun, info); got != expected {
					t.Fatalf("signature SQL position %d, want %d", got, expected)
				}
				if value := info.Types[call.Args[expected-1]].Value; value == nil {
					t.Fatal("constant context witness not type checked")
				}
				if value := info.Types[call.Args[expected]].Value; value != nil {
					t.Fatal("dynamic SQL became constant")
				}
				resolved++
			}
			return true
		})
	}
	if resolved != 3 {
		t.Fatalf("resolved %d execution forms, want three", resolved)
	}
}

func TestDerivedWriterGuardUnresolvedOrAmbiguousSignatureIsUnknown(t *testing.T) {
	for _, source := range []string{
		`package bypass
import "context"
type executor interface{ExecContext(context.Context,string,string,...any)}
func build(v,t string)string{return v+" "+t+" SET title=?"}
func run(db executor,ctx context.Context){db.ExecContext(ctx,"constant tenant",build("UPDATE","catalog_browse_rows"))}`,
		`package bypass
func run(){missing.ExecContext("constant context",build("UPDATE","catalog_browse_rows"))}`,

		`package bypass
import "context"
type statement string
type executor interface{ExecContext(context.Context,string,statement,...any)}
func build(v,t string)string{return v+" "+t+" SET title=?"}
func run(db executor,ctx context.Context){db.ExecContext(ctx,"constant namespace",statement(build("UPDATE","catalog_browse_rows")))}`,
		`package bypass
import "context"
type statement string
type executor interface{ExecContext(context.Context,statement,...any)}
func run(db executor,ctx context.Context){db.ExecContext(ctx,statement(build("UPDATE","catalog_browse_rows")))}`,
	} {
		assertUnknownDerivedSQL(t, source)
	}
}

func TestDerivedWriterGuardBuildSpecificSignatureIsUnknown(t *testing.T) {
	root := t.TempDir()
	source := `package bypass
import "database/sql"
func run(db *sql.DB){db.Exec("SELECT 1")}`
	for _, name := range []string{"windows_windows.go", "tagged.go"} {
		text := source
		if name == "tagged.go" {
			text = "//go:build future_platform\n\n" + text
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"windows_windows.go", "tagged.go"} {
		if got.Dynamic[name].SHA256 == "" {
			t.Fatalf("conditional signature was guessed for %s", name)
		}
	}
}
