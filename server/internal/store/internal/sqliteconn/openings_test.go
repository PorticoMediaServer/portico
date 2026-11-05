package sqliteconn

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

const openingStore = "github.com/PorticoMediaServer/portico/server/internal/store/"
const openingNative = "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"

type openingSite struct{ file, owner, object string }

// Exact objects AND enclosing function identities, including receivers.
// fs.FS.Open is not a database object and needs no telemetry-name exception.
var permittedOpenings = map[openingSite]bool{
	{"internal/sqliteconn/connection.go", openingStore + "internal/sqliteconn.Connector", openingNative + ".NewConnector"}:                                true,
	{"internal/sqliteconn/connection.go", openingStore + "internal/sqliteconn.Open", "database/sql.OpenDB"}:                                               true,
	{"internal/sqliteconn/limits.go", openingStore + "internal/sqliteconn.limitConnector.Connect", "database/sql/driver.Connector.Connect"}:               true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.countedConnector.Connect", "database/sql/driver.Connector.Connect"}:    true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestReplacementRestoresEveryConnectionSetting", "database/sql.OpenDB"}: true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestCancelledReadReopensWithPolicy", "database/sql.OpenDB"}:            true,
	{"internal/sqliteconn/limits_test.go", openingStore + "internal/sqliteconn.limitObserver.Connect", "database/sql/driver.Connector.Connect"}:           true,
	{"internal/sqliteconn/limits_test.go", openingStore + "internal/sqliteconn.limitedReader", "database/sql.OpenDB"}:                                     true,
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.dqsConnector.Connect", "database/sql/driver.Connector.Connect"}:               true,
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.TestDQSAssertRejectsFlagsAndMissingGetter", "database/sql.OpenDB"}:            true,
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.TestDQSAllRolesReplacementAndReaderGrowth", "database/sql.OpenDB"}:            true,
	// Sole intentionally enabled-DQS negative control, never a worker opener.
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.TestDQSActualEnabledDriverRefused", openingNative + ".NewConnector"}: true,
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.TestDQSActualEnabledDriverRefused", "database/sql.OpenDB"}:           true,
	{"writer/transaction.go", openingStore + "writer.writerConnector.Connect", "database/sql/driver.Connector.Connect"}:                          true,
	{"writer/transaction.go", openingStore + "writer.executor.openPool", "database/sql.OpenDB"}:                                                  true,
	{"writer/transaction_test.go", openingStore + "writer.faultConnector.Connect", "database/sql/driver.Connector.Connect"}:                      true,
	{"writer/transaction_test.go", openingStore + "writer.trackedNativeConnector.Connect", "database/sql/driver.Connector.Connect"}:              true,
	{"writer/transaction_test.go", openingStore + "writer.installFaultPool", "database/sql.OpenDB"}:                                              true, // held-source overlay
}

func openingObject(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	name := obj.Name()
	if f, ok := obj.(*types.Func); ok {
		if recv := f.Type().(*types.Signature).Recv(); recv != nil {
			t := types.Unalias(recv.Type())
			if ptr, ok := t.(*types.Pointer); ok {
				t = types.Unalias(ptr.Elem())
			}
			if n, ok := t.(*types.Named); ok {
				name = n.Obj().Name() + "." + name
			}
		}
	}
	return obj.Pkg().Path() + "." + name
}

func prohibitedOpening(obj types.Object) bool {
	key := openingObject(obj)
	switch key {
	case "database/sql.Open", "database/sql.OpenDB", "database/sql.Register",
		"database/sql/driver.Driver.Open", "database/sql/driver.DriverContext.OpenConnector", "database/sql/driver.Connector.Connect":
		return true
	}
	for _, pkg := range []string{openingNative, "modernc.org/sqlite"} {
		for _, name := range []string{"Driver", "NewDriver", "NewConnector", "Driver.Open", "Driver.OpenConnector"} {
			if key == pkg+"."+name {
				return true
			}
		}
	}
	// A local interface cannot hide the same driver method by renaming its
	// receiver type. Resolve the actual method set, not its printed spelling.
	if f, ok := obj.(*types.Func); ok {
		if recv := f.Type().(*types.Signature).Recv(); recv != nil {
			name := map[string]string{"Open": "Driver", "OpenConnector": "DriverContext", "Connect": "Connector"}[f.Name()]
			if p := openingLoad.imports["database/sql/driver"]; p != nil && name != "" {
				iface := p.Scope().Lookup(name).Type().Underlying().(*types.Interface)
				return types.Implements(recv.Type(), iface)
			}
		}
	}
	return false
}

