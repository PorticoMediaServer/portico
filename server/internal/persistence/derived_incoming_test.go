package persistence

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The consumer files contain no reflect import or SQL. Each compiled wrapper
// invokes the same reflected SQLite UPDATE, then must get its own source pin.
func TestDerivedWriterGuardIncomingAndGenericModule(t *testing.T) {
	root := t.TempDir()
	bodies := map[string]string{
		"parameter_later":  `func Run(f func()){f();f=func(){}}`,
		"parameter_branch": `func Run(f func(),clean bool){if clean{f=func(){}};f()}`,
		"parameter_alias":  `func Run(f func()){alias:=f;f=func(){};alias()}`,
		"receiver":         `type Runner func();func(f Runner)Call(){f();f=func(){}};func Run(f func()){Runner(f).Call()}`,
		"capture":          `func Run(f func()){invoke:=func(){f()};invoke();f=func(){}}`,
		"captured_alias":   `func Run(f func()){alias:=f;f=func(){};func(){alias();alias=func(){}}()}`,
		"generic_inline":   `func Run[T ~func()](f T){f()}`,
		"generic_named":    `type Callable interface{~func()};func Run[T Callable](f T){f()}`,
	}
	files := map[string]string{
		"go.mod":             "module example.com/incoming\n\ngo 1.26.10\n\nrequire modernc.org/sqlite v1.58.0\n",
		"owner/a.go":         `package owner;const owned="UPDATE catalog_browse_rows SET title=?"`,
		"generic_local/c.go": `package consumer;func Run[T ~func()](){local:=T(func(){});local()}`,
		"bridge/b.go": `package bridge
import("context";"database/sql";"reflect")
func build(verb,table string)string{return verb+" "+table+" SET title='wrong'"}
func Writer(db *sql.DB)func(){return reflect.MakeFunc(reflect.TypeFor[func()](),func([]reflect.Value)[]reflect.Value{
 result:=reflect.ValueOf(db).MethodByName("ExecContext").Call([]reflect.Value{reflect.ValueOf(context.Background()),reflect.ValueOf(build("UPDATE","catalog_browse_rows"))})
 if !result[1].IsNil(){panic(result[1].Interface())};return nil
}).Interface().(func())}`,
	}
	var imports, cases strings.Builder
	for name, body := range bodies {
		files[name+"/c.go"] = "package consumer\n" + body
		fmt.Fprintf(&imports, "%s %q\n", name, "example.com/incoming/"+name)
		args := "writer"
		if name == "parameter_branch" {
			args += ",false"
		}
		fmt.Fprintf(&cases, "{%q,func(){%s.Run(%s)}},\n", name, name, args)
	}
	template, err := os.ReadFile("testdata/derived_incoming_runner.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	files["witness_test.go"] = strings.NewReplacer("{{imports}}", imports.String(), "{{cases}}", cases.String()).Replace(string(template))

	sum, err := os.ReadFile("../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	files["go.sum"] = string(sum)
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-mod=mod", "-p", "1", "-run", "^TestRun$", "-v", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOTOOLCHAIN=local", "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiled SQLite witnesses failed: %v\n%s", err, output)
	}
	t.Logf("compiled SQLite witnesses:\n%s", output)
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	for name := range bodies {
		if got.Dynamic[name+"/c.go"].SHA256 == "" {
			t.Errorf("compiled SQLite consumer has no source pin: %s", name)
		}
	}
	if got.Dynamic["generic_local/c.go"].SHA256 == "" {
		t.Error("a type-parameter call was accepted as a closed ordinary alias")
	}
}
