package remote_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A crash or power cut while rclone runs leaves its configuration file, holding a sign-in
// token in plain text, in the data folder. The next start removes it, so a token does not
// outlive the source it belongs to, or the owner's removal of that source.
func TestRcloneFileLeftByACrashIsRemovedAtStart(t *testing.T) {
	dir := t.TempDir()
	d, err := db.Open(context.Background(), filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	home := filepath.Join(dir, "rclone")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// rclone's own file, and the temporary one rclone writes beside it while it saves.
	for _, name := range []string{"0123456789abcdef0123456789abcdef.conf", "0123456789abcdef0123456789abcdef.conf123456789"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("[portico]\ntoken = {\"refresh_token\":\"r9\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := remote.NewRegistry(d, dir); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(home, "*.conf*")); len(left) != 0 {
		t.Fatalf("a token file left by a crash is still there after a start: %v", left)
	}
}
