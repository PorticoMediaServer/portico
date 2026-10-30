package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/authoritygate"
)

type capturedLog struct {
	mu    sync.Mutex
	lines []string
}

func (c *capturedLog) logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}
func (c *capturedLog) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.lines
	c.lines = nil
	return out
}

type routeIdentityFixture struct {
	db      *sql.DB
	keys    *ProtectedKeys
	dir     string
	key     ed25519.PrivateKey
	gate    *authoritygate.AuthorityGate
	handler *RouteIdentityHandler
	log     *capturedLog
	clock   time.Time
}

func newRouteIdentityFixture(t *testing.T) *routeIdentityFixture {
	t.Helper()
	state := t.TempDir()
	if e := os.Chmod(state, 0700); e != nil {
		t.Fatal(e)
	}
	db := openFixtureDB(t, storagepaths.Path(state, "server.sqlite"))
	if e := installNetworkingClaimsForTest(t, context.Background(), db); e != nil {
		t.Fatal(e)
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id, e := ServerIdentity(pub)
	if e != nil {
		t.Fatal(e)
	}
	stageIdentityFixture(t, db, nil, DurableIdentity{ServerID: id, PublicKey: pub, KeyIncarnation: "key1"})
	dir := storagepaths.Path(state, "networking-keys")
	if e = privateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "key1.ed25519"), key, 0600); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	keys := &ProtectedKeys{db, root}
	t.Cleanup(func() { keys.Close() })
	gate, runner := lifecycleRunner(t)
	f := &routeIdentityFixture{db: db, keys: keys, dir: dir, key: key, gate: gate, log: &capturedLog{}, clock: time.Date(2026, 9, 23, 14, 45, 0, 0, time.UTC)}
	f.handler = NewRouteIdentityHandler(keys, runner, nil, nil)
	f.handler.failures = newFailureLog(f.log.logf)
	f.handler.failures.now = func() time.Time { return f.clock }
	return f
}

func (f *routeIdentityFixture) prove(t *testing.T) (int, string) {
	t.Helper()
	nonce := make([]byte, 32)
	if _, e := rand.Read(nonce); e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]string{"baseUrl": "http://127.0.0.1:32400", "nonce": base64.RawURLEncoding.EncodeToString(nonce)})
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:32400/v1/networking/identity-proof", bytes.NewReader(body))
	r.Host = "127.0.0.1:32400"
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// expectFailure asserts a 503 and exactly one log line containing want.
func (f *routeIdentityFixture) expectFailure(t *testing.T, want string) {
	t.Helper()
	code, body := f.prove(t)
	if code != 503 || !strings.Contains(body, "identity_unavailable") {
		t.Fatalf("status %d body %s, want 503 identity_unavailable", code, body)
	}
	lines := f.log.take()
	if len(lines) != 1 || !strings.Contains(lines[0], want) {
		t.Fatalf("log %q, want one line containing %q", lines, want)
	}
	t.Logf("logged: %s", lines[0])
	if !strings.HasPrefix(lines[0], "[network] warn: ") {
		t.Fatalf("log line %q is not a network warning", lines[0])
	}
	for _, secret := range []string{base64.RawURLEncoding.EncodeToString(f.key), base64.StdEncoding.EncodeToString(f.key), string(f.key[:8])} {
		if strings.Contains(lines[0], secret) {
			t.Fatal("log line carries key material")
		}
	}
	// A fresh class per test step: move past the window so the next failure,
	// even of the same class, logs.
	f.clock = f.clock.Add(2 * time.Minute)
}

func (f *routeIdentityFixture) expectSuccess(t *testing.T) {
	t.Helper()
	if code, body := f.prove(t); code != 200 {
		t.Fatalf("status %d body %s, want 200", code, body)
	}
	if lines := f.log.take(); len(lines) != 0 {
		t.Fatalf("success logged %q", lines)
	}
}

func TestRouteIdentityFailureClassesAreLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newRouteIdentityFixture(t)
	f.expectSuccess(t)
	path := filepath.Join(f.dir, "key1.ed25519")

	// Key file missing.
	if e := os.Rename(path, path+".moved"); e != nil {
		t.Fatal(e)
	}
	f.expectFailure(t, "step key file, key file lstat (ENOENT)")
	if e := os.Rename(path+".moved", path); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)

	// Key file readable by others: permissions warn at the server level, and
	// proving still succeeds.
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)
	if e := os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}

	// Key file with the wrong size.
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, original[:32], 0600); e != nil {
		t.Fatal(e)
	}
	f.expectFailure(t, "step key file, key file size")

	// A valid key that is not the published identity.
	_, other, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, other, 0600); e != nil {
		t.Fatal(e)
	}
	f.expectFailure(t, "step key mismatch, identity stale")
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)

	// Identity row missing.
	if _, e = f.db.Exec(`CREATE TABLE saved_identity AS SELECT * FROM networking_claim_identity; DELETE FROM networking_claim_identity`); e != nil {
		t.Fatal(e)
	}
	f.expectFailure(t, "step identity row, identity stale")
	if _, e = f.db.Exec(`INSERT INTO networking_claim_identity SELECT * FROM saved_identity; DROP TABLE saved_identity`); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)

	// Authority fenced: the gate refuses the lease before anything is read.
	f.gate.BeginQuiesce()
	f.expectFailure(t, "authority acquire")
	if e = f.gate.Activate(strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)

	// Database gone: the snapshot cannot begin.
	if e = f.db.Close(); e != nil {
		t.Fatal(e)
	}
	f.expectFailure(t, "step snapshot, database (sql: database is closed)")
}

func TestRouteIdentityFailureLogIsRateLimitedPerClass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newRouteIdentityFixture(t)
	path := filepath.Join(f.dir, "key1.ed25519")
	// Loose key permissions warn at the server level; proving still succeeds.
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if code, _ := f.prove(t); code != 200 {
		t.Fatalf("status %d, want 200", code)
	}
	if e := os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	// A missing key file fails, rate-limited to one line per class.
	if e := os.Rename(path, path+".moved"); e != nil {
		t.Fatal(e)
	}
	for range 50 {
		if code, _ := f.prove(t); code != 503 {
			t.Fatalf("status %d, want 503", code)
		}
	}
	if lines := f.log.take(); len(lines) != 1 {
		t.Fatalf("a retry loop logged %d lines, want 1: %q", len(lines), lines)
	}
	// A different class inside the same window still logs at once.
	if e := os.Rename(path+".moved", path); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, f.key[:32], 0600); e != nil {
		t.Fatal(e)
	}
	f.prove(t)
	if lines := f.log.take(); len(lines) != 1 || !strings.Contains(lines[0], "key file size") {
		t.Fatalf("a new class was not logged: %q", lines)
	}
	// After the window the first class reports again with its count.
	if e := os.Rename(path, path+".moved"); e != nil {
		t.Fatal(e)
	}
	f.clock = f.clock.Add(61 * time.Second)
	f.prove(t)
	lines := f.log.take()
	if len(lines) != 1 || !strings.Contains(lines[0], "ENOENT") || !strings.Contains(lines[0], "49 more like this since the last report") {
		t.Fatalf("window rollover logged %q", lines)
	}
}

func TestSummarizeFailureClasses(t *testing.T) {
	live := context.Background()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		request context.Context
		err     error
		want    string
	}{
		{ended, stepFailure("snapshot", context.Canceled), "canceled"},
		{live, stepFailure("identity row", context.Canceled), "canceled/lease canceled while the request was still live"},
		{live, stepFailure("snapshot", context.DeadlineExceeded), "deadline exceeded"},
		{live, &authorityError{"acquire", ErrUnavailable}, "authority acquire"},
		{live, stepFailure("commit", &authorityError{"commit", authoritygate.ErrFenced}), "authority commit"},
		{live, &authorityError{"check", ErrUnavailable}, "authority check"},
		{live, stepFailure("key file", keyFileFailure("open", &os.PathError{Op: "openat", Path: "x", Err: syscall.EMFILE})), "key file open/EMFILE"},
		{live, stepFailure("key file", keyFileFailure("content", nil)), "key file content"},
		{live, stepFailure("identity row", ErrInvalid), "identity invalid"},
		{live, errors.New("anything"), "other/*errors.errorString"},
	}
	for _, c := range cases {
		s := summarizeFailure(c.request, c.err)
		got := s.Class
		if s.Detail != "" {
			got += "/" + s.Detail
		}
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("summarize(%v) = %q, want prefix %q", c.err, got, c.want)
		}
	}
	// Every key-file failure still answers as the sentinel it replaced.
	if !errors.Is(keyFileFailure("lstat", os.ErrNotExist), ErrUnavailable) {
		t.Fatal("key file failure no longer unwraps to ErrUnavailable")
	}
	if !errors.Is(&authorityError{"commit", authoritygate.ErrFenced}, authoritygate.ErrFenced) {
		t.Fatal("authority commit failure hides the gate's error")
	}
}
