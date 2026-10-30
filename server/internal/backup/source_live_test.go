package backup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The probe runs in another process: POSIX record locks belong to a process,
// so a second descriptor in this test process would not observe its own locks.
// It never changes database bytes. Opening/closing the probe's descriptor only
// affects the child process's locks.
func assertSQLiteSourceLocks(t *testing.T, database string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Linux lock regression requires python3:", err)
	}
	const probe = `import errno,fcntl,sys
with open(sys.argv[1], 'r+b', buffering=0) as f:
 try:
  fcntl.lockf(f, fcntl.LOCK_EX | fcntl.LOCK_NB, int(sys.argv[3]), int(sys.argv[2]))
 except OSError as e:
  if e.errno in (errno.EACCES, errno.EAGAIN): sys.exit(0)
  raise
 sys.exit(2)
`
	for _, lock := range []struct {
		suffix string
		at, n  int64
	}{
		{"", 0x40000002, 510}, // SQLite's shared main-file lock range
		{"-shm", 128, 1},      // Unix VFS shared-memory dead-man switch
	} {
		cmd := exec.Command(python, "-c", probe, database+lock.suffix, strconv.FormatInt(lock.at, 10), strconv.FormatInt(lock.n, 10))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("live SQLite %s lock was lost (probe could take it): %v %s", lock.suffix, err, out)
		}
	}
}

func TestLiveSourceSelectionPreservesSQLiteLocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if runtime.GOOS != "linux" {
		t.Skip("checks Linux SQLite POSIX lock ranges with an independent process")
	}
	for _, operation := range []string{"inspect", "stage"} {
		for _, selected := range []string{"database", "database-link", "shm", "shm-link", "manifest-link", "backup-database-link"} {
			t.Run(operation+"/"+selected, func(t *testing.T) {
				s, state, db := testService(t)
				ctx := context.Background()
				if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO libraries(id,name,kind,root) VALUES('in-wal','In WAL','movie','/media/in-wal')`); err != nil {
					t.Fatal(err)
				}
				live := filepath.Join(state, DatabaseName)
				path := live
				valid := selected == "database" || selected == "database-link"
				switch selected {
				case "database-link":
					path = filepath.Join(t.TempDir(), "selected.sqlite")
					if err := os.Link(live, path); err != nil {
						t.Fatal(err)
					}
				case "shm":
					path += "-shm"
				case "shm-link":
					path = filepath.Join(t.TempDir(), "selected.shm")
					if err := os.Link(live+"-shm", path); err != nil {
						t.Fatal(err)
					}
				case "manifest-link":
					path = t.TempDir()
					if err := os.Link(live, filepath.Join(path, ManifestName)); err != nil {
						t.Fatal(err)
					}
				case "backup-database-link":
					id, err := s.Create(ctx, KindManual)
					if err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(state, BackupsDir, id)
					candidate := filepath.Join(path, DatabaseName)
					if err = os.Remove(candidate); err != nil {
						t.Fatal(err)
					}
					if err = os.Link(live, candidate); err != nil {
						t.Fatal(err)
					}
				}
				assertSQLiteSourceLocks(t, live)
				if operation == "inspect" {
					out, err := s.InspectSource(ctx, Source{Path: path})
					if valid && (err != nil || out.Encrypted) || !valid && err == nil {
						t.Fatalf("inspection: %+v %v", out, err)
					}
				} else {
					out, err := s.Stage(ctx, Source{Path: path})
					if valid && (err != nil || !out.Staged) || !valid && err == nil {
						t.Fatalf("staging: %+v %v", out, err)
					}
					if valid {
						raw, err := os.ReadFile(filepath.Join(state, RestoreStagedDir, RestoreMarkerFile))
						var marker restoreMarker
						if err != nil || json.Unmarshal(raw, &marker) != nil || marker.Source.Path != path {
							t.Fatal("staging exposed/replaced the selected source path", err)
						}
					}
				}
				assertSQLiteSourceLocks(t, live)
				var n int
				if err := db.QueryRow(`SELECT count(*) FROM libraries WHERE id='in-wal'`).Scan(&n); err != nil || n != 1 {
					t.Fatalf("live WAL row changed: %d %v", n, err)
				}
				if operation == "inspect" && s.RestoreProgress() != nil {
					t.Fatal("inspection started restore work")
				}
				assertNoScratch(t, state, filepath.Join(state, BackupsDir))
			})
		}
	}
}

func TestClosingLiveSourceKeepsLocksUntilConnectionsDrain(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks Linux SQLite POSIX lock ranges with an independent process")
	}
	s, state, db := testService(t)
	ctx := context.Background()
	if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO libraries(id,name,kind,root) VALUES('in-wal','In WAL','movie','/media/in-wal')`); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := dbwork.ReserveWriterConn(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(state, DatabaseName)
	assertSQLiteSourceLocks(t, live)
	out, err := s.InspectSource(ctx, Source{Path: live})
	if err != nil || out.Encrypted {
		t.Fatalf("inspection: %+v %v", out, err)
	}
	assertSQLiteSourceLocks(t, live)
}
