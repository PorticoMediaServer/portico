package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

// The storage page measures the data folder once and reuses the sizes for ten minutes, so
// opening it again on a large library answers at once; free space is read each time.
func TestStorageSizesAreReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	clock := time.Now()
	s.setClock(func() time.Time { return clock })
	owner := onMainProfile(t, s).AccessToken
	read := func() Storage {
		var out Storage
		if w := call(t, s, "GET", "/v1/admin/storage", owner, nil, &out); w.Code != 200 {
			t.Fatalf("storage answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	size := func(st Storage, name string) int64 {
		for _, f := range st.Folders {
			if f.Name == name {
				return f.Bytes
			}
		}
		return 0
	}
	first := read()
	os.MkdirAll(filepath.Join(filepath.Dir(path), "artwork"), 0o700)
	os.WriteFile(filepath.Join(filepath.Dir(path), "artwork", "big"), make([]byte, 1<<20), 0o600)
	if again := read(); size(again, "artwork") != 0 || !again.MeasuredAt.Equal(first.MeasuredAt) {
		t.Errorf("the page measured the folder again at once: %+v", again.Folders)
	}
	clock = clock.Add(11 * time.Minute)
	if later := read(); size(later, "artwork") != 1<<20 {
		t.Errorf("after ten minutes the page still has the old sizes: %+v", later.Folders)
	}
}
