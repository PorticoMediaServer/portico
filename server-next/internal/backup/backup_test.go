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
	st, _, err := settings.Open(ctx, d, nil)
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
	again, damaged, err := settings.Open(ctx, s.DB, nil)
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
