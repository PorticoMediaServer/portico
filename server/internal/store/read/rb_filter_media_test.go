package read

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func rbMediaFilterHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_filter_media_test.go")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data...)))
}

// rbMediaFilterSource seeds one movie with two accepted versions, accepted
// stream/subtitle facts, a bounded source set and two Reading titles with
// accepted publications and attribution. Legacy raw author/series columns are
// deliberately populated to prove they are never the accepted fact.
func rbMediaFilterSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(4,'episode',1,0,1,1,0),(7,'song',1,0,1,1,0),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1),(20,'ebook',1,0,1,1,0),(21,'comic',1,0,1,1,0)`)
	f.exec(t, `UPDATE catalog_entities SET title='Movie',author='RAW AUTHOR',series='RAW SERIES' WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'media-1',1,'/media/movie.mkv',1000,1,'matroska','h264','aac',3840,2160,7200),(2,'media-2',1,'/media/movie-1080.mkv',2000,2,'matroska','h264','aac',1920,1080,5400),(4,'ebook-1',1,'/media/book.epub',3000,3,'epub','','',0,0,0),(5,'comic-1',1,'/media/book.cbz',4000,4,'cbz','','',0,0,0),(6,'audio-1',1,'/media/book.m4b',5000,5,'m4b','','aac',0,0,3600)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,1,1),(1,2,1)`)
	f.exec(t, `INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES('media-1',1,1000,1,'f'),('media-2',1,2000,2,'f'),('audio-1',1,5000,5,'f')`)
	f.exec(t, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES('media-1',0,'video','h264','','',0,'',1,0,'{"dynamicRange":"hdr10"}'),('media-1',1,'audio','aac','eng','English',2,'stereo',1,0,''),('media-1',2,'audio','aac','','Untagged',2,'stereo',0,0,''),('media-2',0,'video','h264','','',0,'',1,0,'{"dynamicRange":"dolby_vision"}'),('media-2',1,'audio','aac','fra','French',2,'stereo',1,0,'')`)
	f.exec(t, `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('media-1',1,1000,1,0,1,'known','f')`)
	f.exec(t, `INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason) VALUES('sub-1','media-1','embedded','stream:2',2,'srt','eng','English',0,0,'embedded')`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,kind,configured_root,root,incarnation,generation,identity_confirmed,health,enabled) VALUES('source-1','movies','local','/media','/media','inc',1,1,'ready',1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object-1','source-1','media-1','inc','movie.mkv','1','{}',1000,1),('object-2','source-1','media-2','inc','movie-1080.mkv','1','{}',2000,2)`)
	for _, row := range []struct {
		id   int64
		kind int64
	}{{1201, 20}, {1202, 21}, {1203, 8}} {
		public := make([]byte, 16)
		public[0], public[1] = byte(row.id), byte(row.kind)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, row.id, public, row.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,has_reading_attribution_members,author,series) VALUES(?,?,1,?,?, 'r','r','r','r',0,?,?,0,0,0,'RAW AUTHOR','RAW SERIES')`, row.id, public, row.kind, fmt.Sprintf("Reading %d", row.id), public, public)
	}
	f.exec(t, `UPDATE catalog_entities SET reading_author_value='Ursula K. Le Guin',reading_author_key='ursula k. le guin',reading_author_source='file-metadata',reading_series_value='Earthsea',reading_series_key='earthsea',reading_series_id='series-1',reading_series_source='file-metadata',reading_numbered_order=1,reading_number_value=0,has_reading_attribution_members=1 WHERE id IN(1201,1202)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1201,4,1),(1202,5,1),(1203,6,1)`)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1201,4,'epub'),(1202,5,'cbz')`)
	return f
}

func rbMediaFilterFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, rbMediaFilterSource(t), RBMediaFilterDefinitions(), readfixture.LiveConfig(time.Now), false, rbMediaFilterHash(t))
}

func rbMediaFilterRead(t *testing.T, f *browseFixture, in rbMediaFilterInput) (rbMediaFilterResult, error) {
	t.Helper()
	var result rbMediaFilterResult
	var readErr error
	_, err := f.snapshots.ReadForecasted(t.Context(), readfixture.ReadRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: f.snapshots.NativeRowReservation(), MaxQueries: 512}, func(scope *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
		result, readErr = readRBMediaFilterFacts(q, in)
		return readErr
	})
	return result, err
}

