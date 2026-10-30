package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestMemberTraitCountsRemainExactThroughMoveAndDeletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := cc1OpenDB(t, "member-counts.sqlite")
	library := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: library, Kind: Show, Key: "show:a", Title: "A"}, map[string]any{"local_key": "a"})
	season := cc1Entity(t, db, Entity{Library: library, Kind: Season, Parent: show, Key: "season:a", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
	var first int64
	for i := 1; i <= 100; i++ {
		ep := cc1Entity(t, db, Entity{Library: library, Kind: Episode, Parent: season, Key: fmt.Sprint("ep:", i), Title: fmt.Sprint("Episode ", i)}, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i})
		if i == 1 {
			first = ep
		}
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			return SetTermsTx(ctx, tx, ep, VocabGenre, "fixture", []Term{{SourceID: "common", Name: "Common"}, {SourceID: fmt.Sprint(i), Name: fmt.Sprintf("Unique %03d", i)}})
		})
		// Seed incrementally, as import does: bounded facet producers now need
		// several admissions per episode. Keep the existing 500-turn empty-queue
		// guard and every final 100/99/1/source/deletion oracle unchanged.
		if i%8 == 0 {
			cc1Drain(t, db)
		}
	}
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_rec_member_counts WHERE work_id=? AND facet='g:common'`, show); got != 100 {
		t.Fatalf("common count %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_rec_facets WHERE entity_id=? AND facet LIKE 'g:%'`, show); got != recMemberFacetLimit {
		t.Fatalf("published %d member traits", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_term_sources WHERE entity_id IN(SELECT entity_id FROM catalog_episodes WHERE show_id=?)`, show); got != 200 {
		t.Fatalf("source metadata was truncated: %d", got)
	}
	nextShow := cc1Entity(t, db, Entity{Library: library, Kind: Show, Key: "show:b", Title: "B"}, map[string]any{"local_key": "b"})
	nextSeason := cc1Entity(t, db, Entity{Library: library, Kind: Season, Parent: nextShow, Key: "season:b", Title: "Season 1"}, map[string]any{"show_id": nextShow, "number": 1})
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=?`, nextSeason, first); err != nil {
			return err
		}
		return SetFactsTx(ctx, tx, first, map[string]any{"show_id": nextShow, "season_id": nextSeason, "numbering": "seasonal", "number": 1})
	})
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_rec_member_counts WHERE work_id=? AND facet='g:common'`, show); got != 99 {
		t.Fatalf("old parent count %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_rec_member_counts WHERE work_id=? AND facet='g:common'`, nextShow); got != 1 {
		t.Fatalf("new parent count %d", got)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_entities WHERE id=?`, first)
		return err
	})
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_rec_member_counts WHERE work_id=?`, nextShow); got != 0 {
		t.Fatalf("deleted member left %d counts", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_rec_member_work WHERE item_id=?`, first); got != 0 {
		t.Fatal("deleted member mapping retained")
	}
}
