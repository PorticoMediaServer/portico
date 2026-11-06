package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestMain(m *testing.M) {
	// run() configures mediaexec with this test binary as the limits shim.
	if handled, _ := mediaexec.RunHelper(os.Args[1:]); handled {
		os.Exit(1)
	}
	if handled, err := decoder.RunHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		input, output, finish := diagnosticStorageHelperIO()
		if err := storage.Helper(input, output); err != nil {
			finish()
			os.Exit(1)
		}
		finish()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDefaultMediaCompositionAddsScansAndBrowsesWithoutRootsEnv(t *testing.T) {
	old, set := os.LookupEnv("PORTICO_MEDIA_ROOTS")
	if err := os.Unsetenv("PORTICO_MEDIA_ROOTS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if set {
			_ = os.Setenv("PORTICO_MEDIA_ROOTS", old)
		} else {
			_ = os.Unsetenv("PORTICO_MEDIA_ROOTS")
		}
	})
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	state, media := filepath.Join(base, "state"), filepath.Join(base, "media")
	for _, path := range []string{state, media} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	admin := administration.NewAt(db, state)
	store, managed, err := configureLocalMedia(db, state, helper, admin)
	if err != nil {
		t.Fatal(err)
	}
	store.Guard, store.MountedRoot = managed.Guard, managed.RootFor
	cat := catalog.New(db)
	cat.SetStorage(store)
	library, err := cat.Create("Movies", "movie", media)
	if err != nil {
		t.Fatal("ordinary library source refused", err)
	}
	if _, err := cat.Create("Private", "movie", state); err == nil {
		t.Fatal("server state accepted as library source")
	}
	scanner := ingestion.New(db, cat, assets.Probe{Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	job, err := scanner.Queue(library.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the original fixture paths/request lengths; only the children
	// started by Run below opt into the separate diagnostic directory.
	helperDiagnostics := enableStorageHelperDiagnostics(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { scanner.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(10 * time.Second)
	for {
		current, err := scanner.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "complete" {
			break
		}
		if current.Status == "failed" {
			t.Fatalf("default source scan failed: %+v", current)
		}
		select {
		case <-deadline:
			logStorageHelperDiagnostics(t, helperDiagnostics)
			t.Logf("empty-source timeout: GOOS=%s GOARCH=%s CPU=%d GOMAXPROCS=%d helper=%+v DB=%+v", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), store.Supervisor.Stats(), db.Stats())
			inspect, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
			var health, rootIdentity, directoryState, cursor, revision sql.NullString
			inspectErr := db.QueryRowContext(inspect, `SELECT s.health,r.root_identity,d.state,d.cursor,d.revision
 FROM inventory_runs r JOIN library_sources s ON s.id=r.source_id
 LEFT JOIN inventory_directories d ON d.job_id=r.job_id AND d.relative_path='.' WHERE r.job_id=?`, job.ID).Scan(&health, &rootIdentity, &directoryState, &cursor, &revision)
			stop()
			t.Logf("empty-source durable root: health=%+v identity=%+v state=%+v cursor=%+v revision=%+v error=%v", health, rootIdentity, directoryState, cursor, revision, inspectErr)
			stacks := make([]byte, 1<<20)
			n := runtime.Stack(stacks, true)
			t.Logf("empty-source goroutines (%d/%d bytes):\n%s", n, len(stacks), stacks[:n])
			t.Fatalf("source scan did not finish: %+v", current)
		case <-time.After(25 * time.Millisecond):
		}
	}
	page, err := admin.Browse(context.Background(), base, "", 100, false)
	if err != nil {
		t.Fatal("state parent not browsable", err)
	}
	var sawMedia, sawState bool
	for _, entry := range page.Entries {
		sawMedia = sawMedia || entry.Path == media
		sawState = sawState || entry.Path == state
	}
	if !sawMedia || sawState {
		t.Fatalf("picker exposed state or omitted media: %+v", page.Entries)
	}
	if _, err := admin.Browse(context.Background(), media, "", 100, false); err != nil {
		t.Fatal("ordinary media folder not browsable", err)
	}
}
