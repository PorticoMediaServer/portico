//go:build linux

package decoder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPreparedLinuxSandboxMountsExactFDAndHoldsInitLifetime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if e := os.WriteFile(path, []byte("closed fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(path, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	custody, e := os.CreateTemp(t.TempDir(), "custody")
	if e != nil {
		t.Fatal(e)
	}
	defer custody.Close()
	executable := privateTestBinary(t, "encoder")
	// The Go test binary is an actual ELF; a distinct copy stands in for bwrap.
	sandbox := privateTestBinary(t, "bwrap")
	command, e := confinedPreparedFileCommand(sandbox, executable, f, custody, []string{"-i", "/input.media"})
	if e != nil {
		t.Fatal(e)
	}
	defer command.close()
	cmd := command.cmd
	args := strings.Join(cmd.Args, " ")
	for _, v := range []string{"--unshare-net", "--unshare-pid", "--disable-userns", "--clearenv", "--ro-bind-fd 3 /input.media", "--sync-fd 4", "--remount-ro /"} {
		if !strings.Contains(args, v) {
			t.Fatal(v, args)
		}
	}
	for _, v := range []string{path, "--ro-bind ", "--ro-bind-try ", "--sync-fd 5"} {
		if strings.Contains(args, v) {
			t.Fatal("ambient mount, source path or wrong custody", v, args)
		}
	}
	if len(cmd.ExtraFiles) < 4 || cmd.ExtraFiles[0] != f || cmd.ExtraFiles[1] != custody || !strings.HasPrefix(cmd.Path, "/proc/self/fd/") {
		t.Fatal("lost descriptor binding")
	}
	if e = command.validate(); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	// Plain state: a group-readable input still runs sandboxed; loose
	// permissions warn at the server level instead of refusing here.
	if loose, e := confinedPreparedFileCommand(sandbox, executable, f, custody, nil); e != nil {
		t.Fatal("readable input refused", e)
	} else {
		loose.close()
	}
}

func TestPreparedLinuxRejectsDirectoriesScriptsAndMutableLibraries(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	if e = os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, libs := range [][]string{{dir}, {script}, {"/proc/self/exe"}, {"relative.so"}} {
		if tools, e := pinPreparedTools(executable, libs...); e == nil {
			tools.close()
			t.Fatalf("accepted unconfined dependency %v", libs)
		}
	}
	if tools, e := pinPreparedTools(script); e == nil {
		tools.close()
		t.Fatal("host script accepted")
	}
	writable := privateTestBinary(t, "writable")
	if e = os.Chmod(writable, 0775); e != nil {
		t.Fatal(e)
	}
	if tools, e := pinPreparedTools(writable); e == nil {
		tools.close()
		t.Fatal("group-writable executable accepted")
	}
}

func TestPreparedLinuxToolDescriptorsSurvivePathReplacementButDetectInPlaceWrite(t *testing.T) {
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(executable)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "encoder")
	if e = os.WriteFile(path, raw, 0700); e != nil {
		t.Fatal(e)
	}
	tools, e := pinPreparedTools(path)
	if e != nil {
		t.Fatal(e)
	}
	defer tools.close()
	old := path + ".old"
	if e = os.Rename(path, old); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("replaced"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = tools.validate(); e != nil {
		t.Fatalf("pinned bytes should not follow replacement: %v", e)
	}
	if e = os.WriteFile(old, []byte("modified"), 0700); e != nil {
		t.Fatal(e)
	}
	if tools.validate() == nil {
		t.Fatal("in-place modification not detected")
	}
}

func TestPreparedCustodyWaitsForInheritedDescriptionNotMonitor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	// dup models the description inherited by sandbox init after monitor exit.
	fd, e := syscall.Dup(int(f.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	inherited := os.NewFile(uintptr(fd), "init")
	defer inherited.Close()
	c := NewPreparedFileCustody(f)
	defer c.Close()
	next, e := c.waiter()
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { c.retired(next); close(done) }()
	select {
	case <-done:
		t.Fatal("retired while init owns custody")
	case <-time.After(30 * time.Millisecond):
	}
	probe, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer probe.Close()
	if e = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e == nil {
		t.Fatal("restart entered live sandbox")
	}
	inherited.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("custody did not retire")
	}
	if e = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e == nil {
		t.Fatal("parent lost reacquired custody")
	}
}

func TestPreparedLinuxCanonicalEntrypointPinsSymlinkOrigin(t *testing.T) {
	executable, err := filepath.EvalSymlinks(privateTestBinary(t, "encoder"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "encoder")
	if err = os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}
	tools, err := pinPreparedTools(link)
	if err != nil {
		t.Fatal(err)
	}
	defer tools.close()
	if tools.entrypoint != executable || tools.seen[link] == nil || tools.seen[executable] == nil {
		t.Fatal("ELF ORIGIN differs from executed image", tools.entrypoint)
	}
}

func TestPreparedLinuxSystemFFmpegDependencyClosure(t *testing.T) {
	// No media or host decoder is executed: this inspects literal ELF objects.
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		executable, err := exec.LookPath(name)
		if err != nil {
			t.Skip("system FFmpeg ELF fixtures unavailable")
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := pinPreparedTools(executable)
		if err != nil {
			t.Fatal(name, err)
		}
		if len(tools.files) < 1 {
			tools.close()
			t.Fatal("empty closure")
		}
		for _, p := range tools.files {
			if !p.info.Mode().IsRegular() {
				t.Error("ambient directory in closure", p.path)
			}
		}
		if err = tools.validate(); err != nil {
			t.Error(err)
		}
		t.Logf("%s: %d descriptor-bound ELF files/aliases", name, len(tools.files))
		tools.close()
	}
}

// privateTestBinary is a copy of the test binary that only its owner can write.
// The binary go test builds carries the caller's umask (group-writable under the
// common 0002), and a writable tool is rightly refused as mutable; the tests
// that need an accepted ELF must not depend on how the runner's shell is set up.
func privateTestBinary(t *testing.T, name string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask; set the exact bits.
	if err = os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
