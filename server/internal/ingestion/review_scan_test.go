package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestDirectoryHintDoesNotAuthorizeEmptyRoot(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	if err := os.Remove(filepath.Join(f.root, "Film (2000).mkv")); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.ReportPaths(context.Background(), []string{f.root}, nil)
	if err != nil || len(result.Jobs) != 1 {
		t.Fatalf("report: %+v %v", result, err)
	}
	job := result.Jobs[0]
	for n := 0; n < 200; n++ {
		f.s.process(context.Background(), job.ID, f.library)
		state, err := f.s.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "failed" || state.Status == "complete" || state.Status == "complete_with_warnings" {
			if state.Status != "failed" || state.Error != "scan_source_unavailable" || f.count(`SELECT count(*) FROM inventory_objects WHERE state='available'`) != 1 {
				t.Fatalf("ambiguous empty-root report published absence: %+v; missing=%d", state, f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`))
			}
			return
		}
	}
	t.Fatal("scan did not stop")
}

func TestReportedRemovalRespectsStorageGuard(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	path := filepath.Join(f.root, "Film (2000).mkv")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store := storage.New("unused")
	store.Guard = func(string) error { return os.ErrPermission }
	f.s.SetStorage(store)
	evidence, err := f.s.removedPathEvidence(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 0 {
		t.Fatal("removal inspected directly in server despite denied storage operation")
	}
}

func TestScanStopsAtNameReaderDepthBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	relative := strings.Repeat("d/", 129) + "Film (2000).mkv"
	f := newLoopFixture(t, "movie", relative, "Healthy (2001).mkv")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_directories WHERE relative_path=?`, filepath.Dir(relative)); n != 0 {
		t.Fatalf("scan descended beyond name-reader bound: %d deepest directories", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE reason='path_depth_exceeded' AND protect=1`); n == 0 {
		t.Fatal("depth skip not diagnosed/protected")
	}
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Healthy'`); n != 1 {
		t.Fatal("healthy file dropped")
	}
}

func TestHiddenJunkDoesNotAuthorizeEmptyRoot(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.write(".keep", "configuration")
	f.finish(f.queue())
	if err := os.Remove(filepath.Join(f.root, "Film (2000).mkv")); err != nil {
		t.Fatal(err)
	}
	job := f.queue()
	for n := 0; n < 200; n++ {
		f.s.process(context.Background(), job.ID, f.library)
		state, err := f.s.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "failed" || state.Status == "complete" || state.Status == "complete_with_warnings" {
			if state.Status != "failed" || state.Error != "scan_source_unavailable" || f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`) != 0 {
				t.Fatalf("ignored junk authorized root absence: %+v", state)
			}
			return
		}
	}
	t.Fatal("scan did not stop")
}

func TestExplicitIgnoreCanExcludeAllPreviouslySeenMedia(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	f.write(".plexignore", "*.mkv\n")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 1 {
		t.Fatalf("explicit ignore did not reconcile media: %d", n)
	}
}

func TestOlderInventoryPageCannotReviveReportedRemoval(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	job := f.queue()
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	sources, err := f.s.catalog.LibrarySources(context.Background(), f.library)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %v %v", sources, err)
	}
	source := sources[0]
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RootIdentity: source.RootIdentity, RelativePath: ".", Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "Film (2000).mkv")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ReportPaths(context.Background(), []string{path}, nil); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 1 {
		t.Fatalf("report was not immediate: missing=%d", n)
	}
	if err = f.s.catalog.CommitInventoryPage(context.Background(), job.ID, ".", "", page); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 1 {
		t.Fatalf("older page revived deleted file: missing=%d", n)
	}
	f.finish(job)
	f.write("Film (2000).mkv", "returned media")
	report, err := f.s.ReportPaths(context.Background(), []string{path}, nil)
	if err != nil || len(report.Jobs) != 1 {
		t.Fatalf("returned report: %+v %v", report, err)
	}
	f.finish(report.Jobs[0])
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='available'`); n != 1 {
		t.Fatalf("new observation could not restore file: available=%d", n)
	}
}

func TestReportedRemovalBudgetDoesNotWaitForEachBlockedPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if runtime.GOOS == "windows" {
		t.Skip("uses a disposable POSIX helper; Windows cancellation needs native acceptance")
	}
	f := newLoopFixture(t, "movie", "One (2000).mkv", "Two (2001).mkv")
	f.finish(f.queue())
	paths := []string{filepath.Join(f.root, "One (2000).mkv"), filepath.Join(f.root, "Two (2001).mkv")}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(t.TempDir(), "blocked-helper")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.s.SetStorage(storage.New(binary))
	started := time.Now()
	result, err := f.s.ReportPaths(context.Background(), paths, nil)
	if err != nil || len(result.Jobs) != 1 {
		t.Fatalf("inconclusive report: %+v %v", result, err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("proof budget multiplied by path count")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 0 {
		t.Fatalf("inconclusive helper authorized missing=%d", n)
	}
}

func TestUnreadableOnlySubtreeFinishesWithProtectedMedia(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory permissions under a non-root user")
	}
	f := newLoopFixture(t, "movie", "Unreadable/Film (2000).mkv")
	f.finish(f.queue())
	blocked := filepath.Join(f.root, "Unreadable")
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	state := f.finish(f.queue())
	if state.Status != "complete_with_warnings" || f.count(`SELECT count(*) FROM inventory_objects WHERE state='available'`) != 1 {
		t.Fatalf("unreadable subtree failed or lost media: %+v", state)
	}
}
