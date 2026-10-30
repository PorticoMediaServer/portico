package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestFileInfoAdmissionJoinsOriginalDetailSnapshot(t *testing.T) {
	f := newFileInfoLaneFixture(t)
	rollback := errors.New("rollback bound head fixture")
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `UPDATE catalog_entities SET info_source_blockers=1 WHERE id=?`, f.item.ID); e != nil {
			return e
		}
		bound, e := dbwork.BindSnapshot(ctx, tx)
		if e != nil {
			return e
		}
		ad, e := f.s.CaptureFileInfoAdmission(bound, f.viewer, "server", f.item.Public, true, "actor", false)
		if e != nil {
			return e
		}
		if ad.Head.Ready() || ad.Head.SourceBlockers != 1 {
			t.Error("admission escaped the accepted snapshot")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}

func TestFileInfoActionReproofSeesAcceptingTransaction(t *testing.T) {
	f := newFileInfoAlbumFixture(t)
	proof := FileInfoActionProof{Opening: f.opening, Occurrence: f.songOccurrence(t)}
	original, err := f.r.ResolveAction(context.Background(), "family", proof)
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback proof fixture")
	err = dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, original.Item, original.Asset); e != nil {
			return e
		}
		bound, e := dbwork.BindSnapshot(ctx, tx)
		if e != nil {
			return e
		}
		_, e = f.r.ResolveAction(bound, "family", proof)
		if !errors.Is(e, readprep.ErrStale) {
			t.Errorf("proof did not see same-writer removal: %v", e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err = f.r.ResolveAction(context.Background(), "family", proof); err != nil {
		t.Fatal("rolled-back removal changed proof", err)
	}
}
