package httpapi

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// An owner-clear consumer recomputes the automatic mirror inside its own
// transaction. This is that contract against real accepted source facts:
// automatic values ignore owner rows, accepted values prefer them and fall back
// to the source facts after a same-transaction clear, and the facet/series
// readers built from those values stay consistent.
func TestReadingAutomaticContractInOwnerClearTransaction(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	onePath := readingTreeFile(t, r, "Author/Series/One.epub")
	readingTreeFile(t, r, "Author/Series/Two.epub")
	readingScan(t, r.f, r.library)
	one := readingPathItem(t, r, onePath)
	var entity int64
	if err := r.f.db.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, one).Scan(&entity); err != nil {
		t.Fatal(err)
	}
	err := dbwork.WithWriteTx(ctx, r.f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		actor := identity.PersonalKey(r.p.Viewer)
		for field, value := range map[string]string{"author": "Owner Author", "series": "Owner Series"} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,?,?,'',1,?,datetime('now'))`, entity, field, value, actor); err != nil {
				return err
			}
		}
		automatic, err := compactcatalog.ReadingAutomaticFieldsTx(ctx, tx, entity)
		if err != nil {
			return err
		}
		if automatic.Author == nil || automatic.Author.Value != "Author" || automatic.Author.Source != compactcatalog.ReadingFolder || automatic.Series == nil || automatic.Series.Value != "Series" || automatic.Series.Source != compactcatalog.ReadingFolder {
			t.Errorf("automatic baseline saw owner rows: %+v", automatic)
		}
		owners, err := compactcatalog.ReadingOwnerValuesTx(ctx, tx, []int64{entity})
		if err != nil {
			return err
		}
		if owners[entity].Author == nil || owners[entity].Author.Value != "Owner Author" || owners[entity].Series == nil || owners[entity].Series.Value != "Owner Series" {
			t.Errorf("owner rows not projected: %+v", owners[entity])
		}
		accepted, err := compactcatalog.ReadingAcceptedValuesTx(ctx, tx, []int64{entity})
		if err != nil {
			return err
		}
		if accepted[entity].Author == nil || accepted[entity].Author.Value != "Owner Author" || accepted[entity].Series == nil || accepted[entity].Series.Value != "Owner Series" {
			t.Errorf("accepted value lost the owner: %+v", accepted[entity])
		}
		// The same-transaction clear an owner-clear consumer performs.
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_owner_fields SET value='',locked=0 WHERE kind='item' AND entity_id=?`, entity); err != nil {
			return err
		}
		automatic, err = compactcatalog.ReadingAutomaticFieldsTx(ctx, tx, entity)
		if err != nil {
			return err
		}
		if automatic.Author == nil || automatic.Author.Value != "Author" || automatic.Series == nil || automatic.Series.Value != "Series" {
			t.Errorf("automatic baseline changed on owner clear: %+v", automatic)
		}
		accepted, err = compactcatalog.ReadingAcceptedValuesTx(ctx, tx, []int64{entity})
		if err != nil {
			return err
		}
		if accepted[entity].Author == nil || accepted[entity].Author.Value != "Author" || accepted[entity].Author.Source != compactcatalog.ReadingFolder || accepted[entity].Series == nil || accepted[entity].Series.Value != "Series" {
			t.Errorf("owner clear did not fall back to accepted source facts: %+v", accepted[entity])
		}
		items := []compactcatalog.ReadingFacetItem{{Item: entity, TitleKey: "one", Values: accepted[entity]}}
		facets := compactcatalog.ReadingFacets(r.library, items)
		if len(facets.Authors) != 1 || facets.Authors[0].Value != "Author" || len(facets.Series) != 1 || facets.Series[0].Name != "Series" {
			t.Errorf("facets=%+v", facets)
			return nil
		}
		series, ok := compactcatalog.ReadingSeriesItems(r.library, facets.Series[0].ID, items)
		if !ok || len(series) != 1 || series[0].Item != entity {
			t.Errorf("series reader=%+v ok=%v", series, ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.s.List(ctx, r.p, r.library, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var row reading.LibraryRow
	found := false
	for _, v := range list.Items {
		if v.ID == one {
			row, found = v, true
		}
	}
	if !found || row.Author == nil || row.Author.Value != "Author" || row.Series == nil || row.Series.Name != "Series" {
		t.Fatalf("committed owner-clear projection=%+v found=%v", row, found)
	}
}
