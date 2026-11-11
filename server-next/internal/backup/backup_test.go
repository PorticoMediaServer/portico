package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func newService(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 10, 3, 0, 0, 0, time.Local)
	s := &Service{DB: d, Settings: st, DataDir: dir, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { clock = clock.Add(time.Minute); return clock }}
	os.WriteFile(filepath.Join(dir, "remote-sources.key"), make([]byte, 32), 0o600)
	return s
}

// Scheduled backups past the number kept are removed, newest kept; one made with Back up now
// stays. Each holds the database and the key file, and says so.
func TestBackupsKeptAndWhatTheyHold(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"keep":2}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.make(ctx, "portico-20261101-030000", false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"portico-20261102-030000", "portico-20261103-030000", "portico-20261104-030000"} {
		if err := s.make(ctx, id, true); err != nil {
			t.Fatal(err)
		}
		s.removeOld()
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range list {
		ids = append(ids, b.ID)
	}
	if len(list) != 3 || ids[0] != "portico-20261104-030000" || ids[1] != "portico-20261103-030000" || ids[2] != "portico-20261101-030000" {
		t.Fatalf("kept %v", ids)
	}
	if got := list[0].Credentials; len(got) != 2 || got[0] != CredentialServerKeys || got[1] != CredentialKeyFile {
		t.Errorf("the backup says it holds %v", got)
	}
	path, _ := s.Path(list[0].ID)
	contents, err := Inspect(ctx, path, t.TempDir())
	if err != nil || !contents.Database || len(contents.KeyFiles) != 1 {
		t.Fatalf("a good backup's check: %+v %v", contents, err)
	}
}

// A backup is refused whole when its database holds something the schema does not define, or
// when the archive holds a file a backup never holds.
func TestABackupThatFailsItsCheckIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if err := s.make(ctx, "portico-20261101-030000", false); err != nil {
		t.Fatal(err)
	}
	good, _ := s.Path("portico-20261101-030000")
	dir := t.TempDir()
	extracted := filepath.Join(dir, "main.db")
	extract(t, good, "main.db", extracted)

	tampered, err := sql.Open("sqlite", extracted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tampered.Exec(`CREATE TRIGGER sneaky AFTER INSERT ON users BEGIN DELETE FROM sessions; END`); err != nil {
		t.Fatal(err)
	}
	tampered.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	tampered.Close()
	for name, entries := range map[string]map[string]string{
		"a trigger":          {"main.db": extracted},
		"a path outside":     {"main.db": filepath.Join(s.DataDir, "main.db"), "../../etc/cron.d/x": extracted},
		"an unknown file":    {"main.db": filepath.Join(s.DataDir, "main.db"), "config.sh": extracted},
		"no database at all": {"remote-sources.key": extracted},
	} {
		archive := filepath.Join(dir, "bad.tar.gz")
		pack(t, archive, entries)
		var damaged *ErrDamaged
		if _, err := Inspect(ctx, archive, dir); !errors.As(err, &damaged) {
			t.Errorf("a backup with %s was not refused: %v", name, err)
		}
	}
}

func extract(t *testing.T, archive, name, to string) {
	t.Helper()
	f, _ := os.Open(archive)
	defer f.Close()
	zr, _ := gzip.NewReader(f)
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not in the archive: %v", name, err)
		}
		if h.Name == name {
			out, _ := os.Create(to)
			io.Copy(out, tr)
			out.Close()
			return
		}
	}
}

func pack(t *testing.T, archive string, entries map[string]string) {
	t.Helper()
	f, _ := os.Create(archive)
	zw := gzip.NewWriter(f)
	tw := tar.NewWriter(zw)
	for name, from := range entries {
		raw, _ := os.ReadFile(from)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(raw)), Typeflag: tar.TypeReg})
		tw.Write(raw)
	}
	tw.Close()
	zw.Close()
	f.Close()
}

// The schedule: 3 am on the owner's days, and Never with no days.
func TestSchedule(t *testing.T) {
	monday := time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)
	every := Settings{Days: []int{0, 1, 2, 3, 4, 5, 6}, Hour: 3}
	if got := nextDue(every, monday); !got.Equal(time.Date(2026, 11, 10, 3, 0, 0, 0, time.Local)) {
		t.Errorf("next after Monday noon: %v", got)
	}
	weekends := Settings{Days: []int{0, 6}, Hour: 3}
	if got := nextDue(weekends, monday); got.Weekday() != time.Saturday {
		t.Errorf("weekends only: %v", got)
	}
	if got := lastDue(weekends, monday); got.Weekday() != time.Sunday {
		t.Errorf("last weekend run before Monday: %v", got)
	}
	if !nextDue(Settings{Hour: 3}, monday).IsZero() {
		t.Errorf("Never has a next run")
	}
}

