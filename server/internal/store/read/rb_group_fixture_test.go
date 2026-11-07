package read

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"testing"
)

//go:embed rb_group_fixture_test.go
var rbGroupJournal string

func rbGroupFixtureHash() string { return fmt.Sprintf("%x", sha256.Sum256([]byte(rbGroupJournal))) }
func rbGroupSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1),(10,'collection',0,1,1,1,0),(13,'author',0,1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(1,'list','local','a','p','List','create','hash','2026-11-06T00:00:00Z')`)
	add := func(id, kind int64, title, extra string, args ...any) {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
		q := `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation` + extra + `) VALUES(?,?,1,?,?,?,'a',?,'a',0,?,?`
		values := []any{id, public, kind, title, title, title, public, public}
		for range args {
			q += ",?"
		}
		q += ")"
		f.exec(t, q, append(values, args...)...)
	}
	f.exec(t, "BEGIN")
	add(2001, 2, "Show", "")
	add(2002, 3, "Season", ",show_id,season_number", int64(2001), int64(1))
	add(2003, 5, "Artist", "")
	add(2004, 6, "Album", ",artist_id", int64(2003))
	add(2005, 13, "Author", "")
	add(2006, 8, "Book", ",author", "Author")
	add(2007, 10, "Collection", "")
	for n := int64(0); n < 80; n++ {
		disc, part := any(int64(1)), any(n%3)
		if n%4 == 0 {
			disc = nil
		}
		if n%5 == 0 {
			part = nil
		}
		retired := int64(0)
		if n%9 == 0 {
			retired = 1
		}
		add(3000+n, 7, fmt.Sprint("Song", n), ",album_id,disc_number,track_number,retired", int64(2004), disc, part, retired)
		add(4000+n, 9, fmt.Sprint("Part", n), ",book_id,disc_number,part_number,retired", int64(2006), disc, part, retired)
		// The accepted numbering modes can share a number: preserve the ID tie.
		add(5000+n, 4, fmt.Sprint("Episode", n), ",show_id,season_id,numbering,episode_number,retired", int64(2001), int64(2002), []string{"seasonal", "absolute", "date"}[n%3], n/3+1, retired)
		key := "b"
		if n >= 70 {
			key = "c"
		}
		f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(2007,?,?)`, 3000+n, key)
		f.exec(t, `INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) VALUES(?,?,1,?,?)`, n+1, fmt.Sprint("entry", n), 3000+n, fmt.Sprintf("b%03d", n))
	}
	f.exec(t, `INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) VALUES(100,'null',1,NULL,'b')`)
	f.exec(t, "COMMIT")
	return f
}
