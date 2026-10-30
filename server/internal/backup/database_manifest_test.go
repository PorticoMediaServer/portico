package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The launch-audit fixture changed a text payload without violating SQLite's
// integrity_check. Reproduce that corruption through the production scrubber.
func TestBackupReportsStructurallyValidDatabaseCorruption(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []Kind{KindManual, KindScheduled} {
		t.Run(string(kind), func(t *testing.T) {
			svc, state, db := testService(t)
			svc.SetCopyScrubber(identity.ScrubBackupCopy)
			if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('launch_audit_marker','AUDIT_ORIGINAL_VALUE')`); err != nil {
				t.Fatal(err)
			}
			id, err := svc.Create(context.Background(), kind)
			if err != nil {
				t.Fatal(err)
			}
			folder := storagepaths.Path(state, BackupsDir, id)
			manifest, err := readManifest(folder)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range manifest.Files {
				found = found || entry.Name == DatabaseName
			}
			if manifest.Version != Version || !found {
				t.Fatalf("database checksum missing: %+v", manifest)
			}
			if err = checkSourceHashes(folder, manifest); err != nil {
				t.Fatalf("finalized copy hash: %v", err)
			}
			path := filepath.Join(folder, DatabaseName)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(raw, []byte("AUDIT_ORIGINAL_VALUE")) != 1 {
				t.Fatal("audit payload not unique")
			}
			raw = bytes.Replace(raw, []byte("AUDIT_ORIGINAL_VALUE"), []byte("BUDIT_ORIGINAL_VALUE"), 1)
			if err = writeTestFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			// Run SQLite inspection on a separate copy; inspection can checkpoint or
			// rewrite bytes, and must not alter the source whose hashes are tested.
			candidate := filepath.Join(t.TempDir(), DatabaseName)
			if err = writeTestFile(candidate, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = persistence.InspectCandidate(context.Background(), filepath.Dir(candidate), filepath.Base(candidate)); err != nil {
				t.Fatalf("corruption should be structurally valid: %v", err)
			}
			if err = checkSourceHashes(folder, manifest); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("strict validation accepted changed database: %v", err)
			}
			for _, source := range []Source{{BackupID: id}, {Path: folder}} {
				out, err := svc.Stage(context.Background(), source)
				if err != nil || out.Validation.Recovery == nil || out.Validation.Recovery.ManifestDamage == nil || !out.Validation.Recovery.ManifestDamage.UnverifiedDatabase {
					t.Fatalf("readable database recovery was not explicitly disclosed: %+v %v", out, err)
				}
			}
		})
	}
}

func TestBackupDatabaseManifestVersionsAndStructuralChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, version := range []int{LegacyVersion, HashedVersion, Version} {
		t.Run(itoa(version), func(t *testing.T) {
			svc, state, _ := testService(t)
			id, err := svc.Create(context.Background(), KindManual)
			if err != nil {
				t.Fatal(err)
			}
			folder := storagepaths.Path(state, BackupsDir, id)
			manifest, err := readManifest(folder)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Version = version
			manifest.Files = nil // v1's ordinary backup shape
			if err = writeManifest(folder, manifest); err != nil {
				t.Fatal(err)
			}
			out, err := svc.Stage(context.Background(), Source{Path: folder})
			if version == HashedVersion {
				if !errors.Is(err, ErrSourceInvalid) {
					t.Fatalf("hashed v2 missing DB accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("legacy backup compatibility: %v", err)
			}
			if version == Version && (out.Validation.Recovery == nil || out.Validation.Recovery.ManifestDamage == nil || !out.Validation.Recovery.ManifestDamage.UnverifiedDatabase) {
				t.Fatal("v3 missing database expectation was not disclosed")
			}
			// Missing database hashes cannot waive SQLite validation.
			if err = writeTestFile(filepath.Join(folder, DatabaseName), []byte("not a SQLite database"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.Stage(context.Background(), Source{Path: folder}); !errors.Is(err, persistence.ErrCandidateNotPortico) {
				t.Fatalf("legacy structural corruption accepted: %v", err)
			}
		})
	}
	// A matching current-format checksum cannot waive structural validation.
	svc, state, _ := testService(t)
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	if err = writeTestFile(filepath.Join(folder, DatabaseName), []byte("not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := hashFile(context.Background(), folder, DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files = []FileEntry{entry}
	if err = writeManifest(folder, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Stage(context.Background(), Source{Path: folder}); !errors.Is(err, persistence.ErrCandidateNotPortico) {
		t.Fatalf("hashed structural corruption accepted: %v", err)
	}
}

func TestBackupFinalizesScrubberWALBeforeHashing(t *testing.T) {
	svc, state, _ := testService(t)
	svc.SetCopyScrubber(func(ctx context.Context, copy *dbwork.Candidate) error {
		if _, err := copy.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES('wal_marker','scrubbed WAL content')`); err != nil {
			return err
		}
		if size, err := walBytes(copy.Path() + "-wal"); err != nil || size == 0 {
			t.Fatalf("scrubber WAL fixture: %d %v", size, err)
		}
		return nil // Finalization must fold the log before hashing.
	})
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	manifest, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkSourceHashes(folder, manifest); err != nil {
		t.Fatalf("hash changed after final SQLite close: %v", err)
	}
	if size, err := walBytes(filepath.Join(folder, DatabaseName) + "-wal"); err != nil || size != 0 {
		t.Fatalf("finalized WAL: %d %v", size, err)
	}
	if _, err = svc.Stage(context.Background(), Source{BackupID: id}); err != nil {
		t.Fatal(err)
	}
	staged := openReadOnly(t, storagepaths.Path(state, RestoreStagedDir, DatabaseName))
	var value string
	if err = staged.QueryRow(`SELECT value FROM configuration WHERE key='wal_marker'`).Scan(&value); err != nil || value != "scrubbed WAL content" {
		t.Fatalf("WAL content lost: %q %v", value, err)
	}
}

