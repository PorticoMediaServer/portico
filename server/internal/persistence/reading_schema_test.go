package persistence

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestEpisodeThumbnailStoredCreationPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"tv", "anime", "movie", "music", "audiobook", "book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, kind, kind, kind, "/"+kind); err != nil {
				t.Fatal(err)
			}
			var mode string
			var revision int64
			if err := db.QueryRow(`SELECT episode_thumbnail_mode,revision FROM library_scan_policies WHERE library_id=?`, kind).Scan(&mode, &revision); err != nil {
				t.Fatal(err)
			}
			want := "off"
			if kind == "tv" || kind == "anime" {
				want = "provider_then_made"
			}
			if mode != want || revision != 1 {
				t.Fatalf("creation policy = %q/%d, want %q/1", mode, revision, want)
			}
			if _, err := db.Exec(`UPDATE library_scan_policies SET tier='complete',revision=revision+1 WHERE library_id=?`, kind); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT episode_thumbnail_mode FROM library_scan_policies WHERE library_id=?`, kind).Scan(&mode); err != nil || mode != want {
				t.Fatalf("unrelated policy write changed mode: %q, %v", mode, err)
			}
		})
	}
	for _, mode := range []string{"", "automatic", "made", "PROVIDER"} {
		if _, err := db.Exec(`UPDATE library_scan_policies SET episode_thumbnail_mode=? WHERE library_id='tv'`, mode); err == nil {
			t.Fatalf("accepted unknown mode %q", mode)
		}
	}
	for _, mode := range []string{"provider_then_made", "provider", "off"} {
		if _, err := db.Exec(`UPDATE library_scan_policies SET episode_thumbnail_mode=? WHERE library_id='tv'`, mode); err != nil {
			t.Fatalf("rejected mode %q: %v", mode, err)
		}
	}
}

func TestReadingPublicationLocationAndUnlinkIntegrity(t *testing.T) {
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
	for kind, name := range map[compactcatalog.Kind]string{compactcatalog.Book: "book", compactcatalog.Part: "audiobook_file", compactcatalog.Ebook: "ebook", compactcatalog.Comic: "comic"} {
		parsed, err := compactcatalog.ParseKind(name)
		actual, nameErr := kind.Name()
		if err != nil || nameErr != nil || parsed != kind || actual != name {
			t.Fatalf("persisted kind %d/%s changed: %d/%s: %v/%v", kind, name, parsed, actual, err, nameErr)
		}
	}
	if _, err := compactcatalog.Kind(14).Name(); err == nil {
		t.Fatal("unassigned kind became Reading")
	}
	if _, err := compactcatalog.ParseKind(""); err == nil {
		t.Fatal("empty name became a reserved kind")
	}
	count := func(table string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s rows = %d, want %d: %v", table, got, want, err)
		}
	}
	for _, kind := range []int{20, 21} {
		var playable, listening, searchable, browsable int
		if err := db.QueryRow(`SELECT playable,listening,searchable,browsable FROM catalog_kinds WHERE id=?`, kind).Scan(&playable, &listening, &searchable, &browsable); err != nil || playable != 0 || listening != 0 || searchable != 1 || browsable != 1 {
			t.Fatalf("Reading kind %d acquired audio/video permissions: %d/%d/%d/%d: %v", kind, playable, listening, searchable, browsable, err)
		}
	}
	library := persistenceLibrary(t, db, "read", "Read", "book", "/read")
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,20,'Book','book','b')`, library)
	for id := 1; id <= 3; id++ {
		exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,?,1,1,'pdf','','',0,0,0)`, id, fmt.Sprintf("source-%d", id), fmt.Sprintf("/read/%d.pdf", id))
	}
	if _, err := db.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1,3,'pdf')`); err == nil {
		t.Fatal("publication accepted an asset without its catalogue link")
	}
	for id, format := range []string{"epub", "pdf"} {
		exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) VALUES(1,?)`, id+1)
		exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1,?,?)`, id+1, format)
	}
	exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at) VALUES('one',1,1,'observed-1','epub','epubcfi(/6/2)','chapter.xhtml',NULL,0.25,1,10)`)
	exec(`INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,page_index,progression,revision,observed_at) VALUES('one',1,2,'observed-2','pdf',0,0,1,11),('two',1,2,'observed-2','pdf',7,0.5,1,12)`)
	for _, change := range []string{
		`page_index=NULL`, `page_index=-1`, `cfi='not-a-page-location'`, `section='chapter'`,
		`progression=-0.01`, `progression=1.01`, `revision=0`, `revision=9007199254740992`,
		`observed_at=-1`, `source_revision=''`, `format='audiobook'`,
	} {
		if _, err := db.Exec("UPDATE reading_progress SET " + change + " WHERE owner_key='one' AND asset_id=2"); err == nil {
			t.Fatalf("accepted invalid location %s", change)
		}
	}
	if _, err := db.Exec(`UPDATE reading_progress SET page_index=1 WHERE owner_key='one' AND asset_id=1`); err == nil {
		t.Fatal("EPUB accepted a page-index location")
	}
	result, err := db.Exec(`UPDATE reading_progress SET progression=0.5,revision=revision+1 WHERE owner_key='one' AND item_id=1 AND asset_id=1 AND revision=1`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		t.Fatalf("matching version CAS changed %d rows", n)
	}
	result, err = db.Exec(`UPDATE reading_progress SET progression=0.75,revision=revision+1 WHERE owner_key='one' AND item_id=1 AND asset_id=1 AND revision=1`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := result.RowsAffected(); n != 0 {
		t.Fatalf("stale version CAS changed %d rows", n)
	}
	exec(`INSERT INTO retained_title_state(id,profile_id,kind,retained_ms) VALUES(1,'one',20,12),(2,'two',20,12)`)
	exec(`INSERT INTO retained_reading_progress(state_id,source_id,source_revision,format,page_index,progression,revision,observed_at) VALUES(1,'source-2','old-observation','pdf',7,0.5,1,12),(2,'source-2','old-observation','pdf',9,0.75,1,12)`)
	exec(`DELETE FROM catalog_asset_links WHERE entity_id=1 AND asset_id=2`)
	count("reading_publications", 1)
	count("reading_progress", 1)
	count("retained_reading_progress", 2)
	exec(`DELETE FROM retained_title_state WHERE id=1`)
	count("retained_reading_progress", 1)
	var source, revision string
	if err := db.QueryRow(`SELECT source_id,source_revision FROM retained_reading_progress WHERE state_id=2`).Scan(&source, &revision); err != nil || source != "source-2" || revision != "old-observation" {
		t.Fatalf("retained source was retargeted: %q/%q: %v", source, revision, err)
	}
	var badFK sql.NullString
	if err := db.QueryRow(`SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&badFK); err != sql.ErrNoRows {
		t.Fatalf("foreign-key integrity: %v, %v", badFK, err)
	}
}
