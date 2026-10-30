package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func drainIntents(f *loopFixture) {
	f.s.strictIntentRetry = time.Time{}
	f.s.startStrictIntents(context.Background())
}

func deferredStrict(t *testing.T, f *loopFixture, paths ...string) PathReport {
	t.Helper()
	out, err := f.s.ReportPathsStrict(context.Background(), paths, nil)
	if err != nil || !out.Deferred || len(out.Jobs) != 0 || len(out.Paths) != len(paths) {
		t.Fatalf("deferred: %+v %v", out, err)
	}
	for _, p := range out.Paths {
		if p.Status != "deferred" {
			t.Fatalf("path not deferred: %+v", p)
		}
	}
	return out
}
func intentSource(t *testing.T, f *loopFixture) strictSource {
	t.Helper()
	sources, err := f.s.strictSources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.id == f.source {
			return s
		}
	}
	t.Fatal("source missing")
	return strictSource{}
}
func TestStrictIntentSurvivesServiceRestartAndTransfersExactTargets(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Old (2000).mkv", "Other/Keep (2001).mkv")
	f.finish(f.queue())
	busy := f.queue()
	deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
	deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 1 {
		t.Fatal("repeated intent did not coalesce")
	}
	// A fresh service has none of the old service's maps, goroutines or callbacks.
	f.s = New(f.db, f.s.catalog, assets.Probe{})
	if err := f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	drainIntents(f)
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 || f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
		t.Fatal("transfer lost strict/ordinary separation")
	}
	var id string
	if err := f.db.QueryRow(`SELECT job_id FROM inventory_source_active WHERE source_id=?`, f.source).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == busy.ID || f.count(`SELECT count(*) FROM inventory_runs WHERE job_id=? AND reported=2 AND scan_mode='incremental'`, id) != 1 || f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='Alpha'`, id) != 1 || f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='.'`, id) != 0 {
		t.Fatal("restart invented a job or widened its target")
	}
	f.finish(Job{ID: id})
	if f.count(`SELECT count(*) FROM inventory_objects WHERE directory_path='Other' AND seen_job=?`, id) != 0 {
		t.Fatal("strict intent visited a sibling")
	}
}
func TestStrictIntentPausedSourceNeverAutoResumesAndHasNoExpiry(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	busy := f.queue()
	if err := f.s.Control(context.Background(), busy.ID, "pause", nil); err != nil {
		t.Fatal(err)
	}
	deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
	f.exec(`UPDATE inventory_strict_intents SET created_ms=1`)
	for i := 0; i < 3; i++ {
		drainIntents(f)
	}
	if f.count(`SELECT count(*) FROM jobs WHERE id=? AND status='paused'`, busy.ID) != 1 || f.count(`SELECT count(*) FROM inventory_strict_intents`) != 1 || f.count(`SELECT count(*) FROM jobs`) != 1 {
		t.Fatal("pause was resumed, expired or duplicated")
	}
	if err := f.s.Control(context.Background(), busy.ID, "resume", nil); err != nil {
		t.Fatal(err)
	}
	f.finish(busy)
	drainIntents(f)
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 || f.count(`SELECT count(*) FROM jobs`) != 2 {
		t.Fatal("resume did not eventually transfer pending work")
	}
}
func TestStrictIntentEverySourceFenceCancelsWithoutSuccessorWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{`identity_confirmed=0`, `generation=generation+1`, `incarnation='replacement'`, `root_identity='replacement'`, `configured_root=configured_root||'/moved'`, `root=root||'/moved'`, `kind='other'`, `follow_symlinks=1`, `enabled=0`, `health='root_changed'`} {
		t.Run(change, func(t *testing.T) {
			f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
			busy := f.queue()
			deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
			status, err := f.s.catalog.InventoryStatus(context.Background(), f.library)
			if err != nil || len(status.Sources) != 1 || !status.Sources[0].DeferredChanges || status.Sources[0].DeferredScanScope != "folders" {
				t.Fatalf("pending projection: %+v %v", status, err)
			}
			f.exec(`UPDATE library_sources SET `+change+` WHERE id=?`, f.source)
			status, err = f.s.catalog.InventoryStatus(context.Background(), f.library)
			if err != nil || len(status.Sources) != 1 || status.Sources[0].DeferredChanges || status.Sources[0].DeferredScanScope != "" {
				t.Fatalf("stale pending projection: %+v %v", status, err)
			}
			// A paused/running source cannot hide stale intent from retirement.
			drainIntents(f)
			if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 || f.count(`SELECT count(*) FROM jobs`) != 1 || f.count(`SELECT count(*) FROM jobs WHERE id=?`, busy.ID) != 1 {
				t.Fatal("old intent survived drift or touched successor work")
			}
		})
	}
}
func TestStrictIntentMissingPathProofRetainsOnlyNamedIntent(t *testing.T) {
	f := newLoopFixture(t, "movie", "Parent/Alpha/Film (2000).mkv")
	f.finish(f.queue())
	busy := f.queue()
	deferredStrict(t, f, filepath.Join(f.root, "Parent", "Alpha"))
	if err := os.RemoveAll(filepath.Join(f.root, "Parent", "Alpha")); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	drainIntents(f)
	if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path=?`, filepath.Join("Parent", "Alpha")) != 1 || f.count(`SELECT count(*) FROM jobs`) != 2 || f.count(`SELECT count(*) FROM inventory_path_notices`) != 0 {
		t.Fatal("missing proof lost/widened accepted work")
	}
}
func TestStrictIntentSourceCapCollapsesAndRemainsExplicitlyIncremental(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	f.finish(f.queue())
	busy := f.queue()
	source := intentSource(t, f)
	for offset := 0; offset < 501; offset += 100 {
		folders := []string{}
		for i := offset; i < offset+100 && i < 501; i++ {
			folders = append(folders, fmt.Sprintf("Folder-%03d", i))
		}
		gated, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassBackgroundMedia)
		if err != nil {
			t.Fatal(err)
		}
		whole, err := maintainIntentsTx(context.Background(), gated.Tx(), source, folders, false)
		if err == nil {
			err = gated.Commit()
		} else {
			gated.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		if whole != (offset == 500) {
			t.Fatalf("unexpected collapse at %d", offset)
		}
	}
	if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path='.' AND mode='incremental'`) != 1 {
		t.Fatal("501 folders were not coalesced into one source intent")
	}
	out := deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
	if out.Paths[0].ScanScope != "source" {
		t.Fatal("source-wide acceptance was concealed")
	}
	if err := f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	drainIntents(f)
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 || f.count(`SELECT count(*) FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE j.status='queued' AND r.reported=0 AND r.scan_mode='incremental'`) != 1 {
		t.Fatal("source fallback was not the ordinary incremental scanner")
	}
}
func TestStrictIntentTransferChecksExactPathsAfterTheProof(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv", "Beta/Film (2001).mkv")
	busy := f.queue()
	deferredStrict(t, f, filepath.Join(f.root, "Alpha"))
	source := intentSource(t, f)
	if err := f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	// Same row count and source identity, but a different path: a count is not a proof.
	f.exec(`UPDATE inventory_strict_intents SET relative_path='Beta'`)
	gated, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	job, err := transferIntentsTx(context.Background(), gated.Tx(), source, []string{"Alpha"}, false)
	if err != nil || job.ID != "" {
		t.Fatalf("adopted a changed target: %+v %v", job, err)
	}
	if err = gated.Commit(); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path='Beta'`) != 1 || f.count(`SELECT count(*) FROM jobs`) != 1 {
		t.Fatal("old proof removed or queued successor intent")
	}
}
func TestRefreshLibrariesAuthorizationIsAtomicAndJobsAreReal(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	denied := errors.New("denied")
	_, err := f.s.RefreshLibraries(context.Background(), f.library, func(*sql.Tx, string) error { return denied })
	if !errors.Is(err, denied) || f.count(`SELECT count(*) FROM jobs`) != 0 || f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 {
		t.Fatal("failed authorization wrote intent/jobs")
	}
	out, err := f.s.RefreshLibraries(context.Background(), f.library, nil)
	if err != nil || out.Deferred || len(out.Jobs) != 1 || out.Jobs[0].ID == "" || f.count(`SELECT count(*) FROM jobs WHERE id=?`, out.Jobs[0].ID) != 1 {
		t.Fatalf("not a real ordinary job: %+v %v", out, err)
	}
	if out.Jobs[0].ScanMode != "incremental" {
		t.Fatal("explicit refresh was silently promoted to first-import or integrity")
	}
	next, err := f.s.RefreshLibraries(context.Background(), f.library, nil)
	if err != nil || !next.Deferred || len(next.Jobs) != 0 || f.count(`SELECT count(*) FROM inventory_strict_intents WHERE mode='incremental'`) != 1 {
		t.Fatalf("busy refresh wasn't coalesced maintained work: %+v %v", next, err)
	}
}

func TestStrictIntentGlobalCapacityRefusesAtomically(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha/Film (2000).mkv")
	for i := 0; i < 9; i++ {
		root := filepath.Join(f.root, fmt.Sprintf("capacity-%d", i))
		f.exec(`INSERT INTO library_sources(id,library_id,configured_root,root,root_identity,identity_confirmed) VALUES(?,?,?,?,?,1)`, fmt.Sprintf("capacity-%d", i), f.library, root, root, "capacity-root")
	}
	f.exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO inventory_strict_intents(source_id,relative_path,library_id,source_generation,root_incarnation,root_identity,root,configured_root,source_kind,follow_symlinks,mode,created_ms) SELECT s.id,'Folder-'||x,s.library_id,s.generation,s.incarnation,s.root_identity,s.root,s.configured_root,s.kind,s.follow_symlinks,'strict',1 FROM n JOIN library_sources s ON s.id='capacity-'||CAST((x-1)/500 AS INTEGER)`, maxStrictIntentRows)
	source := intentSource(t, f)
	gated, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	_, err = maintainIntentsTx(context.Background(), gated.Tx(), source, []string{"Alpha"}, false)
	gated.Rollback()
	var refusal *StrictPathRefusal
	if !errors.As(err, &refusal) || refusal.Reason != StrictOverloaded || !refusal.Retryable() {
		t.Fatalf("wrong global-cap answer: %v", err)
	}
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != maxStrictIntentRows || f.count(`SELECT count(*) FROM inventory_strict_intents WHERE source_id=?`, f.source) != 0 || f.count(`SELECT count(*) FROM jobs`) != 0 {
		t.Fatal("global-cap refusal changed accepted work")
	}
}

