package compactcatalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type infoPublicationFixture struct {
	c     *catalogtest.Catalog
	asset int64
	token string
	items []int64
	other int64
}

func newInfoPublicationFixture(t *testing.T, holders int) infoPublicationFixture {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("info", "Information", "movie", "/info")
	f := infoPublicationFixture{c: c}
	for n := 0; n < holders; n++ {
		it := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: fmt.Sprintf("holder:%d", n), Title: "Holder"}, nil)
		f.items = append(f.items, it.ID)
		if n == 0 {
			f.asset, f.token = c.File(it.ID, "/info/shared.mkv", 5400)
		} else {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				return compactcatalog.LinkAssetTx(ctx, tx, it.ID, f.asset, compactcatalog.Link{})
			})
		}
	}
	other := c.Movie(lib, "/info/unrelated.mkv", "Unrelated", 2026)
	f.other = other.ID
	c.Drain()
	for _, id := range append(append([]int64{}, f.items...), f.other) {
		if h := f.head(t, id); !h.Ready() {
			t.Fatal("real domain20 did not publish a ready informational head", id, h)
		}
	}
	return f
}

func (f infoPublicationFixture) step(t *testing.T) bool {
	t.Helper()
	done := false
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = compactcatalog.AdvanceInfoPublicationTx(ctx, tx, f.asset, f.token, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func (f infoPublicationFixture) head(t *testing.T, id int64) compactcatalog.InfoHead {
	t.Helper()
	h, err := compactcatalog.InfoHeadTx(context.Background(), f.c.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (f infoPublicationFixture) frame(t *testing.T) compactcatalog.InfoAsset {
	t.Helper()
	var a compactcatalog.InfoAsset
	err := dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
		var err error
		a, err = compactcatalog.ReadInfoAsset(ctx, dbwork.Snapshot(ctx), f.asset, f.token, true)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f infoPublicationFixture) untilEpoch(t *testing.T, epoch int64) {
	t.Helper()
	for n := 0; n < 300; n++ {
		var current int64
		if err := f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&current); err != nil {
			t.Fatal(err)
		}
		if current == epoch {
			return
		}
		f.step(t)
	}
	t.Fatal("publication did not activate")
}

func TestInfoPublicationQueuedAvailabilityAndAcceptedDuration(t *testing.T) {
	f := newInfoPublicationFixture(t, 17)
	beforeOther := f.head(t, f.other)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"available": false, "duration": 90.0})
	})
	if a := f.frame(t); a.Duration != 5400 || !a.OrderAvailable {
		t.Fatal("raw NEW escaped before pre-fence", a)
	}
	f.step(t) // capture a finite frontier and freeze NEW1's order target.
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"available": true, "duration": 180.0})
	})
	f.step(t) // first raw8 holders only.
	blocked := 0
	for _, id := range f.items {
		if h := f.head(t, id); h.SourceBlockers == 1 {
			blocked++
		}
	}
	if blocked != 8 || f.head(t, f.other) != beforeOther {
		t.Fatalf("pre-fence/unrelated scope: blocked%d other%+v", blocked, f.head(t, f.other))
	}
	f.untilEpoch(t, 2)
	if a := f.frame(t); a.Duration != 90 || a.OrderAvailable {
		t.Fatal("queued raw NEW2 replaced sealed NEW1", a)
	}
	f.untilEpoch(t, 3)
	if a := f.frame(t); a.Duration != 180 || !a.OrderAvailable {
		t.Fatal("queued NEW2 did not preserve its own image", a)
	}
	for n := 0; n < 300 && !f.step(t); n++ {
		if n == 299 {
			t.Fatal("source work did not retire")
		}
	}
	for _, id := range f.items {
		var duration float64
		if err := f.c.DB.QueryRow(`SELECT info_duration FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, id, f.asset).Scan(&duration); err != nil || duration != 180 {
			t.Fatal("wrong accepted contribution", duration, err)
		}
		if h := f.head(t, id); h.SourceBlockers != 0 || !h.AvailabilityPending {
			t.Fatal("holder released before availability completion", h)
		}
	}
	var used, dictionary int64
	if err := f.c.DB.QueryRow(`SELECT COALESCE(SUM(168+2*length(CAST(container AS BLOB))+2*length(CAST(video_codec AS BLOB))),0) FROM catalog_info_shapes`).Scan(&dictionary); err != nil {
		t.Fatal(err)
	}
	if err := f.c.DB.QueryRow(`SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used); err != nil || used != dictionary {
		t.Fatal("source retirement leaked ledger bytes", used, err)
	}
}

