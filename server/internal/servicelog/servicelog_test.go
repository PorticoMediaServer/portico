package servicelog

import (
	"archive/zip"
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderFiltersByLevelAndPagesByCursor(t *testing.T) {
	r := New(Options{Capacity: 10})
	r.SetLevel("debug")
	for _, level := range []string{"error", "warn", "info", "debug"} {
		r.Record(level, "server", level+" message")
	}
	all, err := r.Read(Query{Level: "debug"})
	if err != nil || len(all.Items) != 4 || all.Items[0].Level != "debug" {
		t.Fatalf("newest first: %v %+v", err, all)
	}
	warnings, err := r.Read(Query{Level: "warn"})
	if err != nil || len(warnings.Items) != 2 {
		t.Fatalf("a level filter names the least severe level to include: %v %+v", err, warnings)
	}
	page, err := r.Read(Query{Level: "debug", Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("first page: %v %+v", err, page)
	}
	next, err := r.Read(Query{Level: "debug", Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 2 || next.Items[0].Sequence >= page.Items[1].Sequence {
		t.Fatalf("second page overlapped: %v %+v", err, next)
	}
	if _, err = r.Read(Query{Level: "verbose"}); err == nil {
		t.Fatal("an unknown level was accepted")
	}
	if _, err = r.Read(Query{Limit: 9999}); err == nil {
		t.Fatal("an oversized limit was accepted")
	}
}

func TestConfiguredLevelDropsQuieterRecordsAndTheRingIsBounded(t *testing.T) {
	r := New(Options{Capacity: 3})
	r.SetLevel("warn")
	r.Record("debug", "server", "dropped")
	r.Record("error", "server", "kept")
	page, _ := r.Read(Query{Level: "debug"})
	if len(page.Items) != 1 || page.Items[0].Message != "kept" {
		t.Fatalf("level threshold: %+v", page)
	}
	r.SetLevel("debug")
	for i := range 5 {
		r.Record("info", "server", string(rune('a'+i)))
	}
	page, _ = r.Read(Query{Level: "debug"})
	if len(page.Items) != 3 || page.Items[0].Message != "e" {
		t.Fatalf("the ring must drop its oldest records: %+v", page)
	}
}

func TestDebugWindowRaisesTheLevelThenReverts(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	r := New(Options{Capacity: 10, Now: func() time.Time { return now }})
	r.SetLevel("info")
	r.OpenDebugWindow(now.Add(10 * time.Minute))
	if level, until := r.Effective(); level != "debug" || until.IsZero() {
		t.Fatalf("window open: %q %v", level, until)
	}
	r.Record("debug", "server", "inside the window")
	now = now.Add(11 * time.Minute)
	if level, until := r.Effective(); level != "info" || !until.IsZero() {
		t.Fatalf("window must revert on its own: %q %v", level, until)
	}
	r.Record("debug", "server", "outside the window")
	page, _ := r.Read(Query{Level: "debug"})
	if len(page.Items) != 1 || page.Items[0].Message != "inside the window" {
		t.Fatalf("records: %+v", page)
	}
}

func TestStandardLogOutputIsClassifiedAndTeedToStderr(t *testing.T) {
	r := New(Options{Capacity: 10})
	r.SetLevel("debug")
	var also bytes.Buffer
	logger := log.New(r.Tee(&also), "", log.LstdFlags)
	logger.Printf("warn: [playback] the encoder stalled")
	logger.Printf("plain server line")
	page, _ := r.Read(Query{Level: "debug"})
	if len(page.Items) != 2 {
		t.Fatalf("records: %+v", page)
	}
	if page.Items[1].Level != "warn" || page.Items[1].Category != "playback" || page.Items[1].Message != "the encoder stalled" {
		t.Fatalf("prefixes were not read: %+v", page.Items[1])
	}
	if page.Items[0].Level != "info" || page.Items[0].Category != "server" {
		t.Fatalf("an unprefixed line must default to info/server: %+v", page.Items[0])
	}
	if !strings.Contains(also.String(), "plain server line") {
		t.Fatal("stderr lost the line")
	}
}

func TestFilesRotateAndStayBoundedAcrossPlatforms(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Capacity: 10, Directory: dir, FileBytes: 200, FileCount: 2})
	r.SetLevel("debug")
	for range 40 {
		r.Record("info", "server", strings.Repeat("x", 60))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	files := r.Files()
	if len(files) < 2 || len(files) > 3 {
		t.Fatalf("rotation kept %d files: %v", len(files), files)
	}
	total := int64(0)
	for _, name := range files {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		// Each file is capped near FileBytes; one record may overshoot it, but a
		// file that grew without bound would be far larger than this.
		if info.Size() > 2000 {
			t.Fatalf("%s grew to %d bytes", filepath.Base(name), info.Size())
		}
		total += info.Size()
	}
	if total == 0 {
		t.Fatal("rotation lost every line")
	}
	// A rotated file is only created by a write that filled it, so it always
	// has content and Tail can read it.
	rotated := New(Options{Directory: dir})
	defer rotated.Close()
	if lines := rotated.Tail(5); len(lines) == 0 && total > 0 {
		// The active file may have just rotated to empty; the rotated copy must
		// still hold the history.
		if info, err := os.Stat(filepath.Join(dir, "messages.1.log")); err != nil || info.Size() == 0 {
			t.Fatalf("no rotated history: %v", err)
		}
	}
}

func TestSubscribersSeeNewRecordsAndDoNotStallTheServer(t *testing.T) {
	r := New(Options{Capacity: 10})
	r.SetLevel("debug")
	records, cancel := r.Subscribe("warn")
	r.Record("debug", "server", "below the subscription")
	r.Record("error", "server", "above it")
	select {
	case record := <-records:
		if record.Message != "above it" {
			t.Fatalf("a subscriber saw a record below its level: %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("no record arrived")
	}
	// A subscriber that never reads must not block Record.
	done := make(chan struct{})
	go func() {
		for range 1000 {
			r.Record("error", "server", "flood")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a slow subscriber stalled the recorder")
	}
	cancel()
	if _, open := <-records; open {
		// Drain whatever was buffered; the channel must eventually close.
		for range records {
		}
	}
}

func TestRetentionPrunesByCategory(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	r := New(Options{Capacity: 10, Now: func() time.Time { return now }})
	r.SetLevel("debug")
	r.Record("info", "scan", "old scan line")
	r.Record("info", "server", "old server line")
	now = now.Add(48 * time.Hour)
	r.SetRetention(map[string]int{"scan": 1})
	page, _ := r.Read(Query{Level: "debug"})
	if len(page.Items) != 1 || page.Items[0].Category != "server" {
		t.Fatalf("retention pruned the wrong category: %+v", page)
	}
}

func TestBundleCarriesTheDocumentsAndStopsAtItsCap(t *testing.T) {
	var out bytes.Buffer
	truncated, err := WriteBundle(&out, BundleInputs{Settings: map[string]any{"name": "Portico"}, Report: map[string]any{"schemaVersion": 1},
		Connectivity: map[string]any{"observedAt": "now"}, Records: []Record{{Sequence: 1, Level: "info", Message: "hello"}}})
	if err != nil || truncated {
		t.Fatalf("bundle: %v %v", err, truncated)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"manifest.json": false, "settings.json": false, "system-report.json": false, "connectivity.json": false, "messages.json": false}
	for _, file := range archive.File {
		want[file.Name] = true
	}
	for name, present := range want {
		if !present {
			t.Fatalf("bundle is missing %s", name)
		}
	}
	// A writer that refuses everything past a tiny cap stops rather than
	// producing an archive larger than the ceiling.
	counter := &countingWriter{to: io.Discard, cap: 32}
	if _, err = counter.Write(make([]byte, 64)); err == nil || !counter.truncated {
		t.Fatal("the cap did not stop an oversized write")
	}
}

func TestFileRetentionAndPreviousRunRedaction(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	directory := t.TempDir()
	recorder := New(Options{Directory: directory, Now: func() time.Time { return now }})
	recorder.Record("info", "scan", "old scan")
	recorder.Record("info", "server", "Bearer private-secret")
	now = now.Add(48 * time.Hour)
	recorder.SetRetention(map[string]int{"scan": 1})
	recorder.PruneFiles()
	recorder.Close()
	raw, err := os.ReadFile(filepath.Join(directory, "messages.log"))
	if err != nil || strings.Contains(string(raw), "old scan") || strings.Contains(string(raw), "private-secret") {
		t.Fatal(string(raw), err)
	}
	reopened := New(Options{Directory: directory, Now: func() time.Time { return now }})
	defer reopened.Close()
	page, err := reopened.Read(Query{})
	if err != nil || len(page.Items) != 1 || !strings.Contains(page.Items[0].Message, "REDACTED") {
		t.Fatal(page, err)
	}
}