func TestStrictIntentSuccessfulRetryRetiresOnlyCoveredPendingWork(t *testing.T) {
	f := newLoopFixture(t, "movie", "Parent/Alpha/Film (2000).mkv", "Other/Keep (2001).mkv")
	busy := f.queue()
	deferredStrict(t, f, filepath.Join(f.root, "Parent", "Alpha"), filepath.Join(f.root, "Other"))
	if err := f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	job := strictJob(t, f, filepath.Join(f.root, "Parent"))
	if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path=?`, filepath.Join("Parent", "Alpha")) != 0 || f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path='Other'`) != 1 {
		t.Fatal("retry lost an uncovered intent or retained a covered one")
	}
	f.finish(job)
	drainIntents(f)
	if f.count(`SELECT count(*) FROM inventory_strict_intents`) != 0 || f.count(`SELECT count(*) FROM jobs`) != 3 {
		t.Fatal("the exact remaining subtree was not transferred once")
	}
}
func TestStrictIntentTransfersBoundedRealHelperBatchesAndSkipsFailedProof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	files := []string{"A-missing/Old (2000).mkv"}
	for i := 0; i < 24; i++ {
		files = append(files, fmt.Sprintf("F%02d/Film (2000).mkv", i))
	}
	f := newLoopFixture(t, "movie", files...)
	busy := f.queue()
	paths := []string{filepath.Join(f.root, "A-missing")}
	for i := 0; i < 24; i++ {
		paths = append(paths, filepath.Join(f.root, fmt.Sprintf("F%02d", i)))
	}
	deferredStrict(t, f, paths...)
	if err := os.RemoveAll(filepath.Join(f.root, "A-missing")); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.s.SetStorage(storage.New(binary))
	if err = f.s.Control(context.Background(), busy.ID, "cancel", nil); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 8 && f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path<>'A-missing'`) > 0; turn++ {
		drainIntents(f)
		var id string
		err = f.db.QueryRow(`SELECT job_id FROM inventory_source_active WHERE source_id=?`, f.source).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		count := f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=?`, id)
		if count < 1 || count > 16 || f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='.'`, id) != 0 {
			t.Fatalf("unbounded/widened helper batch: %d", count)
		}
		f.finish(Job{ID: id})
	}
	if f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path<>'A-missing'`) != 0 || f.count(`SELECT count(*) FROM inventory_strict_intents WHERE relative_path='A-missing'`) != 1 {
		t.Fatal("bad proof starved unrelated accepted work, or was discarded")
	}
	// A failed proof must not spin on every quantum or silently age out.
	f.s.strictIntentRetry = time.Time{}
	f.s.strictIntentAfter = ""
	f.s.strictIntentFolders = map[string]string{}
	f.s.startStrictIntents(context.Background())
	retry := f.s.strictIntentRetry
	f.s.startStrictIntents(context.Background())
	if !f.s.strictIntentRetry.Equal(retry) {
		t.Fatal("proof retry ignored its backoff")
	}
}