func TestInfoPublicationEnrollsLinkBeyondCapturedFrontier(t *testing.T) {
	f := newInfoPublicationFixture(t, 9)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"duration": 42.0})
	})
	f.step(t) // finite original9-holder frontier.
	var lib int64
	f.c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, f.items[0]).Scan(&lib)
	it := f.c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: "late", Title: "Late"}, nil)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, it.ID, f.asset, compactcatalog.Link{})
	})
	if h := f.head(t, it.ID); h.SourceBlockers != 1 {
		t.Fatal("link beyond frontier was not atomically enrolled", h)
	}
	for n := 0; n < 300 && !f.step(t); n++ {
		if n == 299 {
			t.Fatal("late link did not release")
		}
	}
	var duration float64
	if err := f.c.DB.QueryRow(`SELECT info_duration FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, it.ID, f.asset).Scan(&duration); err != nil || duration != 42 || f.head(t, it.ID).SourceBlockers != 0 {
		t.Fatal("late accepted contribution", duration, f.head(t, it.ID), err)
	}
}

func TestInfoPublicationMoveAndDeleteDischargeAtomically(t *testing.T) {
	f := newInfoPublicationFixture(t, 2)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"duration": 42.0})
	})
	f.step(t)
	f.step(t)
	if f.head(t, f.items[0]).SourceBlockers != 1 {
		t.Fatal("fixture was not pre-fenced")
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET entity_id=? WHERE entity_id=? AND asset_id=?`, f.other, f.items[0], f.asset)
		return err
	})
	if f.head(t, f.items[0]).SourceBlockers != 0 || f.head(t, f.other).SourceBlockers != 1 {
		t.Fatal("move lost or duplicated OLD obligation", f.head(t, f.items[0]), f.head(t, f.other))
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, f.other, f.asset)
	})
	if f.head(t, f.other).SourceBlockers != 0 {
		t.Fatal("delete did not discharge", f.head(t, f.other))
	}
}

// The released A5 worker owns source41 and domain20. No test-side completion
// hook is permitted here: a real scalar input must reach exact summaries through
// the production worker and release every ordinary holder itself.
func TestInfoPublicationProductionSourceAndHolderComposition(t *testing.T) {
	f := newInfoPublicationFixture(t, 17)
	before := f.head(t, f.other)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"duration": 123.0, "height": 1080, "container": "matroska", "video_codec": "h264"})
	})
	if a := f.frame(t); a.Duration != 5400 {
		t.Fatal("source accepted NEW before pre-fence", a)
	}
	f.c.Drain()
	for _, id := range f.items {
		var summary compactcatalog.InfoSummary
		err := dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
			var err error
			summary, err = compactcatalog.ReadInfoSummary(ctx, dbwork.Snapshot(ctx), id)
			return err
		})
		if err != nil || !f.head(t, id).Ready() || summary.Duration != 123 {
			t.Fatalf("holder%d: %+v %+v %v", id, summary, f.head(t, id), err)
		}
		var matches bool
		if err := f.c.DB.QueryRow(`SELECT s.container='matroska' AND s.video_codec='h264' AND s.height=1080 FROM catalog_asset_links l JOIN catalog_info_shapes s ON s.id=l.info_shape_id WHERE l.entity_id=? AND l.asset_id=?`, id, f.asset).Scan(&matches); err != nil || !matches {
			t.Fatal("wrong accepted shape", id, matches, err)
		}
	}
	if f.head(t, f.other) != before {
		t.Fatal("unrelated informational head changed")
	}
	var pending int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("source body not retired", pending, err)
	}
}
