package ingestion

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// TestSonicCurrentnessIsReadBeforeTheWriter is ENQUEUE1 R2: a sonic page's
// binding reads (up to 4097 track rows per object) run in the read-only
// pre-pass, and the writer that plans the same window again reads none of
// them, while the page still queues exactly what the in-writer check queued.
func TestSonicCurrentnessIsReadBeforeTheWriter(t *testing.T) {
	f := newLoopFixture(t, "movie", "One (2000).mkv", "Two (2001).mkv", "Three (2002).mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='custom',operations_json='["probe","sonic"]' WHERE library_id=?`, f.library)
	f.markObjectsSettled([]string{"probe", "sonic", assets.PlaybackAnalysisRevision})
	ids := objectIDs(t, f.db, f.source)
	job := f.analysisOnlyJob()
	f.s.DeepAnalysis = newSonicCapableAnalysis(t, f.db)
	ctx, statements := dbwork.TraceQueryPlans(context.Background())
	if err := f.s.enqueueAnalysisPage(ctx, job.ID, f.librarySource()); err != nil {
		t.Fatal(err)
	}
	window, bindings := 0, 0
	for _, statement := range statements() {
		switch {
		case strings.Contains(statement, "INDEXED BY inventory_objects_source WHERE o.source_id=? AND o.retired=0 AND o.id>?"):
			window++
		case strings.Contains(statement, "e.kind=7"):
			bindings++
			if window != 1 {
				t.Fatalf("a sonic binding read ran after the writer planned its window (window read %d)", window)
			}
		}
	}
	if window != 2 || bindings != len(ids) {
		t.Fatalf("window reads=%d (want pre-pass + writer), binding reads=%d (want %d)", window, bindings, len(ids))
	}
	if got := f.queued(job.ID); !equalIDs(got, ids) {
		t.Fatalf("queued=%v, want every settled row the pre-pass found stale %v", got, ids)
	}
}

func TestSonicAnswersApplyOnlyToTheirWindowPolicyAndRuntime(t *testing.T) {
	a := sonicAnswers{taken: true, cursor: "c", policyRevision: 3, runtime: "r"}
	for _, tc := range []struct {
		cursor   string
		revision int64
		runtime  string
		want     bool
	}{{"c", 3, "r", true}, {"d", 3, "r", false}, {"c", 4, "r", false}, {"c", 3, "swapped", false}} {
		if got := a.usable(tc.cursor, tc.revision, tc.runtime); got != tc.want {
			t.Fatalf("usable(%q,%d,%q)=%v", tc.cursor, tc.revision, tc.runtime, got)
		}
	}
	if (sonicAnswers{}).usable("", 0, "") {
		t.Fatal("answers that were never taken were usable")
	}
}
