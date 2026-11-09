package plan_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

var recordAutoindexPlans = flag.Bool("record-autoindex-plans", false, "deliberately record certified autoindex fixture plans")

func autoindexFixture(t *testing.T, analyzed bool) (*sql.Conn, *sql.Conn, plan.Fixture) {
	t.Helper()
	writer, _, f, _ := fixture(t)
	setup := []string{
		"CREATE TABLE auto_pk(id TEXT PRIMARY KEY,code TEXT COLLATE NOCASE UNIQUE,payload TEXT NOT NULL)",
		"CREATE TABLE auto_wr(a TEXT,b TEXT,value TEXT,PRIMARY KEY(a,b)) WITHOUT ROWID",
		"CREATE TABLE auto_unique(id INTEGER PRIMARY KEY,a TEXT COLLATE NOCASE,b TEXT,UNIQUE(a DESC,b))",
		"INSERT INTO auto_pk VALUES('one','Alpha','One'),('two','Beta','Two')",
		"INSERT INTO auto_wr VALUES('one','x','V1'),('two','y','V2')",
		"INSERT INTO auto_unique VALUES(1,'Alpha','x'),(2,'Beta','y')",
	}
	for _, s := range setup {
		if _, err := writer.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	state := plan.Unanalyzed
	if analyzed {
		if _, err := writer.ExecContext(t.Context(), "ANALYZE"); err != nil {
			t.Fatal(err)
		}
		state = plan.Analyzed
	}
	var seq int64
	var name, path string
	if err := writer.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	policy := f.ConnectionPolicy
	policy.Role = sqliteconn.Reader
	policy.ReadLimits = sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 129}
	db, settings, err := sqliteconn.Open(t.Context(), path, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reader, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	actual, err := sqliteconn.Assert(t.Context(), reader, policy)
	if err != nil {
		t.Fatal(err)
	}
	if settings.QueryOnly != 1 || actual.QueryOnly != 1 || actual.TempStore != 1 {
		t.Fatal("not an actual fixed Reader", settings, actual)
	}
	t.Logf("autoindex actual Reader settings: %+v", actual)
	sum := sha256.Sum256([]byte(f.SHA256 + strings.Join(setup, "\n") + string(state)))
	return reader, writer, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(sum[:]), Statistics: state, Implementations: "bundled SQLite3.53.4; tiny two-row main PK/UNIQUE fixtures; actual fixed Reader C129/L65536; compile and point-read controls only"}
}

func autoindexDefinitions() []stmt.Definition {
	pk := stmt.AutoIndexBinding{Table: "auto_pk", Name: "sqlite_autoindex_auto_pk_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	wr := stmt.AutoIndexBinding{Table: "auto_wr", Name: "sqlite_autoindex_auto_wr_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "a", Collation: "BINARY"}, {Name: "b", Collation: "BINARY"}}}
	unique := stmt.AutoIndexBinding{Table: "auto_unique", Name: "sqlite_autoindex_auto_unique_1", Origin: "u", Columns: []stmt.IndexColumn{{Name: "a", Collation: "NOCASE", Desc: true}, {Name: "b", Collation: "BINARY"}}}
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: "auto.pk", SQL: "SELECT p.id,p.payload FROM auto_pk AS p INDEXED BY sqlite_autoindex_auto_pk_1 WHERE p.id=?", Params: []stmt.Param{text("id")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"one"}}, {Name: "missing", Bindings: []any{"absent"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{pk.Name}, AutoIndexes: []stmt.AutoIndexBinding{pk}, Seeks: []string{"id=?"}}},
		{ID: "auto.unique", SQL: "SELECT u.id FROM auto_unique AS u INDEXED BY sqlite_autoindex_auto_unique_1 WHERE u.a=? AND u.b=?", Params: []stmt.Param{text("a"), text("b")}, Cohorts: []stmt.Cohort{{Name: "nocase", Bindings: []any{"ALPHA", "x"}}, {Name: "missing", Bindings: []any{"absent", "x"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{unique.Name}, AutoIndexes: []stmt.AutoIndexBinding{unique}, Seeks: []string{"a=? AND b=?"}}},
	}
	for _, kind := range []string{"LEFT", "CROSS"} {
		defs = append(defs, stmt.Definition{ID: stmt.ID("auto." + strings.ToLower(kind)), SQL: "SELECT p.id,c.value FROM auto_pk AS p INDEXED BY sqlite_autoindex_auto_pk_1 " + kind + " JOIN auto_wr AS c INDEXED BY sqlite_autoindex_auto_wr_1 ON c.a=p.id AND c.b=? WHERE p.id=?", Params: []stmt.Param{text("child"), text("parent")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"x", "one"}}, {Name: "missing_child", Bindings: []any{"absent", "one"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{pk.Name, wr.Name}, AutoIndexes: []stmt.AutoIndexBinding{pk, wr}, Seeks: []string{"id=?", "a=? AND b=?"}}})
	}
	for i := range defs {
		defs[i].Kind = stmt.Read
		defs[i].Owner = "plan-autoindex-test"
		defs[i].CallSites = []string{"autoindexes_test.go"}
	}
	return defs
}

