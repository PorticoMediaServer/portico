package ingestion

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type audioRefreshFixture struct {
	*loopFixture
	object, asset, revision, relative, embeddedKey string
	size, modified, policy                         int64
}

func audioRefreshPNG(t *testing.T, red uint8) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: red, A: 255})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Seed through the real full-analysis commit with deterministic probe output.
// Subsequent scans have no probe executable and must reuse that baseline.
func newAudioRefreshFixture(t *testing.T, kind string, embeddedCue bool) *audioRefreshFixture {
	t.Helper()
	relative := "Artist/Album/part.m4b"
	if kind == "music" {
		relative = "Artist/Album/part.flac"
	}
	f := newLoopFixture(t, kind, relative)
	f.finish(f.queue())
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	f.s.SetStorage(store)
	f.s.probe.Binary = filepath.Join(f.root, "probe-unavailable")
	f.s.LocalMetadata, err = localmetadata.New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe","local_metadata","subtitles"]' WHERE library_id=?`, f.library)
	a := &audioRefreshFixture{loopFixture: f, relative: relative}
	if err = f.db.QueryRow(`SELECT id,asset_id,revision,size,modified_ns FROM inventory_objects WHERE source_id=? AND retired=0`, f.source).Scan(&a.object, &a.asset, &a.revision, &a.size, &a.modified); err != nil {
		t.Fatal(err)
	}
	job := f.analysisOnlyJob()
	f.runEnqueue(t, job, 5)
	p, err := f.s.catalog.ScanPolicy(context.Background(), f.library)
	if err != nil {
		t.Fatal(err)
	}
	a.policy = p.Revision
	facts := assets.Facts{Container: "mp4", AudioCodec: "aac", Duration: 60, ChapterStatus: "known", Chapters: []assets.Chapter{{Title: "Embedded opening", Start: 0, End: 30}, {Title: "Embedded ending", Start: 30, End: 60}}, Streams: []assets.Stream{{Index: 0, Type: "audio", Codec: "aac", Channels: 2, ChannelLayout: "stereo", Detail: assets.StreamDetail{SampleRate: 48000, BitsPerSample: 24}}}, EmbeddedAudio: &audiotags.Tags{Pictures: []audiotags.Picture{{Type: 3, Data: audioRefreshPNG(t, 50)}}}}
	if kind == "music" && embeddedCue {
		facts.EmbeddedAudio.CueTracks = []audiotags.Chapter{{Title: "Embedded cue one", Start: 0, End: 30}, {Title: "Embedded cue two", Start: 30, End: 60}}
	}
	assets.CopyEmbeddedAudioTags(&facts, map[string]string{"title": "Embedded title", "album": "Album", "artist": "Artist", "track": "1", "genre": "Jazz"})
	facts = f.s.LocalMetadata.PrepareKind(context.Background(), f.library, kind, f.root, filepath.Join(f.root, relative), facts)
	a.embeddedKey = facts.AudioFileMetadata.ArtworkKey
	facts.ObservedSize, facts.ObservedModifiedNS, facts.ObservedRevision = a.size, a.modified, a.revision
	facts.AnalysisOperations = []string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision}
	dbwork.ResetPlans()
	started := time.Now()
	if err = settleInventoryAnalysis(t, f.s.catalog, f.db, context.Background(), job.ID, a.object, a.revision, a.policy, facts); err != nil {
		t.Fatal(err)
	}
	stats := dbwork.Plans()
	t.Logf("per-file audio publication: %+v, elapsed %s", stats.Largest, time.Since(started))
	// FI1 stages and applies technical input in bounded transactions before
	// the file's existing atomic naming/artwork acknowledgement. Every
	// transaction must still obey its unchanged declared write plan.
	if stats.Measured < 2 || stats.Overruns != 0 {
		t.Fatalf("per-file audio publication exceeded its measured budget: %+v", stats)
	}
	f.finish(job)
	return a
}

