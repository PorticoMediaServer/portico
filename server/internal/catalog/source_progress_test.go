package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// sourceProgressFixture is a real library, source and running inventory job on
// a disposable directory. The page commit below is the production writer.
type sourceProgressFixture struct {
	s      *Service
	db     *sql.DB
	root   string
	lib    Library
	source LibrarySource
	job    string
}

func newSourceProgressFixture(t *testing.T, files ...string) *sourceProgressFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "progress.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	lib, err := s.Create("Progress", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.LibrarySource(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := &sourceProgressFixture{s: s, db: db, root: root, lib: lib, source: source, job: "job-" + lib.ID}
	for _, name := range files {
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.queue(t)
	return f
}

// queue installs the rows ingestion writes when it starts a scan.
func (f *sourceProgressFixture) queue(t *testing.T) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		at := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running',?)`, f.job, f.lib.ID, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) VALUES(?,?,?,?,?,1,'inventory','full')`, f.job, f.source.ID, f.source.Generation, f.source.Incarnation, f.source.RootIdentity); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'.')`, f.job); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, f.source.ID, f.job)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *sourceProgressFixture) page(t *testing.T, relative string) storage.InventoryPage {
	t.Helper()
	p, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RootIdentity: f.source.RootIdentity, RelativePath: relative, Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *sourceProgressFixture) progress(t *testing.T, source string) string {
	t.Helper()
	var at string
	if err := f.db.QueryRow(`SELECT last_progress_at FROM library_source_progress WHERE source_id=?`, source).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func (f *sourceProgressFixture) progressRows(t *testing.T) int {
	t.Helper()
	var rows int
	if err := f.db.QueryRow(`SELECT count(*) FROM library_source_progress`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f *sourceProgressFixture) writeProgress(t *testing.T, source, at string) {
	t.Helper()
	if _, err := dbwork.ExecWrite(context.Background(), f.db, dbwork.ClassBackgroundMedia, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, at, source); err != nil {
		t.Fatal(err)
	}
}

// TestCommittedInventoryPageAdvancesProgressWithoutAuthorityWake commits real
// pages with the exact security subscriptions registered first: an ordinary
// folder page, then a page with a supported file (which also reads the source
// for asset custody). The pages read `libraries` and `library_sources` for
// custody and write only work tables and the private progress row, so neither
// moves AuthorityGeneration nor wakes an exact security subscriber, while the
// page's real `inventory_runs` UPDATE still wakes its own exact subscriber.
//
// Negative controls: with the timestamp back on library_sources, the
// generation assertion fails; with only the plain-read wake exemption reverted
// (statementHead.plainSelect), the exact-signal assertions fail and the
// generation assertion still passes.
func TestCommittedInventoryPageAdvancesProgressWithoutAuthorityWake(t *testing.T) {
	f := newSourceProgressFixture(t, filepath.Join("Film (2000)", "Film (2000).mkv"))
	ctx := context.Background()
	if f.source.LastProgressAt != "" {
		t.Fatalf("a new source must project an empty progress value, got %q", f.source.LastProgressAt)
	}
	if rows := f.progressRows(t); rows != 0 {
		t.Fatalf("source creation seeded %d progress rows; the table is sparse", rows)
	}
	libraryWake, sourceWake, runWake := worker.NewSignal(), worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){
		dbwork.WakeOnTableWrites(libraryWake, "libraries"),
		dbwork.WakeOnTableWrites(sourceWake, "library_sources"),
		dbwork.WakeOnTableWrites(runWake, "inventory_runs"),
	} {
		defer stop()
	}
	libraryWake.Take()
	sourceWake.Take()
	runWake.Take()
	before := dbwork.AuthorityGeneration()
	started := time.Now().Add(-time.Second)
	previous := ""
	for _, relative := range []string{".", "Film (2000)"} {
		if err := f.s.CommitInventoryPage(ctx, f.job, relative, "", f.page(t, relative)); err != nil {
			t.Fatalf("page %q: %v", relative, err)
		}
		after, err := f.s.LibrarySource(ctx, f.source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.LastProgressAt == "" || after.LastProgressAt == previous {
			t.Fatalf("page %q did not advance LastProgressAt (%q after %q)", relative, after.LastProgressAt, previous)
		}
		previous = after.LastProgressAt
		at, err := time.Parse(time.RFC3339Nano, after.LastProgressAt)
		if err != nil {
			t.Fatalf("progress is not the original RFC3339Nano text: %q %v", after.LastProgressAt, err)
		}
		if at.Before(started) {
			t.Fatalf("progress %q was not the page's own clock", after.LastProgressAt)
		}
		if got := f.progress(t, f.source.ID); got != after.LastProgressAt {
			t.Fatalf("projection %q and stored progress %q differ", after.LastProgressAt, got)
		}
		if rows := f.progressRows(t); rows != 1 {
			t.Fatalf("page %q left %d progress rows", relative, rows)
		}
		if got := dbwork.AuthorityGeneration(); got != before {
			t.Fatalf("page %q moved AuthorityGeneration %d → %d", relative, before, got)
		}
		if libraryWake.Take() {
			t.Fatalf("page %q woke the exact libraries subscription by reading it", relative)
		}
		if sourceWake.Take() {
			t.Fatalf("page %q woke the exact library_sources subscription by reading it", relative)
		}
		if !runWake.Take() {
			t.Fatalf("page %q's real inventory_runs UPDATE did not wake its exact subscriber", relative)
		}
	}
	var assets int
	if err := f.db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND retired=0`, f.source.ID).Scan(&assets); err != nil || assets != 1 {
		t.Fatalf("the supported-file page published %d objects: %v", assets, err)
	}
	// A real source transition still invalidates and wakes the same live
	// registration, so the silence above is not a dead subscription.
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE id=?`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	if !sourceWake.Take() {
		t.Fatal("a real source disable did not wake the exact library_sources subscription")
	}
	if got := dbwork.AuthorityGeneration(); got <= before {
		t.Fatalf("a real source disable did not move AuthorityGeneration: %d", got)
	}
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `UPDATE libraries SET name=name||' renamed' WHERE id=?`, f.lib.ID); err != nil {
		t.Fatal(err)
	}
	if !libraryWake.Take() {
		t.Fatal("a real library update did not wake the exact libraries subscription")
	}
}

// TestProgressStatementIsSilentInAnOrdinaryTransaction isolates the relocated
// writer. The same transaction has another real write, so the commit would
// announce any target it named; the progress statement names only its private
// table. With the original UPDATE library_sources this test fails on both the
// wake and the generation.
func TestProgressStatementIsSilentInAnOrdinaryTransaction(t *testing.T) {
	f := newSourceProgressFixture(t)
	ctx := context.Background()
	sourceWake := worker.NewSignal()
	stopSource := dbwork.WakeOnTableWrites(sourceWake, "library_sources")
	defer stopSource()
	before := dbwork.AuthorityGeneration()
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `UPDATE jobs SET processed=processed WHERE id=?`, f.job); err != nil {
		t.Fatal(err)
	}
	if sourceWake.Take() {
		t.Fatal("an unrelated jobs write woke the library_sources subscription")
	}
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, "2026-10-27T10:00:00.000000000Z", f.source.ID); err != nil {
		t.Fatal(err)
	}
	if sourceWake.Take() {
		t.Fatal("the relocated progress statement still woke the library_sources security subscription")
	}
	if got := dbwork.AuthorityGeneration(); got != before {
		t.Fatalf("the relocated progress statement moved AuthorityGeneration %d → %d", before, got)
	}
}

