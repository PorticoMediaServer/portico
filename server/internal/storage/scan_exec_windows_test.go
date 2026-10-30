//go:build windows

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestLockedScanInputExcludesReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "library")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "media.mkv")
	if err := os.WriteFile(path, []byte("admitted"), 0600); err != nil {
		t.Fatal(err)
	}
	retained, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	locked, name, closeInput, err := lockWindowsMediaInput(retained)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	if data, err := os.ReadFile(name); err != nil || string(data) != "admitted" {
		t.Fatalf("wrong acquired input: %q %v", data, err)
	}
	if _, err := os.OpenFile(path, os.O_WRONLY, 0600); err == nil {
		t.Fatal("writer admitted while probe input is locked")
	}
	if err := os.Rename(path, path+".old"); err == nil {
		t.Fatal("file replacement was admitted")
	}
	a, _, err := windowsFileBinding(retained)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := windowsFileBinding(locked)
	if err != nil || a != b {
		t.Fatalf("locked handle is not admitted object: %q %q %v", a, b, err)
	}
}

func TestLockedScanInputAllowsAncestorWriters(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "media.mkv")
	if err := os.WriteFile(path, []byte("admitted"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _, closeInput, err := lockWindowsMediaInput(file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	name, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("probe locked an ancestor against writers: %v", err)
	}
	windows.CloseHandle(handle)
}

func TestLockedScanInputRecheckRejectsChangedIdentityOrRevision(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "media.mkv")
	replacement := filepath.Join(directory, "replacement.mkv")
	for _, name := range []string{path, replacement} {
		if err := os.WriteFile(name, []byte("same size"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	locked, name, closeInput, err := lockWindowsMediaInput(file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeInput()
	object, token, err := windowsFileBinding(locked)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkWindowsMediaInput(locked, name, object, token); err != nil {
		t.Fatalf("unchanged input rejected: %v", err)
	}
	if err := checkWindowsMediaInput(locked, replacement, object, token); !errors.Is(err, ErrObservedSourceLost) {
		t.Fatalf("replacement path accepted: %v", err)
	}
	if err := checkWindowsMediaInput(locked, name, object, token+"0"); !errors.Is(err, ErrObservedSourceLost) {
		t.Fatalf("changed revision accepted: %v", err)
	}
}

func TestLockedScanInputRejectsPathSubstitution(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "media.mkv")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Match inventory acquisition: Root.Open permits renames of this handle.
	file, err := root.Open("media.mkv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	_, name, closeInput, err := lockWindowsMediaInput(file)
	if err == nil {
		defer closeInput()
		// A retained handle can legitimately resolve to its renamed old path;
		// it must never name the replacement.
		if data, e := os.ReadFile(name); e != nil || string(data) != "old" {
			t.Fatalf("substituted media: %q %v", data, e)
		}
	}
}

func TestWindowsInventoryCommandRunsOnRetainedMedia(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "media.mkv")
	if err := os.WriteFile(path, []byte("retained fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := New(exe)
	ctx := WithScanInput(context.Background(), client, "fixture", InventoryRequest{Root: root})
	var out bytes.Buffer
	handled, err := RunScanCommand(ctx, exe, []string{"-test.run=^TestWindowsScanProbeFixture$", "--", path}, path, &out)
	if err != nil || !handled || out.String() != "retained fixture" {
		t.Fatalf("Windows inventory command failed: %t %q %v", handled, out.String(), err)
	}
}

func TestWindowsScanProbeFixture(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	data, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, string(data))
	os.Exit(0)
}

func TestWindowsEmbeddedLyricProbeUsesAdmittedMedia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.flac")
	if err := os.WriteFile(path, []byte(`{"format":{"tags":{"lyrics":"admitted lyrics"}},"streams":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tags, err := probeLyricTags(file, []string{exe, "-test.run=^TestWindowsScanProbeFixture$", "--", descriptorInput})
	if err != nil || len(tags) != 1 || tags[0]["lyrics"] != "admitted lyrics" {
		t.Fatalf("embedded lyrics unavailable: %v %v", tags, err)
	}
}

func TestWindowsProbeDeadlineEndsHelperAndDescendant(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsProbeDeadlineFixture$", "--", "deadline")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var pid uint32
	if _, err := fmt.Fscanln(out, &pid); err != nil || pid == 0 {
		t.Fatalf("descendant did not start: %d %v", pid, err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := cmd.Wait(); err == nil || ctx.Err() != nil || cmd.ProcessState.ExitCode() != int(windows.ERROR_TIMEOUT) {
		t.Fatalf("helper was not ended by its own deadline: %v %v %v", err, ctx.Err(), cmd.ProcessState)
	}
	status, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived helper deadline: %d %v", status, err)
	}
}

func TestWindowsProbeDeadlineFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if os.Args[len(os.Args)-1] != "deadline" {
		return
	}
	if err := ensureWindowsProbeJob(); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestWindowsIdleProbeFixture$", "--", "idle")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, cmd.Process.Pid)
	ctx, cancel := windowsProbeContext(2 * time.Second)
	defer cancel()
	<-ctx.Done()
	// Let the deadline's job termination end this process, not the test runner.
	time.Sleep(time.Minute)
	os.Exit(2)
}

func TestWindowsIdleProbeFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if os.Args[len(os.Args)-1] != "idle" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(2)
}