func TestPlanAutoindexGoldenAndNativeKeys(t *testing.T) {
	type population struct {
		Name   string      `json:"name"`
		Golden plan.Golden `json:"golden"`
	}
	var populations []population
	for _, analyzed := range []bool{false, true} {
		conn, _, f := autoindexFixture(t, analyzed)
		defs := autoindexDefinitions()
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(golden.Plans) != 8 {
			t.Fatal("missing actual cohorts", len(golden.Plans))
		}
		registry, err := stmt.New(defs, golden.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range golden.Plans {
			if len(p.AutoIndexes) == 0 || len(p.Explain.Values) == 0 {
				t.Fatal("missing live autoindex evidence", p.ID, p.Cohort)
			}
		}
		for _, c := range []struct {
			id    stmt.ID
			args  []any
			count int
			null  bool
		}{{"auto.pk", []any{"one"}, 1, false}, {"auto.pk", []any{"absent"}, 0, false}, {"auto.unique", []any{"ALPHA", "x"}, 1, false}, {"auto.left", []any{"absent", "one"}, 1, true}, {"auto.cross", []any{"absent", "one"}, 0, false}} {
			s, err := registry.Lookup(c.id)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Bind(c.args...)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := b.QueryContext(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				count++
				if c.null {
					var id string
					var v sql.NullString
					if err := rows.Scan(&id, &v); err != nil || v.Valid {
						t.Fatal("LEFT NULL extension lost", err, v)
					}
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil || count != c.count {
				t.Fatal(c.id, err, count, c.count)
			}
		}
		// A live receipt is bound to the verified name-to-key declaration too.
		changed := registry.Manifest()[0].Definition
		changed.Plan.AutoIndexes[0].Columns[0].Name = "another"
		if _, err := stmt.New([]stmt.Definition{changed}, golden.Receipt()); !errors.Is(err, stmt.ErrUnqualified) {
			t.Fatal("receipt accepted changed key binding", err)
		}
		populations = append(populations, population{string(f.Statistics), golden})
	}
	data, err := json.MarshalIndent(populations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "autoindexes.json")
	if *recordAutoindexPlans {
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, data); err != nil {
		t.Fatal(err)
	}
}

func TestPlanAutoindexBindingRefusalsAndRenumbering(t *testing.T) {
	conn, writer, f := autoindexFixture(t, true)
	for _, mutate := range []func(*stmt.Definition){
		func(d *stmt.Definition) { d.Plan.AutoIndexes = nil },
		func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Name = "code" },
		func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Collation = "NOCASE" },
		func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Desc = true },
		func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Origin = "u" },
		func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Table = "auto_unique" },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " INDEXED BY sqlite_autoindex_auto_pk_1", "", 1)
		},
		func(d *stmt.Definition) { d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, d.Plan.AutoIndexes[0]) },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "sqlite_autoindex_auto_pk_1", "sqlite_autoindex_auto_pk_99", 1)
			d.Plan.Indexes[0] = "sqlite_autoindex_auto_pk_99"
			d.Plan.AutoIndexes[0].Name = "sqlite_autoindex_auto_pk_99"
		},
		func(d *stmt.Definition) {
			// An existing native UNIQUE index still belongs to its own table.
			d.SQL = strings.Replace(d.SQL, "sqlite_autoindex_auto_pk_1", "sqlite_autoindex_auto_unique_1", 1)
			d.Plan.Indexes = []string{"sqlite_autoindex_auto_unique_1"}
			d.Plan.AutoIndexes = autoindexDefinitions()[1].Plan.AutoIndexes
		},
	} {
		d := autoindexDefinitions()[0]
		mutate(&d)
		r, err := stmt.Describe([]stmt.Definition{d})
		if err == nil {
			g, e := plan.Capture(t.Context(), conn, r, f, indexPolicy(t))
			if e == nil || g.Receipt() != nil {
				t.Fatal("invalid autoindex binding qualified", d, e)
			}
		}
	}
	for _, columns := range [][]stmt.IndexColumn{
		{{Name: "a", Collation: "NOCASE", Desc: true}}, // a proper prefix is not the declared UNIQUE(a,b)
		{{Name: "b", Collation: "BINARY"}, {Name: "a", Collation: "NOCASE", Desc: true}},
	} {
		d := autoindexDefinitions()[1]
		d.Plan.AutoIndexes[0].Columns = columns
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if g, err := plan.Capture(t.Context(), conn, r, f, indexPolicy(t)); err == nil || g.Receipt() != nil {
			t.Fatal("subset/reordered UNIQUE key qualified", columns, err)
		}
	}
	for _, sql := range []string{
		"SELECT p.id FROM auto_pk AS p INDEXED BY sqlite_autoindex_auto_pk_1 WHERE p.payload=?",
		"SELECT p.id FROM auto_pk AS p INDEXED BY sqlite_autoindex_auto_pk_1 WHERE p.id>? ORDER BY p.payload",
	} {
		d := autoindexDefinitions()[0]
		d.SQL = sql
		d.Plan.Hot = false
		d.Plan.Seeks = nil
		eqp := reviewRawEQP(t, conn, sql, "one")
		d.Plan.Waivers = nil
		for _, row := range eqp.Values {
			detail := row[3].(string)
			form := "SCAN"
			if strings.Contains(detail, "USE TEMP B-TREE") {
				form = "USE TEMP B-TREE"
			}
			d.Plan.Waivers = append(d.Plan.Waivers, stmt.Waiver{StatementID: d.ID, Stage: "eqp", Form: form, Match: detail, Reason: "Synthetic negative only; autoindex capability must refuse even exact EQP waivers"})
		}
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		if g, err := plan.Capture(t.Context(), conn, r, f, indexPolicy(t)); err == nil || g.Receipt() != nil {
			t.Fatal("waiver admitted non-SEARCH autoindex plan", sql, err)
		}
	}
	d := autoindexDefinitions()[0]
	r, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(t.Context(), conn, r, f, indexPolicy(t)); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"DROP TABLE auto_pk", "CREATE TABLE auto_pk(code TEXT COLLATE NOCASE UNIQUE,id TEXT PRIMARY KEY,payload TEXT NOT NULL)"} {
		if _, err := writer.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	if g, err := plan.Capture(t.Context(), conn, r, f, indexPolicy(t)); err == nil || g.Receipt() != nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatal("renumbered key did not fail loudly", err)
	}
}