func rbMediaFilterInputFor(item int64) rbMediaFilterInput {
	return rbMediaFilterInput{Library: 1, Item: item, LibraryKey: "movies", VariableBytes: 64 << 10}
}

// rbMediaLegacyVersions applies the published versions rule to the fixture's
// stored links. The legacy counter (catalog_item_version_counts) is maintained
// by the baseline trigger `WHEN NEW.part_index=0`; the v2 read fixture carries
// the links but not that materialized counter, so the oracle is the trigger's
// counting predicate over the same rows, run in SQL rather than in kernel logic.
func rbMediaLegacyVersions(t *testing.T, f *browseFixture, item int64) int64 {
	t.Helper()
	var versions int64
	if err := f.conn.QueryRowContext(t.Context(), `SELECT COALESCE((SELECT count(*) FROM catalog_asset_links AS l WHERE l.entity_id=bei.id AND l.part_index=0),0) FROM catalog_entities AS bei WHERE bei.id=?`, item).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	return versions
}

func rbMediaFilterQuery(t *testing.T, query string) rbMediaFilter {
	t.Helper()
	filter, err := compileRBMediaFilter(json.RawMessage(query))
	if err != nil {
		t.Fatal(query, err)
	}
	return filter
}

func rbMediaFilterMatches(t *testing.T, facts rbMediaFilterFacts, query string) bool {
	t.Helper()
	filter := rbMediaFilterQuery(t, query)
	if err := filter.applicable(facts.Kind); err != nil {
		t.Fatal(query, "inapplicable", err)
	}
	return filter.matches(facts)
}

