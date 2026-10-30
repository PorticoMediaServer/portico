//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Shutdown used to be a chain of defers, five of which could block forever. A
// SIGTERM to a server whose scanner was inside a hung ffprobe produced a process
// that never exited; whatever supervisor was managing it then escalated to
// SIGKILL on its own timeout, and the generated-media cleanup, the write-ahead-log
// checkpoint and db.Close never ran at all.
//
// The only honest test of that is the real binary and a real signal. This starts
// it, waits until it is serving, sends SIGTERM under a little load, and requires
// it to be gone inside the drain budget with nothing left behind.
func TestSIGTERMDrainsInsideItsBudgetAndLeavesNoChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("launches the server")
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("process-tree shutdown test requires pgrep")
	}
	state := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(state); err == nil {
		state = resolved
	}
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	binary := serverBinary(t)
	port := freePort(t)
	server := exec.Command(binary)
	server.Env = append(os.Environ(),
		"PORTICO_DATA_DIR="+state, "HOME="+filepath.Dir(state),
		fmt.Sprintf("PORTICO_BIND=127.0.0.1:%d", port),
		"PORTICO_DISCOVERY=0",
	)
	log, err := os.Create(filepath.Join(t.TempDir(), "shutdown.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server.Stdout, server.Stderr = log, log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = server.Wait()
		close(waited)
	}()
	killed := false
	defer func() {
		if !killed {
			_ = server.Process.Kill()
			<-waited
		}
	}()

	client := &http.Client{Timeout: 3 * time.Second}
	address := fmt.Sprintf("http://127.0.0.1:%d/v1/readiness", port)
	ready := false
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(address)
		if err == nil {
			code := response.StatusCode
			response.Body.Close()
			if code == 200 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		contents, _ := os.ReadFile(log.Name())
		t.Fatalf("the server never became ready:\n%s", contents)
	}

	// A little traffic in flight, so the drain has something to drain.
	var traffic sync.WaitGroup
	stopTraffic := make(chan struct{})
	var stopped sync.Once
	stop := func() { stopped.Do(func() { close(stopTraffic) }); traffic.Wait() }
	defer stop()
	for worker := 0; worker < 8; worker++ {
		traffic.Add(1)
		go func() {
			defer traffic.Done()
			for {
				select {
				case <-stopTraffic:
					return
				default:
				}
				if response, err := client.Get(address); err == nil {
					response.Body.Close()
				}
			}
		}()
	}
	time.Sleep(500 * time.Millisecond)

	children := serverChildren(t, server.Process.Pid)
	t.Logf("children before shutdown: %d", len(children))

	started := time.Now()
	if err = server.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
		killed = true
		if waitErr != nil {
			t.Fatal("server did not exit cleanly after SIGTERM", waitErr)
		}
	case <-time.After(drainBudget + 10*time.Second):
		contents, _ := os.ReadFile(log.Name())
		t.Fatalf("the server did not exit within %s of SIGTERM:\n%s", drainBudget+10*time.Second, contents)
	}
	elapsed := time.Since(started)
	stop()
	t.Logf("SIGTERM to exit: %s (drain budget %s)", elapsed.Round(time.Millisecond), drainBudget)
	if elapsed > drainBudget+5*time.Second {
		t.Errorf("shutdown took %s; the barrier's budget is %s", elapsed, drainBudget)
	}

	// No snapshotted child or grandchild may outlive the server. Windows uses
	// job-object tests; this file exercises Unix SIGTERM and signal-zero probes.
	for _, pid := range children {
		if processAlive(t, pid) {
			t.Errorf("child process %d outlived the server", pid)
		}
	}
	// And it must have shut down in an orderly way, which means the log is free of
	// the barrier's own complaint.
	contents, _ := os.ReadFile(log.Name())
	if strings.Contains(string(contents), "Shutdown proceeded after") {
		t.Errorf("the drain barrier reported loops that did not finish:\n%s", contents)
	}
}

// Snapshot the whole descendant tree, not just the direct helper processes.
func serverChildren(t *testing.T, pid int) []int {
	t.Helper()
	var children []int
	pending := []int{pid}
	seen := map[int]bool{pid: true}
	for len(pending) > 0 {
		parent := pending[0]
		pending = pending[1:]
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(parent)).Output()
		cancel()
		if err != nil {
			var exited *exec.ExitError
			if errors.As(err, &exited) && exited.ExitCode() == 1 {
				continue // pgrep's documented no-matches result.
			}
			t.Fatalf("enumerating descendants of %d: %v", parent, err)
		}
		for _, line := range strings.Fields(string(out)) {
			value, err := strconv.Atoi(line)
			if err != nil || value <= 0 {
				t.Fatalf("invalid child PID %q: %v", line, err)
			}
			if !seen[value] {
				seen[value] = true
				children = append(children, value)
				pending = append(pending, value)
			}
		}
	}
	return children
}

func processAlive(t *testing.T, pid int) bool {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal("finding a Unix process", err)
	}
	defer process.Release()
	// Signal 0 asks the kernel whether the process exists without touching it.
	err = process.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false
	}
	t.Fatalf("probing process %d: %v", pid, err)
	return false
}

func TestShutdownProcessLivenessOracle(t *testing.T) {
	if !processAlive(t, os.Getpid()) {
		t.Fatal("the liveness probe reported its own process dead")
	}
	// Run a harmless child to completion, then check the same oracle after reap.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestShutdownExitedChild$")
	if err = child.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(t, child.Process.Pid) {
		t.Fatal("the liveness probe reported a reaped child alive")
	}
}

func TestShutdownExitedChild(t *testing.T) {}
