package operations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestTimeoutAlertOpensAtTwentyOneAndResolvesAfterWindow(t *testing.T) {
	m := NewMeasurements(nil, "")
	now := time.Now()
	m.Clock = func() time.Time { return now }
	transitions := make(chan bool, 4)
	m.TimeoutAlert = func(_ context.Context, active bool) error {
		transitions <- active
		return nil
	}
	defer func() {
		m.timeoutMu.Lock()
		if m.timeoutTimer != nil {
			m.timeoutTimer.Stop()
		}
		m.timeoutMu.Unlock()
	}()
	busy := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":"server_busy"}}`))
	}))
	for i := 0; i < 25; i++ {
		busy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/busy", nil))
	}
	select {
	case transition := <-transitions:
		t.Fatalf("non-timeout opened overload alert: %v", transition)
	default:
	}
	timedOut := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":"timeout"}}`))
	}))
	for i := 0; i < 20; i++ {
		timedOut.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/timed-out", nil))
	}
	select {
	case transition := <-transitions:
		t.Fatalf("alert opened before threshold: %v", transition)
	default:
	}
	timedOut.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/timed-out", nil))
	select {
	case active := <-transitions:
		if !active {
			t.Fatal("opening alert was not active")
		}
	case <-time.After(time.Second):
		t.Fatal("overload alert did not open")
	}
	now = now.Add(overloadWindow + time.Second)
	m.expireTimeouts()
	select {
	case active := <-transitions:
		if active {
			t.Fatal("expired alert remained active")
		}
	case <-time.After(time.Second):
		t.Fatal("overload alert did not resolve")
	}
}

func TestTimeoutBudgetPublishesAndResolvesOwnerAlert(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	m := NewMeasurements(db, "")
	now := time.Now()
	m.Clock = func() time.Time { return now }
	defer func() {
		m.timeoutMu.Lock()
		if m.timeoutTimer != nil {
			m.timeoutTimer.Stop()
		}
		m.timeoutMu.Unlock()
	}()
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":"timeout"}}`))
	}))
	for i := 0; i < 21; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/timeout", nil))
	}
	waitStatus := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			var state string
			if err := db.QueryRow(`SELECT status FROM console_alerts WHERE code='server_overloaded'`).Scan(&state); err == nil && state == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("server_overloaded alert did not become %s", want)
	}
	waitStatus("open")
	var notices int
	if err := db.QueryRow(`SELECT count(*) FROM console_notifications WHERE code='owner-alert'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("owner notification count=%d err=%v", notices, err)
	}
	now = now.Add(overloadWindow + time.Second)
	m.expireTimeouts()
	waitStatus("resolved")
}
