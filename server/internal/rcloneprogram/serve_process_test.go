package rcloneprogram

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// This child models process custody and authenticated readiness only. It is
// deliberately not rclone or proof of provider, cache or egress behavior.
func TestServeProcessChild(t *testing.T) {
	mode := os.Getenv("PORTICO_TEST_SERVE_PROCESS")
	if mode == "" {
		return
	}
	if mode == "crash" {
		os.Exit(3)
	}
	var address, config string
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			switch arg {
			case "--addr":
				address = os.Args[i+1]
			case "--config":
				config = os.Args[i+1]
			}
		}
	}
	if _, err := os.Stat(config); err != nil {
		os.Exit(4)
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		os.Exit(5)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if mode == "unauthorized" || !ok || user != os.Getenv("RCLONE_USER") || pass != os.Getenv("RCLONE_PASS") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if mode == "stream" && r.Method == "GET" && r.URL.Path == "/file" {
			w.Header().Set("ETag", `"fixture"`)
			w.Header().Set("Content-Length", "16")
			_, _ = io.WriteString(w, "0123456789abcdef")
			return
		}
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "0" || r.URL.Path != "/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`)
	})}
	_ = server.Serve(listener)
	os.Exit(6)
}

type processLeaseFixture struct {
	life     context.Context
	released atomic.Int32
	refused  atomic.Bool
}

func (f *processLeaseFixture) Context() context.Context { return f.life }
func (f *processLeaseFixture) Check(ctx context.Context) error {
	if f.refused.Load() {
		return ErrServeStopped
	}
	return ctx.Err()
}
func (f *processLeaseFixture) Release() { f.released.Add(1) }

func serveProcessFixture(t *testing.T, mode string) (ServeCommandSpec, *processLeaseFixture, context.CancelFunc) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	lease := &processLeaseFixture{life: life}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := ServeCommandSpec{SourceID: "fixture", SourceGeneration: "1", PrivateDirectory: t.TempDir(), Configuration: []byte("[media]\ntype=local\n"), Remote: "media:", Port: port, Username: "private", Password: "test-only", Connections: 2, RequestsPerSecond: 2,
		Enforce: func(cmd *exec.Cmd) error {
			// Test-only executable substitution. Production still requires a real
			// enforcing outbound boundary; this callback provides no such proof.
			cmd.Path = executable
			cmd.Args = append([]string{executable, "-test.run=^TestServeProcessChild$", "--"}, cmd.Args[1:]...)
			cmd.Env = append(cmd.Env, "PORTICO_TEST_SERVE_PROCESS="+mode)
			return nil
		}}
	return spec, lease, cancel
}

func TestServeProcessCustodyAndRevocation(t *testing.T) {
	for _, termination := range []string{"stop", "lease", "parent"} {
		t.Run(termination, func(t *testing.T) {
			spec, lease, revoke := serveProcessFixture(t, "ready")
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			process, err := StartServe(parent, Program{Path: "/test-substituted", Included: true}, spec, lease)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Stop()
			entries, err := os.ReadDir(spec.PrivateDirectory)
			if err != nil || len(entries) != 0 {
				t.Fatal("transient config still present at publication", entries, err)
			}
			if process.Generation() == "" || lease.released.Load() != 0 {
				t.Fatal("physical slot released before Wait")
			}
			if _, err = process.Endpoint(parent); err != nil {
				t.Fatal(err)
			}
			switch termination {
			case "stop":
				process.Stop()
			case "lease":
				revoke()
			case "parent":
				cancel()
			}
			wait, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer waitCancel()
			if err = process.Wait(wait); !errors.Is(err, ErrServeStopped) {
				t.Fatal("child not reaped", err)
			}
			if lease.released.Load() != 1 {
				t.Fatal("lease release count", lease.released.Load())
			}
			if _, err = process.Endpoint(context.Background()); err == nil {
				t.Fatal("retired endpoint was published again")
			}
		})
	}
}

