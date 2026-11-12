package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// When the server cannot start rclone, testing an rclone source, pasted or new, answers that
// plainly, with what to do and no path of the server's, and nothing is saved.
func TestRemoteSourceWithoutRcloneSaysSo(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	before := remote.RcloneBinary
	missing := filepath.Join(t.TempDir(), "rclone")
	remote.RcloneBinary = func() string { return missing }
	t.Cleanup(func() { remote.RcloneBinary = before })
	for _, rc := range []RcloneSettings{
		{Config: "[a]\ntype = s3\nendpoint = https://s3.example\n"},
		{Type: "sftp", Options: map[string]string{"host": "nas.example"}},
	} {
		add := RemoteSourceCreate{Kind: "rclone", Name: "Cloud", Rclone: &rc}
		if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "rclone_unavailable") ||
			strings.Contains(w.Body.String(), missing) || strings.Contains(w.Body.String(), "answered") {
			t.Errorf("%+v answered %d %s", rc, w.Code, w.Body)
		}
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a source was saved: %+v", list.Items)
	}
}
