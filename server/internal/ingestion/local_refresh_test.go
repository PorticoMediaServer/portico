package ingestion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// TestSidecarOnlyChangeRefreshesWithoutAProbe is S6: once a video file's probe
// is recorded for its revision, adding a subtitle sidecar re-reads its local
// metadata and subtitles and publishes them, without probing the file again.
// The fixture's probe cannot run at all, so any probe attempt would end the
// scan with a probe warning. The stored technical facts and the embedded track
// survive, and the new sidecar subtitle is discovered.
func TestSidecarOnlyChangeRefreshesWithoutAProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "movie", "Film (2000)/Film (2000).mkv")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	f.s.SetStorage(store)
	if f.s.LocalMetadata, err = localmetadata.New(t.TempDir(), "", store); err != nil {
		t.Fatal(err)
	}
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe","local_metadata","subtitles"]' WHERE library_id=?`, f.library)
	// What a successful probe of this revision left behind.
	var asset string
	var size, modified int64
	if err = f.db.QueryRow(`SELECT asset_id,size,modified_ns FROM inventory_objects WHERE source_id=? AND retired=0`, f.source).Scan(&asset, &size, &modified); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE catalog_assets SET container='matroska',video_codec='h264',audio_codec='aac',width=1920,height=1080,duration=60 WHERE token=?`, asset)
	f.exec(`INSERT INTO asset_subtitle_facts VALUES(?,1,?,?,0,1,'known','probe')`, asset, size, modified)
	f.exec(`INSERT INTO asset_subtitles VALUES('embedded-2',?,'embedded','2',2,NULL,'subrip','eng','English',0,0,'',0)`, asset)
	f.markObjectsSettled([]string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision})

	f.write("Film (2000)/Film (2000).fr.srt", "1\n00:00:00,000 --> 00:00:02,000\nBonjour\n")
	state := f.finish(f.queue())
	if state.Status != "complete" {
		t.Fatalf("the sidecar refresh ended %+v; a probe attempt would warn", state)
	}
	if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=?`, state.ID); n != 0 {
		t.Fatalf("the refresh recorded %d scan issues", n)
	}
	var ops, codec string
	if err = f.db.QueryRow(`SELECT o.analysis_operations_json,a.video_codec FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.source_id=? AND o.retired=0`, f.source).Scan(&ops, &codec); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision} {
		if !strings.Contains(ops, `"`+op+`"`) {
			t.Fatalf("operations after the refresh %s lack %s", ops, op)
		}
	}
	if codec != "h264" {
		t.Fatalf("the refresh changed the stored technical facts: codec %q", codec)
	}
	if n := f.count(`SELECT count(*) FROM asset_subtitles WHERE asset_id=? AND origin='embedded' AND stream_index=2`, asset); n != 1 {
		t.Fatal("the stored embedded track was lost by the refresh")
	}
	if n := f.count(`SELECT count(*) FROM asset_subtitles WHERE asset_id=? AND origin='sidecar' AND locator LIKE ?`, asset, "%"+filepath.FromSlash("Film (2000).fr.srt")); n != 1 {
		t.Fatal("the new sidecar subtitle was not discovered")
	}
}
