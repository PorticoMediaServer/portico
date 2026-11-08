package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaSourceOctetsDefinitions() []stmt.Definition {
	point := stmt.Definition{
		ID: "read.foreground.ia.source.point_octets", SQL: iaSourcePointOctetsSQL, Kind: stmt.Read,
		Owner: "store/read/ia_source", CallSites: []string{"store/read/ia_source.go:IASourcePoint"},
		Params:  []stmt.Param{{Name: "source", Type: stmt.Text}, {Name: "library", Type: stmt.Text}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"source", "movies"}}, {Name: "empty", Bindings: []any{"missing", "movies"}}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{iaSourcePointIndex}, Seeks: []string{"id=?"}},
	}
	roots := stmt.Definition{
		ID: "read.foreground.ia.source.roots_octets", SQL: iaSourceRootsOctetsSQL, Kind: stmt.Read,
		Owner: "store/read/ia_source", CallSites: []string{"store/read/ia_source.go:IASourcePoint"},
		Params:  []stmt.Param{{Name: "library", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(9999999)}}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
	}
	return []stmt.Definition{point, roots}
}
func TestIASourceOctetsNativeCapture(t *testing.T) {
	source := readfixture.Foreground(t)
	cfg := readfixture.ReaderConfig()
	db, _, e := sqliteconn.Open(t.Context(), source.Path(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	// NUL and multibyte bytes falsify character-count accounting.
	if _, e = source.Conn().ExecContext(t.Context(), `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity) VALUES('source','movies','/media','/media','é\x00x','/media')`); e != nil {
		t.Fatal(e)
	}
	odd := "é\x00x"
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE library_sources SET incarnation=? WHERE id='source'`, odd); e != nil {
		t.Fatal(e)
	}
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE library_sources SET name=? WHERE id='source'`, odd); e != nil {
		t.Fatal(e)
	}
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE catalog_libraries SET root=? WHERE id=1`, odd); e != nil {
		t.Fatal(e)
	}
	defs := iaSourceOctetsDefinitions()
	cat, e := stmt.Describe(defs)
	if e != nil {
		t.Fatal(e)
	}
	data, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := ReadIndexPolicy(data)
	if e != nil {
		t.Fatal(e)
	}
	f := Fixture{ConnectionPolicy: cfg, SHA256: readfixture.ForegroundHash(t), Statistics: Unanalyzed, Implementations: "IA source point/library-root octet points; canonical " + schema.SHA256 + "; name and root e-acute,NUL,x"}
	g, e := Capture(context.Background(), c, cat, f, policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = stmt.New(defs, g.Receipt()); e != nil {
		t.Fatal(e)
	}
	var name, root int64
	if e = c.QueryRowContext(t.Context(), defs[0].SQL, "source", "movies").Scan(new(int64), new(int64), new(int64), &name, new(int64), new(int64), new(int64), new(int64), new(int64), new(int64)); e != nil || name != 4 {
		t.Fatal("actual name octets", name, e)
	}
	if e = c.QueryRowContext(t.Context(), defs[1].SQL, int64(1)).Scan(new(int64), &root); e != nil || root != 4 {
		t.Fatal("actual library root octets", root, e)
	}
	actual, e := g.JSON()
	if e != nil {
		t.Fatal(e)
	}
	path := "testdata/ia-source-octets.json"
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, filepath.Base(path)), actual, 0644); e != nil {
			t.Fatal(e)
		}
	}
	expected, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Compare(expected); e != nil {
		t.Fatal(e)
	}
}
