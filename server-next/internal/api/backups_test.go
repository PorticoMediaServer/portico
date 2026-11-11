package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The owner backs up now, sees the backup listed with the credentials it holds, checks it and
// deletes it; an administrator and a member do none of that.
func TestOwnerBackups(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	addMember(t, s, owner, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, s, "max", testPassword, 2).AccessToken
	for _, token := range []string{admin, member} {
		for _, c := range []struct{ method, path string }{{"GET", "/v1/admin/backups"}, {"POST", "/v1/admin/backups"},
			{"POST", "/v1/admin/backups/inspect"}, {"DELETE", "/v1/admin/backups/portico-20261110-030000"}} {
			if code := status(t, s, c.method, c.path, token, nil); code != 403 {
				t.Errorf("%s %s by an administrator or member answered %d", c.method, c.path, code)
			}
		}
	}
	var started BackupStarted
	if w := call(t, s, "POST", "/v1/admin/backups", owner, nil, &started); w.Code != 202 || started.ID == "" {
		t.Fatalf("back up now answered %d: %s", w.Code, w.Body)
	}
	var page Backups
	for deadline := time.Now().Add(30 * time.Second); ; {
		page = Backups{}
		call(t, s, "GET", "/v1/admin/backups", owner, nil, &page)
		if page.Running == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(page.Items) != 1 || page.Items[0].ID != started.ID || page.Items[0].Scheduled || !page.Readable || page.LastError != "" {
		t.Fatalf("the backups page: %+v", page)
	}
	var inspected BackupInspection
	if w := call(t, s, "POST", "/v1/admin/backups/inspect", owner, BackupInspect{ID: started.ID}, &inspected); w.Code != 200 ||
		len(inspected.Restoring) == 0 || inspected.CreatedAt == nil {
		t.Fatalf("checking the backup answered %d: %s", w.Code, w.Body)
	}
	notABackup := filepath.Join(t.TempDir(), "notes.tar.gz")
	os.WriteFile(notABackup, []byte("just some notes"), 0o600)
	if code := status(t, s, "POST", "/v1/admin/backups/inspect", owner, BackupInspect{Path: notABackup}); code != 422 {
		t.Errorf("a file that is not a backup answered %d", code)
	}
	if code := status(t, s, "DELETE", "/v1/admin/backups/"+started.ID, owner, nil); code != 204 {
		t.Fatalf("deleting answered %d", code)
	}
	var after Backups
	if call(t, s, "GET", "/v1/admin/backups", owner, nil, &after); len(after.Items) != 0 {
		t.Errorf("the deleted backup is still listed: %+v", after.Items)
	}
}

// A restore needs the owner's password again and is refused for a wrong one; an administrator
// cannot restore; Restart says how many are playing until it is confirmed.
func TestRestoreNeedsThePasswordAgain(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	var started BackupStarted
	call(t, s, "POST", "/v1/admin/backups", owner, nil, &started)
	for s.Backups().Running() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	restore := BackupRestore{BackupInspect: BackupInspect{ID: started.ID}, CurrentPassword: "not it"}
	if code := status(t, s, "POST", "/v1/admin/backups/restore", owner, restore); code != 403 {
		t.Errorf("a restore with a wrong password answered %d", code)
	}
	restore.CurrentPassword = testPassword
	if code := status(t, s, "POST", "/v1/admin/backups/restore", admin, restore); code != 403 {
		t.Errorf("an administrator's restore answered %d", code)
	}
	var staged RestoreStaged
	if w := call(t, s, "POST", "/v1/admin/backups/restore", owner, restore, &staged); w.Code != 200 {
		t.Fatalf("the restore answered %d: %s", w.Code, w.Body)
	}
	var restart Restart
	if call(t, s, "POST", "/v1/admin/restart", owner, nil, &restart); restart.Restarting {
		t.Errorf("a restart started without being confirmed")
	}
	if code := status(t, s, "DELETE", "/v1/admin/backups/restore", owner, nil); code != 204 {
		t.Errorf("cancelling the restore answered %d", code)
	}
}