// TestInventoryPageRollbackDiscardsProgressWithoutAuthorityChange executes a
// real page write and then fails the batch, so the progress row must be the
// committed value again and no authority may be involved at all.
func TestInventoryPageRollbackDiscardsProgressWithoutAuthorityChange(t *testing.T) {
	f := newSourceProgressFixture(t, "Sub/Film (2000).mkv")
	ctx := context.Background()
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'Sub')`, f.job); err != nil {
		t.Fatal(err)
	}
	const baseline = "2026-10-27T00:00:00.000000000Z"
	f.writeProgress(t, f.source.ID, baseline)
	before := dbwork.AuthorityGeneration()
	valid := InventoryPageWrite{Relative: "Sub", Cursor: "", Page: f.page(t, "Sub")}
	invalid := InventoryPageWrite{Relative: ".", Cursor: "", Page: storage.InventoryPage{Entries: make([]storage.Snapshot, 129)}}
	if err := f.s.CommitInventoryPages(ctx, f.job, []InventoryPageWrite{valid, invalid}); err == nil {
		t.Fatal("the invalid second page committed")
	}
	if got := f.progress(t, f.source.ID); got != baseline {
		t.Fatalf("rolled-back progress persisted: got %q want %q", got, baseline)
	}
	if got := dbwork.AuthorityGeneration(); got != before {
		t.Fatalf("a rolled-back observational page moved AuthorityGeneration %d → %d", before, got)
	}
	var state string
	if err := f.db.QueryRow(`SELECT state FROM inventory_directories WHERE job_id=? AND relative_path='Sub'`, f.job).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Fatalf("rolled-back page left the directory in state %q", state)
	}
}

// TestSourceProgressProjectionAllTargetsAndMissingParent pins the 18-position
// projection: the relocated value is position 15, the missing child projects
// exactly empty text, and the six rows after it keep their own values.
func TestSourceProgressProjectionAllTargetsAndMissingParent(t *testing.T) {
	f := newSourceProgressFixture(t)
	ctx := context.Background()
	const (
		nextScan    = "2026-10-28T01:02:03.000000004Z"
		lastDone    = "2026-10-26T05:06:07.000000008Z"
		rootID      = "root-identity-exact"
		incarnation = "incarnation-exact"
	)
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassInteractive, `UPDATE library_sources SET kind='local',name='Renamed',configured_root=?,root=?,classification='network',generation=3,health='degraded',enabled=0,follow_symlinks=1,interval_seconds=900,next_scan_at=?,last_complete_at=?,root_identity=?,incarnation=?,identity_confirmed=1 WHERE id=?`, f.root, f.root, nextScan, lastDone, rootID, incarnation, f.source.ID); err != nil {
		t.Fatal(err)
	}
	const progress = "2026-10-27T12:34:56.000000007Z"
	f.writeProgress(t, f.source.ID, progress)
	src, err := f.s.LibrarySource(ctx, f.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	check := []struct {
		name string
		got  any
		want any
	}{
		{"ID", src.ID, f.source.ID},
		{"LibraryID", src.LibraryID, f.lib.ID},
		{"Kind", src.Kind, "local"},
		{"Name", src.Name, "Renamed"},
		{"Path", src.Path, f.root},
		{"ResolvedPath", src.ResolvedPath, f.root},
		{"Classification", src.Classification, "network"},
		{"Generation", src.Generation, int64(3)},
		{"Health", src.Health, "degraded"},
		{"Enabled", src.Enabled, false},
		{"FollowSymlinks", src.FollowSymlinks, true},
		{"IntervalSeconds", src.IntervalSeconds, int64(900)},
		{"NextScanAt", src.NextScanAt, nextScan},
		{"LastCompleteAt", src.LastCompleteAt, lastDone},
		{"LastProgressAt", src.LastProgressAt, progress},
		{"RootIdentity", src.RootIdentity, rootID},
		{"Incarnation", src.Incarnation, incarnation},
		{"IdentityConfirmed", src.IdentityConfirmed, true},
	}
	for _, c := range check {
		if c.got != c.want {
			t.Fatalf("%s scanned as %v, want %v: the projection is not the agreed 18 expressions", c.name, c.got, c.want)
		}
	}
	list, err := f.s.LibrarySources(ctx, f.lib.ID)
	if err != nil || len(list) != 1 || list[0].LastProgressAt != progress {
		t.Fatalf("list projection: %+v %v", list, err)
	}
	// A missing parent contributes no child: the original UPDATE's zero-row
	// behavior, with foreign keys enabled, rather than an FK refusal.
	result, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, "2026-10-27T13:00:00Z", "missing-parent")
	if err != nil {
		t.Fatalf("missing-parent progress upsert was refused: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 0 {
		t.Fatalf("missing-parent progress upsert touched %d rows (%v); it must be a no-op", rows, err)
	}
	// A source with no child still projects an exact empty string.
	if _, err = dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, `DELETE FROM library_source_progress WHERE source_id=?`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	src, err = f.s.LibrarySource(ctx, f.source.ID)
	if err != nil || src.LastProgressAt != "" {
		t.Fatalf("missing child did not project empty text: %q %v", src.LastProgressAt, err)
	}
}

// TestSourceProgressLifecycleAndCascade covers the agreed source lifecycle:
// disable/enable and same-ID root/incarnation replacement retain progress, and
// a real parent delete cascades one child so recreation starts empty.
func TestSourceProgressLifecycleAndCascade(t *testing.T) {
	f := newSourceProgressFixture(t)
	ctx := context.Background()
	const progress = "2026-10-27T22:22:22.000000001Z"
	f.writeProgress(t, f.source.ID, progress)
	for _, statement := range []string{
		`UPDATE library_sources SET enabled=0 WHERE id=?`,
		`UPDATE library_sources SET enabled=1 WHERE id=?`,
		`UPDATE library_sources SET incarnation='replacement-incarnation' WHERE id=?`,
		`UPDATE library_sources SET root=?,configured_root=?,root_identity='replacement-root' WHERE id=?`,
	} {
		var args []any
		if statement == `UPDATE library_sources SET root=?,configured_root=?,root_identity='replacement-root' WHERE id=?` {
			args = []any{f.root, f.root, f.source.ID}
		} else {
			args = []any{f.source.ID}
		}
		if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, statement, args...); err != nil {
			t.Fatal(err)
		}
		src, err := f.s.LibrarySource(ctx, f.source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if src.LastProgressAt != progress {
			t.Fatalf("%q cleared progress: got %q", statement, src.LastProgressAt)
		}
	}
	// Remove the real inventory dependants, then the parent itself.
	for _, statement := range []string{
		`DELETE FROM inventory_source_active WHERE source_id=?`,
		`DELETE FROM inventory_runs WHERE source_id=?`,
		`DELETE FROM inventory_objects WHERE source_id=?`,
	} {
		if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassBackgroundMedia, statement, f.source.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `DELETE FROM library_sources WHERE id=?`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	if rows := f.progressRows(t); rows != 0 {
		t.Fatalf("deleting the parent left %d progress rows; ON DELETE CASCADE must remove the one child", rows)
	}
	if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassSecurityFence, `INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES(?,?,'Recreated',?,?)`, f.source.ID, f.lib.ID, f.root, f.root); err != nil {
		t.Fatal(err)
	}
	src, err := f.s.LibrarySource(ctx, f.source.ID)
	if err != nil || src.LastProgressAt != "" {
		t.Fatalf("recreated source did not start with empty progress: %q %v", src.LastProgressAt, err)
	}
}

// TestRealSourceTransitionsStillInvalidateAndWake keeps every genuine source
// authority transition: each statement still moves the generation and wakes the
// exact source subscription. The relocated timestamp is not a substitute for
// any of them.
func TestRealSourceTransitionsStillInvalidateAndWake(t *testing.T) {
	f := newSourceProgressFixture(t)
	ctx := context.Background()
	transitions := []struct {
		name string
		sql  string
		args []any
	}{
		{"disable", `UPDATE library_sources SET enabled=0 WHERE id=?`, []any{f.source.ID}},
		{"enable", `UPDATE library_sources SET enabled=1 WHERE id=?`, []any{f.source.ID}},
		{"generation", `UPDATE library_sources SET generation=generation+1 WHERE id=?`, []any{f.source.ID}},
		{"health", `UPDATE library_sources SET health='offline' WHERE id=?`, []any{f.source.ID}},
		{"root", `UPDATE library_sources SET root=?,configured_root=?,root_identity='adopted-root',incarnation='adopted-incarnation' WHERE id=?`, []any{f.root + "/adopted", f.root + "/adopted", f.source.ID}},
		{"kind", `UPDATE library_sources SET kind='remote' WHERE id=?`, []any{f.source.ID}},
		{"config", `UPDATE library_sources SET name='Renamed',interval_seconds=900 WHERE id=?`, []any{f.source.ID}},
	}
	for _, transition := range transitions {
		t.Run(transition.name, func(t *testing.T) {
			signal := worker.NewSignal()
			unregister := dbwork.WakeOnTableWrites(signal, "library_sources")
			defer unregister()
			before := dbwork.AuthorityGeneration()
			if _, err := dbwork.ExecWrite(ctx, f.db, dbwork.ClassInteractive, transition.sql, transition.args...); err != nil {
				t.Fatal(err)
			}
			if !signal.Take() {
				t.Fatal("the real transition did not wake the exact library_sources subscription")
			}
			if got := dbwork.AuthorityGeneration(); got <= before {
				t.Fatalf("the real transition did not move AuthorityGeneration: %d", got)
			}
		})
	}
}
