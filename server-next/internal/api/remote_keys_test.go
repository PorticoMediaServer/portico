package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// Scanning rule 7 and the lead's ruling: adding, changing and disconnecting a remote source
// take the owner signed in. The owner's own key with the admin scope is refused them, so a key
// that leaked cannot point the server at storage of its holder's choosing; it still reads the
// sources, browses their folders and tests them.
func TestRemoteSourceChangesNeedTheOwnerSignedIn(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat.mkv": "x"})
	add := RemoteSourceCreate{Kind: "webdav", Name: "NAS", WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}
	var src RemoteSource
	if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, &src); w.Code != 200 {
		t.Fatalf("the owner adding answered %d %s", w.Code, w.Body)
	}
	var key KeyCreated
	if w := call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Tool", Scopes: []string{"admin"}}, &key); w.Code != 200 {
		t.Fatalf("making a key answered %d: %s", w.Code, w.Body)
	}
	renamed := "Elsewhere"
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/admin/remote-sources", add},
		{"PATCH", "/v1/admin/remote-sources/" + src.ID, RemoteSourceEdit{Name: &renamed}},
		{"DELETE", "/v1/admin/remote-sources/" + src.ID, nil},
		{"POST", "/v1/admin/remote-sources/drive-sign-in", DriveSignInStart{ClientID: "c", ClientSecret: "s"}},
	} {
		var refused errorBody
		if w := call(t, s, c.method, c.path, key.Secret, c.body, &refused); w.Code != 403 || refused.Error.Code != "session_required" {
			t.Errorf("%s %s with a key answered %d %s, want 403 session_required", c.method, c.path, w.Code, refused.Error.Code)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/admin/remote-sources"}, {"GET", "/v1/admin/remote-sources/" + src.ID},
		{"GET", "/v1/admin/remote-sources/" + src.ID + "/folders"}, {"POST", "/v1/admin/remote-sources/" + src.ID + "/test"},
	} {
		if got := status(t, s, c.method, c.path, key.Secret, nil); got != 200 {
			t.Errorf("%s %s with a key answered %d, want 200", c.method, c.path, got)
		}
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 1 || list.Items[0].Name != "NAS" {
		t.Fatalf("a key changed the sources: %+v", list.Items)
	}
}
