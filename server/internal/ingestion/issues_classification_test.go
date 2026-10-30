package ingestion

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"testing"
)

func TestFilesSkippedExcludesDiagnosticsAndPlayableUnmatched(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	job := f.finish(f.queue())
	reasons := []string{"file_truncated", "unsupported_codec", "unfinished_download", "symlink_outside_root", "directory_changed", "ignored_name", "episode_unmatched", "probe_or_sidecar_failed"}
	for _, reason := range reasons {
		f.exec(`INSERT INTO inventory_scan_issues(job_id,relative_path,reason,warning) VALUES(?,?,?,1)`, job.ID, reason+".mkv", reason)
	}
	out, err := f.s.Issues(context.Background(), f.library, job.ID, 0, 2)
	if err != nil || out.Count != 3 || len(out.Items) != 2 || !out.HasMore {
		t.Fatalf("page %+v %v", out, err)
	}
	for _, issue := range out.Items {
		if issue.Category == "" || issue.MessageKey == "" || issue.Warning {
			t.Fatalf("classification %+v", issue)
		}
	}
	next, err := f.s.Issues(context.Background(), f.library, job.ID, out.NextSequence, 2)
	if err != nil || len(next.Items) != 1 || next.HasMore || next.Items[0].Category != "not-media" {
		t.Fatalf("next %+v %v", next, err)
	}
	if _, err = f.s.Issues(context.Background(), "other-library", job.ID, 0, 2); err == nil {
		t.Fatal("cross-library scan exposed")
	}
	if _, err = f.s.Issues(context.Background(), f.library, job.ID, -1, 2); err == nil {
		t.Fatal("invalid sequence accepted")
	}
}

func TestViewerLibrarySummaryFirstScanHasNoProgress(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	job := f.queue()
	cat := catalog.New(f.db)
	read := func() catalog.LibrarySummary {
		t.Helper()
		out, err := cat.SummarizeLibraries(context.Background(), catalog.Viewer{Libraries: []string{f.library}}, []catalog.Library{{ID: f.library, Kind: "movie"}})
		if err != nil {
			t.Fatal(err)
		}
		return out[f.library]
	}
	if out := read(); !out.FirstScan || !out.Empty || !out.EmptyKnown {
		t.Fatalf("queued first scan %+v", out)
	}
	f.finish(job)
	if out := read(); out.FirstScan || out.Empty {
		t.Fatalf("completed scan %+v", out)
	}
}
