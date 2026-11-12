package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// An rclone remote whose sign-in its provider refuses is refused as a sign-in, with no folder
// set as with one, and nothing is saved (rule 10): rclone would have served it as empty.
func TestRemoteRcloneSignInRefusedIsSaid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in rclone is a shell script")
	}
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	// rclone's own answer, as the bundled rclone gave it against a WebDAV server answering 401.
	fake := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = lsjson ] && { echo 'Failed to lsjson: couldn'\"'\"'t list files: sign in: 401 Unauthorized' >&2; exit 1; }\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := remote.RcloneBinary
	remote.RcloneBinary = func() string { return fake }
	t.Cleanup(func() { remote.RcloneBinary = before })
	for _, folder := range []string{"", "Films"} {
		add := RemoteSourceCreate{Kind: "rclone", Name: "NAS", Rclone: &RcloneSettings{Config: "[a]\ntype = webdav\nurl = https://dav.example/\nuser = u\npass = p\n", Path: folder}}
		if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "source_sign_in") {
			t.Errorf("folder %q answered %d %s", folder, w.Code, w.Body)
		}
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a source with a refused sign-in was saved: %+v", list.Items)
	}
}
