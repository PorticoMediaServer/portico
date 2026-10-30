package servicelog

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func logDirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// TestPruneFilesLeavesUnexpiredLogUntouched proves the common no-expiration pass
// creates no file, writes and syncs nothing, and leaves the log's inode, bytes
// and modification time exactly as they were.
func TestPruneFilesLeavesUnexpiredLogUntouched(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r := New(Options{Directory: directory, Capacity: 100, FileBytes: 1 << 20, FileCount: 2, Now: func() time.Time { return now }})
	defer r.Close()
	r.SetLevel("debug")
	r.Record("info", "scan", "recent scan")
	r.Record("info", "server", "recent server")
	path := filepath.Join(directory, "messages.log")
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeNames := logDirectoryNames(t, directory)
	r.SetRetention(map[string]int{"scan": 1, "server": 1})
	r.PruneFiles()

	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("an unexpired log was replaced")
	}
	if afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("an unexpired log was written: size %d→%d mtime %s→%s",
			beforeInfo.Size(), afterInfo.Size(), beforeInfo.ModTime(), afterInfo.ModTime())
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("unexpired bytes changed: %q → %q", beforeBytes, afterBytes)
	}
	afterNames := logDirectoryNames(t, directory)
	if strings.Join(beforeNames, ",") != strings.Join(afterNames, ",") {
		t.Fatalf("the no-op pass created or removed files: %v → %v", beforeNames, afterNames)
	}
}

// TestPruneFilesRewritesOnlyExpiredLines checks that genuine expiration still
// produces exactly the retained bytes and that the recorder keeps appending.
func TestPruneFilesRewritesOnlyExpiredLines(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r := New(Options{Directory: directory, Capacity: 100, FileBytes: 1 << 20, FileCount: 2, Now: func() time.Time { return now }})
	defer r.Close()
	r.SetLevel("debug")
	r.Record("info", "scan", "old scan")
	r.Record("info", "server", "server kept")
	now = now.Add(48 * time.Hour)
	r.Record("info", "scan", "scan kept")
	path := filepath.Join(directory, "messages.log")
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSuffix(string(before), "\n"), "\n") {
		if strings.HasSuffix(line, "\told scan") {
			continue
		}
		kept = append(kept, line)
	}
	want := strings.Join(kept, "\n") + "\n"

	r.SetRetention(map[string]int{"scan": 1})
	r.PruneFiles()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != want {
		t.Fatalf("retained bytes changed:\n got %q\nwant %q", after, want)
	}
	if strings.Contains(string(after), "old scan") {
		t.Fatalf("expired line survived: %q", after)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("an expired log was not replaced")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if names := logDirectoryNames(t, directory); len(names) != 1 || names[0] != "messages.log" {
		t.Fatalf("rewrite left scratch behind: %v", names)
	}

	r.Record("info", "server", "after prune")
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(final), "\tafter prune\n") {
		t.Fatalf("the recorder stopped appending to the rewritten log: %q", final)
	}
	if strings.Contains(string(final), "old scan") {
		t.Fatalf("expired line returned: %q", final)
	}
}

// TestPruneFilesAbandonsWhenTheLogRotatesUnderIt forces rotation between the
// snapshot stat and scanning, and requires the original bytes to survive.
func TestPruneFilesAbandonsWhenTheLogRotatesUnderIt(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var r *Recorder
	var armed atomic.Bool
	clock := func() time.Time {
		if armed.CompareAndSwap(true, false) {
			// Fill the active file so this record rotates the snapshot the
			// retention pass is about to read. Spaces keep the filler out of
			// the long-token redaction.
			r.Record("info", "server", strings.Repeat("rotate line ", 400))
		}
		return now
	}
	r = New(Options{Directory: directory, Capacity: 100, FileBytes: 4096, FileCount: 2, Now: clock})
	defer r.Close()
	r.SetLevel("debug")
	r.Record("info", "scan", "expired scan")
	r.Record("info", "server", "server kept")
	now = now.Add(48 * time.Hour)
	r.SetRetention(map[string]int{"scan": 1})
	armed.Store(true)
	r.PruneFiles()

	rotated, err := os.ReadFile(filepath.Join(directory, "messages.1.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rotated), "expired scan") || !strings.Contains(string(rotated), "server kept") {
		t.Fatalf("rotation-abandoned snapshot was modified: %q", rotated)
	}
	if !strings.Contains(string(rotated), strings.Repeat("rotate line ", 400)) {
		t.Fatalf("the record that rotated the log is missing: %d bytes", len(rotated))
	}
	active, err := os.ReadFile(filepath.Join(directory, "messages.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("the retention pass wrote across the rotated path: %q", active)
	}
	if names := logDirectoryNames(t, directory); len(names) != 2 || names[0] != "messages.1.log" || names[1] != "messages.log" {
		t.Fatalf("scratch left after abandonment: %v", names)
	}
}

// TestPruneFilesAbandonsWhenTheLogIsAppendedUnderIt forces an append between the
// snapshot stat and scanning, and requires the original expired line to survive
// because publication no longer matches the snapshot.
func TestPruneFilesAbandonsWhenTheLogIsAppendedUnderIt(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var r *Recorder
	var armed atomic.Bool
	clock := func() time.Time {
		if armed.CompareAndSwap(true, false) {
			r.Record("info", "server", "late append")
		}
		return now
	}
	r = New(Options{Directory: directory, Capacity: 100, FileBytes: 1 << 20, FileCount: 2, Now: clock})
	defer r.Close()
	r.SetLevel("debug")
	r.Record("info", "scan", "expired scan")
	r.Record("info", "server", "server kept")
	now = now.Add(48 * time.Hour)
	r.SetRetention(map[string]int{"scan": 1})
	armed.Store(true)
	r.PruneFiles()

	raw, err := os.ReadFile(filepath.Join(directory, "messages.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "expired scan") || !strings.Contains(string(raw), "server kept") || !strings.Contains(string(raw), "late append") {
		t.Fatalf("append-abandoned snapshot was modified: %q", raw)
	}
	if names := logDirectoryNames(t, directory); len(names) != 1 || names[0] != "messages.log" {
		t.Fatalf("scratch left after abandonment: %v", names)
	}
}
