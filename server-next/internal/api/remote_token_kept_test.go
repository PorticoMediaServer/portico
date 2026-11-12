package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A change to a saved source whose test fails (a mistyped folder) is not saved, but a token the
// provider gave during that test is kept with the source's old settings: with refresh tokens
// that work once, the stored one is already retired.
func TestRemoteChangeThatFailsKeepsTheNewestToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in rclone is a shell script")
	}
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	ctx := context.Background()
	// rclone's answers for a folder that is not there, after the provider replaced the token:
	// the new token written where rclone writes it, then "directory not found".
	fake := filepath.Join(t.TempDir(), "rclone")
	script := "#!/bin/sh\nif [ \"$1\" = lsjson ]; then printf '[portico]\\ntype = s3\\ntoken = tok-new\\n' > \"$RCLONE_CONFIG\"; " +
		"echo 'Failed to lsjson: directory not found' >&2; exit 3; fi\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	before := remote.RcloneBinary
	remote.RcloneBinary = func() string { return fake }
	t.Cleanup(func() { remote.RcloneBinary = before })
	config, _ := json.Marshal(remote.Config{RcloneType: "s3", Path: "Films", SecretSet: true})
	sealed, err := s.remote.Seal("src_tok", remote.Secret{Rclone: "[a]\ntype = s3\nendpoint = https://s3.example\ntoken = tok-old\n"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.db.CreateRemoteSource(ctx, "src_tok", db.NewRemoteSource{UserID: 1, Kind: "rclone", Name: "Bucket", Config: string(config), Secret: sealed, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, s, "PATCH", "/v1/admin/remote-sources/"+row.PublicID, owner.AccessToken, RemoteSourceEdit{Rclone: &RcloneSettings{Path: "Flims"}}, nil); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "rclone.path") {
		t.Fatalf("a mistyped folder answered %d %s", w.Code, w.Body)
	}
	secret, err := s.remote.Secret(ctx, row.ID)
	if err != nil || !strings.Contains(secret.Rclone, "token = tok-new") || strings.Contains(secret.Rclone, "tok-old") {
		t.Fatalf("the stored secret: %v %q", err, secret.Rclone)
	}
	var shown RemoteSource
	if call(t, s, "GET", "/v1/admin/remote-sources/"+row.PublicID, owner.AccessToken, nil, &shown); shown.Rclone == nil || shown.Rclone.Path != "Films" {
		t.Fatalf("the change was saved: %+v", shown.Rclone)
	}
}
