package backup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceInspectionUsesCapturedBytesWithoutStartingRestore(t *testing.T) {
	s, p, state := policyFixture(t)
	ctx := context.Background()
	plain, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	password := "inspection private fixture phrase"
	if _, err = p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	protected, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	plainFolder := filepath.Join(state, BackupsDir, plain)
	protectedFolder := filepath.Join(state, BackupsDir, protected)
	// Opposite suffixes demonstrate that neither name nor extension grants a
	// protected/unprotected classification.
	plainFile := filepath.Join(t.TempDir(), "plain.pba")
	if _, err = copyOneFile(filepath.Join(plainFolder, DatabaseName), plainFile, "fixture"); err != nil {
		t.Fatal(err)
	}
	protectedFile := filepath.Join(t.TempDir(), "protected.db")
	if _, err = copyOneFile(filepath.Join(protectedFolder, encryptedPayload), protectedFile, "fixture"); err != nil {
		t.Fatal(err)
	}
	beforeDB := fileHashOrEmpty(t, filepath.Join(state, DatabaseName))
	beforeKey := fileHashOrEmpty(t, filepath.Join(state, "Keys", "backup-passphrase.key"))
	for _, test := range []struct {
		source    Source
		encrypted bool
	}{
		{Source{BackupID: plain}, false},
		{Source{BackupID: protected}, true},
		{Source{Path: plainFolder}, false},
		{Source{Path: protectedFolder}, true},
		{Source{Path: plainFile}, false},
		{Source{Path: protectedFile}, true},
	} {
		got, err := s.InspectSource(ctx, test.source)
		if err != nil || got.Encrypted != test.encrypted {
			t.Fatal("wrong content-based source inspection", got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil || string(raw) != `{"encrypted":`+map[bool]string{true: "true", false: "false"}[test.encrypted]+`}` {
			t.Fatal("inspection returned more than its protection flag", err)
		}
	}
	if s.RestoreProgress() != nil || s.LastBackup() != nil {
		t.Fatal("read-only inspection created a job/result")
	}
	if _, running := s.Running(); running {
		t.Fatal("read-only inspection started work")
	}
	if beforeDB != fileHashOrEmpty(t, filepath.Join(state, DatabaseName)) || beforeKey != fileHashOrEmpty(t, filepath.Join(state, "Keys", "backup-passphrase.key")) {
		t.Fatal("inspection changed live database or Keys")
	}
	if _, err = os.Stat(filepath.Join(state, RestoreStagedDir)); !os.IsNotExist(err) {
		t.Fatal("inspection touched restore staging", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

func TestSourceInspectionRefusesInvalidPathsHeadersAndLinks(t *testing.T) {
	s, _, state := policyFixture(t)
	file := filepath.Join(t.TempDir(), "not-a-backup.db")
	if err := os.WriteFile(file, []byte("not a database or encrypted archive"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []Source{{}, {Path: "../relative.db"}, {BackupID: ".."}, {Path: file, BackupID: "2026-10-23T010000Z"}, {Path: file}, {Path: filepath.Join(t.TempDir(), "missing")}} {
		if _, err := s.InspectSource(context.Background(), source); !errors.Is(err, ErrSourceInvalid) {
			t.Fatal("invalid source inspection accepted", err)
		}
	}
	for _, target := range []string{file, filepath.Dir(file)} {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skip(err)
		}
		if _, err := s.InspectSource(context.Background(), Source{Path: link}); !errors.Is(err, ErrSourceInvalid) {
			t.Fatal("inspection followed a link", err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.InspectSource(canceled, Source{Path: file}); !errors.Is(err, context.Canceled) {
		t.Fatal("inspection ignored cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(state, RestoreStagedDir)); !os.IsNotExist(err) {
		t.Fatal("invalid inspection touched staging", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

func TestSourceInspectionPhysicallyBoundsFolderMetadata(t *testing.T) {
	s, _, state := policyFixture(t)
	for _, test := range []struct {
		name  string
		limit int64
	}{
		{ManifestName, maxManifestBytes},
		{encryptedSummary, maxEncryptedSummaryBytes},
	} {
		folder := t.TempDir()
		path := filepath.Join(folder, test.name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		// A sparse oversized fixture exercises the size fence without copying
		// a large source tree or consuming the limit in actual disk blocks.
		if err = file.Truncate(test.limit + 1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = s.InspectSource(context.Background(), Source{Path: folder}); !errors.Is(err, ErrSourceInvalid) {
			t.Fatal("oversized metadata accepted", err)
		}
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}
