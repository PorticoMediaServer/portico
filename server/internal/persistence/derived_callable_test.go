package persistence

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDerivedWriterGuardReturnedReflectCallModule(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/census\n\ngo 1.26.10\n",
		"owner/a.go": `package owner;import("context";"database/sql");type Executor interface{ExecContext(context.Context,string,...any)(sql.Result,error)};const owned="UPDATE catalog_browse_rows SET title=?"`,
		"bridge/b.go": `package bridge
import "reflect"
type Value=reflect.Value
func NewCall(db any)func([]Value)[]Value{return reflect.ValueOf(db).MethodByName("ExecContext").Call}
func Args(values ...any)[]Value{out:=make([]Value,len(values));for i,v:=range values{out[i]=reflect.ValueOf(v)};return out}`,
		"bypass/b.go": `package bypass
import("example.com/census/owner";"example.com/census/bridge";"time")
var _ = bridge.Args
type constantContext string
func(constantContext)Deadline()(time.Time,bool){return time.Time{},false}
func(constantContext)Done()<-chan struct{}{return nil}
func(constantContext)Err()error{return nil}
func(constantContext)Value(any)any{return nil}
const harmless constantContext="SELECT 1"
func build(v,t string)string{return v+" "+t+" SET title='wrong'"}
func Run(db owner.Executor){invoke:=bridge.NewCall(db);invoke(bridge.Args(harmless,build("UPDATE","catalog_browse_rows")))}`,
	}
	for name, source := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-p", "1", "-run", "^$", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOTOOLCHAIN=local", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("module witness does not compile: %v\n%s", err, out)
	}
	callPath := filepath.Join(root, "bypass/b.go")
	afterSource, err := os.ReadFile(callPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource := strings.Replace(string(afterSource), `func Run(db owner.Executor){invoke:=bridge.NewCall(db);invoke(bridge.Args(harmless,build("UPDATE","catalog_browse_rows")))}`, `func Run(db owner.Executor){_ = db}`, 1)
	if beforeSource == string(afterSource) {
		t.Fatal("missing mutation target")
	}
	if err = os.WriteFile(callPath, []byte(beforeSource), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(callPath, afterSource, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if before.Dynamic["bypass/b.go"].SHA256 != "" {
		t.Fatal("inert consumer already has a callable pin")
	}
	t.Logf("whole census unchanged after adding reflective execution: %v", reflect.DeepEqual(before, got))
	t.Logf("module-aware scan: caller=%v bridge=%v unresolved=%v", got.Dynamic["bypass/b.go"].SHA256 != "", got.Dynamic["bridge/b.go"].SHA256 != "", got.UnresolvedReflection)
	if got.Dynamic["bypass/b.go"].SHA256 == "" {
		t.Error("module-aware reflection census missed callable returned by another package")
	}
}

func TestDerivedWriterGuardClosedCallableOriginsStayOrdinary(t *testing.T) {
	root := t.TempDir()
	source := `package ordinary;func direct(){};func generic[T any](value T){};func run(){first:=direct;second:=first;second();literal:=func(){};literal();values:=make([]int,0);values=append(values,1);_=len(values);generic(1);specific:=generic[int];specific(2)}`
	if err := os.WriteFile(filepath.Join(root, "ordinary.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, nil)
	if err != nil || len(got.Dynamic) != 0 {
		t.Fatal(got, err)
	}
}

func TestDerivedWriterGuardClosedCallablesInModule(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/ordinary\n\ngo 1.26.10\n",
		"ordinary.go": `package ordinary;func direct(){};func generic[T any](v T){};func run(){f:=direct;f();literal:=func(){};literal();items:=make([]int,0);items=append(items,1);_=len(items);generic(1);g:=generic[int];g(2)}`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDerivedWriters(root, nil)
	if err != nil || len(got.Dynamic) != 0 {
		t.Fatal(got, err)
	}
}

func TestDerivedWriterGuardOpaqueCallablesNeedPins(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/callables\n\ngo 1.26.10\n",
	}
	// Each consumer uses only ordinary Go function values. The sole reflective
	// origin lives in a different package; consumer spelling cannot identify it.
	files["provider/p.go"] = `package provider
import "reflect"
func Bound()func(){return reflect.ValueOf(func(){}).Interface().(func())}
func Made()any{return reflect.MakeFunc(reflect.TypeOf(func(){}),func([]reflect.Value)[]reflect.Value{return nil}).Interface()}
func Ordinary()func(){return func(){}}
func Label(v any)string{return reflect.TypeOf(v).String()}
func Replace(v any){reflect.ValueOf(v).Elem().Set(reflect.ValueOf(Bound()))}
type Named func()
func(Named)Error()string{return ""}
func Hidden()error{return Named(func(){})}
type Factory struct{}
func(Factory)New()func(){return Bound()}
type Promoted struct{Factory}
func(Promoted)Extra(){}
`
	files["runner/r.go"] = `package runner;type Callable func();func(f Callable)Invoke(){f()}`
	files["forward/f.go"] = `package forward;func Through(value any)func(){return value.(func())}`
	controls := map[string]string{
		"alias":               `f:=provider.Bound();alias:=f;alias()`,
		"assertion":           `f:=provider.Made().(func());f()`,
		"returned":            `f:=wrap();f()`,
		"global":              `saved=provider.Bound();saved()`,
		"field":               `h:=holder{invoke:provider.Bound()};h.invoke()`,
		"fieldwrite":          `h:=holder{};h.invoke=provider.Bound();h.invoke()`,
		"slice":               `items:=[]func(){provider.Bound()};items[0]()`,
		"mapwrite":            `items:=map[int]func(){};items[0]=provider.Bound();items[0]()`,
		"range":               `for _,f:=range []func(){provider.Bound()}{f()}`,
		"pointer":             `f:=func(){};p:=&f;*p=provider.Bound();f()`,
		"reassigned":          `f:=func(){};f=provider.Bound();f()`,
		"literalreturn":       `factory:=func()func(){return provider.Bound()};f:=factory();f()`,
		"callback":            `apply(provider.Bound())`,
		"typedcallback":       `var applyFn callback=apply;applyFn(provider.Bound())`,
		"boxedmethodcallback": `var boxed any=holder.apply;applyFn:=boxed.(func(holder,func()));applyFn(holder{},provider.Bound())`,
		"boxedfactory":        `var boxed any=wrap;factory:=boxed.(func()func());invoke:=factory();invoke()`,
		"boxedcallback":       `var boxed any=apply;applyFn:=boxed.(func(func()));applyFn(provider.Bound())`,
		"methodcallback":      `holder{}.apply(provider.Bound())`,
		"nonemptyinterface":   `f:=provider.Hidden().(provider.Named);f()`,
		"promotedfactory":     `var factory interface{New()func();Extra()}=provider.Promoted{};invoke:=factory.New();invoke()`,
		"no_call_forwarder":   `invoke:=forward.Through(provider.Bound());invoke()`,
		"interfacefactory":    `var factory interface{New()func()}=provider.Factory{};invoke:=factory.New();invoke()`,
		"tuplecallback":       `var f callback;f,_=pair();f(provider.Bound())`,
		"reflectset":          `f:=func(){};provider.Replace(&f);f()`,
		"tuplebool":           `f,_:=pairBool();f()`,
		"receiverinterface":   `var value interface{Invoke()}=runner.Callable(provider.Bound());invoke:=value.Invoke;invoke()`,
		"providerresult":      `f:=provider.Ordinary();f()`,
		"channel":             `ch:=make(chan func(),1);ch<-provider.Bound();f:=<-ch;f()`,
		"channelrange":        `ch:=make(chan func(),1);ch<-provider.Bound();close(ch);for f:=range ch{f()}`,
		"copy":                `src:=[]func(){provider.Bound()};dst:=make([]func(),1);copy(dst,src);dst[0]()`,
		"receiver":            `holder{invoke:provider.Bound()}.run()`,
		"methodexprreceiver":  `holder.run(holder{invoke:provider.Bound()})`,
	}
	for name, body := range controls {
		imports := `import "example.com/callables/provider"`
		if name == "receiverinterface" {
			imports += `
import "example.com/callables/runner"`
		}
		if name == "no_call_forwarder" {
			imports += `
import "example.com/callables/forward"`
		}
		applyBody := ""
		methodBody := ""
		runBody := ""
		switch name {
		case "callback", "typedcallback", "boxedcallback", "tuplecallback":
			applyBody = "f()"
		case "methodcallback", "boxedmethodcallback":
			methodBody = "f()"
		case "receiver", "methodexprreceiver":
			runBody = "h.invoke()"
		}
		files[name+"/c.go"] = `package consumer
` + imports + `
type holder struct{invoke func()};type callback func(func());var saved func()
func wrap()func(){return provider.Bound()}
func apply(f func()){` + applyBody + `}
func pair()(func(func()),error){return apply,nil}
func pairBool()(func(),bool){return provider.Bound(),true}
func(holder)apply(f func()){` + methodBody + `}
func(h holder)run(){` + runBody + `}
func Run(){` + body + `}
`
	}
	files["scalar/c.go"] = `package scalar
import "example.com/callables/provider"
func consume(string){};func Run(){consume(provider.Label(1))}`
	files["clean/c.go"] = `package clean
func direct(){};func Run(){f:=direct;f();literal:=func(){};alias:=literal;alias();values:=make([]int,0);values=append(values,1);_=len(values)}`

	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-p", "1", "-run", "^$", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOTOOLCHAIN=local", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("propagation module does not compile: %v\n%s", err, out)
	}
	got, err := scanDerivedWriters(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name := range controls {
		if got.Dynamic[name+"/c.go"].SHA256 == "" {
			t.Errorf("reflect callable lost through %s", name)
		}
	}
	if got.Dynamic["runner/r.go"].SHA256 == "" {
		t.Error("interface receiver lost its function origin")
	}
	if got.Dynamic["scalar/c.go"].SHA256 != "" {
		t.Error("reflected scalar became a reflected function")
	}
	if got.Dynamic["clean/c.go"].SHA256 != "" {
		t.Error("ordinary callback acquired a reflective origin")
	}
}
