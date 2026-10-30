package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestEncryptedSummaryRejectsBytesBeyondItsBound(t *testing.T) {
	folder := t.TempDir()
	m := Manifest{Format: Format, Version: Version, Kind: KindManual, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, bytes.Repeat([]byte(" "), (64<<10)-len(raw))...)
	raw = append(raw, []byte("hidden trailing data")...)
	if err = os.WriteFile(filepath.Join(folder, encryptedSummary), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, encryptedPayload), []byte("ciphertext fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = encryptedManifest(folder); !errors.Is(err, ErrSourceInvalid) {
		t.Fatalf("oversized summary accepted: %v", err)
	}
}

func TestMissingAndCorruptPassphraseKeyCannotPublishPlaintextFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, damage := range []string{"missing", "truncated", "replaced", "symlink", "public-mode"} {
		t.Run(damage, func(t *testing.T) {
			s, p, state := policyFixture(t)
			password := "private recovery fixture phrase"
			if _, err := p.Update(context.Background(), SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "Keys", "backup-passphrase.key")
			originalKey, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(originalKey)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "truncated":
				if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.WriteFile(path, bytes.Repeat([]byte{0x51}, 32), 0600); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if runtime.GOOS == "windows" {
					t.Skip("Unix mode bits do not establish Windows ACLs")
				}
				if err := os.WriteFile(path, originalKey, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "outside.key")
				if err := os.WriteFile(target, bytes.Repeat([]byte{0x51}, 32), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			}
			if _, err := s.Create(context.Background(), KindManual); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid key accepted: %v", err)
			}
			listed, err := s.List()
			if err != nil || len(listed) != 0 {
				t.Fatal("invalid key published a backup", listed, err)
			}
			settings, err := p.Settings(context.Background())
			if err != nil || !settings.PassphraseProtected || !settings.PassphraseSet {
				t.Fatal("invalid key weakened saved protection", settings, err)
			}
			assertNoScratch(t, state, filepath.Join(state, BackupsDir))
		})
	}
}

func TestEncryptedRestorePrivateTempIOFailureLeavesLiveStateUntouched(t *testing.T) {
	s, p, state := policyFixture(t)
	password := "private temporary restore fixture phrase"
	if _, err := p.Update(context.Background(), SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	beforeDB := fileHashOrEmpty(t, filepath.Join(state, DatabaseName))
	beforeKey := fileHashOrEmpty(t, filepath.Join(state, "Keys", "backup-passphrase.key"))
	temp := filepath.Join(state, "Temp")
	if err = os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	// An actual filesystem failure, independent of the process's privilege or
	// platform permission-bit semantics: this path cannot contain a directory.
	if err = os.WriteFile(temp, []byte("unavailable private temporary directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StageWithPassphrase(context.Background(), Source{BackupID: id}, password); err == nil {
		t.Fatal("restore accepted an unusable private temporary directory")
	}
	if beforeDB != fileHashOrEmpty(t, filepath.Join(state, DatabaseName)) || beforeKey != fileHashOrEmpty(t, filepath.Join(state, "Keys", "backup-passphrase.key")) {
		t.Fatal("failed restore changed live database or Keys")
	}
	if _, err = os.Stat(filepath.Join(state, RestoreStagedDir, RestoreMarkerFile)); !os.IsNotExist(err) {
		t.Fatal("failed restore installed a marker", err)
	}
	if err = os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StageWithPassphrase(context.Background(), Source{BackupID: id}, password); err != nil {
		t.Fatal("same archive could not stage after repairing private Temp", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

func TestEncryptedBackupCloseFailureNeverPublishes(t *testing.T) {
	s, p, state := policyFixture(t)
	password := "delayed close fixture passphrase"
	if _, err := p.Update(context.Background(), SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	closeFailure := errors.New("injected delayed archive close failure")
	original := finishArchiveFile
	finishArchiveFile = func(file archiveSyncCloser) error { return errors.Join(original(file), closeFailure) }
	t.Cleanup(func() { finishArchiveFile = original })
	if _, err := s.Create(context.Background(), KindManual); !errors.Is(err, closeFailure) {
		t.Fatalf("delayed close failure lost: %v", err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 0 {
		t.Fatal("failed archive published", listed, err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

type barrierFailure struct {
	syncErr, closeErr error
	closed            bool
}

func (f *barrierFailure) Sync() error  { return f.syncErr }
func (f *barrierFailure) Close() error { f.closed = true; return f.closeErr }
func TestEncryptedArchiveFinishingPreservesBothIOErrors(t *testing.T) {
	syncErr, closeErr := errors.New("fixture sync failure"), errors.New("fixture close failure")
	file := &barrierFailure{syncErr: syncErr, closeErr: closeErr}
	err := finishArchiveFile(file)
	if !errors.Is(err, syncErr) || !errors.Is(err, closeErr) || !file.closed {
		t.Fatal("archive barriers lost failure or close", err)
	}
}

func TestEncryptedRetentionAcrossDestinationsKeepsManualCopies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "retention fixture private phrase"
	firstDir, secondDir := t.TempDir(), t.TempDir()
	if _, err := p.Update(ctx, SettingsPatch{BackupFolder: &firstDir, PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	s.now = func() time.Time { now = now.Add(time.Minute); return now }
	manual, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	oldScheduled, err := s.Create(ctx, KindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Update(ctx, SettingsPatch{BackupFolder: &secondDir}); err != nil {
		t.Fatal(err)
	}
	newest, err := s.Create(ctx, KindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	// Failed/unpublished work has no manifest and is never a retention target.
	partial := filepath.Join(firstDir, ".partial-2026-10-23T010000Z")
	if err = os.Mkdir(partial, 0700); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(1); err != nil {
		t.Fatal(err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 2 {
		t.Fatal("wrong retention result", listed, err)
	}
	for _, entry := range listed {
		if !entry.Encrypted || entry.ID != manual && entry.ID != newest {
			t.Fatal("wrong retained archive", entry)
		}
	}
	if _, err = os.Stat(filepath.Join(firstDir, manual, encryptedPayload)); err != nil {
		t.Fatal("manual archive pruned", err)
	}
	if _, err = os.Stat(filepath.Join(firstDir, oldScheduled)); !os.IsNotExist(err) {
		t.Fatal("old scheduled archive remained", err)
	}
	if _, err = os.Stat(partial); err != nil {
		t.Fatal("retention touched partial workspace", err)
	}
	if err = os.RemoveAll(partial); err != nil {
		t.Fatal(err)
	}
	assertNoScratch(t, state, firstDir)
	assertNoScratch(t, state, secondDir)
}
