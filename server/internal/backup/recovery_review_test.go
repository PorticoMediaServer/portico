package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestRestoreManifestCannotReplaceTheLiveDatabaseAsAStateFile(t *testing.T) {
	svc, state, db := testService(t)
	if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('crash_probe','live')`); err != nil {
		t.Fatal(err)
	}
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(svc.backupRoot(), id)
	entry, err := copyOneFile(filepath.Join(folder, DatabaseName), filepath.Join(folder, "server.sqlite"), "server.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = HashedVersion // build the legacy malformed payload without the v3 writer admission
	manifest.Files = append(manifest.Files, entry)
	if err = writeManifest(folder, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Stage(context.Background(), Source{BackupID: id}); err != nil {
		if !errors.Is(err, ErrSourceInvalid) {
			t.Fatal(err)
		}
		return
	}
	db.Close()
	injected := errors.New("verification refused")
	_ = ApplyStaged(context.Background(), state, func(context.Context, string) error { return injected })
	if _, err = os.Stat(storagepaths.Path(state, "server.sqlite")); err != nil {
		t.Fatalf("malformed manifest deleted the live database during rollback: %v", err)
	}
	t.Fatal("staging accepted a reserved live-database state entry")
}

func TestRollbackResultFailureCanResume(t *testing.T) {
	f := stageAfterWrite(t)
	blocker := storagepaths.Path(f.state, RestoreResultFile+".next")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("verification refused")
	if err := ApplyStaged(context.Background(), f.state, func(context.Context, string) error { return injected }); err == nil {
		t.Fatal("blocked result unexpectedly succeeded")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	_ = restart(t, f.state, nil)
	if RestoreUnresolved(f.state) {
		t.Fatal("rollback could not resume after result storage was repaired")
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "live" {
		t.Fatalf("previous data = %q", got)
	}
	last, err := f.svc.LastRestore()
	if err != nil || last == nil || last.Outcome != "rolled_back" {
		t.Fatalf("rollback result = %+v, %v", last, err)
	}
}

func TestExternalDestinationIncludesLocalRecoveryCopies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	svc, state, db := testService(t)
	if err := svc.SetDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('crash_probe','before')`); err != nil {
		t.Fatal(err)
	}
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE configuration SET value='after' WHERE key='crash_probe'`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Stage(context.Background(), Source{BackupID: id}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = ApplyStaged(context.Background(), state, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	var recovery string
	for _, info := range listed {
		if info.Kind == KindPreRestore {
			recovery = info.ID
		}
	}
	if recovery == "" {
		t.Fatal("external backup destination hid the local pre-restore copy")
	}
	if _, err = svc.Stage(context.Background(), Source{BackupID: recovery}); err != nil {
		t.Fatal("could not stage the listed recovery copy by id", err)
	}
	// Pruning must also find old local copies even when scheduled backups are external.
	original := storagepaths.Path(state, BackupsDir, recovery)
	for i := 0; i < 4; i++ {
		dir := storagepaths.Path(state, BackupsDir, time.Date(2020, 1, i+1, 0, 0, 0, 0, time.UTC).Format(timestampLayout)+"-pre-restore")
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, e := os.ReadFile(filepath.Join(original, ManifestName))
		if e != nil {
			t.Fatal(e)
		}
		if err = writeTestFile(filepath.Join(dir, ManifestName), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.Prune(1); err != nil {
		t.Fatal(err)
	}
	listed, err = svc.List()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, info := range listed {
		if strings.HasSuffix(info.ID, "-pre-restore") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("kept %d recovery copies, want 3", n)
	}
	if err = svc.Delete(recovery); err != nil {
		t.Fatal("could not delete local recovery copy", err)
	}
}

func TestFailedAppliedRestoreWithMissingRecoveryCopyStaysUnresolved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	crashAt(t, "applied")
	if err := ApplyStaged(context.Background(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(storagepaths.Path(f.state, BackupsDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-pre-restore") {
			if err = os.RemoveAll(storagepaths.Path(f.state, BackupsDir, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	injected := errors.New("restored state refused")
	_ = restart(t, f.state, func(context.Context, string) error { return injected })
	if !RestoreUnresolved(f.state) {
		t.Fatal("unverified restored state was marked rolled back without the previous database")
	}
}

func TestCompletedRestoreCleanupReplayKeepsItsResultAndNewerState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, outcome := range []string{markerRestored, markerRolledBack} {
		t.Run(outcome, func(t *testing.T) {
			f := stageAfterWrite(t)
			crashAt(t, "completed:"+outcome)
			verify := func(context.Context, string) error { return nil }
			if outcome == markerRolledBack {
				verify = func(context.Context, string) error { return errors.New("verification refused") }
			}
			if err := ApplyStaged(context.Background(), f.state, verify); !errors.Is(err, errCrash) {
				t.Fatalf("completion interruption: %v", err)
			}
			first, err := f.svc.LastRestore()
			if err != nil || first == nil {
				t.Fatal(first, err)
			}
			if _, err = os.Stat(storagepaths.Path(f.state, RestoreStagedDir, RestoreMarkerFile)); err != nil {
				t.Fatal("completion journal lost before cleanup", err)
			}
			// A durable completed journal must not mistake later writes for a new restore.
			db := openReadOnly(t, storagepaths.Path(f.state, "server.sqlite"))
			if _, err = db.Exec(`UPDATE configuration SET value='newer' WHERE key='crash_probe'`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			called := false
			if err = restart(t, f.state, func(context.Context, string) error { called = true; return errors.New("must not verify again") }); err != nil {
				t.Fatal(err)
			}
			if called {
				t.Fatal("completed restore was verified again")
			}
			if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "newer" {
				t.Fatal("newer state was rolled back", got)
			}
			last, err := f.svc.LastRestore()
			if err != nil || last == nil || *last != *first {
				t.Fatalf("completion result changed: %+v -> %+v, %v", first, last, err)
			}
			if RestoreUnresolved(f.state) {
				t.Fatal("completion cleanup did not finish")
			}
		})
	}
}

func TestRestoreNamesAndLinkedManifestFilesAreRefused(t *testing.T) {
	for _, name := range []string{"server.sqlite", "restore.json", "restore-result.json", "backups/x", "../subtitles/a", "subtitles/../a", "subtitles//a", "subtitles/a:stream", "subtitles/a\\b", "subtitles/a\x00", "", ".", ".."} {
		if _, ok := stateFileNames([]FileEntry{{Name: name}}); ok {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	if _, ok := stateFileNames([]FileEntry{{Name: "subtitles/a"}, {Name: "subtitles/a"}}); ok {
		t.Fatal("accepted duplicate manifest file")
	}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := writeTestFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skip("symbolic links unavailable", err)
	}
	if err := checkManifestHashes(root, []FileEntry{{Name: "link", Bytes: 1}}); !errors.Is(err, ErrSourceInvalid) {
		t.Fatal("hash verification opened a link", err)
	}
	if err := os.Symlink(filepath.Dir(target), filepath.Join(root, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := checkManifestHashes(root, []FileEntry{{Name: "parent/target", Bytes: 1}}); !errors.Is(err, ErrSourceInvalid) {
		t.Fatal("hash verification followed an intermediate link", err)
	}
}

func TestManualBackupCannotJoinAPruneableScheduledCopy(t *testing.T) {
	svc, _, _ := testService(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error { close(entered); <-release; return nil })
	id, err := svc.Start(context.Background(), KindScheduled, "scheduled-probe")
	if err != nil {
		t.Fatal(err)
	}
	job, _ := svc.Job(id)
	defer func() { close(release); <-job.Done() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled copy did not start")
	}
	if got, err := svc.Start(context.Background(), KindManual, "manual-probe"); !errors.Is(err, ErrBackupRunning) {
		t.Fatalf("manual backup joined pruneable scheduled job %q: %v", got, err)
	}
}

func TestWindowsBackupNamesCannotAliasParentsOrDevices(t *testing.T) {
	for _, name := range []string{"subtitles/.. /server.sqlite", "subtitles/a.", "subtitles/a ", "subtitles/ a", "subtitles/NUL.srt", "subtitles/con", "subtitles/COM1.txt", "subtitles/LPT³.srt", "subtitles/COM².srt", "subtitles/a?b", "subtitles/a\x01b"} {
		if safeWindowsBackupName(name) {
			t.Fatalf("accepted Windows name alias %q", name)
		}
	}
	for _, name := range []string{"subtitles/film.en.srt", "channel-logos/.hidden.png", "subtitles/Cómédie.srt", "subtitles/COM10.srt"} {
		if !safeWindowsBackupName(name) {
			t.Fatalf("refused ordinary Windows filename %q", name)
		}
	}
}
