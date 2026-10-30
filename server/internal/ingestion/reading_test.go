package ingestion

import (
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
	"testing"
)

// This tests the real scanner/attachment queue, using the installed baseline.
// It must fail if canonical Reading kinds/schema have not reached main; it
// neither installs substitute tables nor turns a static book into a movie.
func TestReadingInventoryUsesStaticKindsAndNeverTimedProbe(t *testing.T) {
	for _, tc := range []struct{ library, extension, kind string }{{"book", "epub", "ebook"}, {"book", "pdf", "ebook"}, {"comic", "cbz", "comic"}, {"comic", "pdf", "comic"}} {
		t.Run(tc.library+"-"+tc.extension, func(t *testing.T) {
			f := newLoopFixture(t, tc.library)
			f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
			var content []byte
			switch tc.extension {
			case "epub":
				content = readingtest.EPUB()
			case "pdf":
				content = readingtest.PDF()
			case "cbz":
				content = readingtest.CBZ()
			}

			path := filepath.Join(f.root, "Original document."+tc.extension)
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			job := f.queue()
			state := f.finish(job)
			if state.Warnings != 0 || state.Analyzed != 0 {
				t.Fatalf("static publication treated as timed media: %+v", state)
			}
			var kind, format, title string
			var files int
			err := f.db.QueryRow(`SELECT k.name,p.format,e.title,count(*) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_asset_links l ON l.entity_id=e.id AND l.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id WHERE a.path=?`, path).Scan(&kind, &format, &title, &files)
			if err != nil || kind != tc.kind || format != tc.extension || title != "Original document" || files != 1 {
				t.Fatalf("actual attachment %s/%s/%s/%d: %v", kind, format, title, files, err)
			}
			if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=?`, job.ID); n != 0 {
				t.Fatalf("static files enqueued for ffprobe: %d", n)
			}
			if n := f.count(`SELECT count(*) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id=? AND e.kind IN(1,8,9)`, f.library); n != 0 {
				t.Fatal("movie/audio entity produced", n)
			}
		})
	}
}
