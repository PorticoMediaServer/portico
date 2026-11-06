package plan_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordIssuerLibraryOctets = flag.Bool("record-issuer-library-octets", false, "deliberately record the closed issuer library-root byte point")

const libraryRootOctetsSQL = "SELECT octet_length(root) FROM catalog_libraries WHERE id=? AND library_id=?"

func issuerLibraryOctetsDefinition() stmt.Definition {
	return stmt.Definition{
		ID: "scan.issuer.library_root_lengths", Owner: "issuer-byte-preflight", CallSites: []string{"issuer_library_octets_test.go"},
		Kind: stmt.Read, SQL: libraryRootOctetsSQL,
		Params:  []stmt.Param{{Name: "library_id", Type: stmt.Integer}, {Name: "library_key", Type: stmt.Text}},
		Cohorts: []stmt.Cohort{{Name: "hit", Bindings: []any{int64(7), "lib"}}, {Name: "missing", Bindings: []any{int64(8), "lib"}}, {Name: "wrong_library", Bindings: []any{int64(7), "other"}}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
	}
}

func TestIssuerLibraryRootOctetLengthsGolden(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	// Exact byte preflight, including NUL and malformed UTF-8 in stored TEXT.
	if _, err := conn.ExecContext(t.Context(), "INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'lib',?)", "\x00\xffé"); err != nil {
		t.Fatal(err)
	}
	f.Implementations = "canonical issuer library-root point; root TEXT NUL,ff,e-acute; native octet length, not character length"
	d := issuerLibraryOctetsDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
		t.Fatal("generic lint admitted issuer-only function")
	}
	g, err := captureIssuerOctets(t, conn, f, policy, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 3 || g.ForeignKeys != nil {
		t.Fatal("closed library-root cohort inventory")
	}
	r, err := stmt.New([]stmt.Definition{d}, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Lookup(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind(int64(7), "lib")
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := b.QueryRowContext(t.Context(), conn).Scan(&got); err != nil || got != 4 {
		t.Fatal("native root byte count", got, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "issuer-library-root-octets.json")
	if *recordIssuerLibraryOctets {
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

func TestIssuerLibraryRootOctetLengthsClosed(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	cases := []struct {
		name   string
		change func(*stmt.Definition)
	}{
		{"other_id", func(d *stmt.Definition) { d.ID = "scan.issuer.other_root_lengths" }},
		{"other_projection", func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(root)", "octet_length(library_id)", 1)
		}},
		{"range", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "id=? AND", "id>? AND", 1) }},
		{"wrong_integer_type", func(d *stmt.Definition) { d.Params[0].Type = stmt.Text }},
		{"nullable_key", func(d *stmt.Definition) { d.Params[1].Nullable = true }},
		{"wrong_primary_key", func(d *stmt.Definition) { d.Plan.PrimaryKeys[0] = "library_sources" }},
		{"wrong_seek", func(d *stmt.Definition) { d.Plan.Seeks[0] = "library_id=?" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := issuerLibraryOctetsDefinition()
			tc.change(&d)
			g, err := captureIssuerOctets(t, conn, f, policy, d)
			if err == nil {
				t.Fatal("non-closed library-root octet Capture accepted")
			}
			if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
				t.Fatal("refused library-root Capture authorized registration")
			}
		})
	}
}
