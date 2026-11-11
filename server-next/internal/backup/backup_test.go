package backup

import (
	"archive/tar"
	"bytes"
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
	"sync"
	"sync/atomic"
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
	// The backup that gave up is still waiting on its folder. It is let go and waited for, so
	// that nothing is writing into the test's folders when they are removed.
	defer func() {
		close(release)
		folderWork.Wait()
		folderWriteLimit, folderHook = saved, nil
	}()

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
	if err := Stage(ctx, archive, dir, "portico-20261101-030000", time.Now(), true); err != nil { // another server's backup, confirmed
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

// The page shows a staged restore only while the next start would make it; and what a stop
// left in the middle of a backup, a check or a restore is cleared when the server starts.
func TestWaitingRestoreAndLeftoversAtStart(t *testing.T) {
	dir := stagedServer(t)
	if WaitingRestore(dir, time.Now()) == nil {
		t.Errorf("a restore staged now is not shown as waiting")
	}
	if WaitingRestore(dir, time.Now().Add(2*time.Hour)) != nil {
		t.Errorf("a restore past its hour is shown as waiting")
	}
	s := newService(t)
	for _, name := range []string{".staging-1", ".backup-2", ".checking-3"} {
		os.MkdirAll(filepath.Join(s.DataDir, name), 0o700)
		os.WriteFile(filepath.Join(s.DataDir, name, "main.db"), []byte("a whole copy"), 0o600)
	}
	s.removeLeftovers()
	for _, name := range []string{".staging-1", ".backup-2", ".checking-3"} {
		if exists(filepath.Join(s.DataDir, name)) {
			t.Errorf("%s was left after a start", name)
		}
	}
}

// A backup carries this server's mark: one this server made restores with no warning; one
// another server made, or one changed since, is not refused but needs the owner's
// confirmation, and the mark is checked over the very bytes unpacked.
func TestABackupNotMadeHereNeedsConfirming(t *testing.T) {
	ctx := context.Background()
	here, there := newService(t), newService(t)
	for _, s := range []*Service{here, there} {
		if err := s.make(ctx, "portico-20261101-030000", false); err != nil {
			t.Fatal(err)
		}
	}
	ours, _ := here.Path("portico-20261101-030000")
	theirs, _ := there.Path("portico-20261101-030000")
	if !MadeHere(here.DataDir, ours) {
		t.Errorf("this server's own backup reads as not made here")
	}
	if err := Stage(ctx, ours, here.DataDir, "ours", time.Now(), false); err != nil {
		t.Errorf("restoring this server's own backup: %v", err)
	}
	if MadeHere(here.DataDir, theirs) {
		t.Errorf("another server's backup reads as made here")
	}
	if err := Stage(ctx, theirs, here.DataDir, "theirs", time.Now(), false); !errors.Is(err, ErrNotMadeHere) {
		t.Errorf("another server's backup without confirming: %v", err)
	}
	if err := Stage(ctx, theirs, here.DataDir, "theirs", time.Now(), true); err != nil {
		t.Errorf("another server's backup, confirmed: %v", err)
	}

	// This server's backup with a file added to it and the description left as it was.
	db := filepath.Join(t.TempDir(), "main.db")
	extract(t, ours, "main.db", db)
	pack(t, ours, map[string]string{"main.db": db, "artwork/ab/abcdef0123456789abcdef01.large": db})
	if MadeHere(here.DataDir, ours) {
		t.Errorf("a changed backup reads as made here")
	}
	if err := Stage(ctx, ours, here.DataDir, "changed", time.Now(), false); !errors.Is(err, ErrNotMadeHere) {
		t.Errorf("a changed backup without confirming: %v", err)
	}
}

// The mark's key is written whole, and two backups starting at once keep one key. A key that
// cannot be read (cut short, say) stops neither a backup, made without its mark, nor a
// confirmed restore; an unconfirmed one is warned.
func TestAnUnreadableMarkKeyWarnsAndDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() { defer wg.Done(); keys[i], _ = markKey(s.DataDir) }()
	}
	wg.Wait()
	for _, k := range keys {
		if len(k) != 32 || !bytes.Equal(k, keys[0]) {
			t.Fatalf("backups starting at once made different keys")
		}
	}
	if left, _ := filepath.Glob(filepath.Join(s.DataDir, markKeyFile+".new-*")); len(left) > 0 {
		t.Errorf("making the key left %v", left)
	}

	os.WriteFile(filepath.Join(s.DataDir, markKeyFile), keys[0][:9], 0o600)
	if problem := MarkKeyProblem(s.DataDir); !strings.Contains(problem, "can't be read") {
		t.Errorf("a key cut short reads as %q", problem)
	}
	if err := s.make(ctx, "portico-20261101-030000", false); err != nil {
		t.Fatalf("a backup with the key cut short: %v", err)
	}
	path, _ := s.Path("portico-20261101-030000")
	if err := Stage(ctx, path, s.DataDir, "unmarked", time.Now(), false); !errors.Is(err, ErrNotMadeHere) {
		t.Errorf("restoring without confirming: %v", err)
	}
	if err := Stage(ctx, path, s.DataDir, "unmarked", time.Now(), true); err != nil {
		t.Errorf("restoring, confirmed: %v", err)
	}
}

