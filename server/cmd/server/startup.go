package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// A supervisor's health check, a browser that was already open, and a phone
// reconnecting on a LAN all reach the server before it has finished opening its
// database. Answering them with a connection refusal tells them nothing;
// answering with an honest 503 and a phase report tells them exactly what to
// wait for. That is why the listener is bound before the database is opened and
// the real handler is swapped in only when the server can actually serve.

// startupPhase is one named step of bringing the server up.
type startupPhase struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Started  int64  `json:"startedUnixMillis"`
	Finished int64  `json:"finishedUnixMillis,omitempty"`
	Failed   string `json:"error,omitempty"`
}

// startupReporter answers every request while the server is coming up.
type startupReporter struct {
	mu      sync.Mutex
	phases  []startupPhase
	failure *startupRecovery
}

func newStartupReporter() *startupReporter { return &startupReporter{} }

func (s *startupReporter) start(name, label string) {
	s.mu.Lock()
	s.phases = append(s.phases, startupPhase{Name: name, Label: label, Started: time.Now().UnixMilli()})
	s.mu.Unlock()
	startupPhaseHook(name)
}

func (s *startupReporter) finish(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.phases {
		if s.phases[index].Name == name && s.phases[index].Finished == 0 {
			s.phases[index].Finished = time.Now().UnixMilli()
			if err != nil {
				s.phases[index].Failed = err.Error()
			}
			return
		}
	}
}

// step opens a phase and returns the function that closes it, so a linear
// startup can be reported and timed without turning every call into a block:
//
//	done := reporter.step("identity", "Load the server identity")
//	ident, e := identity.New(db, state)
//	done(e)
//
// With PORTICO_STARTUP_TRACE=1 each phase logs what it cost, which is how the
// library-sized work between exec and the first served request was found.
func (s *startupReporter) step(name, label string) func(error) {
	s.start(name, label)
	started := time.Now()
	return func(err error) {
		s.finish(name, err)
		if os.Getenv("PORTICO_STARTUP_TRACE") == "1" {
			log.Printf("startup phase %-24s %s", name, time.Since(started).Round(time.Millisecond))
		}
	}
}

func (s *startupReporter) snapshot() []startupPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]startupPhase, len(s.phases))
	copy(out, s.phases)
	return out
}

func (s *startupReporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	failure := s.failure
	s.mu.Unlock()
	if failure != nil {
		failure.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/v1/readiness" || r.URL.Path == "/health/ready" {
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": false, "status": "starting"})
		return
	}
	w.WriteHeader(503)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "starting", "message": "The server is still starting. Retry shortly.", "retryable": true}})
}

// switchableHandler lets the listener serve the startup reporter first and the
// real handler afterwards, without ever closing and reopening the socket.
type switchableHandler struct {
	current atomic.Pointer[http.Handler]
}

// serverReadyHook is replaced only by the isolated idle-budget test process.
// It marks the moment the fully constructed router has replaced the startup
// reporter, before any post-ready maintenance can be mistaken for startup.
var serverReadyHook = func() {}
var deferredMaintenanceDoneHook = func() {}

// startupPhaseHook is replaced only by the isolated startup-serving test
// process, which holds startup at a phase to ask the socket what it answers.
var startupPhaseHook = func(string) {}

func newSwitchableHandler(initial http.Handler) *switchableHandler {
	s := &switchableHandler{}
	s.set(initial)
	return s
}

func (s *switchableHandler) set(h http.Handler) { s.current.Store(&h) }

func (s *switchableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.current.Load()).ServeHTTP(w, r)
}

