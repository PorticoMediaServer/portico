package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"testing"
	"time"
)

//go:embed rs_reading_fixture_test.go
var rsReadingJournal string

func rsReadingHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+rsReadingJournal)))
}
func rsReadingSource(t *testing.T) *browseFixture {
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`)
	id := []byte{0xf0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1101,?,1,20)`, id)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,has_reading_attribution_members,reading_numbered_order,reading_number_value,reading_author_value,reading_author_key,reading_author_source)VALUES(1101,?,1,20,'Accepted title','accepted','a','accepted','a',0,?,?,1,1,0,'Accepted author','accepted author','owner')`, id, id, id)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1101,1,1)`)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(1101,1,'epub')`)
	f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health)VALUES('source','movies','local','/media','/media','inc','ready')`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)VALUES('object','source','asset','inc','title.mkv','r','{}',1000,1)`)
	return f
}
func rsReadingFixture(t *testing.T) (*browseFixture, *RSReadingReader) {
	f := composeReadFixture(t, rsReadingSource(t), append(browseDefinitions(), ReadingFactsDefinitions()...), readfixture.LiveConfig(time.Now), true, rsReadingHash(t))
	r, err := NewRSReadingReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func TestRSReadingFactsPreserveAttribution(t *testing.T) {
	f, r := rsReadingFixture(t)
	title := "é\x00Reading"
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1101`, title)
	in := ReadingFactsInput{View: f.input, Item: 1101}
	in.View.Kind = 20
	p, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.ID != 1101 || p.Title != title || !p.Available || p.Author.Value == nil || *p.Author.Value != "Accepted author" || p.Series.Value != nil || p.NumberedOrder == nil || *p.NumberedOrder != 1 {
		t.Fatal(p, cost, err)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='object'`)
	p, _, err = r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Available || p.ID != 1101 {
		t.Fatal("unavailable title was removed", p, err)
	}
}
