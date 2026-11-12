package remote_test

import (
	"errors"
	"path/filepath"
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
