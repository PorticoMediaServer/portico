package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// A picture's address that redirects is followed only from a provider's picture host to one
// of that provider's own hosts, over https, and never to this machine or the local network.
func TestAPictureFollowsARedirectOnlyWithinItsProvider(t *testing.T) {
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	covers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(picture.Bytes())
	}))
	defer covers.Close()
	_, port, _ := net.SplitHostPort(covers.Listener.Addr().String())
	redirectTo := func(target string) *httptest.Server {
		return httptest.NewServer(http.RedirectHandler(target, http.StatusTemporaryRedirect))
	}
	// The stand-in for the provider's picture host is at 127.0.0.1; its download host is "localhost".
	within := redirectTo("http://localhost:" + port + "/cover.png")
	defer within.Close()
	outside := redirectTo("http://[::1]:" + port + "/cover.png")
	defer outside.Close()

	saved := pictureRedirects
	defer func() { pictureRedirects, redirectsOverHTTP, redirectsToPrivate = saved, false, false }()
	pictureRedirects = map[string][]string{"127.0.0.1": {"localhost"}}
	redirectsOverHTTP, redirectsToPrivate = true, true
	ctx := context.Background()
	if data, err := fetchPicture(ctx, within.URL+"/release/1/front"); err != nil || !bytes.Equal(data, picture.Bytes()) {
		t.Fatalf("a redirect within the provider's hosts was not followed: %v", err)
	}
	if _, err := fetchPicture(ctx, outside.URL+"/release/1/front"); err == nil {
		t.Fatal("a redirect outside the provider's hosts was followed")
	}
	// A host with no redirects of its own (TMDB's) follows none.
	pictureRedirects = map[string][]string{}
	if _, err := fetchPicture(ctx, within.URL+"/release/1/front"); err == nil {
		t.Fatal("a redirect from a host without any was followed")
	}
	// Over plain http, or to this machine, it is refused even within the list.
	pictureRedirects = map[string][]string{"127.0.0.1": {"localhost"}}
	redirectsOverHTTP, redirectsToPrivate = false, true
	if _, err := fetchPicture(ctx, within.URL+"/release/1/front"); err == nil {
		t.Fatal("a redirect to plain http was followed")
	}
	redirectsOverHTTP, redirectsToPrivate = true, false
	pictureHTTP.CloseIdleConnections() // a connection made while the test allowed this machine is not reused
	if _, err := fetchPicture(ctx, within.URL+"/release/1/front"); err == nil {
		t.Fatal("a redirect to this machine was followed")
	}
	for _, c := range []struct {
		ip     string
		public bool
	}{{"127.0.0.1", false}, {"10.1.2.3", false}, {"192.168.1.1", false}, {"169.254.1.1", false}, {"::1", false}, {"fe80::1", false},
		{"fd00::1", false}, {"0.0.0.0", false}, {"100.100.1.1", false}, {"::ffff:127.0.0.1", false}, {"207.241.224.2", true}, {"2620:0:2d0:200::7", true}} {
		if got := publicAddress(net.ParseIP(c.ip)); got != c.public {
			t.Errorf("%s: public %v, want %v", c.ip, got, c.public)
		}
	}
	// The real list: Cover Art Archive to the Internet Archive's download hosts, and nothing else.
	pictureRedirects = saved
	cover, _ := url.Parse("https://coverartarchive.org/release/1/front")
	for host, want := range map[string]bool{"ia800204.us.archive.org": true, "archive.org": true, "archive.org.evil.example": false, "example.com": false} {
		next := &http.Request{URL: &url.URL{Scheme: "https", Host: host}}
		next = next.WithContext(ctx)
		redirectsToPrivate = true // no lookup of real hosts from a test
		err := checkPictureRedirect(next, []*http.Request{{URL: cover}})
		if (err == nil) != want {
			t.Errorf("coverartarchive.org to %s: %v", host, err)
		}
	}
}

// Under go test a picture is never downloaded from anything but this machine: TMDB's picture
// server and Cover Art Archive are refused before a connection is made, naming the rule.
func TestATestCannotDownloadALivePicture(t *testing.T) {
	ctx := context.Background()
	for _, address := range []string{"https://image.tmdb.org/t/p/original/a.jpg", "https://coverartarchive.org/release/1/front",
		"http://10.0.0.1/a.jpg", "http://[2606:4700::1]/a.jpg"} {
		if _, err := fetchPicture(ctx, address); !errors.Is(err, errLiveCallInTest) {
			t.Errorf("%s: %v, want refused as a live call", address, err)
		}
	}
}
