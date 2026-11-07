package plan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var recordIssuerOctets = flag.Bool("record-issuer-octets", false, "deliberately record the closed issuer byte-preflight point")

const sourceOctetsSQL = "SELECT octet_length(id),octet_length(library_id),octet_length(kind),octet_length(root),octet_length(incarnation),octet_length(root_identity),octet_length(health),octet_length(classification),octet_length(name),octet_length(configured_root),octet_length(next_scan_at),octet_length(last_complete_job),octet_length(last_complete_at),octet_length(last_integrity_at),octet_length(remote_source_id),octet_length(double_check_job),octet_length(progress_last_progress_at) FROM library_sources WHERE id=? AND library_id=?"

func issuerOctetsDefinition() stmt.Definition {
	return stmt.Definition{ID: "scan.issuer.source_lengths", Owner: "issuer-byte-preflight", CallSites: []string{"issuer_octets_test.go"}, Kind: stmt.Read, SQL: sourceOctetsSQL, Params: []stmt.Param{{Name: "source", Type: stmt.Text}, {Name: "library", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "hit", Bindings: []any{"source", "lib"}}, {Name: "missing", Bindings: []any{"missing", "lib"}}, {Name: "wrong_library", Bindings: []any{"source", "other"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}}
}

func issuerOctetsFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	conn, _, f, _ := fixture(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "INSERT INTO libraries(id,name,kind,root) VALUES('lib','Library','movies','/fixture')"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "INSERT INTO library_sources(id,library_id,configured_root,root,root_identity,incarnation) VALUES('source','lib','/fixture',?,?,?)", "a\x00\xffb", "é\x00x", strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	f.SHA256 = schema.SHA256
	f.Implementations = "canonical issuer source point; root TEXT a,NUL,ff,b and root_identity e-acute,NUL,x; native octet lengths, not character lengths"
	return conn, f, policy
}

func captureIssuerOctets(t *testing.T, conn *sql.Conn, f plan.Fixture, p plan.IndexPolicy, d stmt.Definition) (plan.Golden, error) {
	t.Helper()
	r, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		return plan.Golden{}, err
	}
	return plan.Capture(t.Context(), conn, r, f, p)
}

func TestIssuerSourceOctetLengthsGolden(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	d := issuerOctetsDefinition()
	// Connection-free entry points cannot certify the ID-bound native extension.
	if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
		t.Fatal("generic lint admitted issuer-only function")
	}
	g, err := captureIssuerOctets(t, conn, f, policy, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 3 || g.ForeignKeys != nil {
		t.Fatal("closed read cohort inventory")
	}
	registry, err := stmt.New([]stmt.Definition{d}, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	s, err := registry.Lookup(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind("source", "lib")
	if err != nil {
		t.Fatal(err)
	}
	var got [17]sql.NullInt64
	var destinations [17]any
	for i := range got {
		destinations[i] = &got[i]
	}
	if err := b.QueryRowContext(context.Background(), conn).Scan(destinations[:]...); err != nil {
		t.Fatal(err)
	}
	for i, n := range [8]int64{6, 3, 5, 4, 32, 4, 5, 5} {
		if !got[i].Valid || got[i].Int64 != n {
			t.Fatalf("native byte counts %v", got)
		}
	}
	if got[14].Valid || got[16].Valid {
		t.Fatal("nullable preserved source fields changed", got)
	}
	var characters int64
	if err := conn.QueryRowContext(t.Context(), "SELECT length(root) FROM library_sources WHERE id='source'").Scan(&characters); err != nil || characters != 1 {
		t.Fatal("NUL contrast", characters, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "issuer-source-octets.json")
	if *recordIssuerOctets {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}

func TestIssuerSourceOctetLengthsClosed(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	cases := []struct {
		name   string
		change func(*stmt.Definition)
	}{
		{"other_id", func(d *stmt.Definition) { d.ID = "scan.issuer.other_lengths" }},
		{"other_projection", func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(root)", "octet_length(configured_root)", 1)
		}},
		{"parameter_argument", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length(root)", "octet_length(?)", 1) }},
		{"nested_argument", func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(root)", "octet_length(lower(root))", 1)
		}},
		{"two_arguments", func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(root)", "octet_length(root,root)", 1)
		}},
		{"range", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "id=? AND", "id>? AND", 1) }},
		{"missing_library", func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " AND library_id=?", "", 1)
			d.Params = d.Params[:1]
			for i := range d.Cohorts {
				d.Cohorts[i].Bindings = d.Cohorts[i].Bindings[:1]
			}
		}},
		{"cold", func(d *stmt.Definition) { d.Plan.Hot = false }},
		{"nullable", func(d *stmt.Definition) { d.Params[0].Nullable = true }},
		{"write_kind", func(d *stmt.Definition) { d.Kind = stmt.Write }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := issuerOctetsDefinition()
			tc.change(&d)
			g, err := captureIssuerOctets(t, conn, f, policy, d)
			if err == nil {
				t.Fatal("non-closed octet_length Capture accepted")
			}
			if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
				t.Fatal("refused Capture authorized registration")
			}
		})
	}
	// Exact SQL still cannot read a TEMP shadow or a flattened view.
	t.Run("temp_shadow", func(t *testing.T) {
		if _, err := conn.ExecContext(t.Context(), "CREATE TEMP TABLE library_sources AS SELECT * FROM main.library_sources"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(t.Context(), "DROP TABLE temp.library_sources")
		if _, err := captureIssuerOctets(t, conn, f, policy, issuerOctetsDefinition()); err == nil || !strings.Contains(err.Error(), "TEMP") {
			t.Fatal("TEMP refused", err)
		}
	})
	t.Run("view", func(t *testing.T) {
		if _, err := conn.ExecContext(t.Context(), "ALTER TABLE library_sources RENAME TO source_base"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(t.Context(), "CREATE VIEW library_sources AS SELECT * FROM source_base"); err != nil {
			t.Fatal(err)
		}
		if _, err := captureIssuerOctets(t, conn, f, policy, issuerOctetsDefinition()); err == nil || !strings.Contains(err.Error(), "view") {
			t.Fatal("view refused", err)
		}
	})
}

func TestIssuerSourceOctetLengthsRejectPartialNativeKey(t *testing.T) {
	conn, _, f, _ := fixture(t)
	if _, err := conn.ExecContext(t.Context(), `CREATE TABLE library_sources(id TEXT,library_id TEXT,kind TEXT,root TEXT,incarnation TEXT,root_identity TEXT,health TEXT,classification TEXT,PRIMARY KEY(id,library_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := captureIssuerOctets(t, conn, f, indexPolicy(t), issuerOctetsDefinition()); err == nil || !strings.Contains(err.Error(), "native key must be exactly id") {
		t.Fatal("partial composite key refused", err)
	}
}

// Use the same test binary in a separate process: registering the reviewer's
// application override must not change any other test's physical connections.
func TestIssuerSourceOctetLengthsForgedFunctionCatalogue(t *testing.T) {
	const childFlag = "PORTICO_ISSUER_OCTET_SHADOW_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIssuerSourceOctetLengthsForgedFunctionCatalogue$", "-test.count=1")
		command.Env = append(os.Environ(), childFlag+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("native override child: %v\n%s", err, output)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	conn, f, policy := issuerOctetsFixture(t)
	var overridden int64
	if err := conn.QueryRowContext(t.Context(), "SELECT octet_length(root) FROM library_sources WHERE id='source'").Scan(&overridden); err != nil || overridden != 777 {
		t.Fatal("actual application override did not run", overridden, err)
	}
	for _, scope := range []string{"TEMP", "MAIN"} {
		t.Run(scope, func(t *testing.T) {
			// Both ordinary-table namespaces could forge the old unqualified SELECT.
			create := "CREATE TEMP TABLE pragma_function_list AS SELECT * FROM main.pragma_function_list() WHERE builtin=1"
			if scope == "MAIN" {
				create = "CREATE TABLE main.pragma_function_list AS SELECT * FROM main.pragma_function_list() WHERE builtin=1"
			}
			if _, err := conn.ExecContext(t.Context(), create); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(t.Context(), "DROP TABLE "+strings.ToLower(scope)+".pragma_function_list")
			var counterfeit int64
			if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM pragma_function_list WHERE name='octet_length' COLLATE NOCASE AND narg IN(1,-1)").Scan(&counterfeit); err != nil || counterfeit != 1 {
				t.Fatal("forged builtin catalogue not installed", counterfeit, err)
			}
			g, err := captureIssuerOctets(t, conn, f, policy, issuerOctetsDefinition())
			if err == nil || !strings.Contains(err.Error(), "unshadowed native") {
				t.Fatal("forged catalogue authorized application function", err)
			}
			if _, err := stmt.New([]stmt.Definition{issuerOctetsDefinition()}, g.Receipt()); err == nil {
				t.Fatal("refused native audit minted authority")
			}
		})
	}
}
