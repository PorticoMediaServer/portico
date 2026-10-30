package ingestion

import (
	"context"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"runtime"
	"testing"
	"time"
)

func TestNativeMonitorStartsALimitedScanForTheChangedFolder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if runtime.GOOS != "linux" {
		t.Skip("native runtime acceptance is exercised on Linux here")
	}
	f := newLoopFixture(t, "movie", "One (2000)/One (2000).mkv", "Two (2001)/Two (2001).mkv")
	f.finish(f.queue())
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	f.s.SetStorage(store)
	f.s.catalog.SetStorage(store)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { f.s.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("monitor or worker did not stop")
		}
	}()
	wait := func(ready func() bool) {
		t.Helper()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("monitor did not advance", ctx.Err())
			case <-ticker.C:
			}
		}
	}
	// The startup scan completes after the watcher was launched. It also proves
	// the service is running its real worker rather than only a helper callback.
	wait(func() bool {
		return f.count(`SELECT count(*) FROM jobs WHERE status IN('complete','complete_with_warnings')`) >= 2
	})
	f.write("One (2000)/One (2000) - second.mkv", "new media")
	relative := "One (2000)/One (2000) - second.mkv"
	wait(func() bool {
		return f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND state='available'`, relative) == 1
	})
	var job string
	if err = f.db.QueryRow(`SELECT seen_job FROM inventory_objects WHERE relative_path=?`, relative).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM inventory_run_targets WHERE job_id=? AND relative_path='One (2000)'`, job); n != 1 {
		t.Fatal("watcher did not seed the changed folder")
	}
	if n := f.count(`SELECT count(*) FROM inventory_directories WHERE job_id=? AND relative_path='Two (2001)'`, job); n != 0 {
		t.Fatal("watcher walked an unrelated folder")
	}
}
