package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const readinessTestBudget = 2 * time.Second

func readinessTestMux(t *testing.T, d Dependencies) *http.ServeMux {
	t.Helper()
	d.httpContractsPrivate = true
	mux := http.NewServeMux()
	d.installationRoutes(mux)
	d.concurrencyRoutes(mux)
	return mux
}

func readinessTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "readiness.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readinessRequest(t *testing.T, mux *http.ServeMux, path string) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	start := time.Now()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	elapsed := time.Since(start)
	if elapsed > readinessTestBudget {
		t.Fatalf("%s took %s, beyond the %s readiness budget", path, elapsed, readinessTestBudget)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
	}
	return w, elapsed
}

func readinessDocument(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return document
}

func requireReadinessRefusal(t *testing.T, w *httptest.ResponseRecorder, path string) map[string]any {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%s = %d %s, want 503", path, w.Code, w.Body)
	}
	document := readinessDocument(t, w)
	if ready, ok := document["ready"].(bool); !ok || ready {
		t.Fatalf("%s body = %s, want ready false", path, w.Body)
	}
	return document
}

func requireReadinessSuccess(t *testing.T, w *httptest.ResponseRecorder, path string) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", path, w.Code, w.Body)
	}
	document := readinessDocument(t, w)
	if ready, ok := document["ready"].(bool); !ok || !ready {
		t.Fatalf("%s body = %s, want ready true", path, w.Body)
	}
	return document
}

func TestReadinessRoutesRefuseWhileBackgroundWriterHeld(t *testing.T) {
	db := readinessTestDB(t)
	mux := readinessTestMux(t, Dependencies{DB: db})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	held, err := dbwork.Begin(ctx, db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()

	health, _ := readinessRequest(t, mux, "/health/ready")
	healthBody := requireReadinessRefusal(t, health, "/health/ready")
	if len(healthBody) != 1 {
		t.Fatalf("/health/ready leaked internals: %s", health.Body)
	}

	versioned, _ := readinessRequest(t, mux, "/v1/readiness")
	versionedBody := requireReadinessRefusal(t, versioned, "/v1/readiness")
	if len(versionedBody) != 2 {
		t.Fatalf("/v1/readiness leaked internals: %s", versioned.Body)
	}
	if status, ok := versionedBody["status"].(string); !ok || status == "" || status == "ready" {
		t.Fatalf("/v1/readiness status = %v, want a non-ready status", versionedBody["status"])
	}
	if got := versioned.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("/v1/readiness Retry-After = %q, want 2", got)
	}

	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}

	health, _ = readinessRequest(t, mux, "/health/ready")
	healthBody = requireReadinessSuccess(t, health, "/health/ready")
	if len(healthBody) != 1 {
		t.Fatalf("/health/ready leaked internals: %s", health.Body)
	}

	versioned, _ = readinessRequest(t, mux, "/v1/readiness")
	versionedBody = requireReadinessSuccess(t, versioned, "/v1/readiness")
	if len(versionedBody) != 2 {
		t.Fatalf("/v1/readiness leaked internals: %s", versioned.Body)
	}
	if status, ok := versionedBody["status"].(string); !ok || status != "ready" {
		t.Fatalf("/v1/readiness status = %v, want ready", versionedBody["status"])
	}
}

func TestReadinessRoutesWithoutDatabase(t *testing.T) {
	mux := readinessTestMux(t, Dependencies{})

	health, _ := readinessRequest(t, mux, "/health/ready")
	healthBody := requireReadinessRefusal(t, health, "/health/ready")
	if len(healthBody) != 1 {
		t.Fatalf("/health/ready leaked internals: %s", health.Body)
	}

	versioned, _ := readinessRequest(t, mux, "/v1/readiness")
	versionedBody := requireReadinessRefusal(t, versioned, "/v1/readiness")
	if len(versionedBody) != 2 {
		t.Fatalf("/v1/readiness leaked internals: %s", versioned.Body)
	}
	if status, ok := versionedBody["status"].(string); !ok || status != "starting" {
		t.Fatalf("/v1/readiness status = %v, want starting", versionedBody["status"])
	}
	if got := versioned.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("/v1/readiness Retry-After = %q, want 2", got)
	}
}