func TestRBFilterMediaAvailability(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		cases := []struct {
			name, sql, restore, want string
		}{
			{"ready", `UPDATE library_sources SET health='ready',enabled=1,incarnation='inc'; UPDATE inventory_objects SET retired=0,state='available',unsupported_reason='',root_incarnation='inc'`, ``, rbMediaAvailable},
			{"root-changed", `UPDATE library_sources SET health='root_changed'`, `UPDATE library_sources SET health='ready'`, rbMediaUnavailable},
			{"disabled", `UPDATE library_sources SET enabled=0`, `UPDATE library_sources SET enabled=1`, rbMediaUnavailable},
			{"stale-root", `UPDATE library_sources SET incarnation='new'`, `UPDATE library_sources SET incarnation='inc'`, rbMediaUnavailable},
			{"retired-object", `UPDATE inventory_objects SET retired=1`, `UPDATE inventory_objects SET retired=0`, rbMediaUnavailable},
			{"unsupported", `UPDATE inventory_objects SET unsupported_reason='unsupported'`, `UPDATE inventory_objects SET unsupported_reason=''`, rbMediaUnavailable},
			{"missing-object", `UPDATE inventory_objects SET state='missing' WHERE asset_id='media-1'`, `UPDATE inventory_objects SET state='available' WHERE asset_id='media-1'`, rbMediaPartial},
			{"dvr-retired", `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(1,0)`, `DELETE FROM dvr_catalog_retirements WHERE item_id=1`, rbMediaUnavailable},
			{"asset-unavailable-fallback", `DELETE FROM inventory_objects WHERE asset_id='media-1'; UPDATE catalog_assets SET available=0 WHERE id=1`, `UPDATE catalog_assets SET available=1 WHERE id=1; INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object-1','source-1','media-1','inc','movie.mkv','1','{}',1000,1)`, rbMediaPartial},
		}
		for _, c := range cases {
			f.exec(t, c.sql)
			result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
			if err != nil {
				t.Fatal(c.name, err)
			}
			if result.Facts.Availability != c.want {
				t.Fatalf("%s: availability %q, want %q", c.name, result.Facts.Availability, c.want)
			}
			if !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"availability","operator":"equals","value":"`+c.want+`"}`) {
				t.Fatal(c.name, "availability predicate did not follow the fact")
			}
			if c.restore != "" {
				f.exec(t, c.restore)
			}
		}
		f.exec(t, `DELETE FROM inventory_objects; DELETE FROM library_sources`)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil || result.Facts.Availability != rbMediaAvailable {
			t.Fatal("no-source fallback must trust the current asset flag", result, err)
		}
	})
}

func TestRBFilterMediaResolutionDurationVersions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil {
			t.Fatal(err)
		}
		if result.Facts.Versions != 2 || !slices.Equal(result.Facts.Resolutions, []string{"1080p", "4k"}) || !reflect.DeepEqual(result.Facts.Durations, []float64{7200, 5400}) {
			t.Fatal("multi-version facts", result.Facts)
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"versions","operator":"equals","value":2}`,
			`{"variant":"predicate","field":"versions","operator":"at-least","value":2}`,
			`{"variant":"predicate","field":"versions","operator":"is-present","value":null}`,
			`{"variant":"predicate","field":"resolution","operator":"equals","value":"4k"}`,
			`{"variant":"predicate","field":"resolution","operator":"in","value":["720p","1080p"]}`,
			`{"variant":"predicate","field":"durationMs","operator":"equals","value":5400000}`,
			`{"variant":"predicate","field":"durationMs","operator":"greater-than","value":7100000}`,
			`{"variant":"predicate","field":"durationMs","operator":"between","value":[5400000,7200000]}`,
		} {
			if !rbMediaFilterMatches(t, result.Facts, query) {
				t.Fatal("positive predicate missed", query)
			}
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"versions","operator":"equals","value":3}`,
			`{"variant":"predicate","field":"resolution","operator":"not-equals","value":"4k"}`,
			`{"variant":"predicate","field":"resolution","operator":"in","value":["720p"]}`,
			`{"variant":"predicate","field":"durationMs","operator":"less-than","value":5400000}`,
		} {
			if rbMediaFilterMatches(t, result.Facts, query) {
				t.Fatal("negative predicate falsely matched", query)
			}
		}
		// An unavailable second child is still a version: it makes the item partial
		// instead of disappearing from the count or the duration set.
		f.exec(t, `DELETE FROM inventory_objects WHERE asset_id='media-2'; UPDATE catalog_assets SET available=0 WHERE id=2`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil || result.Facts.Versions != 2 || result.Facts.Availability != rbMediaPartial {
			t.Fatal("hidden child was dropped", result, err)
		}
	})
}

func TestRBFilterMediaVersionPartsOracle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		public := func(id int64) []byte {
			b := make([]byte, 16)
			b[0], b[1], b[15] = 0xEE, 0x01, byte(id)
			return b
		}
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1300,?,1,1)`, public(1300))
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,has_reading_attribution_members) VALUES(1300,?,1,1,'Split movie','s','s','s','s',0,?,?,0,0,0)`, public(1300), public(1300), public(1300))
		part := func(asset, part int64) {
			token := fmt.Sprintf("split-%02d", asset)
			f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,1,?,?,?,'matroska','h264','aac',1920,1080,120)`, asset, token, "/media/"+token+".mkv", 1000+asset, asset)
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,part_index) VALUES(1300,?,1,?)`, asset, part)
		}
		// One movie version split over parts 0,1,2 is ONE version in the oracle.
		part(20, 0)
		part(21, 1)
		part(22, 2)
		if got := rbMediaLegacyVersions(t, f, 1300); got != 1 {
			t.Fatalf("legacy oracle for a three-part version = %d, want 1", got)
		}
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1300))
		if err != nil {
			t.Fatal(err)
		}
		if result.Facts.Versions != 1 || !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"versions","operator":"equals","value":1}`) {
			t.Fatal("three-part version counted as more than one", result.Facts)
		}
		if rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"versions","operator":"at-least","value":2}`) {
			t.Fatal("three-part version matched at-least 2")
		}
		// Two three-part versions are two versions, still by part_index=0 only.
		part(23, 0)
		part(24, 1)
		part(25, 2)
		if got := rbMediaLegacyVersions(t, f, 1300); got != 2 {
			t.Fatalf("legacy oracle for two three-part versions = %d, want 2", got)
		}
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1300))
		if err != nil {
			t.Fatal(err)
		}
		if result.Facts.Versions != 2 || !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"versions","operator":"at-least","value":2}`) {
			t.Fatal("two three-part versions miscounted", result.Facts)
		}
		// Reading publications are part-0 links too, so the oracle agrees.
		if got := rbMediaLegacyVersions(t, f, 1201); got != 1 {
			t.Fatalf("legacy oracle for the Reading publication = %d, want 1", got)
		}
		reading, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201))
		if err != nil || reading.Facts.Versions != rbMediaLegacyVersions(t, f, 1201) {
			t.Fatal("Reading version count disagrees with the oracle", reading, err)
		}
	})
}

