//go:build linux || darwin

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
)

func TestManagedRecordingLegacyPathsAndExactReceiptClassification(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 64)
	c := New("")
	c.RecordingRoot = root
	calls := 0
	c.RecordingLayout = func(context.Context, string) (bool, error) {
		calls++
		return false, errors.New("must not classify known object")
	}
	for _, path := range []string{filepath.Join(root, id, "objects", id), filepath.Join(root, id, "captures", id, "objects", id)} {
		managed, err := c.managedRecording(context.Background(), path)
		if !managed || err != nil {
			t.Fatalf("legacy object lost physical lease classification: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("legacy fast path queried receipts")
	}
	alias := filepath.Join(root, id, "Example", "Example.ts")
	c.RecordingLayout = func(ctx context.Context, path string) (bool, error) { calls++; return path == alias, nil }
	if managed, err := c.managedRecording(context.Background(), alias); !managed || err != nil {
		t.Fatal("exact receipt not managed", err)
	}
	if managed, err := c.managedRecording(context.Background(), filepath.Join(root, id, "unowned.ts")); managed || err != nil {
		t.Fatal("unowned path trusted", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.managedRecording(canceled, alias); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation downgraded")
	}
}
func TestRecordingLayoutLookupFailureAbortsEveryAcquisition(t *testing.T) {
	c, lease, path, released, _ := observedStorageFixture(t)
	failure := errors.New("receipt snapshot failed")
	c.RecordingLayout = func(context.Context, string) (bool, error) { return false, failure }
	if err := c.runWithStem(context.Background(), "layout", "stat", path, "", func(Snapshot) error { return nil }); !errors.Is(err, failure) {
		t.Fatal("stat downgraded failed receipt", err)
	}
	if _, err := c.readBlob(context.Background(), "layout", path, 1024, false); !errors.Is(err, failure) {
		t.Fatal("blob downgraded failed receipt", err)
	}
	if reader, err := c.OpenPlayback(context.Background(), path, 10, 0); reader != nil || !errors.Is(err, failure) {
		t.Fatal("stream downgraded failed receipt", err)
	}
	if file, err := c.OpenPlaybackDescriptor(context.Background(), path, 10, 0); file != nil || !errors.Is(err, failure) {
		t.Fatal("descriptor downgraded failed receipt", err)
	}
	if reader, err := c.OpenObservedPlayback(context.Background(), lease); reader != nil || !errors.Is(err, failure) {
		t.Fatal("observed downgraded failed receipt", err)
	}
	if released.Load() != 1 || c.Supervisor.Active() != 0 {
		t.Fatal("lookup failure leaked root or started helper")
	}
}
func TestRecordingLayoutReceiptClassificationAddsRealDescriptorLease(t *testing.T) {
	c, lease, path, _, _ := observedStorageFixture(t)
	defer lease.Close()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	c.RecordingLayout = func(_ context.Context, p string) (bool, error) { return p == path, nil }
	descriptor, err := c.OpenPlaybackDescriptor(context.Background(), path, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Open(path)
	if err != nil {
		descriptor.Close()
		t.Fatal(err)
	}
	defer other.Close()
	if err = artifactlease.Exclusive(other); err == nil {
		descriptor.Close()
		t.Fatal("physical retirement was allowed with transferred reader")
	}
	descriptor.Close()
	waitReaderExit(t, c)
	if err = artifactlease.Exclusive(other); err != nil {
		t.Fatal("physical lease not retired after actual descriptor close", err)
	}
}