// A backup drive that stops answering never holds the server's writes: the database is
// copied and checked on the data folder's disk, nothing reaches the backup folder until then,
// and the backup gives up after its limit.
func TestASlowBackupFolderNeverHoldsWrites(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	folder := t.TempDir()
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(folder)+`}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved := folderWriteLimit
	folderWriteLimit = 500 * time.Millisecond
	release := make(chan struct{})
	folderHook = func(string) { <-release }
	defer func() { folderWriteLimit, folderHook = saved, nil; close(release) }()

	done := make(chan error, 1)
	go func() { done <- s.make(ctx, "portico-20261101-030000", false) }()
	// While the backup is waiting on its folder, a person's write goes through at once.
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	if _, err := s.Settings.Write(ctx, 0, "general", json.RawMessage(`{"name":"Still here"}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited > 300*time.Millisecond {
		t.Errorf("a write waited %s for the backup", waited)
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 0 {
		t.Errorf("the backup folder was written before the copy was done: %v", entries)
	}
	if err := <-done; !errors.Is(err, ErrFolderSlow) {
		t.Errorf("a backup whose folder did not answer: %v", err)
	}
}

// A chosen backup folder that is not there at start (a drive not mounted yet) keeps the
// owner's settings and is not made on the disk beneath; the page says it cannot be written,
// and a backup fails saying why.
func TestAMissingBackupDriveKeepsTheSettings(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	missing := filepath.Join(t.TempDir(), "nas", "portico")
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(missing)+`,"keep":3}`), -1, time.Now()); err != nil {
		t.Fatalf("saving a folder that is not there: %v", err)
	}
	again, damaged, err := settings.Open(ctx, s.DB)
	if err != nil || len(damaged) > 0 {
		t.Fatal(err, damaged)
	}
	s.Settings = again
	if got := Document.Get(again); got.Folder != missing || got.Keep != 3 {
		t.Errorf("after a start the settings are %+v", got)
	}
	if s.FolderWritable() {
		t.Errorf("a missing folder reads as writable")
	}
	if err := s.make(ctx, "portico-20261101-030000", false); err == nil || !strings.Contains(err.Error(), "not there") {
		t.Errorf("a backup to a missing folder: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Errorf("the missing folder was made")
	}
}

func strconvQuote(s string) string { raw, _ := json.Marshal(s); return string(raw) }

