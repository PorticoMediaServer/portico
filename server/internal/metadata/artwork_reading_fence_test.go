package metadata

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Regression sources only: exercise the real editor and source fence while
// keeping source reads and AV publication/provider engines entirely absent.
func TestReadingArtworkFenceUsesReadingSourceWithoutAVHeads(t *testing.T) {
	for _, kind := range []compactcatalog.Kind{compactcatalog.Ebook, compactcatalog.Comic} {
		library := "book"
		if kind == compactcatalog.Comic {
			library = "comic"
		}
		t.Run(library, func(t *testing.T) {
			s, c, target := readingOwnerFixture(t, kind, library)
			before := dbwork.ChangingCommits()
			if _, err := s.RepairState(context.Background(), target, allowAll); err != nil {
				t.Fatal("Reading editor required an AV head", err)
			}
			if dbwork.ChangingCommits() != before {
				t.Fatal("editor read adopted missing heads")
			}
			readFence := func() string {
				t.Helper()
				gated, err := dbwork.BeginSnapshot(context.Background(), c.DB)
				if err != nil {
					t.Fatal(err)
				}
				defer gated.Rollback()
				fence, err := artworkFence(context.Background(), gated.Tx(), target)
				if err != nil {
					t.Fatal(err)
				}
				return fence
			}
			original := readFence()
			c.Exec(`UPDATE catalog_assets SET size=size+1 WHERE id=(SELECT asset_id FROM reading_publications WHERE item_id=?)`, c.ID(target.ID))
			if got := readFence(); got == original {
				t.Fatal("physical source change retained artwork authority")
			}
			c.Exec(`UPDATE catalog_assets SET size=size-1 WHERE id=(SELECT asset_id FROM reading_publications WHERE item_id=?)`, c.ID(target.ID))
			if got := readFence(); got == original {
				t.Fatal("source ABA revived the old fence")
			}
			var heads int
			if err := c.DB.QueryRow(`SELECT (SELECT count(*) FROM metadata_publication_heads WHERE item_id=?)+(SELECT count(*) FROM artwork_entity_heads WHERE kind='item' AND entity_id=?)`, c.ID(target.ID), c.ID(target.ID)).Scan(&heads); err != nil || heads != 0 {
				t.Fatal("Reading editor created AV heads", heads, err)
			}
			c.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, c.ID(target.ID))
			if _, err := s.RepairState(context.Background(), target, allowAll); err == nil {
				t.Fatal("retired Reading item remained editable")
			}
		})
	}
}
