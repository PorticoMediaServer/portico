package read

import (
	"encoding/binary"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
	"time"
)

func newIAReadingSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'book',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, 4000)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(4000,?,1,20)`, b)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(4000,?,1,20,'Reading 🦊','reading','r','reading','r',0,?,?)`, b, b, b)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(4000,'reading-token','/media/book.epub',100,1,'epub','','',0,0,0,1)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(4000,4000,1)`)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(4000,4000,'epub')`)
	f.exec(t, `INSERT OR IGNORE INTO reading_path_clock(singleton,sequence) VALUES(1,1)`)
	// Native path evidence has its own revision; it need not match the retained head.
	f.exec(t, `INSERT INTO libraries(id,name,kind,root) VALUES('1','Reading','book','/media')`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('reading-source','1','/media','/media')`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('reading-object','reading-source','reading-token','root','book.epub','revision-2','{}',100,1)`)
	f.exec(t, `UPDATE reading_publications SET has_reading_path_head=1,path_publisher_sequence=1,path_object_id='reading-object',path_source_revision='revision-1' WHERE item_id=4000`)
	f.exec(t, `INSERT INTO reading_publication_types(item_id,asset_id,publication_type,source_format,source_field,source_raw_json,evidence_complete,inventory_object_id,source_revision,classifier_version) VALUES(4000,4000,'magazine','epub','dc:type','[]',1,'reading-object','revision-1',1)`)
	return f
}
func newIAReadingFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	f := composeReadFixture(t, newIAReadingSource(t), append(browseDefinitions(), IAReadingDefinitions()...), iaTVTestGovernorConfig(), true, iaReadingFixtureHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func TestIAReadingAcceptedEvidence(t *testing.T) {
	f, r := newIAReadingFixture(t)
	in := IAReadingInput{View: f.input, Item: 4000}
	in.View.Kind = 20
	d, _, e := r.iaReading(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Publications) != 1 || d.Publications[0].Format != "epub" || *d.Publications[0].SourceID != "reading-source" || *d.Publications[0].SourceRevision != "revision-1" || *d.Publications[0].ObjectRevision != "revision-2" || d.Publications[0].Author != nil {
		t.Fatalf("evidence %+v", d)
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=4000`)
	f.exec(t, `UPDATE inventory_objects SET retired=1,state='missing' WHERE id='reading-object'`)
	d, _, e = r.iaReading(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || d.Publications[0].AssetAvailable || *d.Publications[0].ObjectRetired != 1 {
		t.Fatal("retained evidence", e)
	}
	in.View.Library = 2
	_, _, e = r.iaReading(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e == nil {
		t.Fatal("cross library evidence")
	}
}
func TestIAReadingCompleteCohortSQLMutant(t *testing.T) {
	for _, mutant := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "limit-mutant"}[mutant], func(t *testing.T) {
			f := newIAReadingSource(t)
			for id := 4001; id <= 4008; id++ {
				f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,'/b',1,1,'pdf','','',0,0,0,1)`, id, strings.Repeat("x", id-4000))
				f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(4000,?,1)`, id)
				f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(4000,?,'pdf')`, id)
			}
			defs := IAReadingDefinitions()
			if mutant {
				for i := range defs {
					if defs[i].ID == stmt.ID("read.foreground.ia.reading.members") {
						defs[i].SQL = strings.Replace(defs[i].SQL, "LIMIT 9", "LIMIT 8", 1)
					}
				}
			}
			f = composeReadFixture(t, f, append(browseDefinitions(), defs...), iaTVTestGovernorConfig(), true, iaReadingFixtureHash(t))
			r, _ := NewForegroundReader(f.snapshots)
			in := IAReadingInput{View: f.input, Item: 4000}
			in.View.Kind = 20
			d, _, e := r.iaReading(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !mutant {
				if !errors.Is(e, ErrUnqualified) || len(d.Publications) != 0 {
					t.Fatal("overflow escaped", e)
				}
			} else if e != nil || len(d.Publications) != 8 {
				t.Fatal("mutant did not falsify cohort control", e)
			}
		})
	}
}

// This SQL mutant weakens the actual point scope; Capture must refuse it.
func TestIAReadingScopeSQLMutant(t *testing.T) {
	f := newIAReadingSource(t)
	for _, d := range IAReadingDefinitions() {
		if d.ID == stmt.ID("read.foreground.ia.reading.entity_octets") {
			d.SQL = strings.Replace(d.SQL, "library_id=?", "library_id>=?", 1)
			catalog, e := stmt.Describe([]stmt.Definition{d})
			if e != nil {
				t.Fatal(e)
			}
			_, e = plan.Capture(t.Context(), f.conn, catalog, browsePlanFixture(browseReaderConfig(), iaReadingFixtureHash(t), plan.Unanalyzed), iaTVPolicy(t))
			if e == nil || !strings.Contains(e.Error(), "exact closed point") {
				t.Fatal("scope mutant escaped", e)
			}
		}
	}
}
