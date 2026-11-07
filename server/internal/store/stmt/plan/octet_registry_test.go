package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestOctetRegistryClosedAndUnique(t *testing.T) {
	if len(pointOctets.byID) < 4 || len(pointOctets.byID) != len(pointOctets.bySQL) {
		t.Fatal("missing/duplicate closed point")
	}
	copy := octetRegistry{byID: map[stmt.ID]octetShape{}, bySQL: map[string]stmt.ID{}}
	for id, shape := range pointOctets.byID {
		if id != shape.id || pointOctets.bySQL[shape.sql] != id {
			t.Fatal("inconsistent compiled registry", id)
		}
		copy.add(shape)
	}
	for _, kind := range []string{"ID", "SQL"} {
		t.Run(kind, func(t *testing.T) {
			shape := pointOctets.byID[issuerSourceLengthsID]
			if kind == "ID" {
				shape.sql += " "
			} else {
				shape.id = "another.point"
			}
			defer func() {
				if recover() == nil {
					t.Fatal("duplicate registration accepted")
				}
			}()
			copy.add(shape)
		})
	}
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
