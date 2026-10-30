package backup

import (
	"context"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// O3 review of be/backups: the current database is never deleted, whatever
// step a restore is interrupted at. Each test stages a restore of a backup
// taken before a later write, "crashes" at one step (applyStep), runs the
// startup sequence again (CleanupPartials, then ApplyStaged), and checks that
// the database the server was running is either still in place or safely in
// the pre-restore folder, byte for byte.

var errCrash = errors.New("simulated crash")

type crashFixture struct {
	state, liveHash string
	svc             *Service
}

func stageAfterWrite(t *testing.T) crashFixture {
	t.Helper()
	svc, state, db := testService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('crash_probe','backup')`); err != nil {
		t.Fatal(err)
	}
	id, err := svc.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE configuration SET value='live' WHERE key='crash_probe'`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Stage(ctx, Source{BackupID: id}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	checkpointDB(t, storagepaths.Path(state, "server.sqlite"))
	return crashFixture{state: state, liveHash: fileHashOrEmpty(t, storagepaths.Path(state, "server.sqlite")), svc: svc}
}

func crashAt(t *testing.T, step string) {
	t.Helper()
	previous := applyStep
	applyStep = func(at string) error {
		if at == step {
			return errCrash
		}
		return nil
	}
	t.Cleanup(func() { applyStep = previous })
}

// restart runs what main does at start: clean partial backups, apply.
func restart(t *testing.T, state string, verify VerifyFunc) error {
	t.Helper()
	applyStep = func(string) error { return nil }
	if err := CleanupPartials(state); err != nil {
		t.Fatal(err)
	}
	return ApplyStaged(context.Background(), state, verify)
}

func probe(t *testing.T, path string) string {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow(`SELECT value FROM configuration WHERE key='crash_probe'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func preRestoreHolding(t *testing.T, state, hash string) bool {
	t.Helper()
	entries, err := os.ReadDir(storagepaths.Path(state, BackupsDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-pre-restore") && fileHashOrEmpty(t, storagepaths.Path(state, BackupsDir, entry.Name(), DatabaseName)) == hash {
			return true
		}
	}
	return false
}

func TestApplyStagedCrashWhilePreparingKeepsTheLiveDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	crashAt(t, "preparing:"+DatabaseName+"-wal") // previous-state copy is not yet complete
	if err := ApplyStaged(context.Background(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatalf("crash: %v", err)
	}
	if got := fileHashOrEmpty(t, storagepaths.Path(f.state, "server.sqlite")); got != f.liveHash {
		t.Fatal("preparing changed the live database before its recovery copy completed")
	}
	if !preRestoreHolding(t, f.state, f.liveHash) {
		t.Fatal("the live database is not safe in a pre-restore folder after the crash")
	}
	// The next start's partial cleanup must not touch it, and the restore
	// completes.
	if err := restart(t, f.state, nil); err != nil {
		t.Fatal(err)
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "backup" {
		t.Fatalf("restored probe %q", got)
	}
	if !preRestoreHolding(t, f.state, f.liveHash) {
		t.Fatal("the pre-restore backup doesn't hold the database the server was running")
	}
	if last, _ := f.svc.LastRestore(); last == nil || last.Outcome != "restored" {
		t.Fatalf("last restore %+v", last)
	}
}

func TestApplyStagedCrashWhileSwitchingFinishes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	crashAt(t, "switching")
	if err := ApplyStaged(context.Background(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatalf("crash: %v", err)
	}
	if err := restart(t, f.state, nil); err != nil {
		t.Fatal(err)
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "backup" {
		t.Fatalf("restored probe %q", got)
	}
	if !preRestoreHolding(t, f.state, f.liveHash) {
		t.Fatal("the pre-restore backup doesn't hold the database the server was running")
	}
}

func TestApplyStagedCrashThenFailedVerificationRollsBack(t *testing.T) {
	f := stageAfterWrite(t)
	crashAt(t, "preparing:"+DatabaseName+"-wal")
	if err := ApplyStaged(context.Background(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatalf("crash: %v", err)
	}
	injected := errors.New("injected migration failure")
	if err := restart(t, f.state, func(context.Context, string) error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("apply: %v", err)
	}
	if got := fileHashOrEmpty(t, storagepaths.Path(f.state, "server.sqlite")); got != f.liveHash {
		t.Fatal("the live database was not put back")
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "live" {
		t.Fatalf("probe after rollback %q", got)
	}
	if last, _ := f.svc.LastRestore(); last == nil || last.Outcome != "rolled_back" {
		t.Fatalf("last restore %+v", last)
	}
}

func TestApplyStagedCrashAfterSwitchVerifies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	crashAt(t, "applied")
	if err := ApplyStaged(context.Background(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatalf("crash: %v", err)
	}
	if err := restart(t, f.state, nil); err != nil {
		t.Fatal(err)
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "backup" {
		t.Fatalf("restored probe %q", got)
	}
}

// Rollback crashes (lead review): a crash anywhere in a rollback resumes as a
// rollback at the next start, never as "applied", and ends with the database
// the server was running, byte for byte, and outcome rolled_back.
func TestApplyStagedCrashDuringRollbackResumes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, step := range []string{"rolling_back", "rollback:wal-removed", "rollback:database"} {
		t.Run(step, func(t *testing.T) {
			f := stageAfterWrite(t)
			injected := errors.New("injected migration failure")
			crashAt(t, step)
			if err := ApplyStaged(context.Background(), f.state, func(context.Context, string) error { return injected }); !errors.Is(err, errCrash) {
				t.Fatalf("crash: %v", err)
			}
			raw, err := os.ReadFile(storagepaths.Path(f.state, RestoreStagedDir, RestoreMarkerFile))
			if err != nil || !strings.Contains(string(raw), `"rolling_back"`) {
				t.Fatalf("the marker isn't rolling_back after the crash: %s %v", raw, err)
			}
			// The next start resumes the rollback; a verification that would
			// pass must not be run on whatever database is in place.
			verified := false
			if err = restart(t, f.state, func(context.Context, string) error { verified = true; return nil }); err == nil {
				t.Fatal("a resumed rollback reported success")
			}
			if verified {
				t.Fatal("a resumed rollback verified the database in place")
			}
			if got := fileHashOrEmpty(t, storagepaths.Path(f.state, "server.sqlite")); got != f.liveHash {
				t.Fatal("the live database was not put back byte for byte")
			}
			if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "live" {
				t.Fatalf("probe after the resumed rollback %q", got)
			}
			if last, _ := f.svc.LastRestore(); last == nil || last.Outcome != "rolled_back" {
				t.Fatalf("last restore %+v", last)
			}
			if _, err = os.Stat(storagepaths.Path(f.state, RestoreStagedDir)); !os.IsNotExist(err) {
				t.Fatal("the staged folder is still there after the rollback finished")
			}
			entries, _ := os.ReadDir(storagepaths.Path(f.state, BackupsDir))
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), "-pre-restore") {
					t.Fatalf("the pre-restore folder %s is left after a complete rollback", entry.Name())
				}
			}
		})
	}
}

// The restored database's WAL never survives next to the previous database:
// it is removed before the previous database comes back.
func TestRollbackRemovesTheRestoredWALFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	injected := errors.New("injected migration failure")
	// Verification writes to the restored database (its WAL is non-empty) and
	// then fails; the crash lands right after the WAL removal.
	crashAt(t, "rollback:wal-removed")
	err := ApplyStaged(context.Background(), f.state, func(ctx context.Context, path string) error {
		db, err := persistence.Open(path)
		if err != nil {
			return err
		}
		_, _ = db.Exec(`INSERT INTO configuration(key,value) VALUES('restored_write','x') ON CONFLICT(key) DO UPDATE SET value='x'`)
		db.Close()
		return injected
	})
	if !errors.Is(err, errCrash) {
		t.Fatalf("crash: %v", err)
	}
	if _, err = os.Stat(storagepaths.Path(f.state, "server.sqlite-wal")); !os.IsNotExist(err) {
		t.Fatal("the restored WAL is still beside the database at the rename")
	}
	if err = restart(t, f.state, nil); err == nil {
		t.Fatal("a resumed rollback reported success")
	}
	if got := probe(t, storagepaths.Path(f.state, "server.sqlite")); got != "live" {
		t.Fatalf("probe after rollback %q", got)
	}
}
