package strm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Every refused class is checked at the dial boundary, including numeric hosts
// and DNS answers. None may reach even a test dialer.
func TestRefusedAddressesNeverDial(t *testing.T) {
	refused := []string{
		"0.0.0.0",
		"0.1.2.3",
		"::",
		"127.0.0.1",
		"10.1.2.3",
		"172.16.1.2",
		"192.168.1.2",
		"fc00::1",
		"fec0::1",
		"100::1",
		"169.254.169.254",
		"fe80::1",
		"100.100.100.200",
		"100.64.1.2",
		"255.255.255.255",
		"224.0.0.1",
		"ff02::1",
		"::ffff:127.0.0.1",
		"::127.0.0.1",
		"::ffff:0.0.0.0",
		"::192.168.1.2",
		"64:ff9b::a00:1",
		"64:ff9b:1::a00:1",
		"2002:a00:1::1",
		"2001:0:4136:e378:8000:63bf:f5ff:fffe",
		"93.184.216.34",
	}
	originals := append([]string(nil), refused...)
	for _, raw := range originals {
		ip := netip.MustParseAddr(raw)
		if !ip.Is4() {
			continue
		}
		refused = append(refused, "::ffff:"+raw, "::"+raw)
		ipv4 := ip.As4()
		for _, prefix := range []string{"64:ff9b::", "64:ff9b:1::"} {
			encoded := netip.MustParseAddr(prefix).As16()
			copy(encoded[12:], ipv4[:])
			refused = append(refused, netip.AddrFrom16(encoded).String())
		}
		six := netip.MustParseAddr("2002::").As16()
		copy(six[2:6], ipv4[:])
		refused = append(refused, netip.AddrFrom16(six).String())
		teredo := netip.MustParseAddr("2001::").As16()
		for i, b := range ipv4 {
			teredo[12+i] = ^b
		}
		refused = append(refused, netip.AddrFrom16(teredo).String())
	}

	for _, raw := range refused {
		t.Run(raw, func(t *testing.T) {
			c := New(func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("93.184.216.34")} })
			ip := netip.MustParseAddr(raw)
			c.lookup = func(context.Context, string, string) ([]netip.Addr, error) { return []netip.Addr{ip}, nil }
			c.dial = func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("refused address was dialled")
				return nil, nil
			}
			for _, address := range []string{net.JoinHostPort(raw, "32500"), "untrusted.test:32500"} {
				if _, err := c.connect(context.Background(), "tcp", address); !errors.Is(err, ErrRefused) {
					t.Fatalf("%s: %v", address, err)
				}
			}
		})
	}
}

func TestPointersAndCheckedRanges(t *testing.T) {
	for _, text := range []string{
		"/film.mkv",
		"file:///etc/passwd",
		"ftp://host/movie",
		"rtsp://host/video",
		"data:video/mkv;base64,abc",
		"http://u:p@host/movie",
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
		case "/inward":
			http.Redirect(w, r, "http://127.0.0.1/private?token=secret", 302)
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/headers":
			w.Header().Set("X-Large", strings.Repeat("a", 65<<10))
			w.WriteHeader(206)
		case "/playlist":
			http.ServeContent(w, r, "index.m3u8", time.Time{}, strings.NewReader("#EXTM3U\nhttp://127.0.0.1/private?token=secret"))
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
	c, base := TestClient(t, server.URL)
	for _, test := range []struct {
		path string
		want error
	}{{"/inward", ErrRefused}, {"/loop", ErrRedirects}, {"/headers", ErrUnavailable}, {"/no-range", ErrRange}, {"/wrong-range", ErrRange}, {"/playlist", ErrRange}} {
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
	body, err := f.Stream(20)
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
	shortBody, err := short.Stream(20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(shortBody); err != ErrUnavailable {
		t.Fatalf("short target: %v", err)
	}
	shortBody.Close()
	short.Close()
	f.LimitProbe()
	f.budget = 1
	if _, err := f.ReadAt(data, 0); err != ErrLimit {
		t.Fatalf("probe limit: %v", err)
	}
	f.Close()
	if _, err := f.ReadAt(data, 0); err == nil {
		t.Fatal("closed source still fetches")
	}
}

func TestDNSRebindingCannotChangeADial(t *testing.T) {
	c := New(nil)
	c.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	c.dial = func(_ context.Context, _ string, address string) (net.Conn, error) {
		if address != "93.184.216.34:80" {
			t.Fatalf("unresolved/unchecked dial %q", address)
		}
		return nil, ErrUnavailable
	}
	c.connect(context.Background(), "tcp", "changes.test:80")
	c.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := c.connect(context.Background(), "tcp", "changes.test:80"); err != ErrRefused {
		t.Fatal(err)
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
	client, address := TestClient(t, outside.URL)
	file, err := client.Open(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	chunk, err := file.Stream(0)
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
	client := New(nil)
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