func TestRBFilterMediaLanguagesAndDynamicRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(result.Facts.AudioLanguages, []string{"eng", "fra"}) || !slices.Equal(result.Facts.SubtitleLanguages, []string{"eng"}) || !slices.Equal(result.Facts.DynamicRanges, []string{"dolby_vision", "hdr10"}) {
			t.Fatal("accepted stream facts", result.Facts)
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"audioLanguage","operator":"equals","value":"eng"}`,
			`{"variant":"predicate","field":"audioLanguage","operator":"contains","value":"fra"}`,
			`{"variant":"predicate","field":"audioLanguage","operator":"contains-all","value":["eng","fra"]}`,
			`{"variant":"predicate","field":"audioLanguage","operator":"starts-with","value":"en"}`,
			`{"variant":"predicate","field":"subtitleLanguage","operator":"equals","value":"eng"}`,
			`{"variant":"predicate","field":"dynamicRange","operator":"equals","value":"hdr10"}`,
			`{"variant":"predicate","field":"dynamicRange","operator":"in","value":["dolby_vision"]}`,
			`{"variant":"predicate","field":"dynamicRange","operator":"not-equals","value":"sdr"}`,
		} {
			if !rbMediaFilterMatches(t, result.Facts, query) {
				t.Fatal("language predicate missed", query)
			}
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"audioLanguage","operator":"not-in","value":["eng","fra"]}`,
			`{"variant":"predicate","field":"audioLanguage","operator":"contains-all","value":["eng","jpn"]}`,
			`{"variant":"predicate","field":"subtitleLanguage","operator":"equals","value":"fra"}`,
		} {
			if rbMediaFilterMatches(t, result.Facts, query) {
				t.Fatal("language predicate falsely matched", query)
			}
		}
		// An untagged track is absence, not an empty-string language value.
		f.exec(t, `DELETE FROM asset_streams WHERE asset_id='media-1' AND language='eng'`)
		f.exec(t, `DELETE FROM asset_streams WHERE asset_id='media-2' AND language='fra'`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil {
			t.Fatal(err)
		}
		if !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"audioLanguage","operator":"is-missing","value":null}`) {
			t.Fatal("nullable language became present")
		}
		if rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"audioLanguage","operator":"is-present","value":null}`) {
			t.Fatal("empty language counted as a value")
		}
	})
}

func TestRBFilterMediaSubtitleResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		// Leave media-1 with no stored subtitle track: its only English subtitle is
		// an uploaded (shared) SRT, which the legacy predicate also matches.
		f.exec(t, `DELETE FROM asset_subtitles WHERE asset_id='media-1'`)
		f.exec(t, `DELETE FROM asset_subtitle_facts WHERE asset_id='media-1'`)
		f.exec(t, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,deleted) VALUES('res-1',1,'media-1','shared','',1,0)`)
		f.exec(t, `INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES('res-1',1,'d',10,'srt','eng','English','upload','','d0',1000,1,1,'2026-01-01T00:00:00Z')`)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(result.Facts.SubtitleLanguages, "eng") || !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"subtitleLanguage","operator":"equals","value":"eng"}`) {
			t.Fatal("shared subtitle file ignored", result.Facts)
		}
		// A current personal resource belongs to one viewer and this kernel binds no
		// principal: a named refusal, never an exact false.
		f.exec(t, `UPDATE subtitle_resources SET scope='personal',owner='account-a' WHERE id='res-1'`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if !errors.Is(err, errRBMediaSubtitlePrincipalPending) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("personal subtitle resource must be a named refusal", result, err)
		}
		// A stale shared revision does not match the legacy identity join, so it is
		// absence rather than a refusal or a false value.
		f.exec(t, `UPDATE subtitle_resources SET scope='shared',owner='' WHERE id='res-1'`)
		f.exec(t, `UPDATE subtitle_revisions SET source_size=999 WHERE resource_id='res-1'`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil {
			t.Fatal(err)
		}
		if !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"subtitleLanguage","operator":"is-missing","value":null}`) {
			t.Fatal("stale subtitle resource became a language", result.Facts)
		}
	})
}

func TestRBFilterMediaSubtitleStatusKnown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		// Legacy only accepts a known subtitle inventory. A partial scan is absence,
		// not a stale-identity item failure.
		f.exec(t, `UPDATE asset_subtitle_facts SET status='partial' WHERE asset_id='media-1'`)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil || len(result.Facts.SubtitleLanguages) != 0 {
			t.Fatal("non-known subtitle facts must not supply a language", result, err)
		}
		f.exec(t, `UPDATE asset_subtitle_facts SET status='known' WHERE asset_id='media-1'`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
		if err != nil || !slices.Contains(result.Facts.SubtitleLanguages, "eng") {
			t.Fatal("known subtitle facts must supply the language", result, err)
		}
	})
}

func TestRBFilterMediaReadingAttribution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201))
		if err != nil {
			t.Fatal(err)
		}
		if result.Facts.Kind != 20 || result.Facts.Author == nil || *result.Facts.Author != "Ursula K. Le Guin" || result.Facts.Series == nil || *result.Facts.Series != "Earthsea" {
			t.Fatal("accepted Reading attribution", result.Facts)
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"author","operator":"equals","value":"ursula k. le guin"}`,
			`{"variant":"predicate","field":"author","operator":"starts-with","value":"ursula"}`,
			`{"variant":"predicate","field":"author","operator":"contains-any","value":["earthsea","ursula k. le guin"]}`,
			`{"variant":"predicate","field":"series","operator":"in","value":["earthsea"]}`,
		} {
			if !rbMediaFilterMatches(t, result.Facts, query) {
				t.Fatal("accepted attribution missed", query)
			}
		}
		// Legacy raw author/series columns are not the accepted fact.
		if rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"author","operator":"equals","value":"RAW AUTHOR"}`) {
			t.Fatal("raw author tag substituted")
		}
		if rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"series","operator":"equals","value":"RAW SERIES"}`) {
			t.Fatal("raw series tag substituted")
		}
		if rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"author","operator":"contains-all","value":["ursula k. le guin","earthsea"]}`) {
			t.Fatal("single accepted author cannot equal two values")
		}
		f.exec(t, `UPDATE catalog_entities SET reading_series_value=NULL,reading_series_key=NULL,reading_series_id=NULL,reading_series_source=NULL WHERE id=1202`)
		comic, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1202))
		if err != nil || comic.Facts.Series != nil || !rbMediaFilterMatches(t, comic.Facts, `{"variant":"predicate","field":"series","operator":"is-missing","value":null}`) {
			t.Fatal("absent accepted series is missing, not an empty value", comic, err)
		}
		for _, c := range []struct {
			query string
			want  error
		}{
			{`{"variant":"predicate","field":"narrator","operator":"equals","value":"Someone"}`, errRBMediaNarratorPending},
		} {
			if _, err := compileRBMediaFilter(json.RawMessage(c.query)); !errors.Is(err, c.want) || !errors.Is(err, errRBMediaPending) {
				t.Fatalf("pending source %s: %v", c.query, err)
			}
		}
		audio, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1203))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			query string
			want  error
		}{
			{`{"variant":"predicate","field":"author","operator":"equals","value":"Someone"}`, errRBMediaAudioAuthorPending},
			{`{"variant":"predicate","field":"series","operator":"equals","value":"Someone"}`, errRBMediaSeriesPending},
		} {
			filter := rbMediaFilterQuery(t, c.query)
			if err := filter.applicable(audio.Facts.Kind); !errors.Is(err, c.want) || !errors.Is(err, errRBMediaPending) {
				t.Fatal("non-Reading attribution must stay named pending", c.query, err)
			}
		}
	})
}

func TestRBFilterMediaReadingConverterAndAvailability(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201))
		if err != nil || result.Facts.Availability != rbMediaAvailable || result.Facts.Versions != 1 {
			t.Fatal("accepted ebook availability", result, err)
		}
		f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(8,'ebook-2',1,'/media/book.mobi',6000,6,'mobi','','',0,0,0)`)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1201,8,1)`)
		f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1201,8,'mobi')`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201))
		if err != nil || result.Facts.Availability != rbMediaPartial || result.Facts.Versions != 2 {
			t.Fatal("unsupported converter must keep a truthful partial", result, err)
		}
		in := rbMediaFilterInputFor(1201)
		in.Converter = rbMediaConverter{MOBI: true}
		result, err = rbMediaFilterRead(t, f, in)
		if err != nil || result.Facts.Availability != rbMediaAvailable {
			t.Fatal("trusted converter domain", result, err)
		}
		if result.Facts.Kind != 20 || !rbMediaFilterMatches(t, result.Facts, `{"variant":"predicate","field":"versions","operator":"equals","value":2}`) {
			t.Fatal("Reading version count")
		}
		f.exec(t, `DELETE FROM reading_publications WHERE asset_id=8`)
		f.exec(t, `DELETE FROM catalog_asset_links WHERE entity_id=1201 AND asset_id=8`)
		f.exec(t, `DELETE FROM catalog_assets WHERE id=8`)
		f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=4; DELETE FROM inventory_objects WHERE asset_id='ebook-1'`)
		result, err = rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201))
		if err != nil || result.Facts.Availability != rbMediaUnavailable {
			t.Fatal("unavailable publication", result, err)
		}
	})
}

func TestRBFilterMediaRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)
		for _, c := range []struct{ name, sql, restore string }{
			{"stale-stream-facts", `UPDATE asset_stream_facts SET size=999 WHERE asset_id='media-1'`, `UPDATE asset_stream_facts SET size=1000 WHERE asset_id='media-1'`},
			{"stale-subtitle-facts", `UPDATE asset_subtitle_facts SET modified_ns=9 WHERE asset_id='media-1'`, `UPDATE asset_subtitle_facts SET modified_ns=1 WHERE asset_id='media-1'`},
			{"pending-publication", `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=1`, `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=1`},
			{"bad-dynamic-range", `UPDATE asset_streams SET detail_json='not json' WHERE asset_id='media-1' AND stream_index=0`, `UPDATE asset_streams SET detail_json='{"dynamicRange":"hdr10"}' WHERE asset_id='media-1' AND stream_index=0`},
			{"unknown-dynamic-range", `UPDATE asset_streams SET detail_json='{"dynamicRange":"hdr99"}' WHERE asset_id='media-1' AND stream_index=0`, `UPDATE asset_streams SET detail_json='{"dynamicRange":"hdr10"}' WHERE asset_id='media-1' AND stream_index=0`},
		} {
			f.exec(t, c.sql)
			result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1))
			if !errors.Is(err, errRBMediaUnresolved) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
				t.Fatal(c.name, result, err)
			}
			f.exec(t, c.restore)
		}
		in := rbMediaFilterInputFor(1)
		in.VariableBytes = 1
		if result, err := rbMediaFilterRead(t, f, in); !errors.Is(err, errRBMediaBudget) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("byte budget", result, err)
		}
		in = rbMediaFilterInputFor(1)
		in.VariableBytes = 0
		if _, err := rbMediaFilterRead(t, f, in); !errors.Is(err, ErrInput) {
			t.Fatal("zero allowance", err)
		}
		for n := 0; n < rbMediaLinkCap+1; n++ {
			f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,1,?,1000,1,'matroska','h264','aac',1920,1080,120)`, 100+n, fmt.Sprintf("extra-%03d", n), fmt.Sprintf("/media/extra-%03d.mkv", n))
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,?,1)`, 100+n)
		}
		if result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("link cap", result, err)
		}
		// An attribution tuple outside the accepted facet membership is unresolved,
		// not a value a raw tag could replace.
		f.exec(t, `UPDATE catalog_entities SET has_reading_attribution_members=0 WHERE id=1201`)
		if result, err := rbMediaFilterRead(t, f, rbMediaFilterInputFor(1201)); !errors.Is(err, errRBMediaUnresolved) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("unaccepted attribution membership", result, err)
		}
	})
}

// TestRBFilterMediaWindowCaps proves every bounded window refuses only above
// its cap and completes exactly at it. One fixture is reused window by window so
// the race-enabled run stays inside the package's functional deadline.
func TestRBFilterMediaWindowCaps(t *testing.T) {
	wide := func(item int64) rbMediaFilterInput {
		in := rbMediaFilterInputFor(item)
		in.VariableBytes = 1 << 20
		return in
	}
	synctest.Test(t, func(t *testing.T) {
		f := rbMediaFilterFixture(t)

		// Links: the fixture movie already has two part-0 links.
		addLink := func(asset int64, token string) {
			f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,1,?,1000,1,'matroska','h264','aac',1920,1080,120)`, asset, token, "/media/"+token+".mkv")
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,?,1)`, asset)
		}
		for n := 0; n < rbMediaLinkCap-2; n++ {
			addLink(int64(300+n), fmt.Sprintf("links-%03d", n))
		}
		result, err := rbMediaFilterRead(t, f, wide(1))
		if err != nil || result.Facts.Versions != rbMediaLinkCap || result.Facts.Versions != rbMediaLegacyVersions(t, f, 1) {
			t.Fatal("link window at cap", result, err)
		}
		f.exec(t, `DELETE FROM catalog_asset_links WHERE entity_id=1 AND asset_id>=300 AND asset_id<400`)

		// Sources.
		addSource := func(prefix string, n int) {
			root := fmt.Sprintf("/%s/source-%03d", prefix, n)
			f.exec(t, `INSERT INTO library_sources(id,library_id,kind,configured_root,root,incarnation,generation,identity_confirmed,health,enabled) VALUES(?,?, 'local',?,?,?,1,1,'ready',1)`, fmt.Sprintf("%s-source-%03d", prefix, n), "movies", root, root, fmt.Sprintf("%s-inc-%03d", prefix, n))
		}
		for n := 0; n < rbMediaSourceCap-1; n++ {
			addSource("cap", n)
		}
		if result, err = rbMediaFilterRead(t, f, wide(1)); err != nil || result.Progress.Sources != rbMediaSourceCap {
			t.Fatal("source window at cap", result, err)
		}
		addSource("over", 0)
		if result, err := rbMediaFilterRead(t, f, wide(1)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("source window over cap", result, err)
		}
		f.exec(t, `DELETE FROM library_sources WHERE id LIKE 'cap-source-%' OR id LIKE 'over-source-%'`)

		// Streams: media-1 already has three stream rows.
		addStream := func(index int64) {
			f.exec(t, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES('media-1',?,'audio','aac','','',2,'',0,0,'')`, index)
		}
		for index := int64(3); index < int64(3+rbMediaStreamCap-3); index++ {
			addStream(index)
		}
		if result, err = rbMediaFilterRead(t, f, wide(1)); err != nil || result.Progress.Streams != rbMediaStreamCap+2 {
			t.Fatal("stream window at cap", result, err)
		}
		addStream(int64(3 + rbMediaStreamCap - 3))
		if result, err := rbMediaFilterRead(t, f, wide(1)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("stream window over cap", result, err)
		}
		f.exec(t, `DELETE FROM asset_streams WHERE asset_id='media-1' AND stream_index>=3`)

		// Subtitles: sub-1 already exists.
		addSubtitle := func(total int) {
			f.exec(t, `INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason) VALUES(?,'media-1','embedded',?,0,'srt','eng','',0,0,'embedded')`, fmt.Sprintf("sub-%03d", total), fmt.Sprintf("stream:%03d", total))
		}
		for n := 2; n <= rbMediaSubtitleCap; n++ {
			addSubtitle(n)
		}
		if result, err = rbMediaFilterRead(t, f, wide(1)); err != nil || result.Progress.Subtitles != rbMediaSubtitleCap {
			t.Fatal("subtitle window at cap", result, err)
		}
		addSubtitle(rbMediaSubtitleCap + 1)
		if result, err := rbMediaFilterRead(t, f, wide(1)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("subtitle window over cap", result, err)
		}
		f.exec(t, `DELETE FROM asset_subtitles WHERE asset_id='media-1' AND id<>'sub-1'`)

		// Subtitle resources: current shared files for media-1.
		addResource := func(n int) {
			id := fmt.Sprintf("cap-res-%03d", n)
			f.exec(t, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,deleted) VALUES(?,1,'media-1','shared','',1,0)`, id)
			f.exec(t, `INSERT INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'d',10,'srt','eng','English','upload','','d0',1000,1,1,'2026-01-01T00:00:00Z')`, id)
		}
		for n := 0; n < rbMediaResourceCap; n++ {
			addResource(n)
		}
		if result, err = rbMediaFilterRead(t, f, wide(1)); err != nil || result.Progress.Resources != rbMediaResourceCap {
			t.Fatal("subtitle resource window at cap", result, err)
		}
		addResource(rbMediaResourceCap)
		if result, err := rbMediaFilterRead(t, f, wide(1)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("subtitle resource window over cap", result, err)
		}
		f.exec(t, `DELETE FROM subtitle_resources WHERE id LIKE 'cap-res-%'`)

		// Publications: item 1201 already publishes one epub; each later
		// publication needs its own asset and part-0 link, so the link and
		// publication windows share a cap and both refuse an over-cap set.
		addPublication := func(n int) {
			asset := int64(400 + n)
			token := fmt.Sprintf("cap-pub-%03d", n)
			f.exec(t, `INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,1,?,?,?, 'pdf','','',0,0,0)`, asset, token, "/media/"+token+".pdf", 3000+n, n)
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1201,?,1)`, asset)
			f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1201,?,'pdf')`, asset)
		}
		for n := 1; n < rbMediaPublicationCap; n++ {
			addPublication(n)
		}
		if result, err = rbMediaFilterRead(t, f, wide(1201)); err != nil || result.Facts.Versions != rbMediaPublicationCap || result.Facts.Versions != rbMediaLegacyVersions(t, f, 1201) {
			t.Fatal("publication window at cap", result, err)
		}
		addPublication(rbMediaPublicationCap)
		if result, err := rbMediaFilterRead(t, f, wide(1201)); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(result, rbMediaFilterResult{}) {
			t.Fatal("publication window over cap", result, err)
		}
	})
}

func TestRBFilterMediaClosedVocabulary(t *testing.T) {
	facts := rbMediaFilterFacts{Kind: 1, Availability: rbMediaAvailable, Versions: 1, DiscNumber: 1}
	for _, query := range []string{
		`{"variant":"predicate","field":"runtime","operator":"equals","value":1}`,
		`{"variant":"predicate","field":"availability","operator":"contains","value":"available"}`,
		`{"variant":"predicate","field":"resolution","operator":"equals","value":"8k"}`,
		`{"variant":"predicate","field":"durationMs","operator":"equals","value":1.5}`,
		`{"variant":"predicate","field":"durationMs","operator":"equals","value":-1}`,
		`{"variant":"predicate","field":"versions","operator":"in","value":[]}`,
		`{"variant":"predicate","field":"audioLanguage","operator":"is-present","value":"x"}`,
	} {
		if _, err := compileRBMediaFilter(json.RawMessage(query)); !errors.Is(err, ErrUnqualified) && !errors.Is(err, ErrInput) {
			t.Fatal("unclosed predicate accepted", query, err)
		}
	}
	filter := rbMediaFilterQuery(t, `{"variant":"predicate","field":"resolution","operator":"equals","value":"4k"}`)
	if err := filter.applicable(facts.Kind); err != nil {
		t.Fatal("applicable movie field refused", err)
	}
	filter = rbMediaFilterQuery(t, `{"variant":"predicate","field":"resolution","operator":"equals","value":"4k"}`)
	if err := filter.applicable(7); !errors.Is(err, ErrUnqualified) {
		t.Fatal("video-only field on song", err)
	}
	filter = rbMediaFilterQuery(t, `{"variant":"predicate","field":"availability","operator":"equals","value":"available"}`)
	if err := filter.applicable(2); !errors.Is(err, ErrUnqualified) {
		t.Fatal("container availability must refuse, not guess children", err)
	}
	filter = rbMediaFilterQuery(t, `{"variant":"predicate","field":"discNumber","operator":"equals","value":1}`)
	if err := filter.applicable(1); !errors.Is(err, ErrUnqualified) {
		t.Fatal("disc number on movie", err)
	}
	if !rbMediaFilterMatches(t, facts, `{"variant":"group","not":{"variant":"predicate","field":"availability","operator":"equals","value":"unavailable"}}`) {
		t.Fatal("closed NOT evaluation")
	}
}