// Inspect references, not just calls: captured methods and reflected Driver
// literals still resolve to these objects. Fully adversarial unsafe/linkname
// or foreign driver registration is out of scope; sql.Register is forbidden.
func forbiddenOpenings(file string, syntax *ast.File, info *types.Info) []string {
	var bad []string
	for id, obj := range info.Uses {
		if id.Pos() < syntax.Pos() || id.End() > syntax.End() || !prohibitedOpening(obj) {
			continue
		}
		owner := ""
		for _, decl := range syntax.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok && id.Pos() >= f.Pos() && id.End() <= f.End() {
				owner = openingObject(info.Defs[f.Name])
			}
		}
		site := openingSite{file, owner, openingObject(obj)}
		if !permittedOpenings[site] || site.object == "database/sql.Register" {
			bad = append(bad, fmt.Sprintf("%s#%s: %s bypasses shared opener", file, owner, site.object))
		}
	}
	sort.Strings(bad)
	return bad
}

type openingImports map[string]*types.Package

func (i openingImports) Import(path string) (*types.Package, error) {
	if p := i[path]; p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("opening fixture import not loaded: %s", path)
}

var openingLoad struct {
	sync.Once
	packages []*packages.Package
	imports  openingImports
	root     string
	err      error
}

func loadOpeningPackages(t *testing.T) ([]*packages.Package, openingImports, string) {
	t.Helper()
	openingLoad.Do(func() {
		openingLoad.root, openingLoad.err = filepath.Abs("../..")
		if openingLoad.err != nil {
			return
		}
		openingLoad.packages, openingLoad.err = packages.Load(&packages.Config{
			Dir: filepath.Join(openingLoad.root, "../.."), Tests: true,
			BuildFlags: []string{"-p=1"},
			Mode:       packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		}, "./internal/store/...")
		openingLoad.imports = openingImports{}
		var add func(*types.Package)
		add = func(p *types.Package) {
			if p == nil || openingLoad.imports[p.Path()] != nil {
				return
			}
			openingLoad.imports[p.Path()] = p
			for _, i := range p.Imports() {
				add(i)
			}
		}
		for _, p := range openingLoad.packages {
			for _, e := range p.Errors {
				openingLoad.err = fmt.Errorf("%v; %s", openingLoad.err, e)
			}
			add(p.Types)
		}
	})
	if openingLoad.err != nil {
		t.Fatal(openingLoad.err)
	}
	return openingLoad.packages, openingLoad.imports, openingLoad.root
}

func TestStoreOpeningsUseSharedPolicy(t *testing.T) {
	pkgs, _, root := loadOpeningPackages(t)
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			rel, err := filepath.Rel(root, p.Fset.Position(f.Pos()).Filename)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			} // generated test-main is outside store
			for _, bad := range forbiddenOpenings(filepath.ToSlash(rel), f, p.TypesInfo) {
				t.Error(bad)
			}
		}
	}
}

func TestStoreOpeningsRejectTypedBypasses(t *testing.T) {
	_, imports, _ := loadOpeningPackages(t)
	for _, c := range []struct {
		name, file, pkg, source string
		want                    int
	}{
		{"shadow_and_method_value", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn; import "database/sql/driver"; func bypass(driver driver.Driver){_,_=driver.Open(""); _=driver.Open}`, 2},
		{"local_driver_interface", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import "database/sql/driver";type local interface{Open(string)(driver.Conn,error)};func bypass(d local){_=d.Open}`, 1},
		{"telemetry_sql_and_wrong_receiver", "qual/host.go", openingStore + "qual", `package qual; import "database/sql"; func recordFile(){_,_=sql.Open("sqlite","")}; func telemetryDirectory(){_=sql.Open}; type unrelated struct{};func(unrelated)Open(){_=sql.Open}`, 3},
		{"reflection", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import("reflect";sqlite "` + openingNative + `");func bypass(){reflect.ValueOf(&sqlite.Driver{}).MethodByName("Open")}`, 1},
		{"wrong_receiver_owner", "writer/transaction.go", openingStore + "writer", `package writer;import "database/sql";type unrelated struct{};func(unrelated)openPool(){_=sql.OpenDB}`, 1},
		{"register_and_function_values", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import s "database/sql";var registration=s.Register;var open=s.Open;var pool=s.OpenDB`, 3},
		{"filesystem_is_not_a_database", "qual/host.go", openingStore + "qual", `package qual;import "io/fs";func recordFile(root fs.FS){_,_=root.Open("file")}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, c.file, c.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
			if _, err := (&types.Config{Importer: imports}).Check(c.pkg, fset, []*ast.File{f}, info); err != nil {
				t.Fatal("fixture must type-check", err)
			}
			bad := forbiddenOpenings(c.file, f, info)
			if len(bad) != c.want {
				t.Fatalf("got%d want%d: %v", len(bad), c.want, bad)
			}
		})
	}
}