func (f *audioRefreshFixture) stringQuery(query string, args ...any) string {
	f.t.Helper()
	var value string
	if err := f.db.QueryRow(query, args...).Scan(&value); err != nil {
		f.t.Fatal(err)
	}
	return value
}
func (f *audioRefreshFixture) technical() string {
	f.t.Helper()
	var result []string
	for _, query := range []string{
		`SELECT json_array(container,video_codec,audio_codec,width,height,duration,size,modified_ns) FROM catalog_assets WHERE token=?`,
		`SELECT json_group_array(json_array(stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json)) FROM asset_streams WHERE asset_id=? ORDER BY stream_index`,
		`SELECT json_array(revision,size,modified_ns,fingerprint,detail_version) FROM asset_stream_facts WHERE asset_id=?`,
		`SELECT json_group_array(json_array(codec,sample_rate,bit_depth,channels,lossless,size,modified_ns)) FROM music_technical_facts WHERE asset_id=?`,
	} {
		result = append(result, f.stringQuery(query, f.asset))
	}
	return strings.Join(result, "\n")
}
func (f *audioRefreshFixture) refresh() Job {
	f.t.Helper()
	state := f.finish(f.queue())
	rows, err := f.db.Query(`SELECT reason FROM inventory_scan_issues WHERE job_id=?`, state.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		if err = rows.Scan(&reason); err != nil {
			f.t.Fatal(err)
		}
		if reason != "analysis_worker_unavailable" {
			f.t.Fatalf("refresh attempted probe or failed: %s", reason)
		}
	}
	if err = rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	ops := f.stringQuery(`SELECT analysis_operations_json FROM inventory_objects WHERE id=?`, f.object)
	for _, op := range []string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision} {
		if !strings.Contains(ops, `"`+op+`"`) {
			f.t.Fatalf("missing %s: %s", op, ops)
		}
	}
	return state
}
func (f *audioRefreshFixture) assertCover(key string) {
	f.t.Helper()
	if n := f.count(`SELECT count(*) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_item_details d ON d.entity_id=l.entity_id WHERE a.token=? AND d.poster_url=?`, f.asset, "local:"+key); n == 0 {
		f.t.Fatalf("cover %s not projected", key)
	}
}