func TestPlanAutoindexDMLAndMainSourceOnly(t *testing.T) {
	reader, writer, f := autoindexFixture(t, false)
	definitions := autoindexDefinitions()
	writePolicy := f
	writePolicy.ConnectionPolicy.Role = sqliteconn.Writer
	writePolicy.ConnectionPolicy.ReadLimits = sqliteconn.ReadLimits{}
	for _, sql := range []string{
		"UPDATE auto_pk INDEXED BY sqlite_autoindex_auto_pk_1 SET payload=? WHERE id=? RETURNING payload",
		"DELETE FROM auto_pk INDEXED BY sqlite_autoindex_auto_pk_1 WHERE id=? RETURNING id",
	} {
		d := definitions[0]
		d.Kind = stmt.Write
		d.SQL = sql
		if strings.HasPrefix(sql, "UPDATE") {
			d.Params = []stmt.Param{{Name: "payload", Type: stmt.Text}, {Name: "id", Type: stmt.Text}}
			d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: []any{"changed", "one"}}}
		} else {
			d.Params = []stmt.Param{{Name: "id", Type: stmt.Text}}
			d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: []any{"one"}}}
		}
		catalogue, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(t.Context(), writer, catalogue, writePolicy, indexPolicy(t))
		if err != nil || len(g.Plans) != 1 || len(g.Plans[0].AutoIndexes) != 1 {
			t.Fatal("DML constraint index not certified", err)
		}
	}
	if _, err := writer.ExecContext(t.Context(), "CREATE VIEW auto_view AS SELECT id,payload FROM auto_pk WHERE length(payload)>0"); err != nil {
		t.Fatal(err)
	}
	d := definitions[0]
	d.SQL = strings.Replace(d.SQL, "auto_pk AS p", "auto_view AS p", 1)
	d.Plan.AutoIndexes[0].Table = "auto_view"
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if g, err := plan.Capture(t.Context(), reader, catalogue, f, indexPolicy(t)); err == nil || g.Receipt() != nil {
		t.Fatal("view source received generated-name capability", err)
	}
	if _, err := writer.ExecContext(t.Context(), "CREATE TEMP TABLE auto_pk(id TEXT PRIMARY KEY,payload TEXT)"); err != nil {
		t.Fatal(err)
	}
	d = autoindexDefinitions()[0]
	catalogue, err = stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if g, err := plan.Capture(t.Context(), writer, catalogue, writePolicy, indexPolicy(t)); err == nil || g.Receipt() != nil {
		t.Fatal("TEMP shadow received MAIN index capability", err)
	}
}