func TestReadinessRoutesRefuseExhaustedReadPoolWithFreeWriter(t *testing.T) {
	db := readinessTestDB(t)
	mux := readinessTestMux(t, Dependencies{DB: db})
	var loans []*sql.Conn
	defer func() {
		for _, conn := range loans {
			conn.Close()
		}
	}()
	for range db.Stats().MaxOpenConnections {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		loans = append(loans, conn)
	}
	// The writer pool is separate and available, so testing only that path
	// would incorrectly advertise healthy API reads.
	if err := dbwork.CheckWriteReady(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/ready", "/v1/readiness"} {
		response, _ := readinessRequest(t, mux, path)
		requireReadinessRefusal(t, response, path)
	}
	for _, conn := range loans {
		conn.Close()
	}
	loans = nil
	for _, path := range []string{"/health/ready", "/v1/readiness"} {
		response, _ := readinessRequest(t, mux, path)
		requireReadinessSuccess(t, response, path)
	}
}

func TestReadinessRoutesRefuseUnavailableWriter(t *testing.T) {
	db := readinessTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	mux := readinessTestMux(t, Dependencies{DB: db})

	health, _ := readinessRequest(t, mux, "/health/ready")
	requireReadinessRefusal(t, health, "/health/ready")

	versioned, _ := readinessRequest(t, mux, "/v1/readiness")
	versionedBody := requireReadinessRefusal(t, versioned, "/v1/readiness")
	if status, ok := versionedBody["status"].(string); !ok || status != "degraded" {
		t.Fatalf("/v1/readiness status = %v, want degraded", versionedBody["status"])
	}
}

func TestReadinessStateHonorsWatchdogAndProbe(t *testing.T) {
	db := &sql.DB{}
	probeCalled := false
	failingProbe := func(context.Context) error {
		probeCalled = true
		return errors.New("writer unavailable")
	}
	okProbe := func(context.Context) error {
		probeCalled = true
		return nil
	}

	for _, test := range []struct {
		name   string
		health dbwork.HealthState
		probe  func(context.Context) error
		ready  bool
		status string
		probed bool
	}{
		{"corrupt", dbwork.CorruptState, failingProbe, false, "corrupt", false},
		{"degraded", dbwork.DegradedState, failingProbe, false, "degraded", false},
		{"probe-error", dbwork.HealthyState, failingProbe, false, "degraded", true},
		{"healthy", dbwork.HealthyState, okProbe, true, "ready", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			probeCalled = false
			result := readinessState(context.Background(), db, test.health, test.probe)
			if result.Ready != test.ready || result.Status != test.status {
				t.Fatalf("readinessState = %+v, want ready=%v status=%q", result, test.ready, test.status)
			}
			if probeCalled != test.probed {
				t.Fatalf("probe called = %v, want %v", probeCalled, test.probed)
			}
		})
	}

	probeCalled = false
	result := readinessState(context.Background(), nil, dbwork.HealthyState, failingProbe)
	if result.Ready || result.Status != "starting" || probeCalled {
		t.Fatalf("nil database = %+v called=%v, want not ready/starting without a probe", result, probeCalled)
	}

	deadlineSeen := false
	result = readinessState(context.Background(), db, dbwork.HealthyState, func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		deadlineSeen = ok && time.Until(deadline) > 0 && time.Until(deadline) <= readinessWriteProbeDeadline
		return nil
	})
	if !result.Ready || result.Status != "ready" || !deadlineSeen {
		t.Fatalf("healthy probe = %+v deadline=%v, want ready with a bounded deadline", result, deadlineSeen)
	}
}
