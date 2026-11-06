package plan_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var record = flag.Bool("record-plans", false, "deliberately record reviewed plan baselines")

const predicate = "kind IN(1,2,3,4,10)"

func fixture(t *testing.T) (*sql.Conn, *stmt.Registry, plan.Fixture, *sql.DB) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal}
	db, startup, err := sqliteconn.Open(context.Background(), filepath.Join(t.TempDir(), "fixture.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture startup settings: %+v", startup)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	settings, err := sqliteconn.Assert(ctx, conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture acquired settings: %+v", settings)
	statements := []string{
		"CREATE TABLE items(id INTEGER PRIMARY KEY, library_id INTEGER NOT NULL, kind INTEGER NOT NULL, key INTEGER)",
		"CREATE INDEX item_sort ON items(library_id,kind,key,id) WHERE " + predicate,
		"INSERT INTO items VALUES(1,1,1,1080),(2,1,1,1080),(3,1,1,NULL),(4,1,1,2160)",
	}
	for _, s := range statements {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	definitions := []stmt.Definition{
		{ID: "sort.tail", SQL: "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + predicate + " AND key=? AND id>? ORDER BY key,id LIMIT ?", Owner: "plan-test", CallSites: []string{"plan_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "key", Type: stmt.Integer}, {Name: "after", Type: stmt.Integer}, {Name: "remaining", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "ties", Bindings: []any{int64(1), int64(1), int64(1080), int64(1), int64(2)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"item_sort"}}},
		{ID: "sort.next", SQL: "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + predicate + " AND key>? ORDER BY key,id LIMIT ?", Owner: "plan-test", CallSites: []string{"plan_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "key", Type: stmt.Integer}, {Name: "remaining", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "next-key", Bindings: []any{int64(1), int64(1), int64(1080), int64(2)}}, {Name: "empty-library", Bindings: []any{int64(2), int64(1), int64(1080), int64(2)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"item_sort"}}},
	}
	registry, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(strings.Join(statements, "\n")))
	f := plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(sum[:]), Statistics: plan.Unanalyzed, Implementations: "bundled modernc SQLite builtin functions/collations"}
	golden, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	return conn, registered(t, registry, golden), f, db
}
func TestPlanGoldenAndDeclaredStatistics(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	ctx := context.Background()
	for _, analyzed := range []bool{false, true} {
		name := "unanalyzed"
		if analyzed {
			if _, err := conn.ExecContext(ctx, "ANALYZE"); err != nil {
				t.Fatal(err)
			}
			f.Statistics = plan.Analyzed
			name = "analyzed"
		}
		golden, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testdata", name+".json")
		if *record {
			data, err := golden.JSON()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
		expected, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := golden.Compare(expected); err != nil {
			t.Fatal(err)
		}
		if len(golden.Plans) != 3 || len(golden.Plans[0].Explain.Values) == 0 {
			t.Fatal("cohort or full VM plans lost")
		}
		if err := golden.Compare(nil); err == nil {
			t.Fatal("missing golden accepted")
		}
	}
	reviewed, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	old, err := reviewed.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE sqlite_stat1 SET stat='999999 1'"); err != nil {
		t.Fatal(err)
	}
	changed, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := changed.Compare(old); err == nil {
		t.Fatal("changed statistics inherited old baseline")
	}
	// Unreviewed statistics cannot inherit the unanalyzed baseline.
	f.Statistics = plan.Unanalyzed
	if _, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t)); err == nil {
		t.Fatal("stale statistics accepted")
	}
}
func TestPlanRejectsMissingIndexAndComputedSort(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DROP INDEX item_sort"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t)); err == nil {
		t.Fatal("missing forced index accepted")
	}
	if _, err := conn.ExecContext(ctx, "CREATE INDEX item_sort ON items(library_id,kind,key,id) WHERE "+predicate); err != nil {
		t.Fatal(err)
	}
	definitions := []stmt.Definition{}
	for _, entry := range registry.Manifest() {
		d := entry.Definition
		d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + predicate + " AND key>? ORDER BY (key+0),id LIMIT ?"
		d.Params = []stmt.Param{{Name: "lib", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}, {Name: "key", Type: stmt.Integer}, {Name: "limit", Type: stmt.Integer}}
		d.Cohorts = []stmt.Cohort{{Name: "bad-sort", Bindings: []any{int64(1), int64(1), int64(1080), int64(2)}}}
		definitions = append(definitions, d)
	}
	bad, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, bad, f, indexPolicy(t)); err == nil {
		t.Fatal("computed temp sort accepted")
	}
}
func TestForeignKeyActualEnforcementAndParentCollation(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, s := range []string{"CREATE TABLE parent(code TEXT COLLATE NOCASE PRIMARY KEY)", "CREATE TABLE child(code TEXT REFERENCES parent(code))", "CREATE INDEX child_code ON child(code COLLATE NOCASE)"} {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans.Plans) != 1 || len(plans.Plans[0].Delete.Values) == 0 || len(plans.Plans[0].Update.Values) == 0 || len(plans.Plans[0].Lookup.Values) == 0 {
		t.Fatal("missing FK probes")
	}
	for _, s := range []string{"DROP INDEX child_code", "CREATE INDEX child_code ON child(code COLLATE BINARY)"} {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy); err == nil {
		t.Fatal("wrong parent collation accepted")
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy); err == nil {
		t.Fatal("FK OFF accepted")
	}
}

