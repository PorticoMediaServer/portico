package api

import "testing"

// Updates and storage are the owner's pages: an administrator and a member read neither. The
// storage page names the database among what uses space.
func TestUpdatesAndStorageAreTheOwners(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	addMember(t, s, owner, "max", "member")
	for _, token := range []string{signInAs(t, s, "ada", testPassword, 1).AccessToken, signInAs(t, s, "max", testPassword, 2).AccessToken} {
		for _, c := range []struct{ method, path string }{{"GET", "/v1/admin/updates"}, {"POST", "/v1/admin/updates/check"}, {"GET", "/v1/admin/storage"}} {
			if code := status(t, s, c.method, c.path, token, nil); code != 403 {
				t.Errorf("%s %s by an administrator or member answered %d", c.method, c.path, code)
			}
		}
	}
	var storage Storage
	if w := call(t, s, "GET", "/v1/admin/storage", owner, nil, &storage); w.Code != 200 || storage.DiskBytes == 0 || storage.Level == "" {
		t.Fatalf("storage answered %d: %s", w.Code, w.Body)
	}
	found := false
	for _, f := range storage.Folders {
		found = found || f.Name == "database" && f.Bytes > 0
	}
	if !found {
		t.Errorf("the database is not among what uses space: %+v", storage.Folders)
	}
}
