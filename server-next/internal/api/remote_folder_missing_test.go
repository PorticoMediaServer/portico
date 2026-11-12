package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A mistyped folder on an rclone source is refused on the folder field, naming the folder,
// and nothing is saved: rclone would have served it as an empty folder.
func TestRemoteRcloneFolderThatIsNotThereIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in rclone is a shell script")
	}
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	// rclone's own answer for a folder that is not there: "directory not found", exit code 3.
	fake := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = lsjson ] && { echo 'Failed to lsjson: directory not found' >&2; exit 3; }\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := remote.RcloneBinary
	remote.RcloneBinary = func() string { return fake }
	t.Cleanup(func() { remote.RcloneBinary = before })
	add := RemoteSourceCreate{Kind: "rclone", Name: "Bucket", Rclone: &RcloneSettings{Config: "[a]\ntype = s3\nendpoint = https://s3.example\n", Path: "Films/Meda"}}
	var refused errorBody
	if w := call(t, s, "POST", "/v1/admin/remote-sources", owner.AccessToken, add, &refused); w.Code != 400 || len(refused.Error.Fields) != 1 ||
		refused.Error.Fields[0].Path != "rclone.path" || !strings.Contains(refused.Error.Fields[0].Message, `"Films/Meda"`) {
		t.Fatalf("a mistyped folder answered %d %s", w.Code, w.Body)
	}
	var list Page[RemoteSource]
	if call(t, s, "GET", "/v1/admin/remote-sources", owner.AccessToken, nil, &list); len(list.Items) != 0 {
		t.Fatalf("a source with a mistyped folder was saved: %+v", list.Items)
	}
}
