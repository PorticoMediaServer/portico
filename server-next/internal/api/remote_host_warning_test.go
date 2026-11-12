package api

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// An sftp remote with no host_keys is accepted, and the source warns the owner that the
// server's identity is not checked, on the source and in the answer to saving it. One with
// host_keys, and every other kind, carries no warning.
func TestRemoteSftpWithoutAHostKeyIsWarnedAbout(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct {
		paste string
		warn  bool
	}{
		{"[a]\ntype = sftp\nhost = nas.local\nuser = me\n", true},
		{"[a]\ntype = sftp\nhost = nas.local\nuser = me\nhost_keys = ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample\n", false},
		{"[a]\ntype = s3\nendpoint = https://s3.example\n", false},
	} {
		c, secret := remote.Config{}, remote.Secret{}
		if err := remoteSettings("rclone", &c, &secret, nil, nil, nil, &RcloneSettings{Config: tc.paste}); err != nil {
			t.Fatalf("%q was refused: %v", tc.paste, err)
		}
		config, _ := json.Marshal(c)
		out := s.remoteSourceOut(db.RemoteSource{PublicID: "src_x", Kind: "rclone", Config: string(config)})
		if (out.Warning != "") != tc.warn || tc.warn && out.Warning != "This server's identity is not checked. Add its host key to be sure you are connecting to the right server." {
			t.Errorf("%q: warning %q", tc.paste, out.Warning)
		}
	}
}
