package eventfeed

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// TestSecurityWakeIgnoresPrivateProgressAndKeepsSourceAuthority runs the real
// feed loop, so its security subscription list and Hub.Wake are the production
// ones. The relocated progress statement is ordinary catalogue work; a real
// source revision must still wake every stream.
func TestSecurityWakeIgnoresPrivateProgressAndKeepsSourceAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('progress','Progress','movie',?)`, root); err != nil {
		t.Fatal(err)
	}
	hub := New(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	go hub.Run(ctx)
	// Wait until Run's security subscription is live before testing silence.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=1-enabled WHERE id='progress'`); err != nil {
			t.Fatal(err)
		}
		select {
		case <-subscribed:
			goto primed
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("the real feed never woke for a source authority write")
			}
		}
	}
primed:
	// Let the priming wakes settle, then drain them: the progress write must
	// announce nothing of its own.
	time.Sleep(100 * time.Millisecond)
	for {
		select {
		case <-subscribed:
		default:
			goto drained
		}
	}
drained:
	before := dbwork.AuthorityGeneration()
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassBackgroundMedia, `INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, "2026-10-27T00:00:00.000000000Z", "progress"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-subscribed:
		t.Fatal("the private progress write woke a real feed stream")
	case <-time.After(200 * time.Millisecond):
	}
	if got := dbwork.AuthorityGeneration(); got != before {
		t.Fatalf("the private progress write moved AuthorityGeneration %d → %d", before, got)
	}
	marker := justStepped(t, db, subscribed)
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE id='progress'`); err != nil {
		t.Fatal(err)
	}
	promptSecurityWake(t, db, subscribed, marker, "a real source disable")
	if got := dbwork.AuthorityGeneration(); got <= before {
		t.Fatalf("a real source disable did not move AuthorityGeneration: %d", got)
	}
}

// runHubForTest starts the real feed loop and returns a reader channel that is
// live and drained: a true library authority write has already woken it. The
// cleanup cancels the loop and waits for Run to return.
func runHubForTest(t *testing.T, db *sql.DB, library string) (<-chan struct{}, func()) {
	t.Helper()
	hub := New(db)
	ctx, cancel := context.WithCancel(context.Background())
	subscribed, unsubscribe := hub.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.Run(ctx)
	}()
	t.Cleanup(func() {
		unsubscribe()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the feed loop did not stop")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for primed := false; !primed; {
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE libraries SET name=name WHERE id=?`, library); err != nil {
			t.Fatal(err)
		}
		select {
		case <-subscribed:
			primed = true
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("the real feed never woke for a library authority write")
			}
		}
	}
	return subscribed, func() {
		t.Helper()
		time.Sleep(100 * time.Millisecond)
		for {
			select {
			case <-subscribed:
			default:
				return
			}
		}
	}
}

// justStepped appends a marker event and waits until the feed loop has
// published it to reader. The loop paces its passes at least two seconds apart,
// so for that long no hint a later commit leaves can become a public event: a
// prompt wake in that window is the security subscription's own.
func justStepped(t *testing.T, db *sql.DB, reader <-chan struct{}) int64 {
	t.Helper()
	for drained := false; !drained; {
		select {
		case <-reader:
		default:
			drained = true
		}
	}
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return apievents.Append(tx, "test:none", "test.marker", "test", "marker", "1", nil)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reader:
	case <-time.After(5 * time.Second):
		t.Fatal("the feed loop never published the marker event")
	}
	return lastEventID(t, db)
}

// promptSecurityWake waits for reader after a commit made inside the window
// justStepped opened, and checks that no public event came with it.
func promptSecurityWake(t *testing.T, db *sql.DB, reader <-chan struct{}, marker int64, what string) {
	t.Helper()
	select {
	case <-reader:
	case <-time.After(time.Second):
		t.Fatalf("%s did not wake the open stream promptly", what)
	}
	if got := lastEventID(t, db); got != marker {
		t.Fatalf("%s's wake raced a public event (%d → %d); the wake proves nothing", what, marker, got)
	}
}

func pendingHints(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM api_change_pending`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func lastEventID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT COALESCE(max(id),0) FROM api_events`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestCommittedInventoryPagesDoNotWakeStreamsButSourceDisableDoes is the real
// hub boundary: the production security subscription and Hub.Wake, a real
// catalogue service, real local listings and CommitInventoryPage. Ordinary
// pages (a folder page, then a page with a supported file) must not cause a
// security wake of an open stream; a real source disable after them must wake
// it at once, without an event, a manual Wake or a heartbeat. Public scan
// events stay free to wake streams at the loop's own pace.
func TestCommittedInventoryPagesDoNotWakeStreamsButSourceDisableDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "Film (2000)"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "Film (2000)", "Film (2000).mkv"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service := catalog.New(db)
	lib, err := service.Create("Pages", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.LibrarySource(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	subscribed, drain := runHubForTest(t, db, lib.ID)
	job := "job-" + lib.ID
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		at := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running',?)`, job, lib.ID, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) VALUES(?,?,?,?,?,1,'inventory','full')`, job, source.ID, source.Generation, source.Incarnation, source.RootIdentity); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO inventory_directories(job_id,relative_path) VALUES(?,'.')`, job); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?)`, source.ID, job)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	before := dbwork.AuthorityGeneration()
	for _, relative := range []string{".", "Film (2000)"} {
		page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity, RelativePath: relative, Limit: 64})
		if err != nil {
			t.Fatal(err)
		}
		// The supported-file page legitimately leaves a public hint (a new
		// item), which the loop publishes at its next pass; inside the
		// marker's window only a security wake could reach the stream.
		marker := justStepped(t, db, subscribed)
		if err = service.CommitInventoryPage(ctx, job, relative, "", page); err != nil {
			t.Fatalf("page %q: %v", relative, err)
		}
		select {
		case <-subscribed:
			t.Fatalf("page %q woke an open stream", relative)
		case <-time.After(300 * time.Millisecond):
		}
		if got := lastEventID(t, db); got != marker {
			t.Fatalf("page %q appended a public event directly (%d → %d)", relative, marker, got)
		}
	}
	if got := dbwork.AuthorityGeneration(); got != before {
		t.Fatalf("ordinary pages moved AuthorityGeneration %d → %d", before, got)
	}
	if after, err := service.LibrarySource(ctx, source.ID); err != nil || after.LastProgressAt == "" {
		t.Fatalf("the pages did not record progress: %q %v", after.LastProgressAt, err)
	}
	marker := justStepped(t, db, subscribed)
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE library_sources SET enabled=0 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	promptSecurityWake(t, db, subscribed, marker, "a real source disable")
}