func TestBackupUnlistedWALRequiresExplicitRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, version := range []int{LegacyVersion, HashedVersion, Version} {
		t.Run(itoa(version), func(t *testing.T) {
			svc, state, _ := testService(t)
			id, err := svc.Create(context.Background(), KindManual)
			if err != nil {
				t.Fatal(err)
			}
			folder := storagepaths.Path(state, BackupsDir, id)
			manifest, err := readManifest(folder)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Version = version
			if version == LegacyVersion {
				manifest.Files = nil
			}
			if err = writeManifest(folder, manifest); err != nil {
				t.Fatal(err)
			}
			writer, err := sql.Open("sqlite", filepath.Join(folder, DatabaseName))
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err = writer.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; INSERT INTO configuration(key,value) VALUES('unlisted_wal','must not replay')`); err != nil {
				t.Fatal(err)
			}
			if size, err := walBytes(filepath.Join(folder, DatabaseName) + "-wal"); err != nil || size == 0 {
				t.Fatalf("missing WAL fixture: %d %v", size, err)
			}
			if err = checkSourceHashes(folder, manifest); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("strict hash check accepted unlisted WAL: %v", err)
			}
			for _, source := range []Source{{BackupID: id}, {Path: folder}} {
				out, err := svc.Stage(context.Background(), source)
				if version != StreamVersion {
					if !errors.Is(err, ErrIntegrity) {
						t.Fatalf("legacy unverified WAL accepted: %v", err)
					}
					continue
				}
				if err != nil || out.Validation.Recovery == nil || out.Validation.Recovery.ManifestDamage == nil || !out.Validation.Recovery.ManifestDamage.UnverifiedDatabase {
					t.Fatalf("unlisted WAL recovery was not disclosed: %+v %v", out, err)
				}
				if got := configurationValue(t, storagepaths.Path(state, RestoreStagedDir, DatabaseName), "unlisted_wal"); got != "must not replay" {
					t.Fatalf("readable WAL row lost: %q", got)
				}
			}
		})
	}
}

func configurationValue(t *testing.T, path, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM configuration WHERE key=?", key).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
