package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Hold native foreground admission through the public helper. Discover its
// capacity from the first queued probe, rather than changing a limit or adding
// a shipped test hook. The read pool retains its production reserve throughout.
func holdCooloffSnapshotAdmission(t *testing.T, db *sql.DB) (int, func()) {
	t.Helper()
	if dbwork.Snapshots().Waiting != 0 {
		t.Fatal("fixture starts with snapshot waiters")
	}
	parent, cancel := context.WithCancel(context.Background())
	released := make(chan struct{})
	var workers sync.WaitGroup
	var once sync.Once
	release := func() { once.Do(func() { close(released); workers.Wait(); cancel() }) }
	t.Cleanup(release)
	for count := 0; count <= dbwork.Snapshots().Capacity; count++ {
		ctx, stop := context.WithCancel(parent)
		entered := make(chan struct{})
		done := make(chan error, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer stop()
			done <- dbwork.WithReadSnapshot(ctx, db, func(context.Context) error { close(entered); <-released; return nil })
		}()
		timer := time.NewTimer(5 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		acquired := false
		for !acquired {
			select {
			case <-entered:
				acquired = true
			case err := <-done:
				tick.Stop()
				timer.Stop()
				t.Fatalf("barrier snapshot: %v", err)
			case <-timer.C:
				tick.Stop()
				t.Fatal("snapshot probe never reached admission")
			case <-tick.C:
				if dbwork.Snapshots().Waiting > 0 {
					stop()
					select {
					case err := <-done:
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					case <-timer.C:
						t.Fatal("queued barrier probe ignored cancellation")
					}
					tick.Stop()
					timer.Stop()
					if count < 1 || dbwork.Snapshots().Waiting != 0 {
						t.Fatal("barrier did not own the foreground permits")
					}
					return count, release
				}
			}
		}
		tick.Stop()
		timer.Stop()
	}
	t.Fatal("foreground snapshot probe never queued")
	return 0, release
}

func TestLockedNamesShareSnapshotAdmissionBehavior(t *testing.T) {
	d, _ := completedLocalFoundationFixture(t)
	const peer = "192.0.2.77:443"
	ctx := identity.WithSignInSource(context.Background(), peer)
	for _, name := range []string{"owner", "unlisted"} {
		for range 5 {
			_, _ = d.Identity.DirectLogin(ctx, name, "wrong")
		}
	}
	until := time.Now().Unix() + 60
	for _, table := range []string{"identity_credential_attempts", "identity_unknown_attempts"} {
		if _, err := d.DB.Exec("UPDATE "+table+" SET locked_until=?", until); err != nil {
			t.Fatal(err)
		}
	}
	// Opening cold policy-aware connections must not consume the cache witness's
	// lifetime. Warm the existing pool, with no pool/admission setting changes.
	pool := dbwork.ReadHandle(context.Background(), d.DB)
	var conns []*sql.Conn
	closeConns := func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
		conns = nil
	}
	defer closeConns()
	warm, cancelWarm := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWarm()
	for range pool.Stats().MaxOpenConnections {
		conn, err := pool.Conn(warm)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	closeConns()
	h := New(d)
	request := func(name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/direct/sign-in", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"wrong"}`, name)))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	observe := func(name string) bool {
		// Warm the common policy cache before holding admission. A cold shared read
		// would hide a later username-dependent bypass.
		if w := request("owner"); w.Code != 429 {
			t.Fatalf("warm refusal: %d %s", w.Code, w.Body)
		}
		count, release := holdCooloffSnapshotAdmission(t, d.DB)
		// Non-snapshot reads remain possible; SQL pool exhaustion is not the witness.
		probe, cancelProbe := context.WithTimeout(context.Background(), time.Second)
		var one int
		err := dbwork.QueryRow(probe, d.DB, `SELECT 1`).Scan(&one)
		cancelProbe()
		if err != nil || one != 1 {
			release()
			t.Fatalf("barrier exhausted ordinary SQL reads: %d %v", one, err)
		}
		response := make(chan *httptest.ResponseRecorder, 1)
		var workers sync.WaitGroup
		workers.Add(1)
		go func() { defer workers.Done(); response <- request(name) }()
		defer func() { release(); workers.Wait() }()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		queued := false
		var result *httptest.ResponseRecorder
	waiting:
		for {
			select {
			case result = <-response:
				break waiting
			case <-timer.C:
				t.Fatal("sign-in never reached the admission witness")
			case <-tick.C:
				if dbwork.Snapshots().Waiting > 0 {
					queued = true
					break waiting
				}
			}
		}
		t.Logf("name=%s queued=%t with %d production foreground permits held", name, queued, count)
		release()
		workers.Wait()
		if result == nil {
			result = <-response
		}
		if result.Code != 429 {
			t.Fatalf("%s: %d %s", name, result.Code, result.Body)
		}
		return queued
	}
	known, unknown := observe("owner"), observe("unlisted")
	if !known || !unknown {
		t.Fatalf("both names must queue identically: known=%t unknown=%t", known, unknown)
	}
}