// stagedServer is a data folder holding "the database in use" and a key, with a backup staged.
func stagedServer(t *testing.T) (dir string) {
	t.Helper()
	ctx := context.Background()
	s := newService(t)
	if err := s.make(ctx, "portico-20261101-030000", false); err != nil {
		t.Fatal(err)
	}
	archive, _ := s.Path("portico-20261101-030000")
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.db"), []byte("the database in use"), 0o600)
	os.WriteFile(filepath.Join(dir, "main.db-wal"), []byte("its log"), 0o600)
	os.WriteFile(filepath.Join(dir, "remote-sources.key"), []byte("the key in use"), 0o600)
	if err := Stage(ctx, archive, dir, "portico-20261101-030000", time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func read(dir, name string) string {
	raw, _ := os.ReadFile(filepath.Join(dir, name))
	return string(raw)
}

// A staged restore is made at the next start, and the restore is remembered until its
// sign-ins have been ended, across any number of stops. If the restored database will not
// open, the original goes back.
func TestRestoreIsMadeAtTheNextStartAndCanBeUndone(t *testing.T) {
	ctx := context.Background()
	dir := stagedServer(t)
	if p := PendingRestore(dir); p == nil || p.Backup != "portico-20261101-030000" {
		t.Fatalf("pending: %+v", p)
	}
	if read(dir, "main.db") != "the database in use" {
		t.Fatalf("staging changed the database in use")
	}
	restored, _, err := Prepare(dir, time.Now())
	if err != nil || restored != "portico-20261101-030000" {
		t.Fatalf("the restore: %q %v", restored, err)
	}
	if err := db.CheckCopy(ctx, filepath.Join(dir, "main.db")); err != nil || exists(filepath.Join(dir, "main.db-wal")) {
		t.Errorf("the restored database: %v", err)
	}
	// The server stops before the sign-ins are ended: the next start still knows.
	if again, _, _ := Prepare(dir, time.Now()); again != restored {
		t.Fatalf("after a stop the restore was forgotten: %q", again)
	}
	d, err := db.Open(ctx, filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Opened(ctx, d, dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if again, _, _ := Prepare(dir, time.Now()); again != "" {
		t.Errorf("the restore is still remembered after its sign-ins were ended")
	}

	// A restore whose database will not open goes back.
	dir = stagedServer(t)
	Prepare(dir, time.Now())
	if back, err := PutBack(dir); !back || err != nil {
		t.Fatalf("putting back: %v %v", back, err)
	}
	if read(dir, "main.db") != "the database in use" || read(dir, "main.db-wal") != "its log" || read(dir, "remote-sources.key") != "the key in use" {
		t.Errorf("the original did not come back whole")
	}
}

// A stop at any point of the swap leaves either the backup in place or the original, never an
// empty data folder, and never removes the original. A restore staged over an hour before the
// start is dropped.
func TestAStopDuringARestoreNeverLosesTheDatabase(t *testing.T) {
	cases := map[string]func(dir string){
		"after the journal": func(dir string) {
			raw, _ := os.ReadFile(filepath.Join(dir, pendingDir, readyFile))
			os.MkdirAll(filepath.Join(dir, previousDir), 0o700)
			os.WriteFile(filepath.Join(dir, journalFile), raw, 0o600)
		},
		"after the original moved aside": func(dir string) {
			raw, _ := os.ReadFile(filepath.Join(dir, pendingDir, readyFile))
			os.MkdirAll(filepath.Join(dir, previousDir), 0o700)
			os.WriteFile(filepath.Join(dir, journalFile), raw, 0o600)
			os.Rename(filepath.Join(dir, "main.db"), filepath.Join(dir, previousDir, "main.db"))
		},
	}
	for name, stop := range cases {
		dir := stagedServer(t)
		stop(dir)
		restored, _, err := Prepare(dir, time.Now())
		if err != nil || restored != "" || read(dir, "main.db") != "the database in use" || read(dir, "main.db-wal") != "its log" {
			t.Errorf("%s: %q %v, the database reads %q", name, restored, err, read(dir, "main.db"))
		}
	}

	// The backup's database had moved in: the start finishes the restore.
	dir := stagedServer(t)
	raw, _ := os.ReadFile(filepath.Join(dir, pendingDir, readyFile))
	os.MkdirAll(filepath.Join(dir, previousDir), 0o700)
	os.WriteFile(filepath.Join(dir, journalFile), raw, 0o600)
	for _, f := range []string{"main.db", "main.db-wal", "remote-sources.key"} {
		os.Rename(filepath.Join(dir, f), filepath.Join(dir, previousDir, f))
	}
	os.Rename(filepath.Join(dir, pendingDir, "main.db"), filepath.Join(dir, "main.db"))
	if restored, _, err := Prepare(dir, time.Now()); err != nil || restored == "" || exists(filepath.Join(dir, journalFile)) ||
		read(dir, "remote-sources.key") == "the key in use" || read(dir, previousDir+"/main.db") != "the database in use" {
		t.Errorf("finishing a cut-short restore: %q %v", restored, err)
	}

	// No database in place, the original set aside and no journal: it comes back.
	dir = t.TempDir()
	os.MkdirAll(filepath.Join(dir, previousDir), 0o700)
	os.WriteFile(filepath.Join(dir, previousDir, "main.db"), []byte("the database in use"), 0o600)
	if _, _, err := Prepare(dir, time.Now()); err != nil || read(dir, "main.db") != "the database in use" {
		t.Errorf("an empty data folder with the original aside: %v", err)
	}

	// Staged more than an hour before the start: dropped, nothing changes.
	dir = stagedServer(t)
	restored, note, err := Prepare(dir, time.Now().Add(2*time.Hour))
	if err != nil || restored != "" || note == "" || PendingRestore(dir) != nil || read(dir, "main.db") != "the database in use" {
		t.Errorf("an old staged restore: %q %q %v", restored, note, err)
	}
}
