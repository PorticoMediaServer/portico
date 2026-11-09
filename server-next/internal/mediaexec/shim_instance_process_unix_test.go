//go:build linux || darwin

package mediaexec

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
	"golang.org/x/sys/unix"
)

const shimInstanceTestMode = "PORTICO_TEST_SHIM_INSTANCE_MODE"

// Model the supported helper topology: server A issues a frozen argv, then a
// helper (this test) starts its shim. Both processes remain directly joinable;
// no Linux subreaper/adoption API is needed. Only test commands omit Pdeathsig.
// Native macOS executes its actual Configure guard publication and startup scan.
func TestShimInstanceAcrossPublication(t *testing.T) {
	for _, scenario := range []string{"current", "restart-during-publication", "restart-after-exec", "missing", "unreadable"} {
		t.Run(scenario, func(t *testing.T) { runShimInstanceScenario(t, scenario) })
	}
}

func runShimInstanceScenario(t *testing.T, scenario string) {
	resetForTest(t)
	state := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	report, reportW := shimInstancePipe(t)
	serverInput, _ := shimInstancePipe(t)
	server := exec.Command(self, "-test.run=^TestShimInstanceProcess$")
	server.Env = append(os.Environ(), shimInstanceTestMode+"=server", "PORTICO_TEST_SHIM_INSTANCE_STATE="+state)
	server.Stdout, server.Stdin, server.Stderr = reportW, serverInput, os.Stderr
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	serverJoined := false
	joinServer := func() {
		if !serverJoined {
			_ = server.Process.Kill()
			_ = server.Wait()
			serverJoined = true
		}
	}
	t.Cleanup(joinServer)
	_ = reportW.Close()
	_ = serverInput.Close()
	if err = report.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	frozen, err := bufio.NewReader(report).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	frozen = strings.TrimSpace(frozen)
	raw, err := base64.RawURLEncoding.DecodeString(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var started map[string]any
	if err = json.Unmarshal(raw, &started); err != nil {
		t.Fatal(err)
	}
	ledger := storagepaths.Path(state, "run", "media-processes")
	guard := ledger + ".instance"
	if started["l"] != ledger || started["g"] != true {
		t.Fatalf("wrong frozen launch: %v", started)
	}

	createdR, createdW := shimInstancePipe(t)
	releaseR, releaseW := shimInstancePipe(t)
	toolR, toolW := shimInstancePipe(t)
	shim := exec.Command(self, "-test.run=^TestShimInstanceProcess$")
	shim.Env = append(os.Environ(), shimInstanceTestMode+"=shim", "PORTICO_TEST_SHIM_INSTANCE_SPEC="+frozen)
	shim.ExtraFiles = []*os.File{createdW, releaseR, toolW}
	shim.Stderr = os.Stderr
	shim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // test only: no Linux Pdeathsig
	if err = shim.Start(); err != nil {
		t.Fatal(err)
	}
	shimJoined := false
	t.Cleanup(func() {
		if !shimJoined {
			_ = unix.Kill(-shim.Process.Pid, unix.SIGKILL)
			_ = shim.Wait()
		}
	})
	_ = createdW.Close()
	_ = releaseR.Close()
	_ = toolW.Close()
	awaitShimInstanceEOF(t, createdR)
	path := filepath.Join(ledger, strconv.Itoa(shim.Process.Pid))
	if _, err = readEntry(path); err == nil {
		t.Fatal("shim published before release")
	}

	restart := func() int {
		joinServer() // actual server SIGKILL and Wait precede replacement Configure
		if runtime.GOOS != "darwin" {
			// Enable only the macOS ordering in this Linux test. Native macOS does
			// the actual temp/write/fsync/rename/directory-fsync inside Configure.
			if err := os.WriteFile(guard, []byte("replacement"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return configureForTest(t, state)
	}
	switch scenario {
	case "restart-during-publication":
		if reaped := restart(); reaped != 0 {
			t.Fatalf("startup reaped an unproven group: %d", reaped)
		}
	case "missing":
		if err = os.Remove(guard); err != nil {
			t.Fatal(err)
		}
	case "unreadable":
		if err = os.Remove(guard); err != nil {
			t.Fatal(err)
		}
		// EISDIR is deterministic even when the test runs as root; chmod(000)
		// alone would not establish an unreadable guard on every test host.
		if err = os.Mkdir(guard, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err = os.ReadFile(guard); err == nil {
			t.Fatal("guard was still readable")
		}
	}
	if _, err = releaseW.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = toolR.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var marker [1]byte
	n, readErr := toolR.Read(marker[:])
	executed := scenario == "current" || scenario == "restart-after-exec"
	if executed {
		if n != 1 || readErr != nil || marker[0] != 1 {
			t.Fatalf("current shim did not exec: %d %v %v", n, marker, readErr)
		}
		if scenario == "restart-after-exec" {
			// Passing the check before B publishes is safe: the complete record is
			// proven, so the ordinary startup scan kills exactly this live group.
			if reaped := restart(); reaped != 1 {
				t.Fatalf("complete pre-scan group not reaped: %d", reaped)
			}
		} else if _, err = releaseW.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
	} else if n != 0 || readErr != io.EOF {
		t.Fatalf("stale/unreadable shim executed tool: n=%d marker=%v err=%v", n, marker, readErr)
	}
	err = shim.Wait()
	shimJoined = true
	if scenario == "restart-after-exec" {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("startup did not kill group: %v", err)
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("reaped group retained: %v", err)
		}
	} else if executed {
		if err != nil {
			t.Fatalf("current shim failed: %v", err)
		}
	} else {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 23 {
			t.Fatalf("guard refusal exit: %v", err)
		}
		entry, err := readEntry(path)
		if err != nil || entry.instance != started["i"] {
			t.Fatalf("frozen launch changed or publication missing: %+v %v", entry, err)
		}
		if scenario == "restart-during-publication" {
			if err = SweepLedger(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("dead refused shim retained: %v", err)
			}
		}
	}
}

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
		if runtime.GOOS != "darwin" {
			spec["g"] = true
			if err = os.WriteFile(spec["l"].(string)+".instance", []byte(spec["i"].(string)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		raw, err = json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, base64.RawURLEncoding.EncodeToString(raw))
		_, _ = io.Copy(io.Discard, os.Stdin)
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
		var signal [1]byte
		release := os.NewFile(4, "release")
		defer release.Close()
		if n, err := release.Read(signal[:]); n != 1 || err != nil {
			t.Fatalf("publication release: %d %v", n, err)
		}
		if err = os.Setenv(shimInstanceTestMode, "tool"); err != nil {
			t.Fatal(err)
		}
		if err = execShim(spec, inner); err == nil || err.Error() != "media process server instance changed before exec" {
			t.Fatalf("shim error: %v", err)
		}
		os.Exit(23)
	case "tool":
		if _, err := os.NewFile(5, "tool").Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		var signal [1]byte
		if n, err := os.NewFile(4, "release").Read(signal[:]); n != 1 || err != nil {
			t.Fatalf("tool release: %d %v", n, err)
		}
		os.Exit(0)
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
