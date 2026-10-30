package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Removing a source tombstones it at once and leaves its files to a durable,
// bounded walk: the command's transaction touches no file, each background
// turn marks at most one slice unavailable, a restart carries on from the
// saved place, and nothing outside the source's root is touched.
func TestRemovedSourceFilesBecomeUnavailableInBoundedSlices(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	var root string
	if err := f.s.db.QueryRow(`SELECT root FROM remote_sources WHERE id='remote'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	// Keep the original 197-file regression even when the write slice shrinks.
	const files = 3*64 + 5
	paths := []string{root, root + "-sibling" + string(filepath.Separator) + "kept.mkv"}
	for i := 0; i < files; i++ {
		paths = append(paths, filepath.Join(root, fmt.Sprintf("dir%02d", i%7), fmt.Sprintf("f%04d.mkv", i)))
	}
	if err := dbwork.WithWriteTx(ctx, f.s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, path := range paths {
			if _, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", Duration: 60}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	available := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := f.s.db.QueryRow(`SELECT count(*) FROM catalog_assets WHERE available=1 AND `+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := available(`1`); n != len(paths) {
		t.Fatalf("fixture: %d of %d files available", n, len(paths))
	}
	actor, _ := json.Marshal([]string{owner.Authority, owner.AccountID, owner.ProfileID})
	if _, err := f.s.RemoveDAV(ctx, string(actor), "remote", nativeSaveOperation(700), 1, func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Stat(ctx, filepath.Join(root, "dir00", "f0000.mkv")); err != storage.ErrRemoteOffline {
		t.Fatalf("the removed source still answers: %v", err)
	}
	if n := available(`1`); n != len(paths) {
		t.Fatalf("the removal command marked %d files itself", len(paths)-n)
	}
	var largest dbwork.WriteCost
	turns := 0
	turn := func(service *Service) bool {
		t.Helper()
		dbwork.ResetPlans()
		mctx, cost := dbwork.Measure(ctx)
		more, err := service.markRemovedSourceAssets(mctx)
		if err != nil {
			t.Fatal(err)
		}
		if steps := cost().VMSteps; steps > 20000 {
			t.Fatalf("one turn read %d steps", steps)
		}
		assertRemovedSourcePlan(t)
		costs := dbwork.Plans().Largest
		largest.Rows, largest.Pages, largest.VMOps = max(largest.Rows, costs.Rows), max(largest.Pages, costs.Pages), max(largest.VMOps, costs.VMOps)
		turns++
		return more
	}
	if !turn(f.s) {
		t.Fatal("the first slice reported nothing left")
	}
	if n := len(paths) - available(`1`); n == 0 || n > removedSourceAssetBatch {
		t.Fatalf("one turn marked %d files", n)
	}
	// A restart keeps the saved place: a fresh service carries on.
	var after string
	if err := f.s.db.QueryRow(`SELECT after_path FROM remote_source_removals WHERE source_id='remote'`).Scan(&after); err != nil || after == "" {
		t.Fatalf("no saved place: %q %v", after, err)
	}
	restarted := &Service{db: f.s.db}
	for i := 0; ; i++ {
		if !turn(restarted) {
			break
		}
		if i > files {
			t.Fatal("the walk did not finish")
		}
	}
	if n := available(`(path=? OR substr(path,1,length(?)+1)=?||?)`, root, root, root, string(filepath.Separator)); n != 0 {
		t.Fatalf("%d of the removed source's files are still available", n)
	}
	if n := available(`path=?`, paths[1]); n != 1 {
		t.Fatal("a file beside the removed root was marked unavailable")
	}
	var queued int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_source_removals`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("the finished walk stayed queued: %d %v", queued, err)
	}
	t.Logf("197 descendants plus root: %d turns, largest %+v; plan %+v", turns, largest, dbwork.DefaultPlan(dbwork.ClassBackgroundMedia))
}

// Assert the actual class plan, rather than relying on the looser panic
// threshold. total_changes includes trigger programs and missing-title counts.
func assertRemovedSourcePlan(t *testing.T) {
	t.Helper()
	stats := dbwork.Plans()
	plan := dbwork.DefaultPlan(dbwork.ClassBackgroundMedia)
	if stats.Overruns != 0 || stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
		t.Fatalf("removed-source turn exceeded its unchanged class plan: %+v", stats)
	}
}

