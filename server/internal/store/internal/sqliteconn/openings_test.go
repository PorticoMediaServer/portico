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
	// QL's reviewed real wrapper, explicit raw negative and readback-only fakes.
	{"qual/connection_test.go", openingStore + "qual.probeConnector.Connect", "database/sql/driver.Connector.Connect"}:                                    true,
	{"qual/connection_test.go", openingStore + "qual.connectionPool", "database/sql.OpenDB"}:                                                              true,
	{"qual/connection_test.go", openingStore + "qual.TestConnectionOneTimePolicyCancellationIsInvalid", openingNative + ".NewConnector"}:                  true,
	{"qual/connection_test.go", openingStore + "qual.TestConnectionReceiptRejectsDifferentEngine", "database/sql.OpenDB"}:                                 true,
	{"qual/connection_test.go", openingStore + "qual.TestConnectionReceiptPartialReadIsNull", "database/sql.OpenDB"}:                                      true,
	{"qual/connection_test.go", openingStore + "qual.evidenceScript.Open", openingStore + "qual.evidenceScript.Connect"}:                                  true,
	{"qual/connection_limits_test.go", openingStore + "qual.TestConnectionLateEnforcementReadFailureIsIncomplete", "database/sql.OpenDB"}:                 true,
	{"qual/connection_test.go", openingStore + "qual.TestConnectionReceiptPoolGrowthAndReplacement", "database/sql.Conn.Raw"}:                             true,
	{"qual/connection_limits_test.go", openingStore + "qual.TestConnectionFixedLimitDriftIsInvalidWithoutRepair", "database/sql.Conn.Raw"}:                true,
	{"qual/connection_limits_test.go", openingStore + "qual.TestConnectionFixedLimitsAfterCancellationAndReplacement", "database/sql.Conn.Raw"}:           true,
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
	// Raw is confined to exact discard-only controls and the frame's discard.
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestReplacementRestoresEveryConnectionSetting", "database/sql.Conn.Raw"}:        true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestCancelledReadReopensWithPolicy", "database/sql.Conn.Raw"}:                   true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestReadersAndCheckpointerSharePolicy", "database/sql.Conn.Raw"}:                true,
	{"internal/sqliteconn/connection_test.go", openingStore + "internal/sqliteconn.TestReaderPoolGrowthAssertsEveryPhysicalConnection", "database/sql.Conn.Raw"}:   true,
	{"internal/sqliteconn/dqs_test.go", openingStore + "internal/sqliteconn.TestDQSAllRolesReplacementAndReaderGrowth", "database/sql.Conn.Raw"}:                   true,
	{"internal/sqliteconn/enforcement_test.go", openingStore + "internal/sqliteconn_test.TestEnforcementOnReplacementPhysicalConnection", "database/sql.Conn.Raw"}: true,
	{"internal/sqliteconn/limits_test.go", openingStore + "internal/sqliteconn.TestReadLimitsGrowthDriftAndReplacement", "database/sql.Conn.Raw"}:                  true,
	{"stmt/plan/plan_test.go", openingStore + "stmt/plan_test.TestPlanConnectionPolicyEvidenceAndReplacement", "database/sql.Conn.Raw"}:                            true,
	{"stmt/lifetime_test.go", openingStore + "stmt_test.TestRegistryReaderCleanupReleasesWAL", "database/sql.Conn.Raw"}:                                            true,
	{"writer/transaction.go", openingStore + "writer.transaction.finish", "database/sql.Conn.Raw"}:                                                                 true,
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

func openingImport(p *types.Package, path string, seen map[*types.Package]bool) *types.Package {
	if p == nil || seen[p] {
		return nil
	}
	seen[p] = true
	if p.Path() == path {
		return p
	}
	for _, dep := range p.Imports() {
		if found := openingImport(dep, path, seen); found != nil {
			return found
		}
	}
	return nil
}

func prohibitedOpening(obj types.Object) bool {
	key := openingObject(obj)
	switch key {
	case "database/sql.Open", "database/sql.OpenDB", "database/sql.Register",
		"database/sql.DB.Driver", "database/sql.Conn.Raw", "database/sql.Drivers",
		"database/sql/driver.Driver.Open", "database/sql/driver.DriverContext.OpenConnector", "database/sql/driver.Connector.Connect", "database/sql/driver.Connector.Driver",
		"reflect.Value.Method", "reflect.Value.MethodByName", "reflect.Type.Method", "reflect.Type.MethodByName":
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
			if p := openingImport(obj.Pkg(), "database/sql/driver", map[*types.Package]bool{}); p != nil && name != "" {
				iface := p.Scope().Lookup(name).Type().Underlying().(*types.Interface)
				return types.Implements(recv.Type(), iface)
			}
		}
	}
	return false
}

// Inspect references, not just calls: captured methods and reflected Driver
// literals still resolve to these objects. Fully adversarial unsafe, linkname,
// cgo and assembly are residuals outside this source guard. sql.Register and
// driver extraction/reflected method dispatch are forbidden here.
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
		openingLoad.packages, openingLoad.err = loadOpeningSourceUniverse(openingLoad.root)
		if openingLoad.err != nil {
			return
		}
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
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			if syntheticOpeningTestMain(p) {
				continue
			}
			rel, err := filepath.Rel(root, p.Fset.PositionFor(f.Pos(), false).Filename)
			if err != nil || !filepath.IsLocal(rel) {
				t.Fatalf("unmapped store syntax %s: %v", p.ID, err)
			}
			for _, bad := range forbiddenOpenings(filepath.ToSlash(rel), f, p.TypesInfo) {
				if !seen[bad] {
					t.Error(bad)
					seen[bad] = true
				}
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
		{"reflection", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import("reflect";sqlite "` + openingNative + `");func bypass(){reflect.ValueOf(&sqlite.Driver{}).MethodByName("Open")}`, 2},
		{"existing_pool_reflection", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import("database/sql";"reflect");func bypass(db *sql.DB){reflect.ValueOf(db.Driver()).MethodByName("Open")}`, 2},
		{"reflected_pool_extraction", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import("database/sql";"reflect");func bypass(db *sql.DB){reflect.ValueOf(db).MethodByName("Driver")}`, 1},
		{"raw_connection", "internal/sqliteconn/probe_test.go", openingStore + "internal/sqliteconn", `package sqliteconn;import "database/sql";func bypass(c *sql.Conn){_=c.Raw}`, 1},
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
