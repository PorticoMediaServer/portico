package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// writeTestDatabase writes a small SQLite database holding body.
func writeTestDatabase(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE sample(body TEXT);INSERT INTO sample VALUES(?)`, body); err != nil {
		t.Fatal(err)
	}
}

func TestPreRestoreScrubResumesWithoutRehashingChangedState(t *testing.T) {
	folder := t.TempDir()
	dbPath := filepath.Join(folder, DatabaseName)
	writeTestDatabase(t, dbPath, "database before scrub")
	statePath := filepath.Join(folder, "Keys/networking-tls.pem")
	if err := writeTestFile(statePath, []byte("original certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := hashTree(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeManifest(folder, Manifest{Format: Format, Version: Version, Kind: KindPreRestore, Files: files}); err != nil {
		t.Fatal(err)
	}
	interrupted := errors.New("interrupted after database scrub")
	if err = ScrubPreRestoreCopy(context.Background(), folder, func(ctx context.Context, copy *dbwork.Candidate) error {
		if _, err := copy.ExecContext(ctx, `UPDATE sample SET body='scrubbed database'`); err != nil {
			return err
		}
		return interrupted
	}); !errors.Is(err, interrupted) {
		t.Fatalf("interruption: %v", err)
	}
	// Changing an untouched file must not be blessed by the resume operation.
	if err = writeTestFile(statePath, []byte("changed certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	scrub := func(context.Context, *dbwork.Candidate) error { calls++; return nil }
	if err = ScrubPreRestoreCopy(context.Background(), folder, scrub); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("changed state file accepted: %v", err)
	}
	if calls != 0 {
		t.Fatal("changed backup reached scrubber")
	}
	if err = writeTestFile(statePath, []byte("original certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ScrubPreRestoreCopy(context.Background(), folder, scrub); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(folder)
	if err != nil || !manifest.SignInsRemoved {
		t.Fatalf("completed manifest: %+v %v", manifest, err)
	}
	if err = checkManifestHashes(folder, manifest.Files); err != nil {
		t.Fatalf("scrubbed manifest is invalid: %v", err)
	}
	if err = ScrubPreRestoreCopy(context.Background(), folder, scrub); err != nil || calls != 1 {
		t.Fatalf("completed copy scrubbed again: %d %v", calls, err)
	}
}

func TestPreRestoreScrubRefusesCorruptDatabaseBeforeMutation(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, DatabaseName)
	if err := writeTestFile(path, []byte("original database"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := hashTree(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeManifest(folder, Manifest{Format: Format, Version: Version, Kind: KindPreRestore, Files: files}); err != nil {
		t.Fatal(err)
	}
	if err = writeTestFile(path, []byte("changed database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ScrubPreRestoreCopy(context.Background(), folder, func(context.Context, *dbwork.Candidate) error {
		t.Fatal("corrupt database reached scrubber")
		return nil
	}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("changed database accepted: %v", err)
	}
}

func TestPreRestoreScrubRefusesDatabaseAndWALSymlinks(t *testing.T) {
	for _, name := range []string{DatabaseName, DatabaseName + "-wal"} {
		t.Run(name, func(t *testing.T) {
			folder := t.TempDir()
			path := filepath.Join(folder, name)
			if err := writeTestFile(filepath.Join(folder, DatabaseName), []byte("database"), 0600); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside")
			if err := writeTestFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			if name == DatabaseName {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			files, err := hashTree(folder)
			if err != nil {
				t.Fatal(err)
			}
			if err = writeManifest(folder, Manifest{Format: Format, Version: Version, Kind: KindPreRestore, Files: files}); err != nil {
				t.Fatal(err)
			}
			if err = ScrubPreRestoreCopy(context.Background(), folder, func(context.Context, *dbwork.Candidate) error { t.Fatal("symlink reached SQLite scrubber"); return nil }); !errors.Is(err, ErrSourceInvalid) {
				t.Fatalf("symlink accepted: %v", err)
			}
		})
	}
}
