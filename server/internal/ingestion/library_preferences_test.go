package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func TestLibraryFindExtrasStopsPublicationWithoutTouchingFiles(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv", "Film (2000)-trailer.mkv")
	f.exec(`INSERT INTO library_preferences(library_id,find_extras) VALUES(?,0)`, f.library)
	f.finish(f.queue())
	if f.count(`SELECT count(*) FROM item_extras`) != 0 || f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Film'`) != 1 {
		t.Fatal("extras preference did not affect scanned publication")
	}
	if f.count(`SELECT count(*) FROM inventory_objects`) != 2 {
		t.Fatal("source inventory facts were discarded")
	}
	for _, name := range []string{"Film (2000).mkv", "Film (2000)-trailer.mkv"} {
		data, err := os.ReadFile(filepath.Join(f.root, name))
		if err != nil || string(data) != "media" {
			t.Fatal("preference changed source bytes", name, err)
		}
	}
}

func TestLibraryFileNamesWinWithoutDiscardingEmbeddedEvidence(t *testing.T) {
	f := newLoopFixture(t, "movie", "Heat (1995).mkv")
	f.exec(`INSERT INTO library_preferences(library_id,title_source) VALUES(?,'names')`, f.library)
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	job := f.queue()
	f.until(job, "basic_pending")
	var object, revision string
	var size, modified int64
	if err := f.db.QueryRow(`SELECT id,revision,size,modified_ns FROM inventory_objects LIMIT 1`).Scan(&object, &revision, &size, &modified); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 6000, ObservedSize: size, ObservedModifiedNS: modified, AnalysisOperations: []string{"probe"}, AnalysisAlgorithm: mediaanalysis.Algorithm, VideoName: naming.Embedded{Title: "Wrong embedded title", Year: 2000}}
	if err := settleInventoryAnalysis(t, f.s.catalog, f.db, context.Background(), job.ID, object, revision, 1, facts); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Heat' AND year=1995`) != 1 || f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Wrong embedded title'`) != 0 {
		t.Fatal("embedded naming overrode the selected file name")
	}
	if f.count(`SELECT count(*) FROM inventory_video_names WHERE title='Wrong embedded title' AND year=2000`) != 1 {
		t.Fatal("embedded source facts were discarded")
	}
}
