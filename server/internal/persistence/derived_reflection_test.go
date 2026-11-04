package persistence

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This is the reviewer's executable witness: the SQL method's name appears
// only inside a string, while the verb/table builder has no complete DML literal.
func TestDerivedWriterGuardReflectiveSQLiteExecution(t *testing.T) {
	for _, form := range []string{"named Call", "indexed Method", "CallSlice", "function Interface", "executor Interface", "captured Call", "interface Call", "returned Call"} {
		t.Run(form, func(t *testing.T) {
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
			const harmless derivedStringContext = "SELECT 1"
			build := func(verb, table string) string { return verb + " " + table + " SET title='wrong'" }
			query := build("UPDATE", "catalog_browse_rows")
			value := reflect.ValueOf(executor)
			method := value.MethodByName("ExecContext")
			args := []reflect.Value{reflect.ValueOf(harmless), reflect.ValueOf(query)}
			var result []reflect.Value
			switch form {
			case "named Call":
				result = reflect.ValueOf(executor).MethodByName("ExecContext").Call(args)
			case "indexed Method":
				m, ok := value.Type().MethodByName("ExecContext")
				if !ok {
					t.Fatal("executor method missing")
				}
				result = value.Method(m.Index).Call(args)
			case "CallSlice":
				result = method.CallSlice(append(args, reflect.ValueOf([]any{})))
			case "function Interface":
				execute := method.Interface().(func(context.Context, string, ...any) (sql.Result, error))
				_, err = execute(harmless, query)
			case "executor Interface":
				execute := value.Interface().(derivedSignatureExecutor)
				_, err = execute.ExecContext(harmless, query)
			case "captured Call":
				call := reflect.Value.Call
				result = call(method, args)
			case "returned Call":
				provider := func(db any) func([]reflect.Value) []reflect.Value {
					return reflect.ValueOf(db).MethodByName("ExecContext").Call
				}
				invoke := provider(executor)
				result = invoke(args)
			case "interface Call":
				var call interface {
					Call([]reflect.Value) []reflect.Value
				} = method
				result = call.Call(args)
			}
			if err != nil || (len(result) != 0 && !result[1].IsNil()) {
				t.Fatal("reflective UPDATE failed", err, result)
			}
			var title string
			if err = db.QueryRow(`SELECT title FROM catalog_browse_rows`).Scan(&title); err != nil || title != "wrong" {
				t.Fatal(title, err)
			}

			root := t.TempDir()
			calls := map[string]string{
				"named Call":         `reflect.ValueOf(db).MethodByName("ExecContext").Call([]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})`,
				"indexed Method":     `value:=reflect.ValueOf(db); method,_:=value.Type().MethodByName("ExecContext");value.Method(method.Index).Call([]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})`,
				"CallSlice":          `reflect.ValueOf(db).MethodByName("ExecContext").CallSlice([]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows")),reflect.ValueOf([]any{})})`,
				"function Interface": `execute:=reflect.ValueOf(db).MethodByName("ExecContext").Interface().(func(context.Context,string,...any)(sql.Result,error));execute(harmless,build("UPDATE","catalog_browse_rows"))`,
				"executor Interface": `execute:=reflect.ValueOf(db).Interface().(owner.Executor);execute.ExecContext(harmless,build("UPDATE","catalog_browse_rows"))`,
				"returned Call":      `provider:=func(db any)func([]reflect.Value)[]reflect.Value{return reflect.ValueOf(db).MethodByName("ExecContext").Call};invoke:=provider(db);invoke([]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})`,
				"interface Call":     `var call interface{Call([]reflect.Value)[]reflect.Value}=reflect.ValueOf(db).MethodByName("ExecContext");call.Call([]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})`,
				"captured Call":      `call:=reflect.Value.Call;call(reflect.ValueOf(db).MethodByName("ExecContext"),[]reflect.Value{reflect.ValueOf(harmless),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})`,
			}
			writeDerivedSignatureFixture(t, root, calls[form])
			path := filepath.Join(root, "bypass", "b.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			source = replaceDerivedReflectionFixture(source)
			if err = os.WriteFile(path, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			assertDerivedReflectionPin(t, root, "bypass/b.go")
		})
	}
}

func assertDerivedReflectionPin(t *testing.T, root, file string) writerCensus {
	t.Helper()
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dynamic[file].SHA256 == "" {
		t.Fatalf("reflective SQL execution disappeared from the census: %+v", got)
	}
	// Merely recording a hash does not supply the mandatory reviewed effect pin.
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("reflection passed without an explicit effect pin")
	}
	pinUnknownDerivedSQL(&got)
	if !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"owner"}) {
		t.Fatal(got.Tables)
	}
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("reflective writer bypassed migrated ownership")
	}
	return got
}

func replaceDerivedReflectionFixture(source string) string {
	source = strings.Replace(source, `import("owner";"time")`, `import("owner";"time";"reflect";"context";"database/sql")
var _ context.Context
var _ sql.Result`, 1)
	source = strings.Replace(source, `const contextValue constantContext="constant context"`, `const harmless constantContext="SELECT 1"`, 1)
	source = strings.Replace(source, `SET title=?`, `SET title='wrong'`, 1)
	return source
}

