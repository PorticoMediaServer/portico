package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Test-binary memo for native plan/FK captures. A capture is a deterministic
// function of the schema rows (including rootpages), statistics tables, actual
// connection settings, engine, statement manifest, fixture metadata and index
// policy. All of those inputs are fingerprinted from the LIVE connection on
// every call, so a clone with any difference captures afresh. Only identical
// inputs reuse the earlier, equally native, result. Production never sees this.
var captureMemo = struct {
	sync.Mutex
	golden map[[32]byte]plan.Golden
	fk     map[[32]byte]plan.ForeignKeyAudit
}{golden: map[[32]byte]plan.Golden{}, fk: map[[32]byte]plan.ForeignKeyAudit{}}

func captureInputs(ctx context.Context, conn *sql.Conn, policy sqliteconn.Config, parts ...any) ([32]byte, error) {
	settings, err := sqliteconn.Assert(ctx, conn, policy)
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%#v\n%#v\n", policy, settings)
	for _, q := range []string{
		"SELECT sqlite_version(),sqlite_source_id()",
		"SELECT type,name,tbl_name,rootpage,quote(sql) FROM sqlite_schema ORDER BY type,name",
	} {
		if err := hashRows(ctx, conn, h, q); err != nil {
			return [32]byte{}, err
		}
	}
	for _, stat := range []string{"sqlite_stat1", "sqlite_stat4"} {
		var n int
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name=?", stat).Scan(&n); err != nil {
			return [32]byte{}, err
		}
		if n > 0 {
			if err := hashRows(ctx, conn, h, "SELECT * FROM "+stat+" ORDER BY 1,2"); err != nil {
				return [32]byte{}, err
			}
		}
	}
	for _, p := range parts {
		fmt.Fprintf(h, "%#v\n", p)
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key, nil
}

func hashRows(ctx context.Context, conn *sql.Conn, h interface{ Write([]byte) (int, error) }, q string) error {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		fmt.Fprintf(h, "%#v\n", vals)
	}
	return rows.Err()
}

func cachedCapture(ctx context.Context, conn *sql.Conn, registry *stmt.Registry, fixture plan.Fixture, policy plan.IndexPolicy) (plan.Golden, error) {
	// FTS native audits may read row data; those captures are never memoized.
	for _, entry := range registry.Manifest() {
		if entry.Plan.NativeRead != nil || entry.Plan.NativeInsert != "" || entry.Plan.NativeRetained != "" {
			return plan.Capture(ctx, conn, registry, fixture, policy)
		}
	}
	key, err := captureInputs(ctx, conn, fixture.ConnectionPolicy, registry.Manifest(), fixture, policy)
	if err != nil {
		return plan.Golden{}, err
	}
	captureMemo.Lock()
	g, ok := captureMemo.golden[key]
	captureMemo.Unlock()
	if ok {
		return deepCopy(g), nil
	}
	g, err = plan.Capture(ctx, conn, registry, fixture, policy)
	if err == nil {
		captureMemo.Lock()
		captureMemo.golden[key] = deepCopy(g)
		captureMemo.Unlock()
	}
	return g, err
}

func cachedForeignKeys(ctx context.Context, conn *sql.Conn, policy sqliteconn.Config) (plan.ForeignKeyAudit, error) {
	key, err := captureInputs(ctx, conn, policy, "foreign-keys")
	if err != nil {
		return plan.ForeignKeyAudit{}, err
	}
	captureMemo.Lock()
	fk, ok := captureMemo.fk[key]
	captureMemo.Unlock()
	if ok {
		return deepCopy(fk), nil
	}
	fk, err = plan.ForeignKeys(ctx, conn, policy)
	if err == nil {
		captureMemo.Lock()
		captureMemo.fk[key] = deepCopy(fk)
		captureMemo.Unlock()
	}
	return fk, err
}

// deepCopy gives each caller private slices, maps, pointers and interfaces, so
// a test that edits its returned capture can never poison a later test. The
// unexported sealed receipt is immutable and stays shared.
func deepCopy[T any](v T) T {
	return deepCopyValue(reflect.ValueOf(&v).Elem()).Interface().(T)
}

func deepCopyValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(deepCopyValue(v.Elem()))
		return n
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		n := reflect.New(v.Type()).Elem()
		n.Set(deepCopyValue(v.Elem()))
		return n
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			n.Index(i).Set(deepCopyValue(v.Index(i)))
		}
		return n
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		n := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, k := range v.MapKeys() {
			n.SetMapIndex(k, deepCopyValue(v.MapIndex(k)))
		}
		return n
	case reflect.Struct:
		n := reflect.New(v.Type()).Elem()
		n.Set(v)
		for i := 0; i < n.NumField(); i++ {
			if n.Field(i).CanSet() {
				n.Field(i).Set(deepCopyValue(v.Field(i)))
			}
		}
		return n
	default:
		return v
	}
}

// Same contract as nativeFixtureRegistry (native_registry_test.go), whose bytes
// are hash-pinned by recorded Basic goldens and so stay untouched; unpinned
// callers use this memoized twin.
func cachedNativeFixtureRegistry(t *testing.T, conn *sql.Conn, policy sqliteconn.Config, defs []stmt.Definition, sources ...string) *stmt.Registry {
	t.Helper()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range sources {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "owned native fixture registration setup only; no resource or domain admission qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("registration executed fixture effects", before, after, err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// Memoized twin of nativeInventoryFixtureRegistry for unpinned callers.
func cachedNativeInventoryFixtureRegistry(t *testing.T, w *executor, defs []stmt.Definition, sources ...string) *stmt.Registry {
	t.Helper()
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Called during FULL startup setup, before replacement/cancellation wrappers.
	return cachedNativeFixtureRegistry(t, conn, w.config, defs, sources...)
}

func TestCaptureMemoReturnsPrivateCopies(t *testing.T) {
	type inner struct{ Rows [][]any }
	type outer struct {
		Plans []inner
		Ptr   *inner
		M     map[string][]string
		Any   any
	}
	orig := outer{Plans: []inner{{Rows: [][]any{{int64(1), "a"}}}}, Ptr: &inner{Rows: [][]any{{"p"}}}, M: map[string][]string{"k": {"v"}}, Any: []int{1}}
	c := deepCopy(orig)
	c.Plans[0].Rows[0][0] = int64(9)
	c.Ptr.Rows[0][0] = "x"
	c.M["k"][0] = "y"
	c.Any.([]int)[0] = 7
	if orig.Plans[0].Rows[0][0] != int64(1) || orig.Ptr.Rows[0][0] != "p" || orig.M["k"][0] != "v" || orig.Any.([]int)[0] != 1 {
		t.Fatal("deep copy shared mutable state", orig)
	}
}
