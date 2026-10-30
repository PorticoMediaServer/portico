package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The owner's bar: the server answers requests within one to two seconds of
// launch, whatever the library holds. A two-million-item library used to take
// fifty-five seconds, every start, because the schema was installed from scratch
// each time and nine of the installers carried statements whose cost was the
// size of the catalogue.
//
// This is the regression guard for that. It launches the real binary and waits
// for the first 200 from /v1/readiness, which is the moment the real handler
// replaces the startup reporter — the first instant a client gets an answer
// rather than an honest "starting".
const startupBudget = 2 * time.Second

var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

// serverBinary builds the server once for this package's tests.
func serverBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		// The runner (scripts/runner-test.sh) has already built this package's
		// server as the playback test helper; building it again cost each run
		// about ten seconds, paid while every process test waited.
		if helper := os.Getenv("PORTICO_PLAYBACK_TEST_HELPER"); helper != "" {
			if info, err := os.Stat(helper); err == nil && !info.IsDir() {
				builtBinary = helper
				return
			}
		}
		directory, err := os.MkdirTemp("", "portico-startup-")
		if err != nil {
			buildErr = err
			return
		}
		name := filepath.Join(directory, "portico-server")
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		build := exec.Command("go", "build", "-o", name, ".")
		build.Stderr = os.Stderr
		if err = build.Run(); err != nil {
			buildErr = err
			return
		}
		builtBinary = name
	})
	if buildErr != nil {
		t.Fatalf("build the server: %v", buildErr)
	}
	return builtBinary
}

// freePort asks the OS for a port and gives it straight back, which is the only
// way to pick one without racing another test.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// timeToFirstAnswer launches the server against state and reports how long it
// took to answer a request.
func timeToFirstAnswer(t *testing.T, state string) time.Duration {
	t.Helper()
	// Managed storage refuses a non-canonical state root, and a macOS temp
	// directory is reached through a symlink.
	if resolved, err := filepath.EvalSymlinks(state); err == nil {
		state = resolved
	}
	binary := serverBinary(t)
	port := freePort(t)
	server := exec.Command(binary)
	server.Env = append(os.Environ(),
		"PORTICO_DATA_DIR="+state, "HOME="+filepath.Dir(state),
		fmt.Sprintf("PORTICO_BIND=127.0.0.1:%d", port),
		"PORTICO_DISCOVERY=0",
	)
	log, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	server.Stdout, server.Stderr = log, log
	started := time.Now()
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		// SIGTERM is the shutdown the server is built for; a kill would leave the
		// state directory looking like a crash, which the next launch would then be
		// measured recovering from.
		if err := server.Process.Signal(os.Interrupt); err != nil {
			_ = server.Process.Kill()
		}
		done := make(chan struct{})
		go func() { _, _ = server.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
	}
	t.Cleanup(func() {
		stop()
		log.Close()
	})
	client := &http.Client{Timeout: 2 * time.Second}
	address := fmt.Sprintf("http://127.0.0.1:%d/v1/readiness", port)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(address)
		if err == nil {
			code := response.StatusCode
			response.Body.Close()
			if code == 200 {
				elapsed := time.Since(started)
				// The single-instance lock means the next launch cannot start until
				// this one has let go, which is exactly what it is for.
				stop()
				return elapsed
			}
		}
		if server.ProcessState != nil {
			contents, _ := os.ReadFile(log.Name())
			t.Fatalf("the server exited during startup:\n%s", contents)
		}
		time.Sleep(20 * time.Millisecond)
	}
	contents, _ := os.ReadFile(log.Name())
	t.Fatalf("the server never became ready:\n%s", contents)
	return 0
}

func TestFirstRunAnswersWithinTheStartupBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches the server")
	}
	testtier.Media(t, "a wall-clock startup budget for a real server process; load on a shared runner decides it")
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	elapsed := timeToFirstAnswer(t, state)
	t.Logf("first run on an empty state directory: %s", elapsed.Round(time.Millisecond))
	if elapsed > startupBudget {
		t.Fatalf("a first run took %s to answer; the budget is %s", elapsed, startupBudget)
	}
}

func TestASecondRunAnswersWithinTheStartupBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches the server")
	}
	testtier.Media(t, "a wall-clock startup budget for a real server process; load on a shared runner decides it")
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	timeToFirstAnswer(t, state)
	elapsed := timeToFirstAnswer(t, state)
	t.Logf("second run on the same state directory: %s", elapsed.Round(time.Millisecond))
	if elapsed > startupBudget {
		t.Fatalf("a second run took %s to answer; the budget is %s", elapsed, startupBudget)
	}
}

// PORTICO_STARTUP_FIXTURE_STATE names a state directory the test may start the
// server against and write to — a copy of a large library, never the original.
// Without it there is nothing large to measure, which is a skip rather than a
// pass so the absence is visible.
func TestALargeLibraryAnswersWithinTheStartupBudget(t *testing.T) {
	state := os.Getenv("PORTICO_STARTUP_FIXTURE_STATE")
	if state == "" {
		t.Skip("set PORTICO_STARTUP_FIXTURE_STATE to a writable copy of a large state directory")
	}
	if _, err := os.Stat(storagepaths.Path(state, "server.sqlite")); err != nil {
		t.Fatalf("PORTICO_STARTUP_FIXTURE_STATE has no server.sqlite: %v", err)
	}
	// The first launch may still have a schema version to record; the budget is
	// about every launch after a database is current, which is every launch in a
	// running installation's life.
	first := timeToFirstAnswer(t, state)
	elapsed := timeToFirstAnswer(t, state)
	t.Logf("large library: upgrade launch %s, steady launch %s", first.Round(time.Millisecond), elapsed.Round(time.Millisecond))
	if elapsed > startupBudget {
		t.Fatalf("a large library took %s to answer; the budget is %s", elapsed, startupBudget)
	}
}

// The container health check is the server's own binary asking its own
// readiness route, so that an image needs no curl and no credentials. It has to
// answer correctly in both directions or it is worse than no health check.
func TestHealthCheckAgreesWithReadiness(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches the server")
	}
	binary := serverBinary(t)
	port := freePort(t)
	address := fmt.Sprintf("127.0.0.1:%d", port)
	// Nothing is listening yet.
	down := exec.Command(binary, "--health-check")
	down.Env = append(os.Environ(), "PORTICO_BIND="+address)
	if err := down.Run(); err == nil {
		t.Fatal("the health check reported a server that is not running as ready")
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(state); err == nil {
		state = resolved
	}
	server := exec.Command(binary)
	server.Env = append(os.Environ(), "PORTICO_DATA_DIR="+state, "HOME="+filepath.Dir(state), "PORTICO_BIND="+address, "PORTICO_DISCOVERY=0")
	log, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server.Stdout, server.Stderr = log, log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = server.Process.Signal(os.Interrupt)
		_, _ = server.Process.Wait()
	}()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		up := exec.Command(binary, "--health-check")
		up.Env = append(os.Environ(), "PORTICO_BIND="+address)
		if up.Run() == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	contents, _ := os.ReadFile(log.Name())
	t.Fatalf("the health check never reported ready:\n%s", contents)
}

// A bind address that says "every interface" is where the server listens, not
// somewhere a client can connect to.
func TestHealthCheckResolvesAWildcardBind(t *testing.T) {
	// A port nothing is listening on, so a success would mean it reached
	// something else rather than that it resolved the address correctly.
	port := freePort(t)
	for _, bind := range []string{fmt.Sprintf("0.0.0.0:%d", port), fmt.Sprintf(":%d", port), fmt.Sprintf("[::]:%d", port)} {
		t.Setenv("PORTICO_BIND", bind)
		// Nothing is listening on that port in the test environment, so this is
		// asserting that it tried loopback rather than failing to parse.
		if code := healthCheck(); code != 1 {
			t.Fatalf("%s produced exit code %d", bind, code)
		}
	}
}