func TestServeProcessStartupFailureRetiresLeaseAndConfiguration(t *testing.T) {
	for _, mode := range []string{"crash", "unauthorized", "revoked", "missing_executable"} {
		t.Run(mode, func(t *testing.T) {
			spec, lease, _ := serveProcessFixture(t, mode)
			if mode == "revoked" {
				lease.refused.Store(true)
			}
			if mode == "missing_executable" {
				spec.Enforce = func(cmd *exec.Cmd) error { cmd.Path = filepath.Join(t.TempDir(), "missing"); return nil }
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			process, err := StartServe(ctx, Program{Path: "/test-substituted", Included: true}, spec, lease)
			if err == nil || process != nil || lease.released.Load() != 1 {
				t.Fatal("startup failure leaked process ownership", process, err, lease.released.Load())
			}
			entries, readErr := os.ReadDir(spec.PrivateDirectory)
			if readErr != nil || len(entries) != 0 {
				t.Fatal("startup failure retained private configuration", entries, readErr)
			}
			if strings.Contains(err.Error(), "test-only") {
				t.Fatal("credential in error")
			}
		})
	}
}

func TestServeProcessWaitRemovesRecreatedTransientFilesOnly(t *testing.T) {
	spec, lease, _ := serveProcessFixture(t, "ready")
	enforce := spec.Enforce
	var transient string
	spec.Enforce = func(command *exec.Cmd) error {
		for i, arg := range command.Args {
			if arg == "--config" && i+1 < len(command.Args) {
				transient = filepath.Dir(command.Args[i+1])
			}
		}
		return enforce(command)
	}
	process, err := StartServe(context.Background(), Program{Path: "/test-substituted", Included: true}, spec, lease)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Stop()
	if transient == "" || filepath.Dir(transient) != spec.PrivateDirectory {
		t.Fatal("fixture lost exact transient custody")
	}
	if _, err = os.Stat(transient); !os.IsNotExist(err) {
		t.Fatal("readiness retained transient files", err)
	}
	// Model a later writer recreating this command's private HOME/config path.
	// This tests cleanup custody, not any particular provider's auth refresh.
	if err = os.Mkdir(transient, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(transient, "rclone.conf"), []byte("fixture-copy"), 0600); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(spec.PrivateDirectory, "retained-source.conf")
	if err = os.WriteFile(retained, []byte("fixture-original"), 0600); err != nil {
		t.Fatal(err)
	}
	process.Stop()
	wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = process.Wait(wait); !errors.Is(err, ErrServeStopped) || lease.released.Load() != 1 {
		t.Fatal("physical retirement", err, lease.released.Load())
	}
	if _, err = os.Stat(transient); !os.IsNotExist(err) {
		t.Fatal("physical retirement forgot recreated transient files", err)
	}
	if raw, err := os.ReadFile(retained); err != nil || string(raw) != "fixture-original" {
		t.Fatal("retained source configuration changed", err)
	}
}

func TestServeProcessSharedOperationsKeepOneBudgetAndLifetime(t *testing.T) {
	// Controlled children prove shared resource/lifecycle custody only. They
	// supply no provider, owner-save, content-access or outbound policy proof.
	spec, firstLease, _ := serveProcessFixture(t, "stream")
	spec.Connections = 1
	first, err := StartServe(context.Background(), Program{Path: "/fixture", Included: true}, spec, firstLease)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Stop()
	budget, err := first.SharedOperations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other, secondLease, _ := serveProcessFixture(t, "ready")
	other.Connections, other.SharedOperations = 1, budget
	second, err := StartServe(context.Background(), Program{Path: "/fixture", Included: true}, other, secondLease)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	endpoint, err := first.Endpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := endpoint.OpenFromStart(context.Background(), "file", `"fixture"`, 16, first.Check)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	observer, err := second.ObservationEndpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitSlot, cancelSlot := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = observer.Stat(waitSlot, "", second.Check)
	cancelSlot()
	if !errors.Is(err, context.DeadlineExceeded) || first.OperationCounts().Active != 1 || second.OperationCounts() != first.OperationCounts() {
		t.Fatal("second process bypassed the source operation", err, first.OperationCounts(), second.OperationCounts())
	}
	body.Close()
	if _, err = observer.Stat(context.Background(), "", second.Check); err != nil || budget.Counts().Peak != 1 || budget.Counts().Active != 0 {
		t.Fatal("shared slot was not returned", err, budget.Counts())
	}
	first.Stop()
	wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, process := range []*ServeProcess{first, second} {
		if err = process.Wait(wait); !errors.Is(err, ErrServeStopped) {
			t.Fatal("shared resource revocation left a physical child", err)
		}
	}
	if firstLease.released.Load() != 1 || secondLease.released.Load() != 1 {
		t.Fatal("shared budget confused physical lease release", firstLease.released.Load(), secondLease.released.Load())
	}
}

func TestServeProcessSharedBudgetRejectsUnknownCapacityAndExpiredLifetime(t *testing.T) {
	for _, expired := range []bool{false, true} {
		spec, lease, _ := serveProcessFixture(t, "ready")
		life, cancel := context.WithCancel(context.Background())
		defer cancel()
		spec.SharedOperations = remotemedia.NewOperationBudget(life, 1)
		if expired {
			spec.Connections = 1
			cancel()
		}
		called := false
		spec.Enforce = func(*exec.Cmd) error { called = true; return nil }
		if process, err := StartServe(context.Background(), Program{Path: "/unused", Included: true}, spec, lease); !errors.Is(err, ErrServeConfiguration) || process != nil || called || lease.released.Load() != 1 {
			t.Fatal("invalid shared resource reached a child boundary", process, err, called, lease.released.Load())
		}
		entries, err := os.ReadDir(spec.PrivateDirectory)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid shared resource created private configuration", entries, err)
		}
	}
}
