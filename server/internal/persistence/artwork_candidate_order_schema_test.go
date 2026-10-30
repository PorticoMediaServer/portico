package persistence

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestArtworkCandidateOrderFactsKeepSafeDefaultsAndCompositeSeek(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	library := persistenceLibrary(t, db, "art", "Art", "movie", "/art")
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,1,'Movie','movie','m')`, library)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'asset','/art/movie.mp4',1,1,'mp4','h264','aac',1,1,1)`)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','art','asset','root','movie.mp4','revision','{}',1,1)`)
	key := func(id int) string { return fmt.Sprintf("%064x", id) }
	const rest int64 = 9223372036854775807
	// An unchanged writer may omit the new columns: unknown is never canonical rank zero.
	exec(`INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key) VALUES('object','revision','asset','item',1,'poster',?)`, key(4))
	var name string
	var rank int64
	if err := db.QueryRow(`SELECT source_name,source_rank FROM inventory_artwork_candidates WHERE art_key=?`, key(4)).Scan(&name, &rank); err != nil || name != "" || rank != rest {
		t.Fatalf("legacy ordering default changed: %q/%d, %v", name, rank, err)
	}
	const insert = `INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES('object','revision','asset','item',1,'poster',?,?,?)`
	// Store an alternate first. Ordering uses accepted facts, not hash or arrival order.
	exec(insert, key(1), "poster-7.png", 1031)
	exec(insert, key(3), "poster.jpg", 0)
	exec(insert, key(2), "Poster.jpg", 0)
	for _, value := range []any{nil, ".", "..", "folder/poster.jpg", `folder\poster.jpg`, "poster\x00.jpg", strings.Repeat("é", 8193)} {
		if _, err := db.Exec(`UPDATE inventory_artwork_candidates SET source_name=? WHERE art_key=?`, value, key(1)); err == nil {
			t.Fatalf("accepted invalid basename %T", value)
		}
	}
	for _, value := range []any{nil, -1, 0.5, "unknown"} {
		if _, err := db.Exec(`UPDATE inventory_artwork_candidates SET source_rank=? WHERE art_key=?`, value, key(1)); err == nil {
			t.Fatalf("accepted invalid rank %v", value)
		}
	}
	if _, err := db.Exec(`UPDATE inventory_artwork_candidates SET source_rank=0 WHERE art_key=?`, key(4)); err == nil {
		t.Fatal("unknown basename acquired canonical rank")
	}
	// The limit is bytes, with no truncation; named rest candidates remain representable.
	exec(insert, key(5), strings.Repeat("é", 8192), rest)
	exec(`DELETE FROM inventory_artwork_candidates WHERE art_key=?`, key(5))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const page = `SELECT art_key FROM inventory_artwork_candidates INDEXED BY inventory_artwork_candidates_order WHERE entity_id=1 AND kind='item' AND role='poster' AND (source_rank,source_name,art_key)>(?,?,?) ORDER BY source_rank,source_name,art_key LIMIT ?`
	read := func(afterRank int64, afterName, afterKey string, limit int) []string {
		t.Helper()
		rows, err := db.Query(page, afterRank, afterName, afterKey, limit)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var keys []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	if got := read(-1, "", "", 2); !reflect.DeepEqual(got, []string{key(2), key(3)}) {
		t.Fatalf("reopened first page reordered accepted facts: %v", got)
	}
	if got := read(0, "poster.jpg", key(3), 2); !reflect.DeepEqual(got, []string{key(1), key(4)}) {
		t.Fatalf("composite cursor lost the next accepted facts: %v", got)
	}
	// The old reader's explicitly named index remains available during the transition.
	var definition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='inventory_artwork_candidates_entity'`).Scan(&definition); err != nil || !strings.Contains(definition, "entity_id,kind,role,object_id,art_key") {
		t.Fatalf("old reader index changed: %q, %v", definition, err)
	}
}