func TestAudioSidecarOnlyChangeRefreshesWithoutAProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"music", "audiobook"} {
		for _, embeddedCue := range []bool{false, true} {
			if kind == "audiobook" && embeddedCue {
				continue
			}
			t.Run(fmt.Sprintf("%s/embedded-cue=%t", kind, embeddedCue), func(t *testing.T) {
				f := newAudioRefreshFixture(t, kind, embeddedCue)
				technical := f.technical()
				f.assertCover(f.embeddedKey)
				stem := strings.TrimSuffix(f.relative, filepath.Ext(f.relative))
				metadata := f.relative + ".portico.json"
				f.write(metadata, `{"title":"Sidecar title","genre":"Rock"}`)
				f.write(stem+".cue", "FILE \""+filepath.Base(f.relative)+"\" WAVE\n TRACK 01 AUDIO\n TITLE \"Sidecar one\"\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n TITLE \"Sidecar two\"\n INDEX 01 00:15:00\n")
				cover := filepath.Join(filepath.Dir(f.relative), "cover.png")
				f.write(cover, string(audioRefreshPNG(t, 100)))
				f.refresh()
				if title := f.stringQuery(`SELECT value FROM audio_tag_evidence WHERE asset_id=? AND field='title'`, f.asset); title != "Sidecar title" {
					t.Fatal(title)
				}
				sideKey := f.stringQuery(`SELECT substr(d.poster_url,7) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_item_details d ON d.entity_id=l.entity_id WHERE a.token=? LIMIT 1`, f.asset)
				if sideKey == f.embeddedKey || sideKey == "" {
					t.Fatal("sidecar art did not replace embedded art")
				}
				if kind == "music" {
					if n := f.count(`SELECT count(*) FROM music_virtual_tracks WHERE asset_id=? AND start_seconds=15`, f.asset); n != 1 {
						t.Fatal("sidecar cue not applied")
					}
				} else {
					if title := f.stringQuery(`SELECT title FROM asset_chapters WHERE asset_id=? AND chapter_index=2`, f.asset); title != "Sidecar two" {
						t.Fatal(title)
					}
				}
				// Replacing an existing image invalidates the positive artwork cache.
				f.write(cover, string(audioRefreshPNG(t, 200)))
				f.refresh()
				newKey := f.stringQuery(`SELECT substr(d.poster_url,7) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_item_details d ON d.entity_id=l.entity_id WHERE a.token=? LIMIT 1`, f.asset)
				if newKey == sideKey || newKey == "" {
					t.Fatal("changed sidecar cover was cached")
				}
				for _, side := range []string{metadata, stem + ".cue", cover} {
					if err := os.Remove(filepath.Join(f.root, side)); err != nil {
						t.Fatal(err)
					}
				}
				f.refresh()
				if title := f.stringQuery(`SELECT value FROM audio_tag_evidence WHERE asset_id=? AND field='title'`, f.asset); title != "Embedded title" {
					t.Fatal(title)
				}
				if genre := f.stringQuery(`SELECT value FROM audio_tag_evidence WHERE asset_id=? AND field='genre'`, f.asset); genre != "Jazz" {
					t.Fatal(genre)
				}
				f.assertCover(f.embeddedKey)
				if kind == "music" {
					want := 0
					if embeddedCue {
						want = 2
					}
					if n := f.count(`SELECT count(*) FROM music_virtual_tracks WHERE asset_id=?`, f.asset); n != want {
						t.Fatalf("removed cue: %d want %d", n, want)
					}
				} else if title := f.stringQuery(`SELECT title FROM asset_chapters WHERE asset_id=? AND chapter_index=2`, f.asset); title != "Embedded ending" {
					t.Fatal(title)
				}
				if got := f.technical(); got != technical {
					t.Fatalf("technical facts changed:\n%s\nwant:\n%s", got, technical)
				}
			})
		}
	}
}

func TestAudioRefreshRequiresCurrentFileBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"music", "audiobook"} {
		for _, change := range []string{"media", "missing-baseline", "stale-baseline", "missing-playback", "missing-fallback"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				f := newAudioRefreshFixture(t, kind, false)
				switch change {
				case "media":
					f.write(f.relative, "changed media bytes")
				case "missing-baseline":
					f.exec(`DELETE FROM audio_file_metadata WHERE asset_id=?`, f.asset)
				case "stale-baseline":
					f.exec(`UPDATE audio_file_metadata SET source_revision='stale' WHERE asset_id=?`, f.asset)
				case "missing-fallback":
					file, _, err := f.s.LocalMetadata.Open(f.embeddedKey)
					if err != nil {
						t.Fatal(err)
					}
					path := file.Name()
					file.Close()
					if err = os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "missing-playback":
					f.exec(`UPDATE inventory_objects SET analysis_operations_json='["probe","local_metadata","subtitles"]' WHERE id=?`, f.object)
				}
				f.write(strings.TrimSuffix(f.relative, filepath.Ext(f.relative))+".nfo", `<music><title>Changed</title></music>`)
				state := f.finish(f.queue())
				if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND reason='probe_or_sidecar_failed'`, state.ID); n != 1 {
					t.Fatalf("full probe did not run: %+v", state)
				}
			})
		}
	}
}

func TestAudioRefreshPublicationFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"paused", "disabled", "generation", "root", "policy", "revision", "analysis-revision", "baseline", "asset-stat"} {
		t.Run(change, func(t *testing.T) {
			f := newAudioRefreshFixture(t, "audiobook", false)
			baseline, err := f.s.catalog.InventoryAudioFileMetadata(context.Background(), f.object, f.revision)
			if err != nil || baseline == nil {
				t.Fatal(baseline, err)
			}
			baseline.Tags["title"] = "Must not publish"
			baseline.AnalysisOperations = []string{"probe", "local_metadata", "subtitles", assets.PlaybackAnalysisRevision}
			job := f.analysisOnlyJob()
			f.runEnqueue(t, job, 5)
			switch change {
			case "paused":
				f.exec(`UPDATE jobs SET status='paused' WHERE id=?`, job.ID)
			case "disabled":
				f.exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, f.source)
			case "generation":
				f.exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
			case "root":
				f.exec(`UPDATE library_sources SET incarnation='changed-root' WHERE id=?`, f.source)
			case "policy":
				f.exec(`UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=?`, f.library)
			case "revision":
				f.exec(`UPDATE inventory_objects SET revision='changed' WHERE id=?`, f.object)
			case "analysis-revision":
				f.exec(`UPDATE inventory_objects SET analysis_revision='stale' WHERE id=?`, f.object)
			case "asset-stat":
				f.exec(`UPDATE catalog_assets SET size=size+1 WHERE token=?`, f.asset)
			case "baseline":
				f.exec(`DELETE FROM audio_file_metadata WHERE asset_id=?`, f.asset)
			}
			err = settleInventoryLocalRefresh(t, f.s.catalog, f.db, context.Background(), job.ID, f.object, f.revision, f.policy, *baseline)
			if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("fence accepted or unexpected error: %v", err)
			}
			if n := f.count(`SELECT count(*) FROM audio_tag_evidence WHERE asset_id=? AND value='Must not publish'`, f.asset); n != 0 {
				t.Fatal("stale metadata published")
			}
		})
	}
}

func TestAudioRefreshCoverRemovalPreservesOwnerChoices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"music", "audiobook"} {
		for _, owner := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owner=%t", kind, owner), func(t *testing.T) {
				f := newAudioRefreshFixture(t, kind, false)
				// This synthetic media has no embedded image in this case.
				f.exec(`UPDATE audio_file_metadata SET artwork_key='' WHERE asset_id=?`, f.asset)
				cover := filepath.Join(filepath.Dir(f.relative), "cover.png")
				f.write(cover, string(audioRefreshPNG(t, 100)))
				f.refresh()
				item := f.stringQuery(`SELECT CAST(l.entity_id AS TEXT) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.token=? LIMIT 1`, f.asset)
				key := f.stringQuery(`SELECT substr(poster_url,7) FROM catalog_item_details WHERE entity_id=?`, item)
				if key == "" {
					t.Fatal("sidecar not published")
				}
				if owner {
					f.exec(`UPDATE catalog_entities SET title='Owner title' WHERE id=?`, item)
					f.exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'title','Owner title','Embedded title',1,'owner','now')`, item)
					digest := strings.Repeat("a", 64)
					f.exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now')`, digest)
					f.exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs WHERE digest=?`, digest)
					f.exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('chosen','item',?,'poster','local','sidecar',?,'Local file','fence','now')`, item, "local:"+key)
					f.exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) VALUES('item',?,'poster','chosen',?,1,'owner','now')`, item, digest)
				}
				if err := os.Remove(filepath.Join(f.root, cover)); err != nil {
					t.Fatal(err)
				}
				f.write(f.relative+".portico.json", `{"title":"Refreshed title"}`)
				f.refresh()
				if poster := f.stringQuery(`SELECT COALESCE(poster_url,'') FROM catalog_item_details WHERE entity_id=?`, item); poster != "" {
					t.Fatalf("removed art stuck: %s", poster)
				}
				if owner {
					if title := f.stringQuery(`SELECT title FROM catalog_entities WHERE id=?`, item); title != "Owner title" {
						t.Fatal(title)
					}
					if n := f.count(`SELECT count(*) FROM artwork_selections WHERE entity_id=? AND locked=1 AND actor='owner'`, item); n != 1 {
						t.Fatal("owner selection was removed")
					}
				}
			})
		}
	}
}

func TestAudioRefreshPreservesManuallyLinkedVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"music", "audiobook"} {
		t.Run(kind, func(t *testing.T) {
			f := newAudioRefreshFixture(t, kind, false)
			item := f.stringQuery(`SELECT CAST(l.entity_id AS TEXT) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.token=? LIMIT 1`, f.asset)
			before := f.stringQuery(`SELECT json_array(title,parent_id) FROM catalog_entities WHERE id=?`, item)
			technical := f.technical()
			f.exec(`INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?)`, f.library, f.asset, item)
			f.write(f.relative+".portico.json", `{"title":"New title","album":"New album","artist":"New artist","genre":"Rock"}`)
			f.refresh()
			if got := f.stringQuery(`SELECT json_array(title,parent_id) FROM catalog_entities WHERE id=?`, item); got != before {
				t.Fatalf("manual version regrouped: %s != %s", got, before)
			}
			if genre := f.stringQuery(`SELECT value FROM audio_tag_evidence WHERE asset_id=? AND field='genre'`, f.asset); genre != "Rock" {
				t.Fatal(genre)
			}
			if got := f.technical(); got != technical {
				t.Fatal("technical facts changed")
			}
		})
	}
}

func TestAudioEmbeddedFallbackSurvivesCollectorOnlyWhileCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"revision", "asset-stat", "root", "retired"} {
		t.Run(change, func(t *testing.T) {
			f := newAudioRefreshFixture(t, "audiobook", false)
			ctx := context.Background()
			directory := t.TempDir()
			art := metadata.New(f.db, "")
			if err := art.SetArtworkDirectory(directory); err != nil {
				t.Fatal(err)
			}
			raw := audioRefreshPNG(t, 50)
			f.exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/png',2,2,?,'2000-01-01T00:00:00Z')`, f.embeddedKey, len(raw))
			f.exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/png',2,2,?,?,?,1920,0,0,1,'2000-01-01T00:00:00Z')`, f.embeddedKey, f.embeddedKey, f.embeddedKey, f.embeddedKey)
			path := filepath.Join(directory, f.embeddedKey+".img")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cover := filepath.Join(filepath.Dir(f.relative), "cover.png")
			f.write(cover, string(audioRefreshPNG(t, 100)))
			f.refresh()
			// No selection or displayed carrier names the hidden embedded fallback.
			if n := f.count(`SELECT count(*) FROM local_artwork WHERE art_key=?`, f.embeddedKey); n != 0 {
				t.Fatal("fallback is not hidden")
			}
			if err := art.CleanupArtwork(ctx); err != nil {
				t.Fatal(err)
			}
			if n := f.count(`SELECT count(*) FROM artwork_sources WHERE digest=?`, f.embeddedKey); n != 1 {
				t.Fatal("current fallback collected")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("current fallback file collected", err)
			}
			// The registry lookup must seek the fallback-key index per collector row.
			rows, err := f.db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM (`+artworkref.AudioFallbackKeys+`) fallback WHERE fallback.artwork_key=?`, f.embeddedKey)
			if err != nil {
				t.Fatal(err)
			}
			indexed := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				indexed = indexed || strings.Contains(detail, "SEARCH b USING COVERING INDEX audio_file_metadata_artwork_key") || strings.Contains(detail, "SEARCH b USING INDEX audio_file_metadata_artwork_key")
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !indexed {
				t.Fatal("fallback collector lookup did not seek its key index")
			}
			switch change {
			case "revision":
				f.exec(`UPDATE audio_file_metadata SET source_revision='stale' WHERE asset_id=?`, f.asset)
			case "asset-stat":
				f.exec(`UPDATE audio_file_metadata SET size=size+1 WHERE asset_id=?`, f.asset)
			case "root":
				f.exec(`UPDATE inventory_objects SET root_incarnation='stale' WHERE id=?`, f.object)
			case "retired":
				f.exec(`UPDATE inventory_objects SET retired=1 WHERE id=?`, f.object)
			}
			if err := art.CleanupArtwork(ctx); err != nil {
				t.Fatal(err)
			}
			if n := f.count(`SELECT count(*) FROM artwork_sources WHERE digest=?`, f.embeddedKey); n != 0 {
				t.Fatal("obsolete fallback pins source")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("obsolete fallback file not collected", err)
			}
			// Restore the synthetic current witness, then remove the sidecar: the raw
			// local-artwork fallback remains usable without reopening/probing media.
			f.exec(`UPDATE audio_file_metadata SET source_revision=?,size=? WHERE asset_id=?`, f.revision, f.size, f.asset)
			f.exec(`UPDATE inventory_objects SET root_incarnation=(SELECT incarnation FROM library_sources WHERE id=source_id),retired=0 WHERE id=?`, f.object)
			if err := os.Remove(filepath.Join(f.root, cover)); err != nil {
				t.Fatal(err)
			}
			f.refresh()
			f.assertCover(f.embeddedKey)
			file, _, err := f.s.LocalMetadata.Open(f.embeddedKey)
			if err != nil {
				t.Fatal("fallback bytes lost", err)
			}
			file.Close()
		})
	}
}

func TestAudioRefreshManualLinkRetainsUnchangedCover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"music", "audiobook"} {
		t.Run(kind, func(t *testing.T) {
			f := newAudioRefreshFixture(t, kind, false)
			cover := filepath.Join(filepath.Dir(f.relative), "cover.png")
			f.write(cover, string(audioRefreshPNG(t, 100)))
			f.refresh()
			item := f.stringQuery(`SELECT CAST(l.entity_id AS TEXT) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.token=? LIMIT 1`, f.asset)
			before := f.stringQuery(`SELECT COALESCE(poster_url,'') FROM catalog_item_details WHERE entity_id=?`, item)
			if before == "" {
				t.Fatal("setup lacks cover")
			}
			f.exec(`INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?)`, f.library, f.asset, item)
			f.write(f.relative+".portico.json", `{"genre":"Rock"}`)
			f.refresh()
			after := f.stringQuery(`SELECT COALESCE(poster_url,'') FROM catalog_item_details WHERE entity_id=?`, item)
			if after != before {
				t.Fatalf("unchanged cover lost after manual link: before=%q after=%q", before, after)
			}
		})
	}
}
func TestAudioRefreshInvalidEmbeddedCueRemoval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newAudioRefreshFixture(t, "music", true)
	// Same raw invalid embedded cue used by TestAudioEmbeddedCueBaselineKeepsSidecarPrecedence.
	f.exec(`UPDATE audio_file_chapters SET end_seconds=-1 WHERE asset_id=? AND kind='cue' AND ordinal=0`, f.asset)
	stem := strings.TrimSuffix(f.relative, filepath.Ext(f.relative))
	cue := stem + ".cue"
	f.write(cue, "FILE \"part.flac\" WAVE\n TRACK 01 AUDIO\n TITLE \"Sidecar one\"\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n TITLE \"Sidecar two\"\n INDEX 01 00:15:00\n")
	f.refresh()
	if err := os.Remove(filepath.Join(f.root, cue)); err != nil {
		t.Fatal(err)
	}
	f.write(f.relative+".portico.json", `{"genre":"Rock"}`)
	state := f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND reason='analysis_publication_failed'`, state.ID); n != 0 {
		t.Fatalf("sidecar removal failed publication: %+v", state)
	}
	if n := f.count(`SELECT count(*) FROM music_virtual_tracks WHERE asset_id=?`, f.asset); n != 0 {
		t.Fatalf("rejected embedded cue left %d sidecar virtual tracks", n)
	}
}
