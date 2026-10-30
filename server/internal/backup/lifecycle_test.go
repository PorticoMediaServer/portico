package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func ptr[T any](v T) *T { return &v }
func policyFixture(t *testing.T) (*Service, *PolicyStore, string) {
	t.Helper()
	s, state, db := testService(t)
	p := NewPolicyStore(db, state)
	s.SetPolicyStore(p)
	return s, p, state
}
func assertNoScratch(t *testing.T, state, dest string) {
	t.Helper()
	for _, root := range []string{filepath.Join(state, "Temp"), dest} {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "backup-") || strings.HasPrefix(e.Name(), ".partial-") {
				t.Errorf("unfinished backup scratch %s", filepath.Join(root, e.Name()))
			}
		}
	}
}

func TestEncryptedBackupRelocatesAndRestoresWithKeysAndHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, s.db)
	lib := c.Library("films", "Films", "movie", "/fixture")
	historyTitle := "Private encrypted restore history fixture"
	item := c.Movie(lib, "/fixture/history.mkv", historyTitle, 2020)
	c.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms,position_ms,duration_ms,completed)
 SELECT 'portable-history','local','history-account','history-profile','Fixture viewer',id,'films',kind,title,1700000000000,1700000321000,321000,5400000,0 FROM catalog_entities WHERE id=?`, item.ID)
	dest := t.TempDir()
	password := "Fixture backup phrase 2026!"
	if e := writeTestFile(filepath.Join(state, "Keys", "fixture.key"), []byte("fixture secret bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := writeTestFile(filepath.Join(state, "portico.toml"), []byte("# portable fixture config\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s.SetCopyScrubber(identity.ScrubBackupCopy)
	if _, e := p.Update(ctx, SettingsPatch{BackupFolder: &dest, PassphraseProtected: ptr(true), Passphrase: &password}); e != nil {
		t.Fatal(e)
	}
	id, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	folder := filepath.Join(dest, id)
	entries, e := os.ReadDir(folder)
	if e != nil || len(entries) != 2 {
		t.Fatal("encrypted payload shape", entries, e)
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join(folder, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(b, []byte(password)) || bytes.Contains(b, []byte("fixture secret bytes")) || bytes.Contains(b, []byte("SQLite format 3")) || bytes.Contains(b, []byte(historyTitle)) {
			t.Fatal("plaintext escaped encrypted archive")
		}
	}
	listed, e := s.List()
	if e != nil || len(listed) != 1 || !listed[0].Encrypted || listed[0].Bytes < 1 {
		t.Fatal("encrypted listing", listed, e)
	}
	// A new data root has no original sealing key or stored passphrase.
	target, targetState, targetDB := testService(t)
	if _, e = target.StageWithPassphrase(ctx, Source{Path: folder}, password); e != nil {
		t.Fatal("portable restore staging", e)
	}
	marker, e := os.ReadFile(filepath.Join(targetState, RestoreStagedDir, RestoreMarkerFile))
	if e != nil || bytes.Contains(marker, []byte(password)) {
		t.Fatal("password in marker", e)
	}
	if e = targetDB.Close(); e != nil {
		t.Fatal(e)
	}
	if e = ApplyStaged(ctx, targetState, nil); e != nil {
		t.Fatal("apply verified archive", e)
	}
	got, e := os.ReadFile(filepath.Join(targetState, "Keys", "fixture.key"))
	if e != nil || string(got) != "fixture secret bytes" {
		t.Fatal("Keys lost", e)
	}
	restored, e := persistence.Open(filepath.Join(targetState, DatabaseName))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	var title string
	var position, duration, completed, started, updated int64
	if e = restored.QueryRow(`SELECT title,position_ms,duration_ms,completed,started_ms,updated_ms FROM play_history WHERE playback_id='portable-history' AND item_id=?`, item.ID).Scan(&title, &position, &duration, &completed, &started, &updated); e != nil || title != historyTitle || position != 321000 || duration != 5400000 || completed != 0 || started != 1700000000000 || updated != 1700000321000 {
		t.Fatal("portable restore lost or changed actual play history", e)
	}
	after := NewPolicyStore(restored, targetState)
	snapshot, e := after.Snapshot(ctx)
	assertDerivedSnapshot(t, snapshot, e, password)
	assertNoScratch(t, state, dest)
	assertNoScratch(t, targetState, filepath.Join(targetState, BackupsDir))
}

func TestEncryptedRestoreRejectsWrongPassphraseTamperTruncationAndTrailingBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "Fixture phrase twelve plus"
	if _, e := p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); e != nil {
		t.Fatal(e)
	}
	id, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	folder := filepath.Join(state, BackupsDir, id)
	path := filepath.Join(folder, encryptedPayload)
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		name, phrase string
		mutate       func([]byte) []byte
	}{
		{"wrong-passphrase", "incorrect fixture passphrase", func(b []byte) []byte { return b }},
		{"missing-passphrase", "", func(b []byte) []byte { return b }},
		{"tamper", password, func(b []byte) []byte { b[len(b)/2] ^= 1; return b }},
		{"truncated-tag", password, func(b []byte) []byte { return b[:len(b)-1] }},
		{"missing-final", password, func(b []byte) []byte { return b[:len(b)-20] }},
		{"trailing", password, func(b []byte) []byte { return append(b, 42) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := test.mutate(bytes.Clone(original))
			if e = os.WriteFile(path, b, 0600); e != nil {
				t.Fatal(e)
			}
			before := fileHashOrEmpty(t, filepath.Join(state, DatabaseName))
			if _, e = s.StageWithPassphrase(ctx, Source{BackupID: id}, test.phrase); !errors.Is(e, ErrIntegrity) {
				t.Fatalf("negative accepted: %v", e)
			}
			if after := fileHashOrEmpty(t, filepath.Join(state, DatabaseName)); before != after {
				t.Fatal("failed restore changed live database")
			}
			if _, e = os.Stat(filepath.Join(state, RestoreStagedDir, RestoreMarkerFile)); !os.IsNotExist(e) {
				t.Fatal("failure created restore marker", e)
			}
			assertNoScratch(t, state, filepath.Join(state, BackupsDir))
		})
	}
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StageWithPassphrase(ctx, Source{BackupID: id}, password); e != nil {
		t.Fatal("valid archive after failures", e)
	}
}

func TestBackupPolicyDestinationSecretsAndSourceRotation(t *testing.T) {
	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "Fixture very private phrase"
	for _, patch := range []SettingsPatch{{PassphraseProtected: ptr(true)}, {Passphrase: ptr("short"), PassphraseProtected: ptr(true)}, {BackupFolder: ptr("../escape")}, {BackupFolder: ptr(state)}, {BackupFolder: ptr(filepath.Dir(state))}, {BackupFolder: ptr(filepath.Join(state, "Keys"))}} {
		if _, e := p.Update(ctx, patch); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	dest := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(dest, link); e == nil {
		if _, e = p.Update(ctx, SettingsPatch{BackupFolder: &link}); e == nil {
			t.Fatal("symlink destination accepted")
		}
	}
	settings, e := p.Update(ctx, SettingsPatch{BackupFolder: &dest, PassphraseProtected: ptr(true), Passphrase: &password})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(settings)
	if bytes.Contains(raw, []byte(password)) || !settings.PassphraseSet {
		t.Fatal("read leaks secret")
	}
	var sealed []byte
	if e = s.db.QueryRow(`SELECT passphrase_sealed FROM backup_settings`).Scan(&sealed); e != nil || bytes.Contains(sealed, []byte(password)) {
		t.Fatal("secret stored plaintext", e)
	}
	reopened := NewPolicyStore(s.db, state)
	snap, e := reopened.Snapshot(ctx)
	assertDerivedSnapshot(t, snap, e, password)
	// A source key rotation between database copy and file copy invalidates this run.
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error {
		return writeTestFile(filepath.Join(state, "Keys", "rotated.key"), []byte("new key"), 0600)
	})
	if _, e = s.Create(ctx, KindManual); !errors.Is(e, ErrIntegrity) {
		t.Fatal("inconsistent authority published", e)
	}
	listed, e := s.List()
	if e != nil || len(listed) != 0 {
		t.Fatal("failed copy appeared", e)
	}
	assertNoScratch(t, state, dest)
	s.SetCopyScrubber(nil)
	if _, e = p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(false)}); e != nil {
		t.Fatal(e)
	}
	if got, e := p.Settings(ctx); e != nil || got.PassphraseSet || got.PassphraseProtected {
		t.Fatal("disable did not erase derived key material", e)
	}
}

func TestBackupDefaultFilterPreservesRealOwnerUploadAndUndo(t *testing.T) {
	s, _, state := policyFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, s.db)
	lib := c.Library("films", "Films", "movie", "/fixture")
	item := c.Movie(lib, "/fixture/movie.mkv", "Fixture", 2020)
	m := metadata.New(s.db, "")
	if e := m.SetArtworkDirectory(filepath.Join(state, "Metadata", "artwork")); e != nil {
		t.Fatal(e)
	}
	target := metadata.RepairTarget{Kind: "item", ID: item.Public}
	auth := func(*sql.Tx) error { return nil }
	actor := metadata.MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	for i := 0; i < 2; i++ {
		before, e := m.RepairState(ctx, target, auth)
		if e != nil {
			t.Fatal(e)
		}
		img := image.NewRGBA(image.Rect(0, 0, 24, 36))
		img.Set(0, 0, color.RGBA{R: uint8(i + 70), A: 255})
		var b bytes.Buffer
		if e = png.Encode(&b, img); e != nil {
			t.Fatal(e)
		}
		if _, e = m.UploadArtwork(ctx, target, "poster", "", before.Revision, b.Bytes(), actor, auth); e != nil {
			t.Fatal("real upload", e)
		}
	}
	for _, name := range []string{"Metadata/artwork/downloaded.img", "Metadata/guide-images/provider.png", "Metadata/analysis-artifacts/preview", "Metadata/local-artwork/sidecar.img", "Metadata/subtitles/owner.srt", "Metadata/channel-logos/owner.png", "Cache/rebuild", "Logs/log", "Temp/work", "Transcode/frame"} {
		if e := writeTestFile(filepath.Join(state, filepath.FromSlash(name)), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	id, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	folder := filepath.Join(state, BackupsDir, id)
	// Every file of every uploaded source is carried: both slots and the
	// ambient effect.
	rows, e := s.db.Query(`SELECT s.small_blob FROM artwork_uploads u JOIN artwork_sources s ON s.digest=u.digest UNION SELECT s.large_blob FROM artwork_uploads u JOIN artwork_sources s ON s.digest=u.digest UNION SELECT s.ambient_blob FROM artwork_uploads u JOIN artwork_sources s ON s.digest=u.digest`)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for rows.Next() {
		var d string
		if e = rows.Scan(&d); e != nil {
			t.Fatal(e)
		}
		if got := fileHashOrEmpty(t, filepath.Join(folder, "Metadata", "artwork", d+".img")); got != d {
			t.Fatal("owner upload lost", d)
		}
		count++
	}
	rows.Close()
	if count < 2 {
		t.Fatal("fixture did not create retained uploads")
	}
	for _, name := range []string{"Metadata/local-artwork/sidecar.img", "Metadata/subtitles/owner.srt", "Metadata/channel-logos/owner.png"} {
		if _, e = os.Stat(filepath.Join(folder, name)); e != nil {
			t.Fatal("owner file omitted", name, e)
		}
	}
	for _, name := range []string{"Metadata/artwork/downloaded.img", "Metadata/guide-images", "Metadata/analysis-artifacts", "Cache", "Logs", "Temp", "Transcode"} {
		if _, e = os.Stat(filepath.Join(folder, name)); !os.IsNotExist(e) {
			t.Fatal("rebuildable copied by default", name, e)
		}
	}
	// History in the actual copied database remains byte-for-byte meaningful.
	copyDB := openReadOnly(t, filepath.Join(folder, DatabaseName))
	defer copyDB.Close()
	var history int
	if e = copyDB.QueryRow(`SELECT count(*) FROM metadata_owner_history`).Scan(&history); e != nil || history < 2 {
		t.Fatal("owner history missing", history, e)
	}
}

func TestScheduledAndManualBackupsUseLivePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	first, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	dest := t.TempDir()
	password := "Scheduled private phrase"
	if _, e = p.Update(ctx, SettingsPatch{BackupFolder: &dest, IncludeArtwork: ptr(true), PassphraseProtected: ptr(true), Passphrase: &password}); e != nil {
		t.Fatal(e)
	}
	if e = writeTestFile(filepath.Join(state, "Metadata", "analysis-artifacts", "preview"), []byte("fixture preview"), 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	policy := workpolicy.Policy{BackgroundPriority: "lower", Windows: []workpolicy.Window{{ID: "fixture", Name: "Fixture", Enabled: true, Cadence: "custom", Days: []string{"friday"}, StartMinute: 660, DurationMinutes: 180, Timezone: "UTC", Tasks: []string{"backup"}}}}
	if e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return workpolicy.SaveTx(ctx, tx, policy) }); e != nil {
		t.Fatal(e)
	}
	schedule := s.Schedule(s.db, func(context.Context) (int, error) { return 7, nil })
	schedule.now = func() time.Time { return now }
	schedule.check(ctx)
	listed, e := s.List()
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, b := range listed {
		if b.Kind == KindScheduled {
			found = true
			if !b.Encrypted {
				t.Fatal("scheduled ignored encryption")
			}
			if _, e = os.Stat(filepath.Join(dest, b.ID, encryptedPayload)); e != nil {
				t.Fatal("scheduled ignored destination", e)
			}
			if _, e = s.StageWithPassphrase(ctx, Source{BackupID: b.ID}, password); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(state, RestoreStagedDir, "Metadata", "analysis-artifacts", "preview")); e != nil {
				t.Fatal("include artwork ignored", e)
			}
		}
	}
	if !found {
		t.Fatal("scheduler did not fire")
	}
	if _, e = os.Stat(filepath.Join(state, BackupsDir, first, ManifestName)); e != nil {
		t.Fatal("destination change lost original", e)
	}
}

type failWriter struct{ left int }

func (w *failWriter) Write(b []byte) (int, error) {
	if w.left <= 0 {
		return 0, io.ErrClosedPipe
	}
	n := min(w.left, len(b))
	w.left -= n
	if n < len(b) {
		return n, io.ErrShortWrite
	}
	return n, nil
}
func TestEncryptedStreamShortIOAndChunkReordering(t *testing.T) {
	password := "stream fixture phrase"
	if _, e := encryptedFixtureWriter(&failWriter{}, password); e == nil {
		t.Fatal("header short write accepted")
	}
	var good bytes.Buffer
	w, e := encryptedFixtureWriter(&good, password)
	if e != nil {
		t.Fatal(e)
	}
	block := bytes.Repeat([]byte{7}, chunkBytes)
	if _, e = w.Write(block); e != nil {
		t.Fatal(e)
	}
	if _, e = w.Write([]byte("second")); e != nil {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	b := good.Bytes()
	header := len(envelopeMagic) + kdfSaltBytes + archiveSaltBytes
	end := header + 4 + chunkBytes + 16
	changed := append(bytes.Clone(b[:header]), b[end:len(b)-20]...)
	changed = append(changed, b[header:end]...)
	changed = append(changed, b[len(b)-20:]...)
	r, e := newEncryptedReader(bytes.NewReader(changed), password)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(io.Discard, r); !errors.Is(e, ErrIntegrity) {
		t.Fatal("reordering accepted", e)
	}
	r, e = newEncryptedReader(bytes.NewReader(b), password)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(r)
	if e != nil || !bytes.Equal(raw, append(block, []byte("second")...)) {
		t.Fatal("multi-chunk roundtrip", e)
	}
	partial, e := encryptedFixtureWriter(&failWriter{left: 100}, password)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = partial.Write(block); e == nil {
		t.Fatal("body short write accepted")
	}
}

func TestEncryptedFileRestoreAndDestinationHistory(t *testing.T) {
	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "Portable archive fixture phrase"
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	if _, e := p.Update(ctx, SettingsPatch{BackupFolder: &firstDir, PassphraseProtected: ptr(true), Passphrase: &password}); e != nil {
		t.Fatal(e)
	}
	first, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Update(ctx, SettingsPatch{BackupFolder: &secondDir}); e != nil {
		t.Fatal(e)
	}
	second, e := s.Create(ctx, KindManual)
	if e != nil {
		t.Fatal(e)
	}
	listed, e := s.List()
	if e != nil || len(listed) != 2 || first == second {
		t.Fatal("destination history missing/collision", listed, e)
	}
	portable := filepath.Join(t.TempDir(), "owner-copy.pba")
	entry, e := copyOneFile(filepath.Join(firstDir, first, encryptedPayload), portable, "copy")
	if e != nil || entry.Bytes < 1 {
		t.Fatal(e)
	}
	if _, e = s.StageWithPassphrase(ctx, Source{Path: portable}, password); e != nil {
		t.Fatal("single encrypted file restore", e)
	}
	if e = s.Delete(first); e != nil {
		t.Fatal("old destination delete", e)
	}
	if _, e = os.Stat(filepath.Join(secondDir, second)); e != nil {
		t.Fatal("delete affected current copy", e)
	}
	assertNoScratch(t, state, firstDir)
	assertNoScratch(t, state, secondDir)
}

func TestBackupPartialPublicationFailureAndCancellationCleanUp(t *testing.T) {
	s, p, state := policyFixture(t)
	ctx := context.Background()
	destination := t.TempDir()
	if _, e := p.Update(ctx, SettingsPatch{BackupFolder: &destination}); e != nil {
		t.Fatal(e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.Create(canceled, KindManual); !errors.Is(e, context.Canceled) {
		t.Fatal("cancel accepted", e)
	}
	// A real I/O error inside copied state: a symlink is never copied or followed.
	if e := os.MkdirAll(filepath.Join(state, "Metadata", "subtitles"), 0700); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if e := os.WriteFile(outside, []byte("outside remains unchanged"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(state, "Metadata", "subtitles", "escape.srt")); e != nil {
		t.Skip("symlinks unavailable")
	}
	if _, e := s.Create(ctx, KindManual); !errors.Is(e, ErrSourceInvalid) {
		t.Fatal("source symlink accepted", e)
	}
	got, e := os.ReadFile(outside)
	if e != nil || string(got) != "outside remains unchanged" {
		t.Fatal("outside file affected", e)
	}
	assertNoScratch(t, state, destination)
}

func TestAuthenticatedArchiveCannotEscapeOrInstallUnownedFiles(t *testing.T) {
	s, _, state := policyFixture(t)
	password := "Fixture malicious archive passphrase"
	for _, name := range []string{"../escape", "/absolute", "Cache/escape", "Keys/../../escape", "Keys", "Metadata", "portico.toml/child"} {
		var b bytes.Buffer
		enc, e := encryptedFixtureWriter(&b, password)
		if e != nil {
			t.Fatal(e)
		}
		tw := tar.NewWriter(enc)
		if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1, Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e = tw.Write([]byte("x")); e != nil {
			t.Fatal(e)
		}
		if e = tw.Close(); e != nil {
			t.Fatal(e)
		}
		if e = enc.Close(); e != nil {
			t.Fatal(e)
		}
		file := filepath.Join(t.TempDir(), "source.pba")
		if e = os.WriteFile(file, b.Bytes(), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = s.StageWithPassphrase(context.Background(), Source{Path: file}, password); !errors.Is(e, ErrSourceInvalid) {
			t.Fatal("archive escape accepted", name, e)
		}
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

func TestBackupStartupCleanupIsConfinedToOwnedScratch(t *testing.T) {
	_, _, state := policyFixture(t)
	for _, name := range []string{"Temp/backup-snapshot-stale/work", "Temp/backup-decrypt-stale/work", "Keys/.backup-passphrase.key.stale", "Temp/other/work", "Keys/invitation-link.key"} {
		if e := writeTestFile(filepath.Join(state, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := CleanupScratch(state); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"Temp/backup-snapshot-stale", "Temp/backup-decrypt-stale", "Keys/.backup-passphrase.key.stale"} {
		if _, e := os.Lstat(filepath.Join(state, name)); !os.IsNotExist(e) {
			t.Fatal("stale scratch retained", name, e)
		}
	}
	for _, name := range []string{"Temp/other/work", "Keys/invitation-link.key"} {
		if _, e := os.Stat(filepath.Join(state, name)); e != nil {
			t.Fatal("unowned file removed", name, e)
		}
	}
}

func TestBackupManifestReadHasAPhysicalSizeBound(t *testing.T) {
	dir := t.TempDir()
	f, e := os.Create(filepath.Join(dir, ManifestName))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(maxManifestBytes + 1); e != nil {
		f.Close()
		t.Fatal(e)
	}
	f.Close()
	if _, e = readManifest(dir); !errors.Is(e, ErrSourceInvalid) {
		t.Fatal("oversized manifest accepted", e)
	}
}

func TestBackupDiskEstimateFollowsArtworkPolicy(t *testing.T) {
	s, _, state := policyFixture(t)
	dir := filepath.Join(state, "Metadata", "artwork")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(filepath.Join(dir, "downloaded.img"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(64 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	minimal, e := s.estimate(context.Background(), false)
	if e != nil {
		t.Fatal(e)
	}
	full, e := s.estimate(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	if full-minimal != 64<<20 {
		t.Fatalf("excluded artwork inflated disk requirement: minimal=%d full=%d", minimal, full)
	}
}

func encryptedFixtureWriter(out io.Writer, password string) (*encryptedWriter, error) {
	material, err := newArchiveKeyMaterial(password)
	if err != nil {
		return nil, err
	}
	defer material.clear()
	return newEncryptedWriter(out, material)
}

func assertDerivedSnapshot(t *testing.T, snapshot Snapshot, err error, password string) {
	t.Helper()
	defer snapshot.clear()
	if err != nil || snapshot.material == nil {
		t.Fatal("derived policy capability unavailable", err)
	}
	expected := deriveMaster(password, snapshot.material.salt[:])
	defer clear(expected)
	if !bytes.Equal(snapshot.material.key, expected) {
		t.Fatal("wrong derived policy capability")
	}
}
