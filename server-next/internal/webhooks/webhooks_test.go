package webhooks

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
)

// The sender never reaches loopback or link-local addresses, also when they hide inside an
// IPv6 address, and does reach the home network, where owners run Home Assistant.
func TestDestinations(t *testing.T) {
	for addr, refused := range map[string]bool{
		"127.0.0.1": true, "127.8.9.1": true, "::1": true, "0.0.0.0": true, "::": true,
		"169.254.169.254": true, "fe80::1": true, "224.0.0.251": true, "255.255.255.255": true,
		"::ffff:127.0.0.1": true, "2002:7f00:1::": true, "64:ff9b::7f00:1": true, "::127.0.0.1": true,
		"2001:0:4136:e378:8000:63bf:80ff:fffe": true, // Teredo carrying 127.0.0.1
		"192.168.1.20": false, "10.0.0.5": false, "172.16.4.4": false, "100.101.102.103": false,
		"fd12::5": false, "203.0.113.9": false, "2001:db8::1": false, "2002:c0a8:114::": false,
	} {
		if got := forbidden(netip.MustParseAddr(addr)) != ""; got != refused {
			t.Errorf("%s refused = %v, want %v", addr, got, refused)
		}
	}
	for raw, ok := range map[string]bool{
		"http://192.168.1.20:8123/api/webhook/portico": true, "https://hooks.example.com/x": true,
		"http://localhost:8123/": false, "http://127.0.0.1:8123/": false, "http://[::1]/": false,
		"https://user:pass@example.com/": false, "ftp://example.com/": false, "example.com/hook": false,
		"http://169.254.169.254/latest/meta-data": false,
	} {
		if got := ValidURL(raw) == ""; got != ok {
			t.Errorf("ValidURL(%q) accepted = %v, want %v (%s)", raw, got, ok, ValidURL(raw))
		}
	}
	s := &Service{}
	s.ListensOn(32400)
	if err := s.checkDial("127.0.0.1:8123"); err == nil {
		t.Error("a dial to loopback was allowed")
	}
}

type receiver struct {
	mu       sync.Mutex
	status   int
	location string
	got      []string
	tries    []string
	secret   []byte
	bad      int
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	sig := req.Header.Get(HeaderSignature)
	ts, _, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",")
	var at int64
	json.Unmarshal([]byte(ts), &at)
	if r.secret != nil && Sign(r.secret, at, body) != sig {
		r.bad++
	}
	r.tries = append(r.tries, req.Header.Get(HeaderEvent)+"#"+req.Header.Get(HeaderAttempt))
	if r.location != "" {
		w.Header().Set("Location", r.location)
	}
	w.WriteHeader(r.status)
	if r.status < 300 {
		r.got = append(r.got, req.Header.Get(HeaderEvent))
	}
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T, d *db.DB, box *secrets.Box, c *clock) *Service {
	s := New(d, box, slog.New(slog.NewTextHandler(io.Discard, nil)), c.Now, func() (string, string) { return "srv_test", "Test" })
	s.allow = func(string) error { return nil } // the test receiver is on loopback
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.write(ctx)
	return s
}

func waiting(t *testing.T, d *db.DB, want int64) {
	t.Helper()
	for range 200 {
		hooks, err := d.Webhooks(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if hooks[0].Waiting == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the webhook never had %d messages waiting", want)
}

// Messages go out signed and in order. One that does not arrive is tried again later, also
// after a restart, and the next waits behind it; one not delivered within 8 hours is given
// up; a redirect is not followed.
func TestDelivery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	rcv := &receiver{status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(rcv)
	t.Cleanup(srv.Close)

	s := newService(t, d, box, c)
	plain, sealed := s.NewSecret("whk_one")
	rcv.secret = []byte(plain)
	if _, err := d.CreateWebhook(ctx, db.NewWebhook{PublicID: "whk_one", Name: "Home", URL: srv.URL, Secret: sealed,
		Events: []string{PlaybackStarted, PlaybackStopped}, Now: c.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Emit(PlaybackStarted, map[string]string{"title": "Heat"})
	s.Emit(PlaybackPaused, map[string]string{"title": "Heat"}) // not chosen
	s.Emit(PlaybackStopped, map[string]string{"title": "Heat"})
	waiting(t, d, 2)
	if _, err := s.sendDue(ctx); err != nil {
		t.Fatal(err)
	}
	if hooks, _ := d.Webhooks(ctx); hooks[0].LastError == "" {
		t.Error("a webhook whose message did not arrive is not shown in trouble")
	}
	// The server restarts; the receiver comes back. Nothing is due until the retry time.
	s = newService(t, d, box, c)
	rcv.status = http.StatusNoContent
	if sent, _ := s.sendDue(ctx); sent {
		t.Error("a message was tried again before its retry time")
	}
	c.Add(31 * time.Second)
	for range 3 {
		if _, err := s.sendDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"playback.started#1", "playback.started#2", "playback.stopped#1"}
	if strings.Join(rcv.tries, " ") != strings.Join(want, " ") || rcv.bad != 0 {
		t.Errorf("the receiver saw %v with %d bad signatures, want %v", rcv.tries, rcv.bad, want)
	}
	if hooks, _ := d.Webhooks(ctx); hooks[0].LastError != "" || hooks[0].Waiting != 0 {
		t.Errorf("after delivering, the webhook says %q with %d waiting", hooks[0].LastError, hooks[0].Waiting)
	}

	// A redirect is a failed try and is not followed.
	followed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	t.Cleanup(elsewhere.Close)
	rcv.status, rcv.location = http.StatusTemporaryRedirect, elsewhere.URL
	s.Emit(PlaybackStarted, map[string]string{"title": "Ronin"})
	waiting(t, d, 1)
	s.sendDue(ctx)
	if followed {
		t.Error("the sender followed a redirect")
	}
	// Eight hours later it is given up, without another try.
	tries := len(rcv.tries)
	c.Add(8*time.Hour + time.Second)
	s.sendDue(ctx)
	log, err := d.WebhookDeliveries(ctx, "whk_one")
	if err != nil {
		t.Fatal(err)
	}
	if log[0].State != "gave_up" || !strings.Contains(log[0].Error, "redirect") || len(rcv.tries) != tries {
		t.Errorf("after 8 hours the message is %s (%q) after %d more tries", log[0].State, log[0].Error, len(rcv.tries)-tries)
	}
}
