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
	// Startup queues no scan (#284), so nothing marks the moment the watcher is
	// live: the new file is written again about once a second until the
	// watcher reports it and the real worker publishes it.
	relative := "One (2000)/One (2000) - second.mkv"
	var written time.Time
	wait(func() bool {
		if time.Since(written) > time.Second {
			f.write(relative, "new media")
			written = time.Now()
		}
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
