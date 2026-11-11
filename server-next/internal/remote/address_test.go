package remote_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// B-44, first part: a source never reaches a link-local address, where a cloud machine keeps
// its own sign-in, whether it was typed, is what a name resolves to as it is dialed, or is
// where a redirect points; an rclone remote's plain host option is held to the same, by its
// text when it is pasted or made and by what it resolves to each time it connects. A home NAS
// is an ordinary source.
func TestALinkLocalAddressIsNeverReached(t *testing.T) {
	for _, address := range []string{"http://169.254.169.254/", "http://[fe80::1]:8080/dav/", "http://[fd00:ec2::254]/", "http://100.100.100.200/",
		"http://metadata.google.internal/computeMetadata/", "https://Metadata.Goog./", "http://instance-data/"} {
		if _, err := remote.NewWebDAV(address, "u", "p"); !errors.Is(err, remote.ErrAddressRefused) {
			t.Errorf("%s: %v", address, err)
		}
	}

	// A redirect to the metadata address is refused as it is dialed.
	nas := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/latest/meta-data/", http.StatusFound))
	defer nas.Close()
	dav, _ := remote.NewWebDAV(nas.URL+"/", "", "")
	if _, err := dav.Open(ctx, "film.mkv"); !errors.Is(err, remote.ErrAddressRefused) {
		t.Errorf("a redirect to the metadata address: %v", err)
	}

	// What a name resolves to is checked as it is dialed: here this computer's own addresses
	// stand in for link-local ones.
	served := httptest.NewServer(http.NotFoundHandler())
	defer served.Close()
	named, _ := remote.NewWebDAV(strings.Replace(served.URL, "127.0.0.1", "localhost", 1)+"/", "", "")
	remote.LoopbackIsLinkLocal(t)
	if _, err := named.List(ctx, ""); !errors.Is(err, remote.ErrAddressRefused) {
		t.Errorf("a name resolving to a link-local address: %v", err)
	}
}

func TestAnRcloneHostIsNeverLinkLocal(t *testing.T) {
	fakeRcloneBinary(t)
	for _, paste := range []string{
		"[a]\ntype = sftp\nhost = 169.254.169.254\n",
		"[a]\ntype = smb\nhost = metadata.google.internal\n",
		"[a]\ntype = ftp\nhost = [fe80::1]\n",
		"[a]\ntype = webdav\nurl = http://[fe80::1]:8080/dav\n",
		"[a]\ntype = http\nurl = http://169.254.169.254/latest/\n",
	} {
		if _, err := remote.ParseRcloneConfig(paste); !errors.Is(err, remote.ErrAddressRefused) {
			t.Errorf("%q: %v", paste, err)
		}
	}
	if _, err := remote.NewRcloneRemote(ctx, "sftp", map[string]string{"host": "169.254.169.254"}); !errors.Is(err, remote.ErrAddressRefused) {
		t.Errorf("a new remote at the metadata address: %v", err)
	}
	for _, paste := range []string{"[a]\ntype = sftp\nhost = 192.168.1.10\n", "[a]\ntype = webdav\nurl = http://nas.local:5005/\n", "[a]\ntype = smb\nhost = 100.101.102.103\n"} {
		if _, err := remote.ParseRcloneConfig(paste); err != nil {
			t.Errorf("a home address was refused: %q: %v", paste, err)
		}
	}
	// A name that resolves to a link-local address when the remote connects: rclone is not
	// started.
	remote.ResolveAs(t, map[string]string{"imds.example": "169.254.169.254"})
	options, err := remote.ParseRcloneConfig("[a]\ntype = sftp\nhost = imds.example\n")
	if err != nil {
		t.Fatal(err)
	}
	src := remote.NewRclone(options, "")
	defer src.Close()
	if _, err := src.List(ctx, ""); !errors.Is(err, remote.ErrAddressRefused) {
		t.Errorf("a host resolving to the metadata address: %v", err)
	}
}

// B-44, first part: a source on the internet cannot turn Portico's reads inward. A redirect
// from a public address to this computer or its network is refused, by its address or by
// what its name resolves to; public to public and home to home are followed as before.
func TestARedirectIsNeverFollowedInward(t *testing.T) {
	file := []byte("0123456789")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Unix(1700000000, 0), bytes.NewReader(file))
	}))
	defer target.Close()
	to := target.URL + "/film.mkv"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusFound)
	}))
	defer provider.Close()
	dav, _ := remote.NewWebDAV(provider.URL+"/", "", "")
	open := func() error {
		f, err := dav.Open(ctx, "film.mkv")
		if err == nil {
			part := make([]byte, 3)
			if _, err = f.ReadAt(part, 4); err == nil && string(part) != "456" {
				t.Errorf("read %q", part)
			}
			f.Close()
		}
		return err
	}

	// Home to home: a NAS sending its bytes from another of its own addresses.
	if err := open(); err != nil {
		t.Fatalf("home to home: %v", err)
	}
	// Here this computer's addresses play the internet.
	remote.LoopbackIsPublic(t)
	if err := open(); err != nil {
		t.Fatalf("public to public: %v", err)
	}
	remote.ResolveAs(t, map[string]string{"files.inside": "10.0.0.9"})
	for _, inward := range []string{"http://192.168.77.1:9/film.mkv", "http://10.1.2.3/film.mkv", "http://files.inside/film.mkv", "http://[fd12::5]/film.mkv"} {
		to = inward
		if err := open(); !errors.Is(err, remote.ErrRedirectInward) {
			t.Errorf("public to %s: %v", inward, err)
		}
	}
}
