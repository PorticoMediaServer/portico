package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
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
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	in := setup(t, s)
	owner := onMain(t, s, in).AccessToken
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
	var page Backups
	if call(t, s, "GET", "/v1/admin/backups", owner, nil, &page); page.PendingRestore == nil || page.PendingRestore.Backup != started.ID {
		t.Errorf("the page does not show the restore waiting: %+v", page.PendingRestore)
	}
	var restart Restart
	if call(t, s, "POST", "/v1/admin/restart", owner, nil, &restart); restart.Restarting {
		t.Errorf("a restart started without being confirmed")
	}

	// The next start makes it. Stopped before the sign-ins are ended, the one after still
	// ends them: the owner's sign-in from before the restore cannot be renewed.
	closeDB(s.db)
	dir := filepath.Dir(path)
	if restored, _, err := backup.Prepare(dir, time.Now()); err != nil || restored != started.ID {
		t.Fatalf("making the restore: %q %v", restored, err)
	}
	if restored, _, _ := backup.Prepare(dir, time.Now()); restored != started.ID {
		t.Fatalf("after a stop the restore was forgotten")
	}
	s = openTestServer(t, path)
	if err := backup.Opened(context.Background(), s.db, dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := renew(t, s, in.Session.RefreshToken, 0, "after-restore"); code != 401 {
		t.Errorf("a sign-in from before the restore renewed: %d", code)
	}

	// A restore staged and then cancelled is not made.
	owner = onMain(t, s, signIn(t, s, 0)).AccessToken
	call(t, s, "POST", "/v1/admin/backups/restore", owner, restore, nil)
	if code := status(t, s, "DELETE", "/v1/admin/backups/restore", owner, nil); code != 204 || backup.PendingRestore(dir) != nil {
		t.Errorf("cancelling the restore answered %d", code)
	}
}

// A backup whose mark does not match (made elsewhere, or changed since) is restored only once
// the owner has been warned and confirmed: the first try answers the warning.
func TestRestoreWarnsOfABackupNotMadeHere(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	var started BackupStarted
	call(t, s, "POST", "/v1/admin/backups", owner, nil, &started)
	for s.Backups().Running() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	var inspected BackupInspection
	if call(t, s, "POST", "/v1/admin/backups/inspect", owner, BackupInspect{ID: started.ID}, &inspected); !inspected.MadeHere {
		t.Errorf("this server's own backup reads as not made here")
	}
	// The description is changed, as someone with the backup folder could.
	desc := filepath.Join(s.Backups().Folder(), started.ID+".json")
	raw, _ := os.ReadFile(desc)
	os.WriteFile(desc, []byte(strings.Replace(string(raw), `"mark": "`, `"mark": "0`, 1)), 0o600)
	if call(t, s, "POST", "/v1/admin/backups/inspect", owner, BackupInspect{ID: started.ID}, &inspected); inspected.MadeHere {
		t.Errorf("a changed backup reads as made here")
	}
	restore := BackupRestore{BackupInspect: BackupInspect{ID: started.ID}, CurrentPassword: testPassword}
	w := call(t, s, "POST", "/v1/admin/backups/restore", owner, restore, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "replaces every account, including yours") {
		t.Fatalf("restoring a changed backup without confirming answered %d: %s", w.Code, w.Body)
	}
	restore.NotMadeHereConfirmed = true
	if code := status(t, s, "POST", "/v1/admin/backups/restore", owner, restore); code != 200 {
		t.Errorf("restoring it, confirmed, answered %d", code)
	}
}
