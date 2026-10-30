package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func closureStart(t *testing.T, files ...string) (*loopFixture, Job, catalog.LibrarySource) {
	t.Helper()
	f := newLoopFixture(t, "book", files...)
	job := f.queue()
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	source, e := f.s.catalog.LibrarySource(context.Background(), f.source)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.readySource(context.Background(), job.ID, &source); e != nil {
		t.Fatal(e)
	}
	if job.ScanMode != "first-import" {
		t.Fatalf("not first import: %+v", job)
	}
	return f, job, source
}
func closureList(t *testing.T, f *loopFixture, job Job, source catalog.LibrarySource, paths ...string) {
	t.Helper()
	for _, path := range paths {
		// A listed directory is complete only after its real bounded pages
		// reach EOF. Leave every other top-level directory untouched so the
		// early-publication and independent-bad-top assertions stay meaningful.
		for turn := 0; ; turn++ {
			var state string
			if e := f.db.QueryRow(`SELECT state FROM inventory_directories WHERE job_id=? AND relative_path=?`, job.ID, path).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if state == "done" {
				break
			}
			if state != "pending" || turn == 1000 {
				t.Fatalf("directory %s did not reach EOF: state=%s turns=%d", path, state, turn)
			}
			if e := f.s.readDirectoryPage(context.Background(), job.ID, source, path, inventorySourceNames); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func closureStep(t *testing.T, f *loopFixture, job Job, source catalog.LibrarySource) {
	t.Helper()
	before := dbwork.Plans().Overruns
	if e := f.s.readingClosureQuantum(context.Background(), job.ID, source); e != nil {
		t.Fatal(e)
	}
	if stats := dbwork.Plans(); stats.Overruns != before {
		t.Fatalf("closure turn exceeded the class-default WritePlan: %+v", stats.Recent)
	}
}
func closureWorker(t *testing.T, f *loopFixture) {
	t.Helper()
	if _, e := compactcatalog.NewWorker(f.db).Step(context.Background(), 64); e != nil {
		t.Fatal(e)
	}
}
func closureUntil(t *testing.T, f *loopFixture, job Job, source catalog.LibrarySource, pred func() bool) {
	t.Helper()
	for i := 0; i < 3000; i++ {
		if pred() {
			return
		}
		closureStep(t, f, job, source)
		closureWorker(t, f)
		// Only the accepted D1 quiet gap may need real time in this package.
		if i%50 == 49 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatal("closure/Reading worker did not reach checkpoint")
}
func closureSeries(t *testing.T, f *loopFixture) string {
	t.Helper()
	var item int64
	if e := f.db.QueryRow(`SELECT item_id FROM reading_publications ORDER BY item_id LIMIT 1`).Scan(&item); e != nil {
		t.Fatal(e)
	}
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	v, e := compactcatalog.ReadingAcceptedValuesTx(context.Background(), tx, []int64{item})
	if e != nil {
		t.Fatal(e)
	}
	if v[item].Series == nil {
		return ""
	}
	return v[item].Series.Value
}
func closureIssue(t *testing.T, f *loopFixture, job Job, path string) {
	t.Helper()
	e := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return catalog.InventoryIssueTx(context.Background(), tx, job.ID, storage.InventoryIssue{Path: path, Reason: "directory_unreadable", Protect: true})
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestReadingClosurePublishesWhileOtherTopIsOpenAndReprovesLateCoverage(t *testing.T) {
	f, job, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "B/Later.epub")
	closureList(t, f, job, source, ".", "A", "A/Series")
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='B' AND state='pending'`, job.ID) != 1 {
		t.Fatal("B not open")
	}
	if closureSeries(t, f) != "" {
		t.Fatal("listing alone published folder context")
	}
	// The real derivation worker settles actual library and availability debt.
	closureUntil(t, f, job, source, func() bool { return f.count(`SELECT count(*) FROM reading_path_build WHERE phase='ready'`) > 0 })
	var old int
	if e := f.db.QueryRow(`SELECT id FROM inventory_reading_seals WHERE job_id=? AND top_level='A'`, job.ID).Scan(&old); e != nil {
		t.Fatal(e)
	}
	closureIssue(t, f, job, "B/Bad")
	// No reducer has run: raw coverage must already reject the READY build.
	if e := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, e := compactcatalog.StepReadingPaths(context.Background(), tx, 64)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 20 && f.count(`SELECT count(*) FROM reading_path_build WHERE phase='ready'`) > 0; i++ {
		closureWorker(t, f)
	}
	if closureSeries(t, f) != "" {
		t.Fatal("late issue activated old READY seal")
	}
	closureUntil(t, f, job, source, func() bool { return closureSeries(t, f) == "Series" })
	var next int
	if e := f.db.QueryRow(`SELECT max(id) FROM inventory_reading_seals WHERE job_id=? AND top_level='A'`, job.ID).Scan(&next); e != nil || next <= old {
		t.Fatalf("seal not renewed: %d -> %d %v", old, next, e)
	}
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='B' AND state='pending'`, job.ID) != 1 {
		t.Fatal("publication waited for B")
	}
	if f.count(`SELECT count(*) FROM reading_path_scope q JOIN reading_path_build b ON b.scope_id=q.id WHERE b.phase IN('capture','stage','ready') AND b.closure_seal<>q.closure_seal`) > 0 {
		t.Fatal("build/scope proof mismatch")
	}
}

func TestReadingClosureGenericDescendantAndIndependentBadTop(t *testing.T) {
	f, job, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "A/Series/Books/03 - Third.epub", "B/Bad.epub")
	closureList(t, f, job, source, ".", "A", "A/Series")
	for i := 0; i < 25; i++ {
		closureStep(t, f, job, source)
		closureWorker(t, f)
	}
	if f.count(`SELECT count(*) FROM inventory_reading_seals WHERE top_level='A'`) != 0 {
		t.Fatal("unlisted generic descendant sealed")
	}
	closureIssue(t, f, job, "B")
	closureList(t, f, job, source, "A/Series/Books")
	key, e := compactcatalog.ReadingDirectoryKey("A/Series/Books")
	if e != nil || key != "A/Series" {
		t.Fatalf("generic physical prefix: %q %v", key, e)
	}
	closureUntil(t, f, job, source, func() bool { return closureSeries(t, f) == "Series" })
	if f.count(`SELECT count(*) FROM inventory_reading_seals WHERE top_level='B'`) != 0 {
		t.Fatal("bad subtree sealed")
	}
	// A protecting generic descendant invalidates the exact logical dependency.
	closureIssue(t, f, job, "A/Series/Books")
	for i := 0; i < 60; i++ {
		closureStep(t, f, job, source)
		closureWorker(t, f)
	}
	if f.count(`SELECT count(*) FROM inventory_reading_directories WHERE relative_path='A' AND phase='blocked'`) != 1 {
		t.Fatal("protected generic descendant did not hold A")
	}
	// Accepted heads keep their original acceptance rules after publication.
	if closureSeries(t, f) != "Series" {
		t.Fatal("late issue erased accepted facts")
	}
}

func TestReadingClosureInterruptedFirstImportReconcilesRemovedDescendants(t *testing.T) {
	f, old, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/Books/Old/02 - Gone.epub", "B/Later.epub")
	closureList(t, f, old, source, ".", "A", "A/Series", "A/Series/Books", "A/Series/Books/Old")
	if e := f.s.Cancel(old.ID); e != nil {
		t.Fatal(e)
	}
	if e := os.RemoveAll(filepath.Join(f.root, "A/Series/Books")); e != nil {
		t.Fatal(e)
	}
	job := f.queue()
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	if e := f.s.readySource(context.Background(), job.ID, &source); e != nil {
		t.Fatal(e)
	}
	closureList(t, f, job, source, ".", "A", "A/Series")
	closureUntil(t, f, job, source, func() bool {
		return f.count(`SELECT count(*) FROM inventory_reading_seals WHERE job_id=? AND top_level='A'`, job.ID) > 0
	})
	if f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='A/Series/Books/Old/02 - Gone.epub' AND state='missing' AND last_missing_job=?`, job.ID) != 1 {
		t.Fatal("interrupted first-import leftover escaped absence reconciliation")
	}
	if f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND relative_path='A' AND missing_object='' AND reconciled=1`, job.ID) != 1 {
		t.Fatal("absence subcursor not at true EOF")
	}
}

func TestReadingClosureUnpublishedTurnsRejectSourceDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"cancel", "generation", "root", "rootpath", "incarnation", "job", "coverage", "availability", "library", "desired", "global", "disabled"} {
		for _, phase := range []string{"capture", "stage", "ready"} {
			t.Run(change+"/"+phase, func(t *testing.T) {
				f, job, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "B/Later.epub")
				closureList(t, f, job, source, ".", "A", "A/Series")
				closureUntil(t, f, job, source, func() bool { return f.count(`SELECT count(*) FROM reading_path_build WHERE phase=?`, phase) > 0 })
				switch change {
				case "cancel":
					if e := f.s.Cancel(job.ID); e != nil {
						t.Fatal(e)
					}
				case "generation":
					f.exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
				case "rootpath":
					f.exec(`UPDATE library_sources SET root=root||'/moved' WHERE id=?`, f.source)
				case "availability":
					f.exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,attempts,failed_ms) SELECT 20,item_id,'unsettled',1,1 FROM reading_publications LIMIT 1`)
				case "library":
					f.exec(`UPDATE reading_path_inputs SET revision=revision+1`)
				case "desired":
					f.exec(`UPDATE reading_path_scope SET desired=desired+1,queued=1 WHERE directory_key='A/Series'`)
				case "global":
					f.exec(`UPDATE catalog_derivations SET rebuilding=1 WHERE domain=28`)
				case "disabled":
					f.exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, f.source)
				case "root":
					f.exec(`UPDATE library_sources SET root_identity='replacement' WHERE id=?`, f.source)
				case "incarnation":
					f.exec(`UPDATE library_sources SET incarnation='replacement' WHERE id=?`, f.source)
				case "job":
					f.exec(`UPDATE reading_path_source_barrier SET job_id='replacement' WHERE source_id=?`, f.source)
				case "coverage":
					closureIssue(t, f, job, "A/Series/Books")
				}
				for i := 0; i < 50; i++ {
					if e := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
						_, e := compactcatalog.StepReadingPaths(context.Background(), tx, 64)
						return e
					}); e != nil {
						t.Fatal(e)
					}
				}
				if closureSeries(t, f) != "" {
					t.Fatal("unpublished drift activated old proof")
				}
			})
		}
	}
}

func TestReadingClosureFallbackWithoutProofAndChangedDirectory(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "remote", "changed"} {
		t.Run(mode, func(t *testing.T) {
			f, job, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "B/Later.epub")
			if mode == "unconfirmed" {
				f.exec(`UPDATE library_sources SET identity_confirmed=0 WHERE id=?`, f.source)
				source.IdentityConfirmed = false
			}
			if mode == "remote" {
				f.exec(`UPDATE library_sources SET kind='remote' WHERE id=?`, f.source)
				source.Kind = "remote"
			}
			// The adapter has already produced a local page in this fixture; testing
			// registration independently must still refuse unsupported source kinds.
			if mode != "remote" {
				closureList(t, f, job, source, ".", "A", "A/Series")
			}
			if mode == "changed" {
				f.write("A/Series/New.epub", "new")
			}
			for i := 0; i < 30; i++ {
				closureStep(t, f, job, source)
				closureWorker(t, f)
			}
			if f.count(`SELECT count(*) FROM inventory_reading_seals`) > 0 || closureSeriesOrEmpty(t, f) != "" {
				t.Fatal("uncertain closure published")
			}
			if mode == "changed" && f.count(`SELECT count(*) FROM inventory_reading_directories WHERE phase='blocked'`) == 0 {
				t.Fatal("changed listing was accepted")
			}
		})
	}
}
func closureSeriesOrEmpty(t *testing.T, f *loopFixture) string {
	if f.count(`SELECT count(*) FROM reading_publications`) == 0 {
		return ""
	}
	return closureSeries(t, f)
}

func TestReadingClosureCrashRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, checkpoint := range []string{"edge", "verify", "coverage", "coverage-partial", "reconcile", "reconcile-partial", "absence", "absence-parent", "seal", "sealed", "capture", "stage", "ready", "renew"} {
		t.Run(checkpoint, func(t *testing.T) {
			f, old, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "A/Series/Books/Old/03 - Gone.epub", "B/Later.epub")
			closureList(t, f, old, source, ".", "A", "A/Series", "A/Series/Books", "A/Series/Books/Old")
			if e := f.s.Cancel(old.ID); e != nil {
				t.Fatal(e)
			}
			if e := os.RemoveAll(filepath.Join(f.root, "A/Series/Books")); e != nil {
				t.Fatal(e)
			}
			job := f.queue()
			f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
			closureList(t, f, job, source, ".", "A", "A/Series")
			f.exec(`INSERT INTO inventory_scan_issues(job_id,relative_path,reason) VALUES(?,'B/nonprotecting','ignored_name')`, job.ID)
			var seq int
			var name, dbPath string
			if e := f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &dbPath); e != nil {
				t.Fatal(e)
			}
			if e := f.db.Close(); e != nil {
				t.Fatal(e)
			}
			binary, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(binary, "-test.run=^TestReadingClosureCrashChild$")
			cmd.Env = append(os.Environ(), "PORTICO_CLOSURE_CRASH="+checkpoint, "PORTICO_CLOSURE_DB="+dbPath, "PORTICO_CLOSURE_JOB="+job.ID, "PORTICO_CLOSURE_SOURCE="+source.ID)
			out, e := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 91 {
				t.Fatalf("crash writer: %v\n%s", e, out)
			}
			if st, e := os.Stat(dbPath + "-wal"); e != nil || st.Size() == 0 {
				t.Fatalf("no abrupt WAL: %v", e)
			}
			f.db, e = persistence.Open(dbPath)
			if e != nil {
				t.Fatal(e)
			}
			defer f.db.Close()
			f.s = New(f.db, catalog.New(f.db), assets.Probe{})
			closureUntil(t, f, job, source, func() bool { return closureSeries(t, f) == "Series" })
			if f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE '%Gone.epub' AND state='missing' AND last_missing_job=?`, job.ID) != 1 {
				t.Fatal("crash lost absence work")
			}
		})
	}
}
func TestReadingClosureCrashChild(t *testing.T) {
	checkpoint := os.Getenv("PORTICO_CLOSURE_CRASH")
	if checkpoint == "" {
		t.Skip("subprocess only")
	}
	db, e := persistence.Open(os.Getenv("PORTICO_CLOSURE_DB"))
	if e != nil {
		t.Fatal(e)
	}
	f := &loopFixture{t: t, db: db, source: os.Getenv("PORTICO_CLOSURE_SOURCE")}
	f.s = New(db, catalog.New(db), assets.Probe{})
	source, e := f.s.catalog.LibrarySource(context.Background(), f.source)
	if e != nil {
		t.Fatal(e)
	}
	job := Job{ID: os.Getenv("PORTICO_CLOSURE_JOB")}
	for i := 0; i < 3000; i++ {
		closureStep(t, f, job, source)
		closureWorker(t, f)
		reached := false
		switch checkpoint {
		case "edge":
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND relative_path='A/Series' AND phase='done' AND propagated=1`, job.ID) > 0
		case "coverage-partial":
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND phase='coverage' AND after_issue>0`, job.ID) > 0
		case "reconcile-partial":
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND phase='reconcile' AND after_path<>''`, job.ID) > 0
		case "absence-parent":
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND missing_object<>'' AND absence_parent='A/Series/Books'`, job.ID) > 0
		case "absence":
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE missing_object<>'' AND absence_parent<>''`) > 0
		case "sealed":
			reached = f.count(`SELECT count(*) FROM inventory_reading_seals WHERE job_id=?`, job.ID) > 0
		case "capture", "stage", "ready":
			reached = f.count(`SELECT count(*) FROM reading_path_build WHERE phase=?`, checkpoint) > 0
		case "renew":
			if f.count(`SELECT count(*) FROM reading_path_build WHERE phase='ready'`) > 0 {
				closureIssue(t, f, job, "B/Bad")
				closureStep(t, f, job, source)
				reached = true
			}
		default:
			reached = f.count(`SELECT count(*) FROM inventory_reading_directories WHERE job_id=? AND relative_path='A' AND phase=?`, job.ID, checkpoint) > 0
		}
		if reached {
			f.exec(`UPDATE inventory_runs SET last_turn='abrupt-closure-checkpoint' WHERE job_id=?`, job.ID)
			os.Exit(91)
		}
		if i%50 == 49 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("checkpoint not reached: %s", checkpoint)
}

func TestReadingClosureRetentionDrainsBoundedAndPreservesActive(t *testing.T) {
	f, job, source := closureStart(t, "A/Series/01 - First.epub", "B/Later.epub")
	closureList(t, f, job, source, ".", "A", "A/Series")
	for i := 0; i < 140; i++ {
		f.exec(`INSERT INTO inventory_reading_directories(job_id,relative_path,parent_path,top_level) VALUES(?,?,'A','A')`, job.ID, strings.Repeat("x", i+1))
	}
	before := f.count(`SELECT count(*) FROM inventory_reading_directories`)
	if _, e := f.s.PruneSource(context.Background(), source.ID); e != nil {
		t.Fatal(e)
	}
	if f.count(`SELECT count(*) FROM inventory_reading_directories`) != before {
		t.Fatal("active proof pruned")
	}
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	f.exec(`UPDATE jobs SET status='cancelled',created_at=? WHERE id=?`, old, job.ID)
	f.exec(`UPDATE inventory_runs SET finished_at=? WHERE job_id=?`, old, job.ID)
	for i := 0; i < 12 && f.count(`SELECT count(*) FROM inventory_reading_jobs`) > 0; i++ {
		before = f.count(`SELECT count(*) FROM inventory_reading_directories`)
		if _, e := f.s.PruneSource(context.Background(), source.ID); e != nil {
			t.Fatal(e)
		}
		after := f.count(`SELECT count(*) FROM inventory_reading_directories`)
		if before-after > 64 {
			t.Fatal("unbounded closure retirement")
		}
	}
	if f.count(`SELECT count(*) FROM inventory_reading_jobs`) > 0 {
		t.Fatal("proof job FK retained")
	}
	f.exec(`DELETE FROM inventory_directories WHERE job_id=?`, job.ID)
	f.exec(`DELETE FROM inventory_source_active WHERE job_id=?`, job.ID)
	f.exec(`DELETE FROM inventory_runs WHERE job_id=?`, job.ID)
	f.exec(`DELETE FROM jobs WHERE id=?`, job.ID)
}

func TestReadingClosureBusyProofRetriesAndSourceWideFallback(t *testing.T) {
	f, job, source := closureStart(t, "A/Series/01 - First.epub", "A/Series/02 - Second.epub", "B/Later.epub")
	closureList(t, f, job, source, ".", "A", "A/Series")
	f.s.storage = &storage.Client{Guard: func(string) error { return storage.ErrBusy }}
	if e := f.s.readingClosureQuantum(context.Background(), job.ID, source); !errors.Is(e, storage.ErrBusy) {
		t.Fatalf("busy proof swallowed: %v", e)
	}
	if f.count(`SELECT count(*) FROM inventory_reading_directories WHERE phase='blocked'`) > 0 {
		t.Fatal("busy proof was permanently blocked")
	}
	f.s.storage = nil
	closureUntil(t, f, job, source, func() bool { return closureSeries(t, f) == "Series" })
	// The same real ingestion/Reading pipeline still works without early proofs.
	g := newLoopFixture(t, "book", "A/Series/01 - First.epub", "A/Series/02 - Second.epub")
	full, e := g.s.QueueMode(context.Background(), g.library, g.source, "full", nil)
	if e != nil {
		t.Fatal(e)
	}
	g.finish(full)
	if g.count(`SELECT count(*) FROM inventory_reading_jobs`) > 0 {
		t.Fatal("non-first-import registered early proof")
	}
	for i := 0; i < 300 && closureSeries(t, g) != "Series"; i++ {
		closureWorker(t, g)
	}
	if closureSeries(t, g) != "Series" {
		t.Fatal("source-wide fallback stopped publishing")
	}
}

func TestReadingClosureRawCursorChargesRejectedObjects(t *testing.T) {
	f, job, source := closureStart(t, "A/Series/01 - First.epub", "B/Later.epub")
	closureList(t, f, job, source, ".", "A", "A/Series")
	var token string
	if e := f.db.QueryRow(`SELECT asset_id FROM inventory_objects LIMIT 1`).Scan(&token); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 140; i++ {
		f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,retired,seen_job) VALUES(?,?,?,?,?,'A/Series','r','{}',1,1,1,?)`, strings.Repeat("r", i+1), source.ID, token, source.Incarnation, "A/Series/"+strings.Repeat("z", i+1), job.ID)
	}
	closureUntil(t, f, job, source, func() bool {
		return f.count(`SELECT count(*) FROM inventory_reading_directories WHERE relative_path='A' AND phase='reconcile'`) > 0
	})
	// The captured upper pair is stable while every retired/rejected raw object
	// advances separately. No filtered LIMIT hides an uncharged input walk.
	for i := 0; i < 142; i++ {
		var before, after string
		f.db.QueryRow(`SELECT after_object FROM inventory_reading_directories WHERE relative_path='A'`).Scan(&before)
		closureStep(t, f, job, source)
		f.db.QueryRow(`SELECT after_object FROM inventory_reading_directories WHERE relative_path='A'`).Scan(&after)
		if i < 141 && before == after {
			t.Fatalf("raw cursor failed to advance at %d", i)
		}
	}
	closureUntil(t, f, job, source, func() bool { return f.count(`SELECT count(*) FROM inventory_reading_seals WHERE job_id=?`, job.ID) > 0 })
}
