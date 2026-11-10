package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// The owner adds a WebDAV source only once its connection works, browses its folders, changes
// it and disconnects it. Its password is never sent back, is not in the database as typed,
// and goes to no other address without being typed again. An admin cannot touch sources.
func TestRemoteSources(t *testing.T) {
	dir := t.TempDir()
	s := openTestServer(t, filepath.Join(dir, "main.db"))
	owner := onMainProfile(t, s)
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat (1995).mkv": "x", "Shows/Andor/S01E01.mkv": "y", ".hidden/a": "z"})
	add := RemoteSourceCreate{Kind: "webdav", Name: "NAS", WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: "wrong"}}

	if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "source_sign_in") {
		t.Fatalf("a wrong password answered %d %s", w.Code, w.Body)
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a source that failed its test was saved: %+v", list.Items)
	}
	add.WebDAV.Password = dav.Password
	var src RemoteSource
	if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, &src); w.Code != 200 || src.State != "setup" || src.Preset != "read-once" ||
		!src.WebDAV.PasswordSet || strings.Contains(w.Body.String(), dav.Password) {
		t.Fatalf("adding answered %d %s", w.Code, w.Body)
	}

	var folders Page[RemoteFolder]
	if w := call(t, s, "GET", "/v1/admin/remote-sources/"+src.ID+"/folders", owner.AccessToken, nil, &folders); w.Code != 200 || len(folders.Items) != 2 ||
		folders.Items[1] != (RemoteFolder{Name: "Shows", Path: "Shows"}) {
		t.Fatalf("the top folders answered %d %s", w.Code, w.Body)
	}
	if call(t, s, "GET", "/v1/admin/remote-sources/"+src.ID+"/folders?path=Shows", owner.AccessToken, nil, &folders); len(folders.Items) != 1 ||
		folders.Items[0].Path != "Shows/Andor" {
		t.Fatalf("a folder inside: %+v", folders.Items)
	}

	admin := signInAs(t, s, addMember(t, s, owner.AccessToken, "ada", "admin").Username, testPassword, 1)
	for _, path := range []string{"/v1/admin/remote-sources", "/v1/admin/remote-sources/" + src.ID + "/folders"} {
		if code := status(t, s, "GET", path, admin.AccessToken, nil); code != 403 {
			t.Errorf("an admin's GET %s answered %d", path, code)
		}
	}

	elsewhere := "https://elsewhere.invalid/dav/"
	if code := status(t, s, "PATCH", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken, RemoteSourceEdit{WebDAV: &WebDAVSettings{Address: elsewhere}}); code != 400 {
		t.Fatalf("moving the stored password to another address answered %d", code)
	}
	if code := status(t, s, "PATCH", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken,
		RemoteSourceEdit{WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: "wrong"}}); code != 422 {
		t.Fatalf("a wrong new password answered %d", code)
	}
	preset := "everything"
	if w := call(t, s, "PATCH", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken,
		RemoteSourceEdit{Preset: &preset, WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User}}, &src); w.Code != 200 || src.Preset != "everything" {
		t.Fatalf("an edit keeping the password answered %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "POST", "/v1/admin/remote-sources/"+src.ID+"/test", owner.AccessToken, nil); code != 200 {
		t.Fatalf("testing the saved source answered %d", code)
	}

	for _, name := range []string{"main.db", "main.db-wal"} {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		if strings.Contains(string(raw), dav.Password) {
			t.Fatalf("the password is in %s as typed", name)
		}
	}

	if code := status(t, s, "DELETE", "/v1/admin/remote-sources/"+src.ID, owner.AccessToken, nil); code != 204 {
		t.Fatalf("disconnecting answered %d", code)
	}
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a disconnected source is still listed: %+v", list.Items)
	}
}
