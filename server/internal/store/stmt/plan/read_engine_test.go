package plan_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func readEngineDefinition() stmt.Definition {
	return stmt.Definition{ID: "read.full.native.engine", SQL: "SELECT sqlite_version(),sqlite_source_id() FROM search_revision WHERE singleton=?", Owner: "read-engine-test", CallSites: []string{"read_engine_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(2)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_revision"}}}
}
func engineDatabase(t *testing.T) string {
	t.Helper()
	path := ftsReadDatabase(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), "INSERT INTO search_revision(singleton,revision) VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestReadEngineActualPoint(t *testing.T) {
	conn, f, policy := ftsReader(t, engineDatabase(t))
	sum := sha256.Sum256([]byte(f.SHA256 + "; engineDatabase adds search_revision(singleton=1,revision=1)"))
	f.SHA256 = fmt.Sprintf("%x", sum)
	f.Implementations += "; exact singleton1 revision1; measured native engine metadata"
	d := readEngineDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
		t.Fatal("generic lint admitted engine functions")
	}
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New([]stmt.Definition{d}, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	s, err := registry.Lookup(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.Bind(int64(1))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := bound.QueryContext(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	var version, source string
	if !rows.Next() {
		t.Fatal("missing measured engine")
	}
	if err = rows.Scan(&version, &source); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("non-point engine result")
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	var actualVersion, actualSource string
	if err = conn.QueryRowContext(t.Context(), "SELECT sqlite_version(),sqlite_source_id()").Scan(&actualVersion, &actualSource); err != nil {
		t.Fatal(err)
	}
	if version == "" || source == "" || version != actualVersion || source != actualSource {
		t.Fatal("engine was not read from executing physical connection")
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		if err = os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err = z.Write(data); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(evidence, "read-engine.json.gz"), b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open("testdata/read-engine.json.gz")
	if err != nil {
		t.Fatal("owned golden required; actual runner capture written to evidence", err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	want, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(want, data); err != nil {
		t.Fatal(err)
	}
}
func TestReadEngineClosedDefinition(t *testing.T) {
	conn, f, policy := ftsReader(t, engineDatabase(t))
	d := readEngineDefinition()
	for _, mutate := range []func(*stmt.Definition){func(d *stmt.Definition) { d.ID = "borrowed.engine" }, func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "sqlite_source_id()", "random()", 1) }, func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil }, func(d *stmt.Definition) { d.Params[0].Name = "other" }} {
		x := d
		x.Params = append([]stmt.Param(nil), d.Params...)
		mutate(&x)
		c, err := stmt.Describe([]stmt.Definition{x})
		if err == nil {
			_, err = plan.Capture(t.Context(), conn, c, f, policy)
		}
		if err == nil {
			t.Fatal("altered exact engine definition admitted")
		}
	}
	writer := f
	writer.ConnectionPolicy.Role = sqliteconn.Writer
	c, _ := stmt.Describe([]stmt.Definition{d})
	if _, err := plan.Capture(t.Context(), conn, c, writer, policy); err == nil {
		t.Fatal("Writer metadata custody admitted")
	}
}
func TestReadEngineOverrideRefuses(t *testing.T) {
	if os.Getenv("PORTICO_READ_ENGINE_OVERRIDE") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestReadEngineOverrideRefuses$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PORTICO_READ_ENGINE_OVERRIDE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("override control: %v\n%s", err, out)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("SQLITE_SOURCE_ID", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return "counterfeit", nil }); err != nil {
		t.Fatal(err)
	}
	conn, f, policy := ftsReader(t, engineDatabase(t))
	c, err := stmt.Describe([]stmt.Definition{readEngineDefinition()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Capture(t.Context(), conn, c, f, policy); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
		t.Fatal("counterfeit engine function admitted", err)
	}
}
