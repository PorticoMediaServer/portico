package persistence

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadingSourceValuesKeepTypedPerPublicationEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	library := persistenceLibrary(t, db, "read", "Read", "book", "/read")
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,20,'Book','book','b')`, library)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'asset-token','/read/book.epub',1,1,'epub','','',0,0,0)`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) VALUES(1,1)`)
	exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1,1,'epub')`)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','read','asset-token','root','book.epub','opaque:revision','{}',1,1)`)
	const insert = `INSERT INTO reading_source_values(item_id,asset_id,field,text_value,number_value,source,inventory_object_id,source_revision) VALUES(1,1,?,?,?,?,?,'opaque:revision')`
	for _, source := range []string{"folder", "file-name", "file-metadata"} {
		exec(insert, "author", "Author from "+source, nil, source, "object")
	}
	exec(insert, "series", "Dune", nil, "folder", "object")
	exec(insert, "seriesNumber", nil, 2.5, "folder", "object")
	var number float64
	if err := db.QueryRow(`SELECT number_value FROM reading_source_values WHERE field='seriesNumber'`).Scan(&number); err != nil || number != 2.5 {
		t.Fatalf("decimal source number changed: %v, %v", number, err)
	}
	for _, input := range []struct {
		field, source string
		text, number  any
	}{
		{"title", "folder", "Title", nil}, {"author", "owner", "Owner", nil},
		{"author", "folder", "", nil}, {"author", "folder", strings.Repeat("x", 4097), nil},
		{"author", "folder", nil, nil}, {"author", "folder", "Author", 1},
		{"seriesNumber", "file-name", "2.5", 2.5}, {"seriesNumber", "file-name", nil, nil},
		{"seriesNumber", "file-name", nil, -1}, {"seriesNumber", "file-name", nil, math.Inf(1)},
		{"seriesNumber", "file-name", nil, math.NaN()}, {"seriesNumber", "file-name", nil, "unknown"},
	} {
		if _, err := db.Exec(`UPDATE reading_source_values SET field=?,text_value=?,number_value=?,source=? WHERE field='author' AND source='folder'`, input.field, input.text, input.number, input.source); err == nil {
			t.Fatalf("accepted invalid %s/%s source fact", input.field, input.source)
		}
	}
	// Refreshing path candidates cannot erase a higher-priority embedded value.
	exec(`DELETE FROM reading_source_values WHERE source IN('folder','file-name')`)
	var text, revision string
	if err := db.QueryRow(`SELECT text_value,source_revision FROM reading_source_values WHERE field='author' AND source='file-metadata'`).Scan(&text, &revision); err != nil || text != "Author from file-metadata" || revision != "opaque:revision" {
		t.Fatalf("path refresh changed accepted embedded evidence: %q/%q, %v", text, revision, err)
	}
	for _, change := range []string{"inventory_object_id='missing'", "source_revision=''", "item_id=2", "asset_id=2"} {
		if _, err := db.Exec(`UPDATE reading_source_values SET ` + change); err == nil {
			t.Fatalf("accepted invalid publication/source association: %s", change)
		}
	}
	if _, err := db.Exec(insert, "author", "Duplicate", nil, "file-metadata", "object"); err == nil {
		t.Fatal("duplicate source replaced accepted evidence")
	}
	count := func(want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM reading_source_values`).Scan(&got); err != nil || got != want {
			t.Fatalf("source rows=%d, want=%d: %v", got, want, err)
		}
	}
	exec(`DELETE FROM inventory_objects WHERE id='object'`)
	count(0)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','read','asset-token','root','book.epub','opaque:revision','{}',1,1)`)
	exec(insert, "author", "Author", nil, "folder", "object")
	exec(`DELETE FROM reading_publications WHERE item_id=1 AND asset_id=1`)
	count(0)
}