// The call-site file need not import reflect or have usable type information.
// Requiring its own pin prevents an upstream import pin from hiding that escape.
func TestDerivedWriterGuardReflectionWithoutImport(t *testing.T) {
	for _, body := range []string{
		`var value bridge.Value; value.Call(nil)`,
		`var value bridge.Caller;value.Call(nil)`,
		`var value bridge.ExtendedCaller;value.Call(nil)`,
		`var value bridge.Caller;bridge.Caller.Call(value,nil)`,
		`value:=bridge.NewValue(db);value.MethodByName("ExecContext").Call(nil)`,
		`typ:=bridge.TypeOf(db);typ.MethodByName("ExecContext")`,
		`var value bridge.Value; value.CallSlice(nil)`,
		`var value bridge.Value; value.Method(0)`,
		`var value bridge.Value; value.MethodByName("ExecContext")`,
		`var value bridge.Value; execute:=value.Interface().(bridge.Exec);execute(nil,build("UPDATE","catalog_browse_rows"))`,
		`var value bridge.Value; execute:=value.Interface().(owner.Executor);execute.ExecContext(nil,build("UPDATE","catalog_browse_rows"))`,
		`var value bridge.Value; bridge.Value.Call(value,nil)`,
		`call:=bridge.Value.Call; var value bridge.Value;call(value,nil)`,
		`var value bridge.Value; call:=value.Call;call(nil)`,
		`var value bridge.Value; value.Elem().Type().Method(0)`,
		`var value bridge.Value; value.MapRange().Next()`,
		`missing.MethodByName("ExecContext").Call(nil)`,
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeDerivedSignatureFixture(t, root, body)
			path := filepath.Join(root, "bypass", "b.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(string(data), `import("owner";"time")`, `import("owner";"time";"bridge")
var _ bridge.Value`, 1)
			if err = os.WriteFile(path, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			bridge := filepath.Join(root, "bridge")
			if err = os.MkdirAll(bridge, 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(bridge, "values.go"), []byte(`package bridge
import("reflect";"context";"database/sql")
type Value = reflect.Value
type Caller interface{Call([]Value)[]Value}
type ExtendedCaller interface{Caller;Extra()}
type Exec = func(context.Context,string,...any)(sql.Result,error)
func NewValue(value any)reflect.Value{return reflect.ValueOf(value)}
func TypeOf(value any)reflect.Type{return reflect.TypeOf(value)}
`), 0644); err != nil {
				t.Fatal(err)
			}
			got := assertDerivedReflectionPin(t, root, "bypass/b.go")
			if strings.HasPrefix(body, "missing.") {
				if len(got.UnresolvedReflection) == 0 {
					t.Fatal("missing-type fallback was not diagnosed")
				}
			} else if len(got.UnresolvedReflection) != 0 {
				t.Fatal("valid no-import controls must resolve by type", got.UnresolvedReflection)
			}
		})
	}
}

func TestDerivedWriterGuardReflectionImportIsConservative(t *testing.T) {
	for _, source := range []string{
		`package bypass; import "reflect"; func run(){reflect.DeepEqual(1,1)}`,
		`package bypass; import r "reflect"; var valueOf=r.ValueOf`,
		`package bypass; import . "reflect"; var valueOf=ValueOf`,
		`package bypass; import _ "reflect"`,
		"//go:build future_platform\n\npackage bypass; import r `reflect`; var valueOf=r.ValueOf",
	} {
		assertUnknownDerivedSQL(t, source)
	}
}

func TestDerivedWriterGuardReflectionTestCodeIsNotProduction(t *testing.T) {
	root := t.TempDir()
	writeDerivedSignatureFixture(t, root, "")
	if err := os.WriteFile(filepath.Join(root, "bypass", "reflection_test.go"), []byte(`package bypass; import "reflect"; func test(){reflect.ValueOf(nil).Call(nil)}`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil || len(got.Dynamic) != 0 {
		t.Fatal(got, err)
	}
}

func TestDerivedWriterGuardOrdinaryMethodsAreNotReflection(t *testing.T) {
	root := t.TempDir()
	source := `package ordinary
import("os";"errors")
func run(err error,f *os.File){_ = err.Error(); _ = f.Close(); _ = errors.New("ordinary").Error()}
`
	if err := os.WriteFile(filepath.Join(root, "ordinary.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil || len(got.Dynamic) != 0 {
		t.Fatalf("ordinary Error/Close methods were treated as reflection: %+v, %v", got, err)
	}
}

func TestDerivedWriterGuardUnresolvedReflectionCannotBePinnedAway(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "unknown.go"), []byte(`package unknown; func run(){missing.MethodByName("ExecContext").Call(nil)}`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	pinUnknownDerivedSQL(&got)
	if len(got.UnresolvedReflection) == 0 || len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("a wildcard pin waived unresolved reflection types", got)
	}
}

func TestDerivedWriterGuardUncoveredBuildCannotBeIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/guard\n\ngo 1.26.10\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "future.go"), []byte("//go:build future_guard_profile\n\npackage future\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanDerivedWriters(root, nil); err == nil || !strings.Contains(err.Error(), "no reflection type-check build covers") {
		t.Fatal("uncovered production build silently omitted", err)
	}
}
