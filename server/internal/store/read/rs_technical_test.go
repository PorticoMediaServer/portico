package read

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"testing"
	"time"
)

func rsTechnicalSource(t *testing.T) *browseFixture {
	f := newRSDetailSource(t)
	for n := int64(2); n <= 65; n++ {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1000,1,'matroska','h264','aac',1920,1080,120,1)`, n, fmt.Sprint("tech-", n), fmt.Sprint("/media/", n, ".mkv"))
	}
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('tech-source','movies','/media','/media','root-a')`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('tech-object','tech-source','asset','root-a','title.mkv','r1','{}',1000,1)`)
	f.exec(t, `UPDATE catalog_assets SET info_owner_object='tech-object',info_owner_revision='r1' WHERE id=1`)
	f.exec(t, `UPDATE asset_chapter_facts SET status='known' WHERE asset_id='asset'`)
	return f
}
func rsTechnicalFixture(t *testing.T) (*browseFixture, *RSSelectedTechnicalReader) {
	gc := readfixture.LiveConfig(time.Now)
	gc.Preparation = &governor.PreparationProfile{MaxActive: 2, MaxBytes: 16 << 20, MaxJobBytes: 8 << 20, ProtectedBytes: 32 << 20}
	gc.Preparation.MaxJobBytes = 16 << 20
	f := composeReadFixture(t, rsTechnicalSource(t), append(append(HarnessDefinitions(), TitleFactsDefinitions()...), TechnicalFactsDefinitions()...), gc, true, rsTechnicalHash(t))
	r, e := NewRSSelectedTechnicalReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func TestRSTechnicalCompleteAndSourceFence(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	in := RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1}
	w, ref, e := BeginRSTechnicalWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "technical"}, time.Now().Add(12*time.Second), in)
	if e != nil {
		t.Fatal(ref, e)
	}
	defer w.Close()
	in.Work = w
	for poll := 0; poll < 20; poll++ {
		p, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(poll, e)
		}
		if p.Preparing {
			if len(p.Streams) != 0 || p.Path != nil {
				t.Fatal("prefix escaped")
			}
			continue
		}
		if len(p.Streams) != 2 || len(p.Chapters) != 2 || p.File.Container != "matroska" || p.Path != nil {
			t.Fatalf("incomplete facts %+v", p)
		}
		return
	}
	t.Fatal("did not complete")
}
func TestRSTechnicalRejectsReading(t *testing.T) {
	for _, kind := range []int64{20, 21} {
		in := RSSelectedTechnicalInput{Item: 1, Asset: 1, View: BrowseInput{Kind: kind, Library: 1, Limit: 1, Database: "db", TokenHash: "token"}}
		if validRSTechnical(in) {
			t.Fatal("Reading entered timed-media family")
		}
	}
}
func TestRSTechnicalRetiredSourceFailsClosed(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	in := RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1}
	w, _, e := BeginRSTechnicalWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "stale-tech"}, time.Now().Add(12*time.Second), in)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	in.Work = w
	if _, _, e = r.poll(t.Context(), foregroundRequest(), time.Second, f.output, in); e != nil {
		t.Fatal(e)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='tech-object'`)
	p, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrDenied) || p.Preparing || len(p.Streams) > 0 {
		t.Fatal("retired source published", p, e)
	}
}
func TestRSTechnicalOwnerAndDiscObservation(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	f.exec(t, `INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,media_title_key,media_stream_revision,media_chapter_revision) SELECT 'tech-object','asset','r1',generation,'root-a','bluray','iso','none',?, 'pl:1',1,1 FROM library_sources WHERE id='tech-source'`, fmt.Sprintf("%064d", 1))
	f.exec(t, `INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count) VALUES('tech-object','pl:1','sel-1',1,2,60000,2),('tech-object','pl:2','sel-2',2,1,60000,1)`)
	f.exec(t, `INSERT INTO inventory_disc_chapters(object_id,title_key,chapter_order,start_ms) VALUES('tech-object','pl:1',0,0)`)
	in := RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1}
	w, _, e := BeginRSTechnicalWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "owner-tech"}, time.Now().Add(12*time.Second), in)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	in.Work = w
	for poll := 0; poll < 25; poll++ {
		p, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil {
			t.Fatal(poll, e)
		}
		if p.Preparing {
			continue
		}
		if p.Path == nil || p.Disc == nil || len(p.Disc.Titles) != 2 || p.Disc.Titles[0].Number != 2 || p.Disc.Media == nil || p.Disc.Media.Angles != 2 || p.Disc.Chapters[1] != 1 {
			t.Fatalf("owner disc %+v", p)
		}
		return
	}
	t.Fatal("did not complete")
}

//go:embed rs_technical_test.go
var rsTechnicalJournal string

func rsTechnicalHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsDetailHash(t)+rsTechnicalJournal)))
}
