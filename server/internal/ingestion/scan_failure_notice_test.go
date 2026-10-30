package ingestion

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

func unreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0700) })
	if _, err := os.ReadDir(path); err == nil {
		t.Skip("this user can read a directory without permission")
	}
}

func noticeFixture(t *testing.T, root string) (*sql.DB, *Service, catalog.Library) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The first account is the owner, whose inbox receives the notice.
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	return db, New(db, cat, assets.Probe{}), lib
}

// One folder that cannot be read does not stop a scan: the folder is left
// out, the rest of the library is scanned, and the owner is told which folder
// was skipped, by its place in the library and never by its path on the disk.
func TestUnreadableFolderIsSkippedAndTheOwnerIsToldWhich(t *testing.T) {
	root := t.TempDir()
	broken := filepath.Join(root, "Films", "Unreadable")
	if err := os.MkdirAll(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Films", "Kept (2001).mkv"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	unreadable(t, broken)
	db, s, lib := noticeFixture(t, root)
	if _, err := db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	var state Job
	for n := 0; n < 200; n++ {
		s.process(context.Background(), job.ID, lib.ID)
		if state, err = s.Get(job.ID); err != nil || state.Status != "running" && state.Status != "queued" {
			break
		}
	}
	if err != nil || state.Status != "complete_with_warnings" {
		t.Fatalf("a scan with one unreadable folder: %+v %v", state, err)
	}
	var kept, notices int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND state='available'`, filepath.Join("Films", "Kept (2001).mkv")).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the readable part of the library was not scanned: %d %v", kept, err)
	}
	var reason string
	var protect bool
	where := filepath.Join("Films", "Unreadable")
	if err = db.QueryRow(`SELECT reason,protect FROM inventory_scan_issues WHERE job_id=? AND relative_path=?`, job.ID, where).Scan(&reason, &protect); err != nil || reason != "directory_unreadable" || !protect {
		t.Fatalf("the skipped folder is not recorded for the owner: %q %v %v", reason, protect, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND relative_path LIKE ?`, job.ID, root+"%").Scan(&notices); err != nil || notices != 0 {
		t.Fatalf("a skipped folder is recorded by its absolute path: %d %v", notices, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM notification_records WHERE category='scan.failed'`).Scan(&notices); err != nil || notices != 0 {
		t.Fatalf("a scan that finished told the owner it had stopped: %d %v", notices, err)
	}
}

// A scan that cannot go on at all (here the library's own folder cannot be
// read) stops, and the owner's notice does not carry the path on the disk.
func TestStoppedScanTellsTheOwnerWithoutTheDiskPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Library")
	if err := os.MkdirAll(filepath.Join(root, "Films"), 0700); err != nil {
		t.Fatal(err)
	}
	db, s, lib := noticeFixture(t, root)
	job, err := s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	unreadable(t, root)
	s.process(context.Background(), job.ID, lib.ID)
	if j, err := s.Get(job.ID); err != nil || j.Status != "failed" {
		t.Fatalf("scan of an unreadable library folder did not stop: %+v %v", j, err)
	}
	var body, arguments string
	if err = db.QueryRow(`SELECT body,arguments FROM notification_records WHERE category='scan.failed'`).Scan(&body, &arguments); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, root) || strings.Contains(arguments, root) || strings.Contains(body, filepath.Dir(root)) {
		t.Fatalf("notice carries the absolute source path: %q %s", body, arguments)
	}
}

// When a scan stops inside the library, the notice names the folder by its
// place in the library.
func TestStoppedScanNoticeNamesTheFolderItStoppedAt(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	where := filepath.Join("Films", "Unreadable")
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, e := notify.NotifyScanFailure(tx, 1, "library", "source", "scan_inventory_incomplete", where)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var body, arguments string
	if err = db.QueryRow(`SELECT body,arguments FROM notification_records WHERE category='scan.failed'`).Scan(&body, &arguments); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, where) || !strings.Contains(arguments, where) {
		t.Fatalf("notice does not name the folder: %q %s", body, arguments)
	}
}