// Real linked titles exercise metadata source heads, dirty work and the
// canonical missing-title counter triggers, rather than only bare file rows.
// A nested live source and an already unavailable file must still let the
// path cursor advance, including when the service is reconstructed each turn.
func TestRemovedSourceLinkedFilesKeepWritesWithinClassPlan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	f := newObservationFixture(t)
	var root string
	if err := f.s.db.QueryRow(`SELECT root FROM remote_sources WHERE id='remote'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	const links = 4
	paths := []string{root, filepath.Join(root, "live", "kept.mkv"), root + "-sibling/kept.mkv"}
	for i := 0; i < 17; i++ {
		paths = append(paths, filepath.Join(root, fmt.Sprintf("f%04d.mkv", i)))
	}
	if err := dbwork.WithWriteTx(ctx, f.s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		library, err := compactcatalog.LibraryTx(ctx, tx, f.library)
		if err != nil {
			return err
		}
		for i, path := range paths {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", Duration: 60})
			if err != nil {
				return err
			}
			for j := 0; j < links; j++ {
				item, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: fmt.Sprintf("removed-%d-%d", i, j), Title: fmt.Sprintf("Film %d/%d", i, j)})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
					return err
				}
			}
			if i == len(paths)-1 {
				if err = compactcatalog.SetAssetAvailableTx(ctx, tx, asset, false); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO remote_sources(id,kind,name,root,generation) VALUES('live','webdav','Live',?,1)`, filepath.Join(root, "live")); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE remote_sources SET removed=1 WHERE id='remote'`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_source_removals(source_id,root) VALUES('remote',?)`, root)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Publish the fixture through the canonical reducers before measuring the
	// removal writes, including implementations that defer link settlement.
	if err := compactcatalog.Drain(ctx, f.s.db); err != nil {
		t.Fatal(err)
	}
	var largest dbwork.WriteCost
	for turn := 0; ; turn++ {
		dbwork.ResetPlans()
		more, err := (&Service{db: f.s.db}).markRemovedSourceAssets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertRemovedSourcePlan(t)
		cost := dbwork.Plans().Largest
		largest.Rows, largest.Pages, largest.VMOps = max(largest.Rows, cost.Rows), max(largest.Pages, cost.Pages), max(largest.VMOps, cost.VMOps)
		if !more {
			break
		}
		if turn >= len(paths) {
			t.Fatal("linked walk failed to advance")
		}
	}
	t.Logf("largest linked turn: %+v; plan: %+v", largest, dbwork.DefaultPlan(dbwork.ClassBackgroundMedia))
	// The source walk queues derived work. Count its settled result separately
	// so worker writes cannot be mistaken for the removal transaction's cost.
	if err := compactcatalog.Drain(ctx, f.s.db); err != nil {
		t.Fatal(err)
	}
	var available, missing, counted, queued int
	if err := f.s.db.QueryRow(`SELECT (SELECT count(*) FROM catalog_assets WHERE available=1), (SELECT count(*) FROM catalog_missing_titles), (SELECT sum(total) FROM catalog_missing_title_counts), (SELECT count(*) FROM remote_source_removals)`).Scan(&available, &missing, &counted, &queued); err != nil {
		t.Fatal(err)
	}
	if available != 2 || missing != (len(paths)-2)*links || counted != missing || queued != 0 {
		t.Fatalf("linked availability/canonical missing counts: available=%d missing=%d counted=%d queued=%d", available, missing, counted, queued)
	}
	for _, path := range paths[1:3] {
		var kept bool
		if err := f.s.db.QueryRow(`SELECT available FROM catalog_assets WHERE path=?`, path).Scan(&kept); err != nil || !kept {
			t.Fatalf("live/sibling file changed: %q %t %v", path, kept, err)
		}
	}
}

// A queued walk whose source is no longer removed ends without touching a file.
func TestRemovedSourceWalkStopsForALiveSource(t *testing.T) {
	ctx := context.Background()
	f, _, _, _, _, _ := nativeSaveFixture(t)
	var root string
	if err := f.s.db.QueryRow(`SELECT root FROM remote_sources WHERE id='remote'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithWriteTx(ctx, f.s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: filepath.Join(root, "a.mkv"), Size: 1, ModifiedNS: 1, Container: "mkv", Duration: 60}); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO remote_source_removals(source_id,root) VALUES('remote',?)`, root)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for {
		more, err := f.s.markRemovedSourceAssets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	var available, queued int
	if err := f.s.db.QueryRow(`SELECT (SELECT count(*) FROM catalog_assets WHERE available=1),(SELECT count(*) FROM remote_source_removals)`).Scan(&available, &queued); err != nil {
		t.Fatal(err)
	}
	if available != 1 || queued != 0 {
		t.Fatalf("a live source's walk marked files or stayed queued: available %d, queued %d", available, queued)
	}
}
