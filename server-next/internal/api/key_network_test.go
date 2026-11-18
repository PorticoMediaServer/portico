package api

import "testing"

// No key changes the Network or Remote access pages (Justin, 18 Nov): one that leaked could
// call every address home, which ends guess blocking, home-only accounts and "Only the owner"
// from outside. A key still reads both.
func TestAKeyCannotChangeNetworkOrRemoteAccess(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var key KeyCreated
	if w := call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Script", Scopes: []string{"admin"}}, &key); w.Code != 200 {
		t.Fatalf("making a key answered %d: %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/admin/settings/network", "/v1/admin/settings/remote-access"} {
		var refused errorBody
		if w := call(t, s, "PUT", path, key.Secret, map[string]any{}, &refused); w.Code != 403 || refused.Error.Code != "session_required" {
			t.Errorf("PUT %s with a key answered %d %s, want 403 session_required", path, w.Code, refused.Error.Code)
		}
		if code := status(t, s, "GET", path, key.Secret, nil); code != 200 {
			t.Errorf("GET %s with a key answered %d, want 200", path, code)
		}
	}
}