// runDeferredMaintenance does the database housekeeping that used to be
// tempting to run at startup: planner statistics and a WAL checkpoint. It runs after the listener is live and only
// once foreground work has gone quiet, so a viewer who opened the app the moment
// the server came up is never behind it.
//
// The write gate is never held across work the size of the library. Planner
// statistics used to be `PRAGMA optimize` under the gate: on a table with no
// sqlite_stat1 rows that counts the whole b-tree inside one write transaction,
// and on a million-song library every sign-in queued behind it for eight
// minutes. persistence.RefreshPlannerStatistics does the counting and sampling
// on a background read connection and holds the gate only to write each
// table's few statistics rows.
func runDeferredMaintenance(ctx context.Context, db *sql.DB) {
	defer deferredMaintenanceDoneHook()
	if db == nil || !dbwork.Yield(ctx) {
		return
	}
	if err := persistence.RefreshPlannerStatistics(ctx, db); err != nil && ctx.Err() == nil {
		log.Printf("Planner statistics refresh stopped early and will resume on the next start: %v", err)
	}
	if !dbwork.Yield(ctx) {
		return
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
}

// Recovery never opens the database, writes state, or offers an unauthenticated
// repair button. Public replies contain an actionable category, not log errors
// that may include media paths, URLs or credentials. The lock-specific
// diagnostic intentionally includes the state-folder path and locking error.
type startupRecovery struct{ Title, Phase, Advice, Recovery, AdviceID string }

var recoveryPage = template.Must(template.New("recovery").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><body><main><h1>{{.Title}}</h1><p>{{.Phase}}</p><p>{{.Advice}}</p><p>{{.Recovery}}</p></main></body></html>`))

func (s *startupReporter) fail(err error) {
	phase := startupCopy["server.startup.phase"]
	for _, p := range s.snapshot() {
		if p.Failed != "" || p.Finished == 0 {
			phase = p.Label
			break
		}
	}
	adviceID := "server.startup.storage"
	var lockFailure *instanceLockFailure
	text := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &lockFailure):
		adviceID = "server.startup.locking"
	case strings.Contains(text, "earlier pre-release"), strings.Contains(text, "migration"), strings.Contains(text, "schema"):
		adviceID = "server.startup.version"
	case strings.Contains(text, "permission"), strings.Contains(text, "access denied"):
		adviceID = "server.startup.permissions"
	case strings.Contains(text, "space"), strings.Contains(text, "disk full"):
		adviceID = "server.startup.space"
	case strings.Contains(text, "another portico"), strings.Contains(text, "already using"):
		adviceID = "server.startup.instance"
	}
	s.mu.Lock()
	advice := startupCopy[adviceID]
	if lockFailure != nil {
		advice = lockFailure.Error()
	}
	s.failure = &startupRecovery{Title: startupCopy["server.startup.failed"], Phase: phase, Advice: advice, Recovery: startupCopy["server.startup.recovery"], AdviceID: adviceID}
	s.mu.Unlock()
}
func (s *startupRecovery) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if r.URL.Path == "/" && strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = recoveryPage.Execute(w, s)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"ready": false, "status": "startup_failed", "phase": s.Phase, "message": s.Advice, "messageId": s.AdviceID, "retryable": false})
}
func serveStartupRecovery(ctx context.Context, listener net.Listener, reporter *startupReporter) {
	server := &http.Server{Handler: reporter, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan struct{})
	supervise.Go("startup.recovery", func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	})
	_ = server.Serve(capConnections(listener, consoleConnectionReserve))
	close(done)
}

// Errors before a state directory or listener is available have no reporter in
// run. Bind the configured port independently, without touching the bad state.
func serveEarlyStartupFailure(err error) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if os.Getenv(supervise.WorkerEnvironment) == "1" {
		supervise.WatchParent(cancel, os.Stdin)
	}
	listener, listenErr := net.Listen("tcp", env("PORTICO_BIND", defaultBind))
	if listenErr != nil {
		log.Printf("The recovery page could not bind its port: %v", listenErr)
		return
	}
	defer listener.Close()
	reporter := newStartupReporter()
	reporter.fail(err)
	serveStartupRecovery(ctx, listener, reporter)
}