func TestPlanSharedPartialPredicates(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	policy, err := plan.ReadIndexPolicy([]byte(`{"indexes":[{"index":"item_sort","predicate":"kind IN(1,2,3,4,10)","choice":"repeat_exact_predicate"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := policy.Predicate("item_sort")
	if err != nil || value != predicate {
		t.Fatalf("predicate: %q %v", value, err)
	}
	if _, err := policy.Predicate("missing"); err == nil {
		t.Fatal("missing predicate accepted")
	}
	golden, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range registry.Manifest() {
		if err := policy.CheckPredicates(entry.Definition, golden.Environment.Schema); err != nil {
			t.Fatal(err)
		}
	}
	d := registry.Manifest()[0].Definition
	for _, sql := range []string{
		"SELECT id FROM items INDEXED BY item_sort WHERE library_id=? AND kind=1 ORDER BY key,id",
		"SELECT id FROM items INDEXED BY item_sort WHERE library_id=? /* AND " + predicate + " ORDER BY */ ORDER BY key,id",
		"SELECT 'AND " + predicate + " ORDER BY ' FROM items INDEXED BY item_sort WHERE library_id=? ORDER BY key,id",
	} {
		d.SQL = sql
		if err := policy.CheckPredicates(d, golden.Environment.Schema); err == nil {
			t.Fatalf("missing exact predicate accepted: %s", sql)
		}
	}
}

func indexPolicy(t *testing.T) plan.IndexPolicy {
	t.Helper()
	p, err := plan.ReadIndexPolicy([]byte(`{"indexes":[{"index":"item_sort","predicate":"kind IN(1,2,3,4,10)","choice":"repeat_exact_predicate"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanForcedIndexCannotBeQuotedOrCommented(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	if _, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t)); err != nil {
		t.Fatal(err)
	}
	original := registry.Manifest()[0].Definition
	for _, hint := range []string{" /* INDEXED BY item_sort */", ""} {
		d := original
		d.SQL = strings.Replace(d.SQL, " INDEXED BY item_sort", hint, 1)
		if hint == "" {
			d.SQL = strings.Replace(d.SQL, "SELECT id,key", "SELECT id,key,'INDEXED BY item_sort '", 1)
		}
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.Capture(context.Background(), conn, r, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "not forced") {
			t.Fatalf("unforced SQL accepted: %s: %v", d.SQL, err)
		}
	}
}

func TestPlanRejectsRowTupleOmittedLibraryAndReversedOrder(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	ctx := context.Background()
	baseline, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := baseline.JSON()
	if err != nil {
		t.Fatal(err)
	}
	// A tuple condition can still name an index; it must fail the cursor property.
	tail, err := registry.Lookup("sort.tail")
	if err != nil {
		t.Fatal(err)
	}
	d, err := tail.Definition()
	if err != nil {
		t.Fatal(err)
	}
	d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? AND " + predicate + " AND (key,id)>(?,?) ORDER BY key,id LIMIT ?"
	tuple, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, tuple, f, indexPolicy(t)); err == nil || !strings.Contains(err.Error(), "unapproved SQL lint") {
		t.Fatal("row tuple comparison accepted", err)
	}
	defs := []stmt.Definition{}
	for _, entry := range registry.Manifest() {
		d := entry.Definition
		d.SQL = strings.ReplaceAll(d.SQL, "library_id=?", "1=?")
		defs = append(defs, d)
	}
	bad, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, bad, f, indexPolicy(t)); err == nil {
		t.Fatal("omitted library seek accepted")
	}
	defs = nil
	for _, entry := range registry.Manifest() {
		d := entry.Definition
		d.SQL = strings.ReplaceAll(d.SQL, "ORDER BY key,id", "ORDER BY key DESC,id DESC")
		defs = append(defs, d)
	}
	reversed, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := plan.Capture(ctx, conn, reversed, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := changed.Compare(expected); err == nil {
		t.Fatal("reversed sort inherited the old baseline")
	}
}

func TestPlanConnectionPolicyEvidenceAndReplacement(t *testing.T) {
	conn, registry, f, db := fixture(t)
	ctx := context.Background()
	before, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if before.Environment.Connection.TempStore != 1 || before.Environment.Connection.Engine != "3.53.4" {
		t.Fatalf("missing connection receipt: %+v", before.Environment.Connection)
	}
	expected, err := before.JSON()
	if err != nil {
		t.Fatal(err)
	}
	// Deliberate drift: capture must reject the actual connection, not repair it
	// or accept values copied from an earlier settings receipt.
	if _, err := conn.ExecContext(ctx, "PRAGMA temp_store=MEMORY"); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t)); !errors.Is(err, sqliteconn.ErrPolicy) {
		t.Fatalf("drifted capture accepted: %v", err)
	}
	if _, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy); !errors.Is(err, sqliteconn.ErrPolicy) {
		t.Fatalf("drifted FK audit accepted: %v", err)
	}
	var actual int
	if err := conn.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&actual); err != nil || actual != 2 {
		t.Fatalf("capture repaired drift: %d %v", actual, err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	conn.Close()
	replacement, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	acquired, err := sqliteconn.Assert(ctx, replacement, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("replacement acquisition settings: %+v", acquired)
	after, err := plan.Capture(ctx, replacement, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if after.Environment.Connection != acquired {
		t.Fatal("replacement capture discarded acquisition evidence")
	}
	if err := after.Compare(expected); err != nil {
		t.Fatal(err)
	}
	audit, err := plan.ForeignKeys(ctx, replacement, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Connection != acquired {
		t.Fatal("FK audit discarded acquisition evidence")
	}
}

func registered(t *testing.T, catalogue *stmt.Registry, golden plan.Golden) *stmt.Registry {
	t.Helper()
	var definitions []stmt.Definition
	for _, e := range catalogue.Manifest() {
		definitions = append(definitions, e.Definition)
	}
	r, err := stmt.New(definitions, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
