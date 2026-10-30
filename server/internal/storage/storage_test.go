package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--portico-disc-scan-fixture" {
		raw := os.Args[2]
		if !strings.HasPrefix(raw, "bluray:") {
			os.Exit(2)
		}
		file, err := os.Open(strings.TrimPrefix(raw, "bluray:"))
		if err != nil {
			os.Exit(3)
		}
		_, err = io.WriteString(os.Stdout, "bluray:")
		if err == nil {
			_, err = io.Copy(os.Stdout, io.LimitReader(file, 64))
		}
		file.Close()
		if err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}

	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if Helper(helperInput(), os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestRealHelperInventoryAndMissingSource(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c := New(binary)
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("test"), 0600); e != nil {
		t.Fatal(e)
	}
	observed, e := c.InspectRoot(context.Background(), root)
	if e != nil || !observed.Directory {
		t.Fatalf("root %+v %v", observed, e)
	}
	count := 0
	if e = c.Inventory(context.Background(), "root", root, func(v Snapshot) error {
		count++
		if v.Name != "movie.mp4" || v.Size != 4 {
			t.Fatalf("entry %+v", v)
		}
		return nil
	}); e != nil || count != 1 {
		t.Fatalf("inventory %d %v", count, e)
	}
	if _, e = c.InspectRoot(context.Background(), filepath.Join(root, "missing")); e == nil {
		t.Fatal("missing source accepted")
	}
}
func TestCancellationKillsOwnedChildAndAdmissionRemainsBounded(t *testing.T) {
	s := &Supervisor{Limit: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	e := s.Run(ctx, "root", exec.Command("sh", "-c", "sleep 30"), func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("cancellation %v after %v", e, time.Since(started))
	}
	for n := 0; n < 100 && s.Active() != 0; n++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Active() != 0 {
		t.Fatal("reaped child retained slot")
	}
	s.mu.Lock()
	s.active["wedged-root"] = true
	s.counts[poolHelper] = 1
	s.mu.Unlock()
	e = s.Run(context.Background(), "another-root", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil })
	if !errors.Is(e, ErrBusy) {
		t.Fatal("quarantine did not cap new admissions", e)
	}
}
