//go:build rcloneintegration

package rcloneprogram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// This explicit integration round requires the installed locked upstream tool.
// It exercises a real serve process with a local fixture backend. It makes no
// provider, immutable-identity or outbound-enforcement qualification claim.
func TestLockedRcloneServeLifecycleAndObservableReplacement(t *testing.T) {
	program, approved := lockedServeProgram(t)
	spec, lease, _ := serveProcessFixture(t, "unused")
	control, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	spec.MetadataPort = control.Addr().(*net.TCPAddr).Port
	_ = control.Close()
	root := t.TempDir()
	file := filepath.Join(root, "episode.mkv")
	before := bytes.Repeat([]byte("A"), 2<<20)
	if err = os.WriteFile(file, before, 0600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err = os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	spec.Remote = "media:" + root
	spec.Enforce = func(cmd *exec.Cmd) error {
		// Only this test fixture uses local rclone. No source provider or
		// production egress installer is substituted by this callback.
		if cmd.Path != program || spec.Remote != "media:"+root {
			t.Fatal("unexpected test process or remote")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	spec.ApprovedProgramDigest = approved
	process, err := StartServe(ctx, Program{Path: program}, spec, lease)
	if err != nil {
		t.Fatal("real rclone startup", err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); !errors.Is(err, ErrServeStopped) {
			t.Error("real child retirement", err)
		}
	})
	entries, err := os.ReadDir(spec.PrivateDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatal("config survived real readiness", entries, err)
	}
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	if err = endpoint.VisitSnapshot(ctx, "", process.Check, func(entry remotemedia.DAVEntry) error {
		if entry.Relative != "episode.mkv" || entry.Size != int64(len(before)) || entry.Directory {
			t.Fatal("real streaming listing changed child facts", entry)
		}
		listed++
		return nil
	}); err != nil || listed != 1 {
		t.Fatal("real complete streaming listing", listed, err)
	}
	stat, err := endpoint.Stat(ctx, "episode.mkv", process.Check)
	if err != nil {
		t.Fatal("real stat", err)
	}
	metadata, err := process.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := metadata.Stat(ctx, "episode.mkv", process.Check)
	if err != nil || fresh.Size != int64(len(before)) || !fresh.Modified.Equal(mtime) || len(fresh.Hashes) != 0 {
		t.Fatal("fresh same-process metadata after private config cleanup", fresh, err)
	}
	got, err := endpoint.ReadRange(ctx, "episode.mkv", stat.ETag, int64(len(before)), 1700000, 64, process.Check)
	if err != nil || !bytes.Equal(got, before[:64]) {
		t.Fatal("real late range", err)
	}
	if err = os.WriteFile(file, bytes.Repeat([]byte("B"), len(before)), 0600); err != nil {
		t.Fatal(err)
	}
	mtime = mtime.Add(2 * time.Second)
	if err = os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	changed, err := endpoint.Stat(ctx, "episode.mkv", process.Check)
	if err != nil || changed.ETag == stat.ETag {
		t.Fatal("observable local replacement not refreshed", changed.ETag, err)
	}
	fresh, err = metadata.Stat(ctx, "episode.mkv", process.Check)
	if err != nil || !fresh.Modified.Equal(mtime) {
		t.Fatal("fresh metadata retained old revision", fresh, err)
	}
	got, err = endpoint.ReadRange(ctx, "episode.mkv", stat.ETag, int64(len(before)), 1800000, 64, process.Check)
	if !errors.Is(err, remotemedia.ErrObservationChanged) || len(got) != 0 {
		t.Fatal("old served precondition exposed changed local fixture", len(got), err)
	}
	process.Stop()
	if err = process.Wait(ctx); !errors.Is(err, ErrServeStopped) || lease.released.Load() != 1 {
		t.Fatal("real process slot was not physically retired", err, lease.released.Load())
	}
}

func TestLockedCustomServeRejectsEnforceImageSubstitution(t *testing.T) {
	program, digest := lockedServeProgram(t)
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.ApprovedProgramDigest = digest
	spec.Enforce = func(cmd *exec.Cmd) error { cmd.Path = "/unapproved/replacement"; return nil }
	if _, err := StartServe(context.Background(), Program{Path: program}, spec, lease); !errors.Is(err, ErrServeProgram) {
		t.Fatal("custom image substitution admitted", err)
	}
	if lease.released.Load() != 1 {
		t.Fatal("failed preparation retained physical slot")
	}
	files, err := os.ReadDir(spec.PrivateDirectory)
	if err != nil || len(files) != 0 {
		t.Fatal("failed custom approval retained private config", files, err)
	}
}

func lockedServeProgram(t *testing.T) (string, string) {
	t.Helper()
	program := os.Getenv("PORTICO_RCLONE_TEST_PROGRAM")
	if program == "" {
		t.Fatal("PORTICO_RCLONE_TEST_PROGRAM must name the locked rclone executable")
	}
	raw, err := os.ReadFile("../../../third_party/rclone/bundle.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Targets map[string]struct{ Files map[string]string }
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	if hex.EncodeToString(digest[:]) != lock.Targets["linux-amd64"].Files["rclone"] {
		t.Fatal("integration tool does not match the canonical locked linux-amd64 binary")
	}
	clear(binary)
	return program, hex.EncodeToString(digest[:])
}

type changingProgramLease struct {
	processLeaseFixture
	checks int
	mutate func() error
}

func (l *changingProgramLease) Check(ctx context.Context) error {
	l.checks++
	if l.checks == 2 {
		if err := l.mutate(); err != nil {
			return err
		}
	}
	return l.processLeaseFixture.Check(ctx)
}
func TestLockedCustomServeRechecksImageImmediatelyBeforeStart(t *testing.T) {
	program, digest := lockedServeProgram(t)
	owned := filepath.Join(t.TempDir(), "rclone")
	in, err := os.Open(program)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(owned, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	in.Close()
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	spec, base, _ := serveProcessFixture(t, "unused")
	spec.ApprovedProgramDigest = digest
	spec.Enforce = func(*exec.Cmd) error { return nil } // Local fixture only, never production egress authority.
	lease := &changingProgramLease{processLeaseFixture: *base, mutate: func() error {
		file, err := os.OpenFile(owned, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		var value [1]byte
		if _, err = file.ReadAt(value[:], 64); err != nil {
			return err
		}
		value[0] ^= 1
		_, err = file.WriteAt(value[:], 64)
		return err
	}}
	if _, err = StartServe(context.Background(), Program{Path: owned}, spec, lease); !errors.Is(err, ErrServeProgram) {
		t.Fatal("changed post-preparation image was started", err)
	}
	if lease.checks != 2 || lease.released.Load() != 1 {
		t.Fatal("startup guard/custody", lease.checks, lease.released.Load())
	}
	files, err := os.ReadDir(spec.PrivateDirectory)
	if err != nil || len(files) != 0 {
		t.Fatal("changed image retained credential config", files, err)
	}
}
