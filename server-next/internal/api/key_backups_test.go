package api

import "testing"

// A backup without protection holds what signs in as the owner, so a key neither removes the
// protection nor chooses where backups are written (Justin, 18 Nov). It may still start one.
func TestAKeyCannotUnprotectBackupsOrMoveThem(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var key KeyCreated
	if w := call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Script", Scopes: []string{"admin"}}, &key); w.Code != 200 {
		t.Fatalf("making a key answered %d: %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		path string
		body any
	}{
		{"/v1/admin/backups/passphrase", BackupPassphrase{}},
		{"/v1/admin/settings/backups", map[string]string{"folder": t.TempDir()}},
	} {
		var refused errorBody
		if w := call(t, s, "PUT", c.path, key.Secret, c.body, &refused); w.Code != 403 || refused.Error.Code != "session_required" {
			t.Errorf("PUT %s with a key answered %d %s, want 403 session_required", c.path, w.Code, refused.Error.Code)
		}
	}
}
