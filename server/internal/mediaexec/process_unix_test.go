//go:build linux || darwin

package mediaexec

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
)

func configureForTest(t *testing.T, state string) int {
	t.Helper()
	reaped, err := Configure(Options{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	return reaped
}

func ledgerEntries(t *testing.T, state string) []string {
	t.Helper()
	entries, err := os.ReadDir(storagepaths.Path(state, "run", "media-processes"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func gone(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// A long, cheap job: silent audio paced in real time to the null muxer.
func longJob(ffmpeg string) Job {
	return Job{Executable: ffmpeg, Args: []string{"-nostdin", "-v", "error", "-re", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "60", "-f", "null", "-"}}
}

// The shim bounds what a job may write: past MaxFileBytes a write fails (EFBIG,
// not a kill), and a job with no output folder can't grow any file.
func TestShimBoundsOutputFiles(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	configureForTest(t, t.TempDir())
	output := t.TempDir()
	target := filepath.Join(output, "long.wav")
	out, err := run(t, Job{Executable: ffmpeg, Args: []string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=5", "-c:a", "pcm_s16le", "-y", target}, WriteDirs: []string{output}, MaxFileBytes: 64 << 10})
	if err == nil {
		t.Fatalf("a 5 s PCM file fits in 64 KiB? %q", out)
	}
	if info, statErr := os.Stat(target); statErr == nil && info.Size() > 64<<10 {
		t.Fatalf("output grew past its bound: %d", info.Size())
	}
	if out, err = run(t, Job{Executable: ffmpeg, Args: []string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=0.1", "-c:a", "pcm_s16le", "-y", filepath.Join(output, "short.wav")}, WriteDirs: []string{output}, MaxFileBytes: 64 << 10}); err != nil {
		t.Fatalf("a small output within its bound failed: %v %q", err, out)
	}
	// No output folder: no file may be written at all (in the baseline too).
	elsewhere := filepath.Join(t.TempDir(), "x.wav")
	sandboxProbe = func(string) (string, bool, error) { return "", false, errors.New("simulated host without a sandbox") }
	postures.Lock()
	postures.byTool = map[string]*postureEntry{}
	postures.Unlock()
	if _, err = run(t, Job{Executable: ffmpeg, Args: []string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=0.1", "-c:a", "pcm_s16le", "-y", elsewhere}}); err == nil {
		if info, statErr := os.Stat(elsewhere); statErr == nil && info.Size() > 0 {
			t.Fatalf("a baseline job with no output folder wrote %d bytes", info.Size())
		}
	}
}

// BE-MEDIA-06: cancelling a job leaves no descendant, sandbox wrapper included.
func TestCancelLeavesNoDescendant(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	configureForTest(t, t.TempDir())
	marker := filepath.Join(t.TempDir(), "marker-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".wav")
	ctx, cancel := context.WithCancel(context.Background())
	job := Job{Executable: ffmpeg, Args: []string{"-nostdin", "-v", "error", "-re", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "60", "-c:a", "pcm_s16le", "-y", marker}, WriteDirs: []string{filepath.Dir(marker)}}
	cmd, err := CommandContext(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(processesMentioning(t, marker)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(processesMentioning(t, marker)) == 0 {
		t.Fatal("job never started")
	}
	cancel()
	_ = cmd.Wait()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(processesMentioning(t, marker)) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendants survived cancellation: %v", processesMentioning(t, marker))
}

// processesMentioning lists live (non-zombie) processes whose command line
// names marker. Test-only: production never identifies processes by name.
func processesMentioning(t *testing.T, marker string) []string {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,stat=,args=").Output()
	if err != nil {
		t.Fatal(err)
	}
	found := []string{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && !strings.HasPrefix(fields[1], "Z") && strings.Contains(scanner.Text(), marker) {
			found = append(found, fields[0])
		}
	}
	return found
}

// The ledger records each job's group while it runs and forgets it once it has
// ended; a restart kills only groups provably started by an earlier run.
func TestLedgerRecordsSweepsAndReapsOnlyProvenGroups(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	state := t.TempDir()
	configureForTest(t, state)
	cmd, err := Command(longJob(ffmpeg))
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	deadline := time.Now().Add(5 * time.Second)
	for !contains(ledgerEntries(t, state), pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !contains(ledgerEntries(t, state), pid) {
		t.Fatalf("running job not in the ledger: %v", ledgerEntries(t, state))
	}
	// A sweep keeps a running job of this run.
	if err = SweepLedger(); err != nil || !contains(ledgerEntries(t, state), pid) {
		t.Fatalf("sweep dropped a live job: %v %v", err, ledgerEntries(t, state))
	}
	// An entry whose PID is alive but started at another time (a recycled PID;
	// here the test process itself) must never be killed, only forgotten.
	self := strconv.Itoa(os.Getpid())
	identity, err := identityOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Join([]string{ledgerHeader, "previous", self, identity.start + "1", identity.boot}, "\n") + "\n"
	if err = os.WriteFile(storagepaths.Path(state, "run", "media-processes", self), []byte(forged), 0600); err != nil {
		t.Fatal(err)
	}
	// A new run of the server (new instance) reaps the earlier run's job.
	reaped := configureForTest(t, state)
	if reaped != 1 {
		t.Fatalf("reaped %d groups, want 1", reaped)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("job was not killed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier run's job survived the restart")
	}
	if names := ledgerEntries(t, state); len(names) != 0 {
		t.Fatalf("ledger not emptied: %v", names)
	}
	// A finished job of this run is forgotten by the next sweep.
	cmd, err = Command(Job{Executable: ffmpeg, Args: []string{"-hide_banner", "-version"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = nil
	if err = cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err = SweepLedger(); err != nil || len(ledgerEntries(t, state)) != 0 {
		t.Fatalf("finished job kept: %v %v", err, ledgerEntries(t, state))
	}
}

// Without the single-instance lock another server may still be running on
// this state directory, so Configure with SkipOrphanKill must leave its live
// jobs alone (and keep their ledger entries), forgetting dead ones only.
func TestConfigureWithoutLockKillsNothing(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	state := t.TempDir()
	configureForTest(t, state)
	cmd, err := Command(longJob(ffmpeg))
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for !contains(ledgerEntries(t, state), strconv.Itoa(pid)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !contains(ledgerEntries(t, state), strconv.Itoa(pid)) {
		t.Fatalf("running job not in the ledger: %v", ledgerEntries(t, state))
	}
	reaped, err := Configure(Options{StateDir: state, SkipOrphanKill: true})
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 0 {
		t.Fatalf("reaped %d group(s) without the lock, want 0", reaped)
	}
	if err = syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the other server's job did not survive: %v", err)
	}
	if !contains(ledgerEntries(t, state), strconv.Itoa(pid)) {
		t.Fatalf("the sweep dropped a live job: %v", ledgerEntries(t, state))
	}
	_ = unix.Kill(-pid, unix.SIGKILL)
	_ = cmd.Wait()
	if !gone(pid) {
		t.Fatalf("test job %d survived cleanup", pid)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// BE-MEDIA-06 acceptance: kill -9 of the server, then a restart, leaves no
// FFmpeg from the previous run. (On Linux the parent-death signal usually ends
// it first; on macOS only the ledger does.)
func TestRestartAfterKillLeavesNoFFmpegFromThePreviousRun(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	state := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(self, "-test.run=^$")
	server.Env = append(os.Environ(), "PORTICO_TEST_CRASHED_SERVER="+state, "PORTICO_TEST_FFMPEG="+ffmpeg)
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = server.Process.Kill()
		t.Fatalf("fake server: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("fake server said %q", line)
	}
	if err = syscall.Kill(pid, 0); err != nil {
		t.Fatalf("job not running: %v", err)
	}
	_ = server.Process.Signal(syscall.SIGKILL)
	_ = server.Wait()
	reaped := configureForTest(t, state)
	if !gone(pid) {
		_ = unix.Kill(-pid, unix.SIGKILL)
		t.Fatalf("FFmpeg %d from the killed server survived the restart (reaped %d)", pid, reaped)
	}
	t.Logf("restart reaped %d group(s)", reaped)
}

func init() { crashedServerHook = crashedServer }

// crashedServer is the fake server: configure, start a long job, report its
// PID, and wait to be killed.
func crashedServer() {
	state := os.Getenv("PORTICO_TEST_CRASHED_SERVER")
	if _, err := Configure(Options{StateDir: state}); err != nil {
		os.Exit(2)
	}
	cmd, err := Command(longJob(os.Getenv("PORTICO_TEST_FFMPEG")))
	if err != nil {
		os.Exit(3)
	}
	if err = cmd.Start(); err != nil {
		os.Exit(4)
	}
	// Wait until the shim has recorded the group before reporting it.
	for i := 0; i < 500; i++ {
		if _, err = os.Stat(storagepaths.Path(state, "run", "media-processes", strconv.Itoa(cmd.Process.Pid))); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Stdout.WriteString(strconv.Itoa(cmd.Process.Pid) + "\n")
	time.Sleep(time.Hour)
}
