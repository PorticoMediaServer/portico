package remote_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// Where rclone is not there to start, a source served by it and a new remote both say so,
// rather than that the source answered wrongly: nothing was asked of the source.
func TestRcloneThatCannotBeStartedSaysSo(t *testing.T) {
	before := remote.RcloneBinary
	missing := filepath.Join(t.TempDir(), "rclone")
	remote.RcloneBinary = func() string { return missing }
	t.Cleanup(func() { remote.RcloneBinary = before })
	src := remote.NewRclone(map[string]string{"type": "s3", "endpoint": "https://s3.example"}, "")
	defer src.Close()
	if _, err := src.List(ctx, ""); !errors.Is(err, remote.ErrRcloneMissing) {
		t.Errorf("serving a remote: %v", err)
	}
	if _, err := remote.NewRcloneRemote(ctx, "sftp", map[string]string{"host": "nas.example"}); !errors.Is(err, remote.ErrRcloneMissing) {
		t.Errorf("making a remote: %v", err)
	}
}

// rclone is never taken from PATH: it is handed a source's credentials. With no PORTICO_RCLONE
// and none beside the server, the one beside the server is named, and starting it fails.
func TestRcloneIsNeverTakenFromPath(t *testing.T) {
	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, "rclone"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PORTICO_RCLONE", "")
	if got := remote.RcloneBinary(); strings.HasPrefix(got, onPath) || filepath.Base(filepath.Dir(got)) != "rclone" {
		t.Fatalf("rclone was taken from %q", got)
	}
	src := remote.NewRclone(map[string]string{"type": "s3", "endpoint": "https://s3.example"}, "")
	defer src.Close()
	if _, err := src.List(ctx, ""); !errors.Is(err, remote.ErrRcloneMissing) {
		t.Errorf("with no rclone beside the server: %v", err)
	}
	t.Setenv("PORTICO_RCLONE", filepath.Join(onPath, "rclone"))
	if got := remote.RcloneBinary(); got != filepath.Join(onPath, "rclone") {
		t.Fatalf("PORTICO_RCLONE was not used: %q", got)
	}
}
