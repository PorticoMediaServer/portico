package remote_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A source's folder reaches rclone as the owner wrote it: "/srv/media" on an sftp server is
// that folder from the server's top, not "srv/media" inside the user's home folder.
func TestAnRcloneFolderKeepsItsLeadingSlash(t *testing.T) {
	fakeRcloneBinary(t)
	options, err := remote.ParseRcloneConfig("[g]\ntype = drive\nclient_id = own-client\ntoken = " + fakeToken + "\n")
	if err != nil {
		t.Fatal(err)
	}
	src := remote.NewRclone(options, "/srv/media/")
	defer src.Close()
	listed, err := src.List(ctx, "Films")
	if err != nil || len(listed) != 1 || listed[0].Name != "Ronin.mkv" {
		t.Fatalf("the folder from the top: %v %+v", err, listed)
	}
}