// The newest backup is known without reading the backup folder on every look: it is read once
// at start, then kept as backups are made and deleted.
func TestTheNewestBackupIsKept(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, known := s.Newest(); known {
		t.Errorf("the newest backup is known before the folder was read")
	}
	for _, id := range []string{"portico-20261101-030000", "portico-20261102-030000"} {
		if err := s.make(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if b, known := s.Newest(); !known || b == nil || b.ID != "portico-20261102-030000" {
		t.Errorf("after two backups the newest is %+v", b)
	}
	// A restart reads it from the folder.
	started := &Service{DB: s.DB, Settings: s.Settings, DataDir: s.DataDir, Version: s.Version, Log: s.Log, Now: s.Now}
	started.readNewest()
	if b, known := started.Newest(); !known || b == nil || b.ID != "portico-20261102-030000" {
		t.Errorf("read at start, the newest is %+v", b)
	}
	if err := started.Delete("portico-20261102-030000"); err != nil {
		t.Fatal(err)
	}
	if b, _ := started.Newest(); b == nil || b.ID != "portico-20261101-030000" {
		t.Errorf("after deleting the newest, it is %+v", b)
	}
}

// A server that stops while the nightly backup is being made cuts it off and waits for it,
// so nothing is still copying the database as it closes; no half backup is listed, and no
// failure is held against it. After the restart that backup counts as missed, and is made.
func TestAScheduledBackupCutOffByAStopIsMadeAfterTheRestart(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	// Last night's backup is there.
	if err := s.make(ctx, "portico-20261110-030000", true); err != nil {
		t.Fatal(err)
	}
	// Tonight's is being made, waiting on its folder, when the server stops.
	tonight := time.Date(2026, 11, 11, 3, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return tonight }
	release := make(chan struct{})
	folderHook = func(string) { <-release }
	answered := sync.OnceFunc(func() {
		close(release)
		folderWork.Wait()
		folderHook = nil
	})
	defer answered()
	stopping, stop := context.WithCancel(ctx)
	s.Stopping = stopping
	if _, err := s.Start(true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	stop()
	waited := make(chan struct{})
	go func() { s.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop was still waiting for the backup after 5 seconds")
	}
	if s.Running() != nil || s.LastError() != nil {
		t.Errorf("after the stop: running %v, failure %v", s.Running(), s.LastError())
	}
	if list, err := s.List(); err != nil || len(list) != 1 {
		t.Fatalf("after the stop the backups are %v, %v", list, err)
	}
	// The backup folder answers again.
	answered()

	// The server starts again ten minutes later.
	again := &Service{DB: s.DB, Settings: s.Settings, DataDir: s.DataDir, Version: "test", Log: s.Log,
		Now: func() time.Time { return tonight.Add(10 * time.Minute) }}
	if !again.missed() {
		t.Fatal("after the restart the backup that was cut off does not count as missed, so it would be skipped until tomorrow")
	}
	saved := missedAfter
	missedAfter = 0
	defer func() { missedAfter = saved }()
	running, end := context.WithCancel(ctx)
	ended := make(chan struct{})
	go func() { again.Run(running); close(ended) }()
	defer func() { end(); <-ended; again.Wait() }()
	for began := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		list, _ := again.List()
		if len(list) == 2 && list[0].Scheduled && again.Running() == nil {
			break
		}
		if time.Since(began) > 10*time.Second {
			t.Fatalf("the missed backup was not made after the restart: %v", list)
		}
	}
}

// Console rule 55: a scheduled backup that fails is tried again for 4 hours, and only then is
// the owner told. The alert ends when a scheduled backup succeeds. A server that has no
// scheduled backup though one was due since it was set up makes it soon after it starts; a
// server nobody has set up yet has missed none.
func TestAFailedScheduledBackupIsRetriedForFourHoursBeforeTheOwnerIsTold(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	drive := filepath.Join(t.TempDir(), "drive") // not mounted
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(drive)+`}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 11, 3, 10, 0, 0, time.Local).UnixNano())
	s.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	if s.missed() {
		t.Fatal("a server nobody has set up yet counts a backup as missed")
	}
	setUp := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local) // yesterday noon; a backup was due at 3 this morning
	if _, err := s.DB.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "x", ProfileName: "Owner", Now: setUp,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "r", ExpiresAt: setUp.Add(24 * time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}}); err != nil {
		t.Fatal(err)
	}
	savedAfter, savedEvery := missedAfter, retryEvery
	missedAfter, retryEvery = 0, 20*time.Millisecond
	running, end := context.WithCancel(ctx)
	ended := make(chan struct{})
	go func() { s.Run(running); close(ended) }()
	defer func() { end(); <-ended; s.Wait(); missedAfter, retryEvery = savedAfter, savedEvery }()
	until := func(what string, ok func() bool) {
		t.Helper()
		for began := time.Now(); !ok(); time.Sleep(10 * time.Millisecond) {
			if time.Since(began) > 10*time.Second {
				t.Fatal(what)
			}
		}
	}
	until("no backup was tried after the start, though one was due and there is none", func() bool { return s.LastError() != nil })
	time.Sleep(200 * time.Millisecond) // several more tries
	if failed := s.Failed(); failed != nil {
		t.Fatalf("the owner was told before the 4 hours of retries were used up: %v", failed)
	}
	clock.Add(int64(4*time.Hour + time.Minute))
	until("the owner was not told after 4 hours of failed retries", func() bool { return s.Failed() != nil })
	// The drive is back, and the next scheduled backup succeeds.
	if err := os.MkdirAll(drive, 0o700); err != nil {
		t.Fatal(err)
	}
	until("no scheduled backup could be started", func() bool { _, err := s.Start(true); return err == nil })
	until("the alert stayed after a scheduled backup succeeded", func() bool { return s.Running() == nil && s.Failed() == nil })
}

// The 4 hours before the owner is told are counted from the first failure and survive a
// restart: a computer that is switched off every evening still tells its owner that the
// nightly backup keeps failing. Choosing Never ends the alert, since no scheduled backup
// can then succeed to end it.
func TestTheFourHoursBeforeTheBackupAlertSurviveARestart(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	drive := filepath.Join(t.TempDir(), "drive") // not mounted
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(drive)+`}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	setUp := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	if _, err := s.DB.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "x", ProfileName: "Owner", Now: setUp,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "r", ExpiresAt: setUp.Add(24 * time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}}); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 11, 3, 10, 0, 0, time.Local).UnixNano())
	s.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	savedAfter, savedEvery := missedAfter, retryEvery
	missedAfter, retryEvery = 0, time.Hour // one try for each start
	defer func() { missedAfter, retryEvery = savedAfter, savedEvery }()
	until := func(what string, ok func() bool) {
		t.Helper()
		for began := time.Now(); !ok(); time.Sleep(10 * time.Millisecond) {
			if time.Since(began) > 10*time.Second {
				t.Fatal(what)
			}
		}
	}
	run := func(s *Service) (stop func()) {
		running, end := context.WithCancel(ctx)
		ended := make(chan struct{})
		go func() { s.Run(running); close(ended) }()
		return func() { end(); <-ended; s.Wait() }
	}

	// The first night: the backup fails, and the computer is switched off before 4 hours pass.
	stop := run(s)
	until("no backup was tried", func() bool { return s.LastError() != nil && s.Running() == nil })
	if s.Failed() != nil {
		t.Fatal("the owner was told at the first failure")
	}
	stop()

	// Switched on again five hours later: the first failed try tells the owner.
	clock.Add(int64(5 * time.Hour))
	again := &Service{DB: s.DB, Settings: s.Settings, DataDir: s.DataDir, Version: "test", Log: s.Log, Now: s.Now}
	if again.Failed() != nil {
		t.Fatal("the owner was told before any backup was tried after the start")
	}
	stop = run(again)
	defer stop()
	until("backups have failed for more than 4 hours across a restart and the owner was not told", func() bool { return again.Failed() != nil })

	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(drive)+`,"days":[]}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	until("the alert stayed up after the owner chose Never", func() bool { return again.Failed() == nil })
	if !again.failingSince().IsZero() {
		t.Error("after Never the time backups began to fail is still kept")
	}
}

// The owner who mounts the drive and backs up by hand has a backup: that ends the alert and
// the count towards it, though it was a scheduled backup that failed.
func TestABackupMadeByHandEndsTheBackupAlert(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	drive := filepath.Join(t.TempDir(), "drive") // not mounted
	if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(`{"folder":`+strconvQuote(drive)+`}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := func(scheduled bool) {
		t.Helper()
		if _, err := s.Start(scheduled); err != nil {
			t.Fatal(err)
		}
		s.Wait()
	}
	start(true)
	s.GiveUp()
	if s.Failed() == nil || s.failingSince().IsZero() {
		t.Fatalf("a failed scheduled backup: alert %v, failing since %v", s.Failed(), s.failingSince())
	}
	if err := os.MkdirAll(drive, 0o700); err != nil {
		t.Fatal(err)
	}
	start(false)
	if s.Failed() != nil || s.LastError() != nil || !s.failingSince().IsZero() {
		t.Fatalf("after a backup made by hand: alert %v, last failure %v, failing since %v", s.Failed(), s.LastError(), s.failingSince())
	}
}

