package strm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A `.strm` may name the owner's own network as well as the internet (Justin, 18 Nov
// 2026): a NAS web server or a stream proxy on this machine is the usual case. Only the
// server reads it.
func TestHomeNetworkTargetsOpen(t *testing.T) {
	content := bytes.Repeat([]byte("film"), 300)
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); r.URL.Path == "/locked" && (!ok || user != "nas" || pass != "word") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(content))
	}))
	defer nas.Close()
	c := New()
	locked := strings.Replace(nas.URL, "http://", "http://nas:word@", 1) + "/locked"
	for _, address := range []string{nas.URL + "/film.mkv", locked} {
		if _, err := Parse([]byte(address)); err != nil {
			t.Fatalf("%s: pointer refused: %v", address, err)
		}
		f, err := c.Open(context.Background(), address)
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		got := make([]byte, 8)
		if _, err := f.ReadAt(got, 4); err != nil || !bytes.Equal(got, content[4:12]) {
			t.Fatalf("%s: read %q, %v", address, got, err)
		}
		f.Close()
	}
	if _, err := c.Open(context.Background(), nas.URL+"/locked"); err != ErrUnavailable {
		t.Fatalf("a refused sign-in at the NAS answered %v", err)
	}
}

func TestPointersAndCheckedRanges(t *testing.T) {
	for _, text := range []string{
		"/film.mkv",
		"file:///etc/passwd",
		"ftp://host/movie",
		"rtsp://host/video",
		"data:video/mkv;base64,abc",
		"http://host/movie#token",
		"http://host/a\nhttp://host/b",
		strings.Repeat("a", PointerLimit+1),
	} {
		if _, err := Parse([]byte(text)); err != ErrPointer {
			t.Fatalf("accepted pointer %q", text)
		}
	}
	if got, err := Parse([]byte("\ufeff https://host/movie?token=secret\r\n")); err != nil || got != "https://host/movie?token=secret" {
		t.Fatal("ordinary pointer refused")
	}
	content := bytes.Repeat([]byte("test-media"), 100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/short":
			if r.Header.Get("Range") == "bytes=20-999" {
				w.Header().Set("Content-Range", "bytes 20-999/1000")
				w.WriteHeader(206)
			} else {
				http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(content))
			}
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/headers":
			w.Header().Set("X-Large", strings.Repeat("a", 65<<10))
			w.WriteHeader(206)
		case "/playlist":
			http.ServeContent(w, r, "index.mpd", time.Time{}, strings.NewReader(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"><BaseURL>http://127.0.0.1/private?token=secret</BaseURL></MPD>`))
		case "/no-range":
			fmt.Fprint(w, "secret response body")
		case "/wrong-range":
			w.Header().Set("Content-Range", "bytes 1-1/1000")
			w.WriteHeader(206)
			fmt.Fprint(w, "x")
		default:
			w.Header().Set("Content-Type", "text/plain")
			http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(content))
		}
	}))
	defer server.Close()
	c, base := New(), server.URL
	for _, test := range []struct {
		path string
		want error
	}{{"/loop", ErrRedirects}, {"/headers", ErrUnavailable}, {"/no-range", ErrRange}, {"/wrong-range", ErrRange}, {"/playlist", ErrRange}} {
		_, err := c.Open(context.Background(), base+test.path+"?token=secret")
		if err != test.want {
			t.Errorf("%s: %v, want %v", test.path, err, test.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Fatal("error leaked a URL/body")
		}
	}
	f, err := c.Open(context.Background(), base+"/film?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data := make([]byte, 30)
	if n, err := f.ReadAt(data, 7); err != nil || n != len(data) || !bytes.Equal(data, content[7:37]) {
		t.Fatalf("range: %d %v", n, err)
	}
	body, err := f.(*File).Stream(20)
	if err != nil {
		t.Fatal(err)
	}
	streamed, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(streamed, content[20:]) {
		t.Fatal("relay changed source bytes")
	}
	short, err := c.Open(context.Background(), base+"/short")
	if err != nil {
		t.Fatal(err)
	}
	shortBody, err := short.(*File).Stream(20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(shortBody); err != ErrUnavailable {
		t.Fatalf("short target: %v", err)
	}
	shortBody.Close()
	short.Close()
	f.LimitProbe()
	f.(*File).budget = 1
	if _, err := f.ReadAt(data, 0); err != ErrLimit {
		t.Fatalf("probe limit: %v", err)
	}
	f.Close()
	if _, err := f.ReadAt(data, 0); err == nil {
		t.Fatal("closed source still fetches")
	}
}

func TestPausedSTRMReaderKeepsItsChunk(t *testing.T) {
	before := ReadTimeout
	ReadTimeout = 100 * time.Millisecond
	defer func() { ReadTimeout = before }()
	content := bytes.Repeat([]byte("video"), 1<<18)
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "video.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer outside.Close()
	client, address := New(), outside.URL
	file, err := client.Open(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	chunk, err := file.(*File).Stream(0)
	if err != nil {
		t.Fatal(err)
	}
	defer chunk.Close()
	first := make([]byte, 10)
	if _, err := io.ReadFull(chunk, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * ReadTimeout) // a pause is not a stalled read from the provider
	rest, err := io.ReadAll(chunk)
	if err != nil || !bytes.Equal(append(first, rest...), content) {
		t.Fatalf("paused stream: %v", err)
	}
}

func TestSTRMRedirectsNeverDowngrade(t *testing.T) {
	client := New()
	for _, addresses := range [][]string{
		{"https://media.test/start", "http://media.test/end"},
		{"http://media.test/start", "https://media.test/middle", "http://media.test/end"},
	} {
		var via []*http.Request
		for _, address := range addresses[:len(addresses)-1] {
			u, _ := url.Parse(address)
			via = append(via, &http.Request{URL: u})
		}
		target, _ := url.Parse(addresses[len(addresses)-1])
		if err := client.http.CheckRedirect(&http.Request{URL: target, Header: http.Header{}}, via); err != ErrRefused {
			t.Fatalf("downgrade accepted: %v", err)
		}
	}
}
