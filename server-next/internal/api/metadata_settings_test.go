package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The owner's provider keys are stored sealed: no route answers them, the database files do not
// hold them as typed, they are in use again after a restart, and a lost key file says to enter
// them again rather than failing. Administrators cannot see the page.
func TestProviderKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMain(t, s, setup(t, s)).AccessToken
	addMember(t, s, owner, "ada", "admin")
	const tmdbKey, subtitleKey, password = "eyJhbGciOiJIUzI1NiJ9.tmdb-read-token-for-the-test", "os-api-key-for-the-test", "os-password-for-the-test"
	put := func(token string, edit MetadataSettingsEdit, want int) *bytes.Buffer {
		t.Helper()
		w := call(t, s, "PUT", "/v1/admin/metadata/settings", token, edit, nil)
		if w.Code != want {
			t.Fatalf("saving the settings answered %d, want %d: %s", w.Code, want, w.Body)
		}
		return w.Body
	}
	key, apiKey, user, pass := tmdbKey, subtitleKey, "subtitle-account", password
	body := put(owner, MetadataSettingsEdit{TMDBKey: &key, OpenSubtitles: &OpenSubtitlesSettingsEdit{APIKey: &apiKey, Username: &user, Password: &pass}}, 200)
	for _, secret := range []string{tmdbKey, subtitleKey, password} {
		if bytes.Contains(body.Bytes(), []byte(secret)) {
			t.Fatal("the answer to saving the settings holds a key")
		}
	}
	var page MetadataSettings
	if w := call(t, s, "GET", "/v1/admin/metadata/settings", owner, nil, &page); w.Code != 200 || !page.TMDB.KeySet || !page.OpenSubtitles.APIKeySet ||
		!page.OpenSubtitles.PasswordSet || page.OpenSubtitles.Username != "subtitle-account" || bytes.Contains(w.Body.Bytes(), []byte(tmdbKey)) {
		t.Fatalf("the settings page: %d %+v", w.Code, page)
	}
	if s.metadata.Providers["tmdb"] == nil {
		t.Fatal("a saved TMDB key is not in use")
	}
	long := string(make([]byte, 5000))
	put(owner, MetadataSettingsEdit{TMDBKey: &long}, 400)
	if w := call(t, s, "GET", "/v1/admin/metadata/settings", signInAs(t, s, "ada", testPassword, 5).AccessToken, nil, nil); w.Code != 403 {
		t.Fatalf("an administrator opened the provider keys page: %d", w.Code)
	}
	for _, file := range []string{path, path + "-wal"} {
		raw, _ := os.ReadFile(file)
		for _, secret := range []string{tmdbKey, subtitleKey, password} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("%s holds a key as typed", filepath.Base(file))
			}
		}
	}

	// After a restart the keys are opened and in use again.
	closeDB(s.db)
	s = openTestServer(t, path)
	owner = onMain(t, s, signIn(t, s, 1)).AccessToken
	if call(t, s, "GET", "/v1/admin/metadata/settings", owner, nil, &page); !page.TMDB.KeySet || s.metadata.Providers["tmdb"] == nil {
		t.Fatalf("after a restart: %+v", page)
	}
	// Without its key file the server cannot open them: the page says to enter them again.
	closeDB(s.db)
	if err := os.Remove(filepath.Join(filepath.Dir(path), "secrets.key")); err != nil {
		t.Fatal(err)
	}
	s = openTestServer(t, path)
	owner = onMain(t, s, signIn(t, s, 2)).AccessToken
	page = MetadataSettings{}
	if call(t, s, "GET", "/v1/admin/metadata/settings", owner, nil, &page); page.TMDB.KeySet || !page.TMDB.EnterAgain || !page.OpenSubtitles.EnterAgain ||
		s.metadata.Providers["tmdb"] != nil {
		t.Fatalf("with the key file gone: %+v", page)
	}
	empty := ""
	put(owner, MetadataSettingsEdit{TMDBKey: &empty}, 200)
	page = MetadataSettings{}
	if call(t, s, "GET", "/v1/admin/metadata/settings", owner, nil, &page); page.TMDB.KeySet || page.TMDB.EnterAgain {
		t.Fatalf("after removing the key: %+v", page)
	}
}
