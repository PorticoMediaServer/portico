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

var recordN1RecordOctets = flag.Bool("record-n1-record-octets", false, "record the closed N1 existing-record byte points")

func n1LibraryRecordOctetDefinition() stmt.Definition {
	return stmt.Definition{ID: "scan.inventory_accept.library_record_lengths", Kind: stmt.Read, Owner: "N1 record byte-preflight", CallSites: []string{"w_n1_record_octets_test.go"},
		SQL:     "SELECT octet_length(id),octet_length(name),octet_length(kind),octet_length(root),octet_length(preference_local_tags),octet_length(preference_lyrics_source),octet_length(preference_title_source),octet_length(preference_episode_numbering),octet_length(playback_origin_incarnation),octet_length(metadata_language) FROM libraries WHERE id=?",
		Params:  []stmt.Param{{Name: "library_key", Type: stmt.Text}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}},
		Cohorts: []stmt.Cohort{{Name: "own_library", Bindings: []any{"lib"}}, {Name: "missing", Bindings: []any{"absent"}}}}
}

func TestN1RecordOctetsGolden(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	if _, err := conn.ExecContext(t.Context(), `UPDATE libraries SET name=?,root=? WHERE id='lib'`, "é\x00\xff", "/fixture"); err != nil {
		t.Fatal(err)
	}
	f.Implementations = "canonical N1 own-library 10-field point and extended 17-field run point; exact native bytes, including NUL/invalid UTF8"
	defs := []stmt.Definition{n1LibraryRecordOctetDefinition(), n1OctetTestDefinitions()[0]}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	s, err := registry.Lookup(defs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind("lib")
	if err != nil {
		t.Fatal(err)
	}
	var id, name, kind, root, incarnation, language int64
	var local, lyrics, title, numbering any
	if err := b.QueryRowContext(t.Context(), conn).Scan(&id, &name, &kind, &root, &local, &lyrics, &title, &numbering, &incarnation, &language); err != nil || id != 3 || name != 4 || root != 8 || incarnation != 16 || local != nil || lyrics != nil || title != nil || numbering != nil {
		t.Fatal("exact native byte/NULL projection changed", id, name, root, incarnation, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "n1-record-octets.json")
	if *recordN1RecordOctets {
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

func TestN1LibraryRecordOctetsAreClosed(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	d := n1LibraryRecordOctetDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
		t.Fatal("generic octet function admitted")
	}
	for _, change := range []func(*stmt.Definition){
		func(d *stmt.Definition) { d.ID += ".other" },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length(name)", "length(name)", 1) },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "id=?", "id>?", 1) },
		func(d *stmt.Definition) { d.Params[0].Nullable = true },
	} {
		d := n1LibraryRecordOctetDefinition()
		change(&d)
		g, err := captureIssuerOctets(t, conn, f, policy, d)
		if err == nil {
			t.Fatal("foreign definition obtained Capture", d)
		}
		if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
			t.Fatal("failed Capture authorized registry")
		}
	}
}
