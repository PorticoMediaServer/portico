package api

import (
	"os"
	"path/filepath"
	"testing"
)

// The owner removes the copy kept before the last upgrade, giving up the way back to the version
// before: the copy is gone and the database is not, the activity record says who removed it, and
// asking again changes nothing. An administrator cannot remove it.
func TestOwnerRemovesTheUpgradeCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	kept := path + ".before-upgrade-1-to-2"
	if err := os.WriteFile(kept, []byte("the database before the upgrade"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "DELETE", "/v1/admin/upgrade-copy", admin, nil); code != 403 {
		t.Errorf("an administrator removing the copy answered %d", code)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the copy went when an administrator asked: %v", err)
	}
	for range 2 {
		if code := status(t, s, "DELETE", "/v1/admin/upgrade-copy", owner, nil); code != 204 {
			t.Fatalf("removing the copy answered %d", code)
		}
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Errorf("the copy is still there: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the database itself: %v", err)
	}
	var record Page[ActivityEntry]
	call(t, s, "GET", "/v1/admin/audit?action=upgrade_copy", owner, nil, &record)
	if len(record.Items) != 1 || record.Items[0].Actor == "" {
		t.Errorf("the activity record of the removal: %+v", record.Items)
	}
}
