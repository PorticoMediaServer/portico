package remote_test

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// A source's folder that is not on the remote, or is a file, is refused before rclone serves
// it: rclone would serve it as an empty folder, and a mistyped folder would pass its test and
// show an empty browser. A folder that is there, and the remote's top, are served.
func TestAnRcloneFolderThatIsNotThereIsRefused(t *testing.T) {
	fakeRcloneBinary(t)
	options, err := remote.ParseRcloneConfig("[g]\ntype = drive\nclient_id = own-client\ntoken = " + fakeToken + "\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"Meda", "Media/Films/Heat.mkv", "/srv/nothing"} {
		src := remote.NewRclone(options, folder)
		if _, err := src.List(ctx, ""); !errors.Is(err, remote.ErrFolderMissing) {
			t.Errorf("%q: %v", folder, err)
		}
		src.Close()
	}
	for _, folder := range []string{"Media", "/srv/media", ""} {
		src := remote.NewRclone(options, folder)
		if _, err := src.List(ctx, ""); err != nil {
			t.Errorf("%q: %v", folder, err)
		}
		src.Close()
	}
	secret := remote.Secret{Rclone: "[g]\ntype = drive\nclient_id = own-client\ntoken = " + fakeToken + "\n"}
	if err := remote.Test(ctx, "rclone", remote.Config{Path: "Meda"}, secret); !errors.Is(err, remote.ErrFolderMissing) {
		t.Errorf("Test connection with a mistyped folder: %v", err)
	}
}