// A backup setting changed while a failed backup is being tried again ends those tries, and
// the next night's backup has its own 4 hours of them: when it fails it is tried again, where
// a mark left from the night before would have counted its tries as used up.
func TestChangingBackupSettingsDuringRetriesGivesTheNextNightItsOwn(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	drive := filepath.Join(t.TempDir(), "drive") // not mounted
	write := func(doc string) {
		t.Helper()
		if _, err := s.Settings.Write(ctx, 0, "backups", json.RawMessage(doc), -1, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"folder":` + strconvQuote(drive) + `}`)
	setUp := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	if _, err := s.DB.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "x", ProfileName: "Owner", Now: setUp,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "r", ExpiresAt: setUp.Add(24 * time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}}); err != nil {
		t.Fatal(err)
	}
	var clock, tries, tomorrow atomic.Int64
	clock.Store(time.Date(2026, 11, 11, 3, 10, 0, 0, time.Local).UnixNano())
	s.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	almostThree := time.Date(2026, 11, 12, 2, 59, 59, 990_000_000, time.Local).UnixNano()
	folderHook = func(string) {
		tries.Add(1)
		if clock.Load() >= almostThree {
			tomorrow.Add(1)
		}
		// Tomorrow's backup, once begun, is no longer due: only a retry can try it again.
		clock.CompareAndSwap(almostThree, almostThree+int64(time.Second))
	}
	savedAfter, savedEvery := missedAfter, retryEvery
	missedAfter, retryEvery = 0, 20*time.Millisecond
	running, end := context.WithCancel(ctx)
	ended := make(chan struct{})
	go func() { s.Run(running); close(ended) }()
	defer func() {
		end()
		<-ended
		s.Wait()
		folderWork.Wait()
		missedAfter, retryEvery, folderHook = savedAfter, savedEvery, nil
	}()
	until := func(what string, ok func() bool) {
		t.Helper()
		for began := time.Now(); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Since(began) > 10*time.Second {
				t.Fatal(what)
			}
		}
	}
	until("the failed backup was not tried again", func() bool { return tries.Load() >= 2 })
	// The owner changes a setting during the retries; the next backup is due tomorrow at 3.
	write(`{"folder":` + strconvQuote(drive) + `,"keep":5}`)
	time.Sleep(1500 * time.Millisecond) // the schedule looks at a backup under way once a second
	// A moment before 3 tomorrow. That backup fails too, and is tried again.
	clock.Store(almostThree)
	write(`{"folder":` + strconvQuote(drive) + `,"keep":6}`) // wakes the schedule to look at the clock
	until("tomorrow's failed backup was not tried again", func() bool { return tomorrow.Load() >= 2 })
}
