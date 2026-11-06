//go:build linux

package mediaexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"golang.org/x/sys/unix"
)

const shimInstanceTestMode = "PORTICO_TEST_SHIM_INSTANCE_MODE"

// Each scenario has its own subprocess/subreaper so the test runner's adoption
// policy is unchanged. The fake server and shim deliberately omit Pdeathsig to
// recreate macOS lifetime ordering on Linux; production Linux is not modified.
func TestShimInstanceAcrossPublication(t *testing.T) {
	if os.Getenv(shimInstanceTestMode) == "coordinator" {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		runShimInstanceScenario(t, os.Getenv("PORTICO_TEST_SHIM_INSTANCE_DIES") == "1")
		return
	}
	for _, dies := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent-dies-%t", dies), func(t *testing.T) {
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(self, "-test.run=^TestShimInstanceAcrossPublication$", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), shimInstanceTestMode+"=coordinator", "PORTICO_TEST_SHIM_INSTANCE_DIES="+strconv.Itoa(map[bool]int{false: 0, true: 1}[dies]))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("restart scenario: %v\n%s", err, output)
			}
		})
	}
}

func runShimInstanceScenario(t *testing.T, dies bool) {
	state := t.TempDir()
	createdR, createdW := shimInstancePipe(t)
	releaseR, releaseW := shimInstancePipe(t)
	toolR, toolW := shimInstancePipe(t)
	serverR, _ := shimInstancePipe(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(self, "-test.run=^TestShimInstanceProcess$")
	server.Env = append(os.Environ(), shimInstanceTestMode+"=server", "PORTICO_TEST_SHIM_INSTANCE_STATE="+state)
	server.ExtraFiles = []*os.File{createdW, releaseR, toolW}
	server.Stdin = serverR
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.Stderr = os.Stderr
	report, reportW := shimInstancePipe(t)
	server.Stdout = reportW
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	joinServer := func() {
		if !joined {
			_ = server.Process.Kill()
			_ = server.Wait()
			joined = true
		}
	}
	t.Cleanup(joinServer)
	_ = reportW.Close()
	_ = createdW.Close()
	_ = releaseR.Close()
	_ = toolW.Close()
	_ = serverR.Close()
	if err = report.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(report).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	shimPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || shimPID <= 1 {
		t.Fatalf("shim PID %q: %v", line, err)
	}
	// Keep the shim unreaped until cleanup or the explicit Wait4 below, so its
	// process group ID cannot be reused before failure-path signalling.
	shimJoined := false
	t.Cleanup(func() {
		joinServer() // orphan is now adopted by this isolated subreaper
		if !shimJoined {
			_ = unix.Kill(-shimPID, unix.SIGKILL)
			var status unix.WaitStatus
			_, _ = unix.Wait4(shimPID, &status, 0, nil)
		}
	})
	awaitShimInstanceEOF(t, createdR)
	ledger := storagepaths.Path(state, "run", "media-processes")
	path := filepath.Join(ledger, strconv.Itoa(shimPID))
	if _, err = readEntry(path); err == nil {
		t.Fatal("shim unexpectedly published before release")
	}
	if dies {
		joinServer() // actual SIGKILL/server Wait precede the replacement's scan
		// Inject macOS Configure's fence-before-scan ordering on Linux. The
		// production Linux path intentionally leaves that policy disabled.
		if err = os.WriteFile(ledger+".instance", []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
		if reaped := configureForTest(t, state); reaped != 0 {
			t.Fatalf("startup reaped unproven group: %d", reaped)
		}
		if err = unix.Kill(shimPID, 0); err != nil {
			t.Fatalf("macOS-style shim did not survive parent: %v", err)
		}
	}
	if _, err = releaseW.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = toolR.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	marker, err := io.ReadAll(toolR)
	if err != nil {
		t.Fatal(err)
	}
	joinServer()
	var status unix.WaitStatus
	if pid, err := unix.Wait4(shimPID, &status, 0, nil); err != nil || pid != shimPID {
		t.Fatalf("join shim %d: %d %v", shimPID, pid, err)
	}
	shimJoined = true
	if dies {
		if len(marker) != 0 || !status.Exited() || status.ExitStatus() != 23 {
			t.Fatalf("late shim executed tool after restart: marker=%v status=%v", marker, status)
		}
		if _, err = readEntry(path); err != nil {
			t.Fatalf("incarnation check did not follow actual publication: %v", err)
		}
		if err = SweepLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dead refused shim retained: %v", err)
		}
	} else if !bytes.Equal(marker, []byte{1}) || !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("live-parent shim refused: marker=%v status=%v", marker, status)
	}
}

