//go:build linux

package mediaexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configureProbePool(t *testing.T) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Configure(Options{StateDir: t.TempDir(), Helper: binary, SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		configured.Lock()
		configured.helper, configured.ledger = "", ""
		configured.Unlock()
		closeIdleProbeWorkers()
	})
}

func pooledProbe(t *testing.T, ffprobe, input string, args ...string) (ProbeResult, bool, error) {
	t.Helper()
	f, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return RunPooledProbe(ctx, PooledProbe{Job: Job{Executable: ffprobe, Args: args, Background: true, MaxCPUSeconds: 30}, Input: f, OutputLimit: 4 << 20})
}

// Scanner M4: probes run on a long-lived sandbox. Sequential probes reuse one
// worker, each sees only its own input, and a failure is the tool's own.
func TestProbeWorkerRunsSequentialProbesInOneSandbox(t *testing.T) {
	ffmpeg, ffprobe := sandboxed(t)
	configureProbePool(t)
	dir := t.TempDir()
	first := sample(t, ffmpeg, dir)
	second := filepath.Join(dir, "second.mkv")
	if raw, err := os.ReadFile(first); err != nil || os.WriteFile(second, raw, 0600) != nil {
		t.Fatal(err)
	}
	before := ProbePoolSnapshot()
	args := []string{"-v", "error", "-show_entries", "format=filename", "-of", "default=nw=1:nk=1", "/proc/self/fd/3"}
	for _, input := range []string{first, second, first} {
		result, handled, err := pooledProbe(t, ffprobe, input, args...)
		if !handled || err != nil || result.Err != nil {
			t.Fatalf("probe of %s: handled=%v err=%v tool=%v stderr=%q", input, handled, err, result.Err, result.Stderr)
		}
		// The tool reads its input as descriptor 3, never by host path.
		if got := strings.TrimSpace(string(result.Stdout)); got != "/proc/self/fd/3" {
			t.Fatalf("probe output %q", got)
		}
	}
	after := ProbePoolSnapshot()
	if started := after.WorkersStarted - before.WorkersStarted; started != 1 {
		t.Fatalf("three sequential probes started %d sandboxes, want 1", started)
	}
	if jobs := after.Jobs - before.Jobs; jobs != 3 {
		t.Fatalf("jobs=%d", jobs)
	}
	// A tool failure is reported as the process's exit, with its stderr, and
	// the worker stays usable.
	bad := filepath.Join(dir, "not-media.txt")
	if err := os.WriteFile(bad, []byte("not media"), 0600); err != nil {
		t.Fatal(err)
	}
	result, handled, err := pooledProbe(t, ffprobe, bad, "-v", "error", "-show_format", "/proc/self/fd/3")
	if !handled || err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "exit status") || result.Stderr == "" {
		t.Fatalf("failed probe: handled=%v err=%v tool=%v stderr=%q", handled, err, result.Err, result.Stderr)
	}
	if _, _, err = pooledProbe(t, ffprobe, first, args...); err != nil {
		t.Fatal(err)
	}
	if started := ProbePoolSnapshot().WorkersStarted - before.WorkersStarted; started != 1 {
		t.Fatalf("a failed probe cost its sandbox: %d started", started)
	}
}

// Cancellation kills the sandbox; the next probe gets a fresh one.
func TestProbeWorkerCancellationKillsItsSandbox(t *testing.T) {
	ffmpeg, ffprobe := sandboxed(t)
	configureProbePool(t)
	input := sample(t, ffmpeg, t.TempDir())
	f, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, handled, err := RunPooledProbe(ctx, PooledProbe{Job: Job{Executable: ffprobe, Args: []string{"/proc/self/fd/3"}, Background: true}, Input: f, OutputLimit: 1 << 20}); !handled || err == nil {
		t.Fatalf("a cancelled probe: handled=%v err=%v", handled, err)
	}
	if _, _, err := pooledProbe(t, ffprobe, input, "-v", "error", "-show_format", "/proc/self/fd/3"); err != nil {
		t.Fatalf("the probe after a cancelled one: %v", err)
	}
}

// An unconfigured process (a test binary that never called Configure) never
// starts itself inside a sandbox as the worker.
func TestProbePoolNeedsAConfiguredHelper(t *testing.T) {
	_, ffprobe := sandboxed(t)
	if ProbePoolAccepts(Job{Executable: ffprobe, Background: true}) {
		t.Fatal("the pool accepted a job without a configured helper")
	}
}
