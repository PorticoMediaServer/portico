package plan

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestOctetRegistryClosedAndUnique(t *testing.T) {
	if len(pointOctets.byID) < 4 || len(pointOctets.bySQL) > len(pointOctets.byID) {
		t.Fatal("missing/duplicate closed point")
	}
	copy := octetRegistry{byID: map[stmt.ID]octetShape{}, bySQL: map[string]struct{}{}}
	for id, shape := range pointOctets.byID {
		if _, ok := pointOctets.bySQL[shape.sql]; id != shape.id || !ok {
			t.Fatal("inconsistent compiled registry", id)
		}
		copy.add(shape)
	}

	t.Run("ID", func(t *testing.T) {
		shape := pointOctets.byID[issuerSourceLengthsID]
		shape.sql += " "
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate ID registration accepted")
			}
		}()
		copy.add(shape)
	})

	// Neither generic lint entry nor an unknown ID gets the point extension.
	if len(LintSQL(issuerSourceLengthsSQL)) == 0 {
		t.Fatal("generic grammar widened")
	}
	if closedOctetDefinition(stmt.Definition{ID: "unknown.point", SQL: issuerSourceLengthsSQL}) {
		t.Fatal("unknown octet point accepted")
	}
}

func TestOctetRegistryPreservesIssuerDefinitions(t *testing.T) {
	source := stmt.Definition{ID: issuerSourceLengthsID, SQL: issuerSourceLengthsSQL, Kind: stmt.Read,
		Params: []stmt.Param{{Name: "source", Type: stmt.Text}, {Name: "library", Type: stmt.Text}},
		Plan:   stmt.Plan{Hot: true, Indexes: []string{issuerSourceIndex}, Seeks: []string{"id=?"}}}
	library := stmt.Definition{ID: issuerLibraryRootLengthsID, SQL: issuerLibraryRootLengthsSQL, Kind: stmt.Read,
		Params: []stmt.Param{{Name: "library_id", Type: stmt.Integer}, {Name: "library_key", Type: stmt.Text}},
		Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}}
	for _, d := range []stmt.Definition{source, library} {
		if !closedOctetDefinition(d) || checkSyntax(d) != nil {
			t.Fatal("existing issuer shape rejected", d.ID)
		}
		changed := d
		changed.SQL = strings.Replace(d.SQL, "id=?", "id>?", 1)
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("point-to-range mutation admitted", d.ID)
		}
		changed = d
		changed.ID = "unregistered.point"
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("unregistered identical SQL admitted", d.ID)
		}
		changed = d
		changed.Plan.Hot = false
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("cold issuer admitted", d.ID)
		}
	}
}

func TestOctetRegistryApprovedSQLAliases(t *testing.T) {
	// Exact aliases already approved in N1/INT2: distinct purpose IDs and root
	// parameter names share literal SQL. They must not panic at package init.
	for _, point := range []struct {
		name, sql string
		params    [][]stmt.Param
	}{
		{"job_lengths", "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", [][]stmt.Param{
			{{Name: "run_id", Type: stmt.Text}}, {{Name: "run_id", Type: stmt.Text}}}},
		{"root_lengths", "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", [][]stmt.Param{
			{{Name: "run_id", Type: stmt.Text}, {Name: "root_path", Type: stmt.Text}}, {{Name: "run_id", Type: stmt.Text}, {Name: "path", Type: stmt.Text}}}},
	} {
		t.Run(point.name, func(t *testing.T) {
			r := octetRegistry{byID: map[stmt.ID]octetShape{}, bySQL: map[string]struct{}{}}
			var defs []stmt.Definition
			for i, prefix := range []string{"scan.issuer.", "scan.directory."} {
				d := stmt.Definition{ID: stmt.ID(prefix + point.name), SQL: point.sql, Kind: stmt.Read, Params: point.params[i], Plan: stmt.Plan{Hot: true}}
				defs = append(defs, d)
				r.add(octetShape{id: d.ID, sql: d.SQL, mismatch: "exact alias required",
					definition: func(got stmt.Definition) bool {
						return got.ID == d.ID && got.SQL == d.SQL && got.Kind == d.Kind && got.Plan.Hot && slices.Equal(got.Params, d.Params)
					},
					audit: func(context.Context, *sql.Conn) error { return ErrCaptureRequired }, point: func(Rows) error { return ErrCaptureRequired }})
			}
			if len(r.byID) != 2 || len(r.bySQL) != 1 {
				t.Fatal("aliases must share parser membership, not purpose authority")
			}
			for _, d := range defs {
				if _, ok := r.match(d); !ok {
					t.Fatal("approved alias refused", d.ID)
				}
				d.ID = "unregistered.octet.alias"
				if _, ok := r.match(d); ok {
					t.Fatal("parser membership authorized an unknown purpose")
				}
			}
			d := defs[0]
			d.Params = []stmt.Param{{Name: "wrong", Type: stmt.Integer}}
			if _, ok := r.match(d); ok {
				t.Fatal("alias skipped its own definition check")
			}
		})
	}
}
