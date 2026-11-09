package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
)

//go:embed rs_detail_fixture_test.go
var rsDetailJournal string

func rsDetailHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+rsDetailJournal)))
}
func newRSDetailSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1),(10,'collection',0,1,1,1,0)`)
	f.exec(t, `BEGIN`)
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	for kind := int64(1); kind <= 10; kind++ {
		id := 1100 + kind
		var authorKey any
		if kind == 8 {
			authorKey = "author:author"
		}
		public := make([]byte, 16)
		public[0] = 0x80
		public[15] = byte(kind)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_number,episode_number,numbering,artist_id,album_id,book_id,author,book_author_identity_key) VALUES(?,?,1,?,'Typed title','title','t','title','t',0,?,?,?,3,7,'absolute',?,?,?,'Author',?)`, id, public, kind, public, public, int64(1102), int64(1105), int64(1106), int64(1108), authorKey)
	}
	f.exec(t, `UPDATE catalog_entities SET overview='Accepted overview',year=2024,original_title='Original',tagline='Accepted tagline',edition='Deluxe',release_date='2024-01-01',content_rating='PG',studio='Studio',network='Network',country='CA',added_at='2026-11-01T00:00:00.000000000Z',last_content_added_at='2026-11-05T00:00:00.000000000Z',record_label='Record label',release_country='CA',edition_key='deluxe',disc_number=2,track_number=9,author='Author',narrator='Narrator',series='Series',series_position='2.5',part_number=4,collection_name_key='typed collection',collection_revision=5 WHERE id BETWEEN 1101 AND 1110`)
	// Only a show may carry nondefault show settings, and the E canonical CHECK
	// requires explicit presence for them: this row is an honest owner save at
	// revision three, not absent settings inferred from the default booleans.
	f.exec(t, `UPDATE catalog_entities SET hide_seasons=1,absolute_numbering=1,newest_first=1,show_settings_present=1,show_settings_revision=3,show_ranges='1-7' WHERE id=1102`)
	f.exec(t, `COMMIT`)
	return f
}
