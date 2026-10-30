package metadata

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// An explicit clear is an owner decision, not "use automatic": the owner row
// stays locked with an empty value, the writer's value is kept as the automatic
// provenance, and a later scanner/provider write cannot refill it. Only "use
// automatic" releases the field back to that writer.
func TestOwnerExplicitClearKeepsAutomaticProvenance(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	c := catalogtest.New(t, db)
	target := editorTarget(names, "item", "movie")
	c.Fields(names["movie"].ID, map[string]any{"tagline": "Provider tagline"})
	empty := ""
	state := editField(t, s, target, "tagline", RepairFieldEdit{Value: &empty})
	assertClearedWithAutomatic(t, state, "tagline", "Provider tagline")
	if got := readDetailText(t, db, names["movie"].ID, "tagline"); got != "" {
		t.Fatalf("cleared tagline still projects %q", got)
	}
	// A later automatic write is recorded as the new automatic value but
	// cannot refill the clear.
	c.Fields(names["movie"].ID, map[string]any{"tagline": "Later tagline"})
	state, err := s.RepairState(context.Background(), target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	assertClearedWithAutomatic(t, state, "tagline", "Later tagline")
	if got := readDetailText(t, db, names["movie"].ID, "tagline"); got != "" {
		t.Fatalf("cleared tagline refilled to %q", got)
	}
	// Use automatic restores the latest writer value and releases the lock.
	restored := editField(t, s, target, "tagline", RepairFieldEdit{Automatic: true})
	field := restored.Snapshot.Fields["tagline"]
	if field.Value != "Later tagline" || field.Locked {
		t.Fatalf("use automatic left %+v", field)
	}
	if got := readDetailText(t, db, names["movie"].ID, "tagline"); got != "Later tagline" {
		t.Fatalf("use automatic projects %q", got)
	}
}

// A list clear removes the catalogue edges and keeps the same owner lock.
func TestOwnerExplicitClearRemovesListEdges(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	target := editorTarget(names, "item", "movie")
	values := []string{"Noir", "Rewatch"}
	state := editField(t, s, target, "tags", RepairFieldEdit{Values: &values})
	if got := state.Snapshot.Fields["tags"].Values; len(got) != 2 {
		t.Fatalf("owner tags read back as %v", got)
	}
	none := []string{}
	state = editField(t, s, target, "tags", RepairFieldEdit{Values: &none})
	field := state.Snapshot.Fields["tags"]
	if !field.Locked || field.Source != "manual" || len(field.Values) != 0 {
		t.Fatalf("cleared tags lost the owner decision: %+v", field)
	}
	var edges int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=? AND f.field='tags'`, names["movie"].ID).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges != 0 {
		t.Fatalf("cleared tags left %d browse attributes", edges)
	}
}

func assertClearedWithAutomatic(t *testing.T, state RepairState, field, automatic string) {
	t.Helper()
	f := state.Snapshot.Fields[field]
	if f.Value != "" || !f.Locked || f.Source != "manual" {
		t.Fatalf("clear of %s is not a locked empty owner value: %+v", field, f)
	}
	if f.Automatic != automatic {
		t.Fatalf("clear of %s recorded automatic %q, want %q", field, f.Automatic, automatic)
	}
}

func readDetailText(t *testing.T, db *sql.DB, entity int64, column string) string {
	t.Helper()
	var out string
	if err := db.QueryRow(`SELECT COALESCE(`+column+`,'') FROM catalog_item_details WHERE entity_id=?`, entity).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
