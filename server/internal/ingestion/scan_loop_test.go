package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
	"time"
)

type loopFixture struct {
	t                     *testing.T
	db                    *sql.DB
	s                     *Service
	root, library, source string
}

func newLoopFixture(t *testing.T, kind string, files ...string) *loopFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalog.New(db)
	lib, err := c.Create("Library", kind, root)
	if err != nil {
		t.Fatal(err)
	}
	f := &loopFixture{t: t, db: db, s: New(db, c, assets.Probe{}), root: root, library: lib.ID}
	if err = db.QueryRow(`SELECT id FROM library_sources WHERE library_id=?`, lib.ID).Scan(&f.source); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID)
	for _, file := range files {
		f.write(file, "media")
	}
	return f
}
func (f *loopFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *loopFixture) write(relative, content string) {
	f.t.Helper()
	path := filepath.Join(f.root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *loopFixture) count(query string, args ...any) int {
	f.t.Helper()
	var count int
	if err := f.db.QueryRow(query, args...).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}
func (f *loopFixture) queue() Job {
	f.t.Helper()
	job, err := f.s.Queue(f.library)
	if err != nil {
		f.t.Fatal(err)
	}
	return job
}
func (f *loopFixture) finish(job Job) Job {
	f.t.Helper()
	for n := 0; n < 200; n++ {
		scanInfoTurn(f.t, f.s, f.db, job.ID, f.library)
		state, err := f.s.Get(job.ID)
		if err != nil {
			f.t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			return state
		}
		if state.Status == "failed" {
			f.t.Fatalf("failed scan: %+v", state)
		}
	}
	f.t.Fatal("scan did not finish")
	return Job{}
}
func (f *loopFixture) until(job Job, phase string) {
	f.t.Helper()
	for n := 0; n < 200; n++ {
		state, err := f.s.Get(job.ID)
		if err != nil {
			f.t.Fatal(err)
		}
		if state.Phase == phase {
			return
		}
		if state.Status == "failed" {
			f.t.Fatal(state)
		}
		f.s.processQuantum(context.Background(), job.ID, f.library)
		advanceScanInfo(f.t, f.db)
	}
	f.t.Fatalf("scan did not reach %s", phase)
}

func TestScanLoopUsesSourceRelativeNamesVersionsAndParts(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alien (1979) {tmdb-348}/movie.mkv", "Alien (1979) {tmdb-348}/Alien (1979) {edition-Extended} - pt1.mkv", "Alien (1979) {tmdb-348}/Alien (1979) {edition-Extended} - pt2.mkv", "Conflicting (2000) {tmdb-2}/Heat (1995).mkv")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1`); n != 2 {
		t.Fatalf("titles: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id WHERE e.title='Alien'`); n != 3 {
		t.Fatalf("versions: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links WHERE part_index IN(1,2) AND edition='Extended'`); n != 2 {
		t.Fatalf("parts/editions: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_external_ids x JOIN catalog_entities e ON e.id=x.entity_id WHERE e.title='Heat' AND x.provider='tmdb'`); n != 0 {
		t.Fatal("conflicting folder identity overrode the filename")
	}
}
func TestScanLoopEpisodesKeepFileSeasonZeroAndParts(t *testing.T) {
	f := newLoopFixture(t, "tv", "Show (2020)/Season 1/Show - S02E00 - pt1.mkv", "Show (2020)/Season 1/Show - S02E00 - pt2.mkv", "Show (2020)/Staffel 3/01.mkv")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM catalog_shows`); n != 1 {
		t.Fatalf("shows: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM catalog_episodes ep JOIN catalog_seasons s ON s.entity_id=ep.season_id WHERE ep.number=0 AND s.number=2`); n != 1 {
		t.Fatal("file season or episode zero lost")
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links WHERE part_index IN(1,2)`); n != 2 {
		t.Fatal("episode parts lost")
	}
	if n := f.count(`SELECT count(*) FROM episodic_sources WHERE issue<>''`); n != 0 {
		t.Fatal("valid episode evidence rejected")
	}
}
func TestScanLoopExtrasBelongToOneTitleAndDiscsAreOneEntry(t *testing.T) {
	f := newLoopFixture(t, "movie", "Alpha (2000).mkv", "Beta (2001).mkv", "Alpha (2000)-trailer.mkv", "Disc Film (2002)/BDMV/STREAM/00001.m2ts", "Disc Film (2002)/BDMV/STREAM/00002.m2ts")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM item_extras x JOIN catalog_entities e ON e.id=x.owner_id WHERE e.title='Alpha'`); n != 1 {
		t.Fatal("loose trailer did not attach to its title")
	}
	// A host that can mount a directory descriptor publishes the folder holding
	// BDMV as one title; any other host keeps the layout explicitly unsupported.
	disc, reason := "Disc Film (2002)", ""
	if !storage.DirectoryDiscScanSupported() {
		disc, reason = "Disc Film (2002)/BDMV", "unsupported_disc_layout"
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE 'Disc Film (2002)%'`); n != 1 {
		t.Fatalf("disc folder was not one logical entry: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path=? AND unsupported_reason=?`, disc, reason); n != 1 {
		t.Fatalf("disc entry is not %q with reason %q", disc, reason)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE '%STREAM%'`); n != 0 {
		t.Fatal("disc streams became individual titles")
	}
}
func TestScanLoopDiscFolderOutsideAVideoLibraryIsOneUnsupportedEntry(t *testing.T) {
	f := newLoopFixture(t, "music", "Album/01.flac", "Concert (2002)/BDMV/STREAM/00001.m2ts")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path LIKE 'Concert (2002)%'`); n != 1 {
		t.Fatalf("disc folder was not one entry: %d", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='Concert (2002)/BDMV' AND unsupported_reason='unsupported_disc_layout'`); n != 1 {
		t.Fatal("disc layout in a music library was not recorded as unsupported")
	}
}
func TestScanLoopToleratesDanglingAndEscapingLinks(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	for _, pair := range [][2]string{{"missing", filepath.Join(f.root, "absent")}, {"escape", t.TempDir()}} {
		if err := os.Symlink(pair[1], filepath.Join(f.root, pair[0])); err != nil {
			t.Skip(err)
		}
	}
	f.exec(`UPDATE library_sources SET follow_symlinks=1 WHERE id=?`, f.source)
	state := f.finish(f.queue())
	if state.Status != "complete_with_warnings" {
		t.Fatalf("state %+v", state)
	}
	if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE protect=1 AND reason IN('symlink_unreadable','symlink_outside_root')`); n != 2 {
		t.Fatal("link diagnostics missing")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects`); n != 1 {
		t.Fatal("safe file dropped or escaping file catalogued")
	}
}
func TestScanLoopProtectsUnreadableSubtreeButContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission isolation needs a non-root user")
	}
	f := newLoopFixture(t, "movie", "Unreadable/Kept (2000).mkv", "Healthy/Gone (2001).mkv", "Healthy/Remaining (2002).mkv")
	f.finish(f.queue())
	if err := os.Remove(filepath.Join(f.root, "Healthy/Gone (2001).mkv")); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(f.root, "Unreadable")
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	state := f.finish(f.queue())
	if state.Status != "complete_with_warnings" {
		t.Fatalf("state: %+v", state)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='Unreadable/Kept (2000).mkv' AND state='available'`); n != 1 {
		t.Fatal("unreadable subtree marked missing")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='Healthy/Gone (2001).mkv' AND state='missing'`); n != 1 {
		t.Fatal("healthy subtree did not reconcile")
	}
}
func TestScanLoopKeepsChangedFolderAndStillAnalyzesOthers(t *testing.T) {
	f := newLoopFixture(t, "movie", "Changing/Keep (2000).mkv", "Safe/Gone (2001).mkv", "Safe/Remaining (2002).mkv")
	f.finish(f.queue())
	for _, path := range []string{"Changing/Keep (2000).mkv", "Safe/Gone (2001).mkv"} {
		if err := os.Remove(filepath.Join(f.root, path)); err != nil {
			t.Fatal(err)
		}
	}
	job := f.queue()
	f.until(job, "verifying")
	f.write("Changing/new.nfo", "<movie/>")
	state := f.finish(job)
	if state.Status != "complete_with_warnings" {
		t.Fatal(state)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='Changing/Keep (2000).mkv' AND state='available'`); n != 1 {
		t.Fatal("changed folder judged absent")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE relative_path='Safe/Gone (2001).mkv' AND state='missing'`); n != 1 {
		t.Fatal("stable folder not reconciled")
	}
	if n := f.count(`SELECT count(*) FROM inventory_path_notices WHERE source_id=? AND relative_path='Changing'`, f.source); n != 1 {
		t.Fatal("changed folder was not scheduled for another pass")
	}
}
func TestScanLoopChecksInPlaceChangesAndOnlyQueuesChangedEvidence(t *testing.T) {
	f := newLoopFixture(t, "movie", "One (2000)/One (2000).mkv", "Two (2001)/Two (2001).mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe","local_metadata","subtitles"]' WHERE library_id=?`, f.library)
	completed, _ := json.Marshal([]string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision})
	mark := func() {
		f.exec(`UPDATE inventory_objects SET analysis_revision=revision,analysis_policy=(SELECT revision FROM library_scan_policies WHERE library_id=?),analysis_operations_json=?,analysis_generation=(SELECT generation FROM library_sources WHERE id=source_id),analysis_algorithm=?,analysis_state='complete'`, f.library, string(completed), mediaanalysis.Algorithm)
	}
	mark()
	unchanged := f.queue()
	f.finish(unchanged)
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=?`, unchanged.ID); n != 0 {
		t.Fatalf("unchanged files queued: %d", n)
	}
	f.write("One (2000)/One (2000).nfo", "<movie><title>One</title></movie>")
	changed := f.queue()
	f.until(changed, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, changed.ID); n != 1 {
		t.Fatalf("added sidecar queued %d files", n)
	}
	f.finish(changed)
	mark()
	f.write("One (2000)/One (2000).nfo", "<movie><title>New title</title></movie>")
	changed = f.queue()
	f.until(changed, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, changed.ID); n != 1 {
		t.Fatal("edited sidecar missed")
	}
	f.finish(changed)
	mark()
	if err := os.Remove(filepath.Join(f.root, "One (2000)/One (2000).nfo")); err != nil {
		t.Fatal(err)
	}
	changed = f.queue()
	f.until(changed, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, changed.ID); n != 1 {
		t.Fatal("removed sidecar missed")
	}
	f.finish(changed)
	mark()
	f.write("Two (2001)/Two (2001).mkv", "new bytes written in place")
	changed = f.queue()
	f.until(changed, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, changed.ID); n != 1 {
		t.Fatal("in-place media edit missed")
	}
}
func TestScanLoopProbeFailureDoesNotStopDuringPlayback(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	f.exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) SELECT 'playing','session','account','profile',l.entity_id,a.token,1,'playing','grant','grant',?,'request',100 FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id LIMIT 1`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	started := time.Now()
	state := f.finish(f.queue())
	if time.Since(started) > 5*time.Second {
		t.Fatal("analysis waited on playback")
	}
	if state.Status != "complete_with_warnings" {
		t.Fatal(state)
	}
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1`); n != 1 {
		t.Fatal("unprobeable file dropped")
	}
	if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE reason='probe_or_sidecar_failed'`); n != 1 {
		t.Fatal("probe failure not reported")
	}
}
func TestScanLoopIgnoreFilesApplyAndReconcileOldFiles(t *testing.T) {
	f := newLoopFixture(t, "movie", "Good (2000).mkv", "Skip/Bad (2001).mkv", "Nested/Keep (2002).mkv", "Nested/Skipped (2003).mkv")
	f.finish(f.queue())
	f.write(".plexignore", "Skip/\n[broken\n")
	f.write("Nested/.ignore", "Skipped*.mkv\n")
	job := f.queue()
	f.finish(job)
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 2 {
		t.Fatalf("excluded old files: %d", n)
	}
	issues, err := f.s.Issues(context.Background(), f.library, job.ID, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues.Items {
		if issue.Reason == "invalid_ignore_pattern" {
			t.Fatal("log-only ignore diagnostic leaked into Files skipped")
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM inventory_scan_issues WHERE job_id=? AND reason='invalid_ignore_pattern' AND line=2`, job.ID); n != 1 {
		t.Fatalf("ignore diagnostic was not retained for logs: %d", n)
	}
}
func TestScanLoopAliasesStaySafeAcrossTwoScans(t *testing.T) {
	f := newLoopFixture(t, "movie", "Real/Film (2000).mkv")
	if err := os.Symlink(filepath.Join(f.root, "Real"), filepath.Join(f.root, "Alias")); err != nil {
		t.Skip(err)
	}
	f.exec(`UPDATE library_sources SET follow_symlinks=1 WHERE id=?`, f.source)
	f.finish(f.queue())
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE retired=0 AND state='available'`); n != 1 {
		t.Fatal("alias duplicated or lost the physical file")
	}
}

func TestReportedLastFileRemovalIsImmediateInAReadableRoot(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	path := filepath.Join(f.root, "Film (2000).mkv")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.ReportPaths(context.Background(), []string{path}, nil)
	if err != nil || len(result.Jobs) != 1 {
		t.Fatalf("report: %+v %v", result, err)
	}
	f.finish(result.Jobs[0])
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 1 {
		t.Fatal("last file was not marked missing immediately")
	}
}

func TestScannedEmbeddedNameReachesAttachmentAndMatchingEvidence(t *testing.T) {
	f := newLoopFixture(t, "movie", "Wrong Folder (2000)/movie.mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	job := f.queue()
	f.until(job, "basic_pending")
	var object, revision string
	var size, modified int64
	if err := f.db.QueryRow(`SELECT id,revision,size,modified_ns FROM inventory_objects LIMIT 1`).Scan(&object, &revision, &size, &modified); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 6000, ObservedSize: size, ObservedModifiedNS: modified, AnalysisOperations: []string{"probe"}, AnalysisAlgorithm: mediaanalysis.Algorithm, VideoName: naming.Embedded{Title: "Heat", Year: 1995, Edition: "Extended", IDs: []naming.ProviderID{{Provider: "tmdb", ID: "949"}}}}
	if err := settleInventoryAnalysis(t, f.s.catalog, f.db, context.Background(), job.ID, object, revision, 1, facts); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND title='Heat' AND year=1995`); n != 1 {
		t.Fatal("embedded name did not reach its title")
	}
	if n := f.count(`SELECT count(*) FROM catalog_asset_links WHERE edition='Extended'`); n != 1 {
		t.Fatal("embedded edition lost")
	}
	if n := f.count(`SELECT count(*) FROM inventory_video_ids WHERE provider='tmdb' AND provider_id='949'`); n != 1 {
		t.Fatal("embedded provider evidence lost")
	}
}

func TestEditedIgnoreRulesRetireCachedFoldersWithoutDirectoryTimeChanges(t *testing.T) {
	f := newLoopFixture(t, "movie", "Skip/Film (2000).mkv")
	f.write(".plexignore", "# no exclusions\n")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_directory_cache WHERE relative_path='Skip'`); n != 1 {
		t.Fatal("fixture cache missing")
	}
	f.write(".plexignore", "Skip/\n")
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_directory_cache WHERE relative_path='Skip'`); n != 0 {
		t.Fatal("old excluded folder stayed cached")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != 1 {
		t.Fatal("ignored old media was not reconciled")
	}
}
func TestScanOnlyRequeuesMissingDeepHeads(t *testing.T) {
	f := newLoopFixture(t, "movie", "One (2000).mkv", "Two (2001).mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='custom',operations_json='["probe","checksum"]' WHERE library_id=?`, f.library)
	done, _ := json.Marshal([]string{"probe", "checksum", assets.PlaybackAnalysisRevision})
	f.exec(`UPDATE inventory_objects SET analysis_revision=revision,analysis_policy=1,analysis_operations_json=?,analysis_algorithm=?,analysis_generation=(SELECT generation FROM library_sources WHERE id=source_id),analysis_state='complete'`, string(done), mediaanalysis.Algorithm)
	f.exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) SELECT 'head:'||id,id,asset_id,revision,root_incarnation,(SELECT generation FROM library_sources WHERE id=source_id),1,'checksum',?,'e','digest',1,'{}',1 FROM inventory_objects`, mediaanalysis.Algorithm)
	f.exec(`INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) SELECT id,revision,'checksum','head:'||id FROM inventory_objects`)
	f.exec(`DELETE FROM analysis_heads WHERE object_id IN(SELECT id FROM inventory_objects WHERE relative_path='One (2000).mkv')`)
	job := f.queue()
	f.until(job, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='pending'`, job.ID); n != 1 {
		t.Fatalf("missing head queued %d files", n)
	}
}

func TestSelectiveEnqueueAdvancesPastAnUnchangedCandidatePage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	files := []string{}
	for n := 0; n < 40; n++ {
		files = append(files, fmt.Sprintf("Film %d (2000).mkv", n))
	}
	f := newLoopFixture(t, "movie", files...)
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe","local_metadata","subtitles"]' WHERE library_id=?`, f.library)
	completed, _ := json.Marshal([]string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision})
	f.exec(`UPDATE inventory_objects SET analysis_revision=revision,analysis_policy=(SELECT revision FROM library_scan_policies WHERE library_id=?),analysis_operations_json=?,analysis_algorithm=?,analysis_state='complete',analysis_generation=(SELECT generation FROM library_sources WHERE id=source_id)`, f.library, string(completed), mediaanalysis.Algorithm)
	f.exec(`UPDATE inventory_objects SET analysis_revision='' WHERE id=(SELECT id FROM inventory_objects ORDER BY id DESC LIMIT 1)`)
	job := f.queue()
	f.until(job, "basic_pending")
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=?`, job.ID); n != 1 {
		t.Fatalf("selective candidate pages queued %d files", n)
	}
}