// Test helper only: incarnation policy is encoded in the real shim wire so this
// regression also compiles on old main (which ignores g) and fails by exec'ing
// the tool, rather than by referring to a newly added field/function.
func TestShimInstanceProcess(t *testing.T) {
	switch os.Getenv(shimInstanceTestMode) {
	case "server":
		configureForTest(t, os.Getenv("PORTICO_TEST_SHIM_INSTANCE_STATE"))
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		argv := shimArgv(Job{}, []string{self})
		raw, err := base64.RawURLEncoding.DecodeString(argv[2])
		if err != nil {
			t.Fatal(err)
		}
		var spec map[string]any
		if err = json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		spec["g"] = true // inject macOS's incarnation policy on this Linux harness
		if err = os.WriteFile(spec["l"].(string)+".instance", []byte(spec["i"].(string)), 0600); err != nil {
			t.Fatal(err)
		}
		raw, err = json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(self, "-test.run=^TestShimInstanceProcess$")
		cmd.Env = append(os.Environ(), shimInstanceTestMode+"=shim", "PORTICO_TEST_SHIM_INSTANCE_SPEC="+base64.RawURLEncoding.EncodeToString(raw))
		cmd.ExtraFiles = []*os.File{os.NewFile(3, "created"), os.NewFile(4, "release"), os.NewFile(5, "tool")}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // macOS: no parent-death signal
		cmd.Stderr = os.Stderr
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for _, file := range cmd.ExtraFiles {
			_ = file.Close()
		}
		fmt.Fprintln(os.Stdout, cmd.Process.Pid)
		_, _ = io.Copy(io.Discard, os.Stdin) // killed by coordinator, never a timing wait
	case "shim":
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		spec, inner, err := parseShim([]string{ShimFlag, os.Getenv("PORTICO_TEST_SHIM_INSTANCE_SPEC"), "--", self, "-test.run=^TestShimInstanceProcess$"})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(spec.Ledger, strconv.Itoa(os.Getpid()))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_ = os.NewFile(3, "created").Close()
		release := os.NewFile(4, "release")
		var signal [1]byte
		if n, err := release.Read(signal[:]); n != 1 || err != nil {
			t.Fatalf("publication release: %d %v", n, err)
		}
		_ = release.Close()
		if err = os.Setenv(shimInstanceTestMode, "tool"); err != nil {
			t.Fatal(err)
		}
		if err = execShim(spec, inner); err == nil || err.Error() != "media process server incarnation changed before exec" {
			t.Fatalf("shim returned unexpected error: %v", err)
		}
		os.Exit(23)
	case "tool":
		if _, err := os.NewFile(5, "tool").Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
}

func TestLinuxShimRetainsParentDeathPolicy(t *testing.T) {
	resetForTest(t)
	configureForTest(t, t.TempDir())
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := shimArgv(Job{}, []string{self})
	raw, err := base64.RawURLEncoding.DecodeString(argv[2])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["g"]; ok {
		t.Fatal("Linux shim gained a macOS incarnation guard")
	}
	cmd, err := CommandArgv(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Linux Pdeathsig changed: %+v", cmd.SysProcAttr)
	}
}

func shimInstancePipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}
func awaitShimInstanceEOF(t *testing.T, r *os.File) {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if n, err := r.Read(signal[:]); n != 0 || err != io.EOF {
		t.Fatalf("publication gate: %d %v", n, err)
	}
}
