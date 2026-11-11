package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// The owner renames the server in Settings › General: every app sees the new name at once,
// a save from a page that is out of date is refused, a name that cannot be used is refused
// naming the field, and nobody but the owner reads or saves the settings.
func TestOwnerSettingsDocuments(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "ada", "admin")
	addMember(t, s, owner.AccessToken, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 1)
	member := signInAs(t, s, "max", testPassword, 2)

	var doc SettingsDocument
	w := call(t, s, "GET", "/v1/admin/settings/general", owner.AccessToken, nil, &doc)
	if w.Code != 200 || w.Header().Get("ETag") != `"0"` || !strings.Contains(string(doc.Defaults), `"name":"Portico"`) {
		t.Fatalf("the general settings answered %d %q %s", w.Code, w.Header().Get("ETag"), w.Body)
	}
	save := func(etag string, body string) (int, SettingsDocument) {
		r := newRequest("PUT", "/v1/admin/settings/general", owner.AccessToken, json.RawMessage(body))
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
		w := serve(s, r)
		var out SettingsDocument
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, out
	}
	if code, out := save(`"0"`, `{"name":"  Den  "}`); code != 200 || !strings.Contains(string(out.Values), `"name":"Den"`) || out.ETag != `"1"` {
		t.Fatalf("renaming answered %d %+v", code, out)
	}
	var system System
	if call(t, s, "GET", "/v1/system", "", nil, &system); system.Name != "Den" {
		t.Errorf("the server is called %q after the rename", system.Name)
	}
	if code, _ := save(`"0"`, `{"name":"Attic"}`); code != 412 {
		t.Errorf("a save from an out-of-date page answered %d, want 412", code)
	}
	w = call(t, s, "PUT", "/v1/admin/settings/general", owner.AccessToken, json.RawMessage(`{"name":""}`), nil)
	var refused errorBody
	jsonInto(w, &refused)
	if w.Code != 422 || len(refused.Error.Fields) != 1 || refused.Error.Fields[0].Path != "name" {
		t.Errorf("an empty name answered %d %s", w.Code, w.Body)
	}
	if code := status(t, s, "GET", "/v1/admin/settings/nothing", owner.AccessToken, nil); code != 404 {
		t.Errorf("an unknown document answered %d", code)
	}
	var list Page[SettingsDocument]
	if call(t, s, "GET", "/v1/admin/settings", owner.AccessToken, nil, &list); len(list.Items) == 0 {
		t.Errorf("the owner's list of documents is empty")
	}
	for _, token := range []string{admin.AccessToken, member.AccessToken} {
		for _, c := range []struct{ method, path string }{{"GET", "/v1/admin/settings"}, {"GET", "/v1/admin/settings/general"}, {"PUT", "/v1/admin/settings/general"}} {
			if code := status(t, s, c.method, c.path, token, json.RawMessage(`{"name":"Mine"}`)); code != 403 {
				t.Errorf("%s %s by an admin or member answered %d", c.method, c.path, code)
			}
		}
	}
}
