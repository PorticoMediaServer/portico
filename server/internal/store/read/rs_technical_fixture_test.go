package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_technical_test.go
var rsTechnicalJournal string

//go:embed rs_technical_fixture_test.go
var rsTechnicalFixtureJournal string

func rsTechnicalHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+rsTechnicalJournal+rsTechnicalFixtureJournal)))
}

// rsTechnicalSource seeds one accepted movie occurrence with a ready current
// source, basic stream/chapter/subtitle heads, an owner path and owner disc
// inventory. The extra 64 assets keep the analyzed capture from collapsing into
// a single-row scan on catalog_assets.
func rsTechnicalSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newRSDetailSource(t)
	for n := int64(2); n <= 65; n++ {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1000,1,'matroska','h264','aac',1920,1080,120,1)`, n, fmt.Sprint("tech-", n), fmt.Sprint("/media/", n, ".mkv"))
	}
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('tech-source','movies','/media','/media','root-a')`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('tech-object','tech-source','asset','root-a','title.mkv','r1','{}',1000,1)`)
	f.exec(t, `UPDATE catalog_assets SET info_owner_object='tech-object',info_owner_revision='r1',music_sample_rate=48000,music_bit_depth=24,music_channels=2,music_lossless=1,music_technical_source_revision='r1' WHERE id=1`)
	f.exec(t, `UPDATE asset_chapter_facts SET status='known',revision=1 WHERE asset_id='asset'`)
	f.exec(t, `UPDATE asset_stream_facts SET revision=1,detail_version=5 WHERE asset_id='asset'`)
	f.exec(t, `UPDATE asset_streams SET detail_json=? WHERE asset_id='asset' AND stream_index=0`, `{"profile":"High","width":1920,"height":1080,"frameRate":24,"bitDepth":8,"dynamicRange":"hdr10"}`)
	f.exec(t, `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES('asset',1,1000,1,0,1,'known','subtitle-facts')`)
	f.exec(t, `INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('tech-subtitle','asset','embedded','0',2,'srt','eng','English SDH',1,0,'',1)`)
	f.exec(t, `INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,media_title_key,media_stream_revision,media_chapter_revision) SELECT 'tech-object','asset','r1',generation,'root-a','bluray','iso','none',?, 'pl:1',1,1 FROM library_sources WHERE id='tech-source'`, fmt.Sprintf("%064d", 1))
	f.exec(t, `INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count) VALUES('tech-object','pl:1','sel-1',1,2,60000,2),('tech-object','pl:2','sel-2',2,1,60000,1)`)
	f.exec(t, `INSERT INTO inventory_disc_chapters(object_id,title_key,chapter_order,start_ms) VALUES('tech-object','pl:1',0,0),('tech-object','pl:1',1,30000)`)
	return f
}

func rsTechnicalFixture(t *testing.T) (*browseFixture, *RSSelectedTechnicalReader) {
	t.Helper()
	gc := readfixture.LiveConfig(time.Now)
	gc.Preparation = &governor.PreparationProfile{MaxActive: 2, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 32 << 20}
	f := composeReadFixture(t, rsTechnicalSource(t), append(append(HarnessDefinitions(), TitleFactsDefinitions()...), TechnicalFactsDefinitions()...), gc, true, rsTechnicalHash(t))
	r, e := NewRSSelectedTechnicalReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func rsTechnicalBegin(t *testing.T, f *browseFixture, r *RSSelectedTechnicalReader, in RSSelectedTechnicalInput) *RSTechnicalWork {
	t.Helper()
	if in.View.Kind == 0 {
		in.View = f.input
	}
	if in.Item == 0 {
		in.Item = 1
	}
	if in.Asset == 0 {
		in.Asset = 1
	}
	w, ref, e := BeginRSTechnicalWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "rs-technical"}, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(ref, e)
	}
	t.Cleanup(func() {
		if e := w.Close(); e != nil {
			t.Error(e)
		}
	})
	return w
}

// rsTechnicalFinish polls until the complete answer is published. Every
// intermediate poll must be an empty pending prefix, never a partial fact set.
func rsTechnicalFinish(t *testing.T, r *RSSelectedTechnicalReader, f *browseFixture, w *RSTechnicalWork) RSTechnicalFacts {
	t.Helper()
	in := RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w}
	for poll := 0; poll < 60; poll++ {
		p, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal("poll", poll, e)
		}
		if p.Preparing {
			if len(p.Streams) != 0 || len(p.Chapters) != 0 || len(p.Subtitles) != 0 || p.Path != nil || p.Disc != nil || p.File.Container != "" {
				t.Fatalf("pending prefix escaped at poll %d: %+v", poll, p)
			}
			continue
		}
		return p
	}
	t.Fatal("did not complete")
	return RSTechnicalFacts{}
}

func rsTechnicalPoll(t *testing.T, r *RSSelectedTechnicalReader, f *browseFixture, w *RSTechnicalWork) (RSTechnicalFacts, error) {
	t.Helper()
	// The private poll is the same reducer public Poll wraps in live custody;
	// TestRSTechnicalPublicPollCustody proves the public admission path.
	p, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w})
	return p, e
}
