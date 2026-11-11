package remote_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A token rclone is given in place of the pasted one is kept sealed with the source, so the
// next start signs in with it (a provider that replaces its refresh token would otherwise need
// the remote pasted again after every restart). rclone's own file with it is gone once rclone
// stops. A key file left empty by a crash is made again.
func TestRcloneTokenOutlivesARestart(t *testing.T) {
	fakeRcloneBinary(t)
	before := remote.RcloneTokenCheck
	remote.RcloneTokenCheck = 20 * time.Millisecond
	t.Cleanup(func() { remote.RcloneTokenCheck = before })
	dir := t.TempDir()
	d, err := db.Open(context.Background(), filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := os.WriteFile(filepath.Join(dir, "remote-sources.key"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := remote.NewRegistry(d, dir)
	if err != nil {
		t.Fatalf("an empty key file: %v", err)
	}
	pasted := "[mine]\ntype = drive\ntoken = " + fakeToken + "\n"
	sealed, err := registry.Seal("src_one", remote.Secret{Rclone: pasted})
	if err != nil {
		t.Fatal(err)
	}
	row, err := d.CreateRemoteSource(ctx, "src_one", db.NewRemoteSource{UserID: 1, Kind: "rclone", Name: "Drive", Config: `{"path":"Media"}`, Secret: sealed, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	storage, _, err := registry.Open(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.List(ctx, "Films"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		secret, err := registry.Secret(ctx, row.ID)
		if err == nil && strings.Contains(secret.Rclone, "token = "+refreshedToken) && strings.Count(secret.Rclone, "token =") == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new token was not kept: %v %q", err, secret.Rclone)
		}
		time.Sleep(20 * time.Millisecond)
	}
	registry.Close()
	if left, _ := filepath.Glob(filepath.Join(dir, "rclone", "*.conf")); len(left) != 0 {
		t.Fatalf("rclone's file with the token is still there: %v", left)
	}

}
