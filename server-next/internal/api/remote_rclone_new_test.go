package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// A new rclone remote is given by its type and options or pasted, never both, and Google
// Drive is not made this way; a refusal never repeats an option's value, and nothing is saved.
func TestRemoteRcloneNewRefusals(t *testing.T) {
	s := openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
	owner := onMainProfile(t, s)
	token := `{"access_token":"at-` + `3e7f"}`
	for _, rc := range []RcloneSettings{
		{Config: "[a]\ntype = dropbox\n", Type: "dropbox", Options: map[string]string{"token": token}},
		{Type: "drive", Options: map[string]string{"token": token}},
	} {
		add := RemoteSourceCreate{Kind: "rclone", Name: "Cloud", Rclone: &rc}
		if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, nil); w.Code != 400 || !strings.Contains(w.Body.String(), `"rclone"`) ||
			strings.Contains(w.Body.String(), "at-3e7f") {
			t.Fatalf("%+v answered %d %s", rc, w.Code, w.Body)
		}
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a refused source was saved: %+v", list.Items)
	}
}
