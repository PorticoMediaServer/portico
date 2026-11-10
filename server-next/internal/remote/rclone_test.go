package remote_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

const fakeToken = "tok-" + "4f1c9e"

// TestMain lets the test binary stand in for rclone: "serve webdav portico:<root>" serves
// a few files over WebDAV the way rclone would, with what a hostile listing could hold.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "serve" && os.Args[2] == "webdav" {
		os.Exit(fakeRclone(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeRclone(args []string) int {
	// The remote's options come in the environment, never on the command line.
	for _, arg := range args {
		if strings.Contains(arg, fakeToken) {
			fmt.Fprintln(os.Stderr, "a secret was on the command line")
			return 2
		}
	}
	if os.Getenv("RCLONE_CONFIG_PORTICO_TYPE") != "drive" || os.Getenv("RCLONE_CONFIG_PORTICO_TOKEN") != fakeToken {
		fmt.Fprintln(os.Stderr, "Failed to create file system: didn't find section in config file")
		return 1
	}
	root := strings.TrimPrefix(args[2], "portico:")
	files := map[string]string{}
	for name, body := range map[string]string{"Media/Films/Heat.mkv": "0123456789", "Other/x.mkv": "x"} {
		if inside, ok := strings.CutPrefix(name, root+"/"); ok {
			files[inside] = body
		}
	}
	dav := remotetest.NewHandler(files, "")
	dav.User, dav.Password = os.Getenv("RCLONE_USER"), os.Getenv("RCLONE_PASS")
	dav.BadNames = []string{"..", ".", "back%5Cslash.mkv", "slash%2Fname.mkv"}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1
	}
	fmt.Fprintf(os.Stderr, "2026/11/10 12:00:00 NOTICE: WebDAV Server started on [http://%s/]\n", listener.Addr())
	http.Serve(listener, dav)
	return 0
}

func fakeRcloneBinary(t *testing.T) {
	before := remote.RcloneBinary
	remote.RcloneBinary = func() string { return os.Args[0] }
	t.Cleanup(func() { remote.RcloneBinary = before })
}

// An rclone remote, pasted from rclone's configuration, is served by rclone on a loopback
// port and read like WebDAV: a folder within the remote is listed, names that could point
// elsewhere are dropped, a file is read by range, and Close stops rclone. A configuration
// rclone refuses fails to start.
func TestRclone(t *testing.T) {
	fakeRcloneBinary(t)
	options, err := remote.ParseRcloneConfig("# pasted\n[gdrive]\ntype = drive\ntoken = " + fakeToken + "\n")
	if err != nil {
		t.Fatal(err)
	}
	src := remote.NewRclone(options, "Media")
	listed, err := src.List(ctx, "Films")
	if err != nil || len(listed) != 1 || listed[0].Name != "Heat.mkv" || listed[0].Size != 10 {
		t.Fatalf("listing: %v %+v", err, listed)
	}
	file, err := src.Open(ctx, "Films/Heat.mkv")
	if err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 3)
	if _, err := file.ReadAt(part, 4); err != nil || string(part) != "456" {
		t.Fatalf("read: %v %q", err, part)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(part, 0); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("rclone still serves after Close: %v", err)
	}
	// Stopped, it starts again when asked.
	if _, err := src.List(ctx, ""); err != nil {
		t.Fatal(err)
	}
	src.Close()

	if _, err := remote.ParseRcloneConfig("[a]\ntype = drive\n[b]\ntype = s3\n"); err == nil {
		t.Fatal("two remotes were taken")
	}
	if _, err := remote.ParseRcloneConfig("[a]\ntoken " + fakeToken + "\n"); err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("a bad line: %v", err)
	}
	wrong, _ := remote.ParseRcloneConfig("[a]\ntype = drive\ntoken = other\n")
	if _, err := remote.NewRclone(wrong, "").List(ctx, ""); !errors.Is(err, remote.ErrNeedsAttention) {
		t.Fatalf("a remote rclone refused: %v", err)
	}
}
