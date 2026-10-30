package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

func contentEligibleTotal(t *testing.T, db *sql.DB, library string, kind int) int {
	t.Helper()
	var total int
	if err := db.QueryRow(`SELECT COALESCE((SELECT total FROM catalog_content_eligible_counts WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=?),0)`, library, kind).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

func drainContentEligible(t *testing.T, db *sql.DB, limit int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		worked, err := StepContentEligible(context.Background(), tx, limit)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if worked == 0 {
			return
		}
	}
	t.Fatal("content eligibility did not drain")
}

// queueContentEligible mirrors the browse-row and membership triggers: the
// entity's eligibility job is re-queued with its cursor reset.
func queueContentEligible(t *testing.T, db *sql.DB, entity int64) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_content_eligible_jobs(entity_id) VALUES(?) ON CONFLICT(entity_id) DO UPDATE SET sequence=sequence+1,after_item_id=0,found=0`, entity)
		return err
	})
}

func TestContentEligibleCursorRestartsAndLibraryMove(t *testing.T) {
	db := cc1OpenDB(t, "eligible.sqlite")
	other := cc1Library(t, db, "other", "Other", "tv", "/other")
	tv := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: tv, Kind: Show, Key: "show:show", Title: "Show", Year: 2020},
		map[string]any{"local_key": "show"})
	episodes := map[string]int64{}
	for n, key := range []string{"a", "b", "c"} {
		episodes[key] = cc1Entity(t, db, Entity{Library: tv, Kind: Episode, Key: "episode:show/absolute/0/" + key, Title: key},
			map[string]any{"show_id": show, "numbering": "absolute", "number": n + 1, "local_identity_status": "parsed"})
	}
	var firstAsset, lastAsset int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if firstAsset, _, err = UpsertAssetTx(ctx, tx, Asset{Path: "/first", Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 60}); err != nil {
			return err
		}
		if lastAsset, _, err = UpsertAssetTx(ctx, tx, Asset{Path: "/last", Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 60}); err != nil {
			return err
		}
		return LinkAssetTx(ctx, tx, episodes["c"], lastAsset, Link{})
	})
	cc1Drain(t, db)
	if got := contentEligibleTotal(t, db, "tv", 2); got != 1 {
		t.Fatalf("initial show count %d", got)
	}
	cc1Exec(t, db, `DELETE FROM catalog_asset_links WHERE entity_id=?`, episodes["c"])
	queueContentEligible(t, db, show)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := StepContentEligible(context.Background(), tx, 1); err != nil || n != 1 {
		tx.Rollback()
		t.Fatalf("partial step: %d %v", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	if err := db.QueryRow(`SELECT after_item_id FROM catalog_content_eligible_jobs WHERE entity_id=?`, show).Scan(&cursor); err != nil || cursor == 0 {
		t.Fatalf("missing partial cursor %d %v", cursor, err)
	}
	cc1Exec(t, db, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available) VALUES(?,?,0,1)`, episodes["a"], firstAsset)
	queueContentEligible(t, db, show)
	if err := db.QueryRow(`SELECT after_item_id FROM catalog_content_eligible_jobs WHERE entity_id=?`, show).Scan(&cursor); err != nil || cursor != 0 {
		t.Fatalf("source edit failed to reset cursor %d %v", cursor, err)
	}
	drainContentEligible(t, db, 1)
	if got := contentEligibleTotal(t, db, "tv", 2); got != 1 {
		t.Fatalf("replayed show count %d", got)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET library_id=? WHERE id=?`, other, show); err != nil {
			return err
		}
		return TouchTx(ctx, tx, DomainBrowseRows, show)
	})
	cc1Drain(t, db)
	if old, moved := contentEligibleTotal(t, db, "tv", 2), contentEligibleTotal(t, db, "other", 2); old != 0 || moved != 1 {
		t.Fatalf("show move counts %d %d", old, moved)
	}
}
