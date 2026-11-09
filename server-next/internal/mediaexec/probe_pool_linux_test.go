//go:build linux

package mediaexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configureProbePool(t testing.TB) {
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

// Outside its sandbox the worker would SIGKILL every process its user owns
// after a job; it must refuse to start there instead.
func TestProbeWorkerRefusesOutsideItsPIDNamespace(t *testing.T) {
	if inOwnPIDNamespace() {
		t.Skip("the test itself runs as PID 2 under an init")
	}
	if err := runProbeWorker([]string{ProbeWorkerFlag, "/usr/bin/ffprobe"}); !errors.Is(err, errProbeWorkerOutsideSandbox) {
		t.Fatalf("worker outside a PID namespace: %v", err)
	}
}

// Ownership belongs to namespace creation. Warm eligibility and probes must not
// reread the ELF closure; a replacement sandbox must still validate it.
func TestProbeWorkerValidatesLibrariesOnlyForNewSandboxes(t *testing.T) {
	_, ffprobe := sandboxed(t)
	configureProbePool(t)
	job := Job{Executable: ffprobe, Background: true}
	// Version output is deterministic and still launches the real confined tool.
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	p := PooledProbe{Job: job, Input: input, OutputLimit: 1 << 20}
	p.Job.Args = []string{"-version"}
	previous := openLibraryMetadataInput
	reads := 0
	openLibraryMetadataInput = func(path string, flags int, mode os.FileMode) (*os.File, error) {
		reads++
		return previous(path, flags, mode)
	}
	t.Cleanup(func() { openLibraryMetadataInput = previous })
	run := func() {
		t.Helper()
		result, handled, err := RunPooledProbe(context.Background(), p)
		if !handled || err != nil || result.Err != nil {
			t.Fatalf("probe handled=%v err=%v tool=%v stderr=%s", handled, err, result.Err, result.Stderr)
		}
	}
	run()
	if reads == 0 {
		t.Fatal("new sandbox skipped library validation")
	}
	reads = 0
	if !ProbePoolAccepts(job) {
		t.Fatal("warm probe refused")
	}
	run()
	if reads != 0 {
		t.Fatalf("warm eligibility/probe reread library metadata %d times", reads)
	}
	closeIdleProbeWorkers()
	run()
	if reads == 0 {
		t.Fatal("replacement sandbox skipped library validation")
	}
	closeIdleProbeWorkers()
	openLibraryMetadataInput = func(string, int, os.FileMode) (*os.File, error) { return nil, os.ErrPermission }
	before := ProbePoolSnapshot().WorkersStarted
	if _, handled, err := RunPooledProbe(context.Background(), p); handled || err != nil {
		t.Fatalf("unverifiable closure must fall back without starting a worker: handled=%v err=%v", handled, err)
	}
	if ProbePoolSnapshot().WorkersStarted != before {
		t.Fatal("new sandbox started without library ownership")
	}
}

// Two eligibility decisions per probe model storage admission and pool dispatch.
func BenchmarkProbeWorkerProbeLaunchPlanning(b *testing.B) {
	tool, err := exec.LookPath("ffprobe")
	if err != nil {
		b.Skip(err)
	}
	if posture := Decide(tool); !posture.Sandboxed {
		b.Skip(posture.Reason)
	}
	configureProbePool(b)
	job := Job{Executable: tool, Background: true}
	if !ProbePoolAccepts(job) {
		b.Fatal("probe refused")
	}
	b.ResetTimer()
	for b.Loop() {
		for range 2 {
			if !ProbePoolAccepts(job) {
				b.Fatal("probe refused")
			}
		}
	}
}
