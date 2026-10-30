package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// seedBulkFacts creates the same reference shape with set-based fact inserts.
// All production triggers remain enabled. This measures derivation and reads,
// not scanner throughput; incremental additions still use the write API.
func seedBulkFacts(db *sql.DB, n int) (first, last float64, err error) {
	return seedBulkFactsDensity(db, n, false)
}

func seedBulkFactsDensity(db *sql.DB, n int, narrow bool) (first, last float64, err error) {
	ctx := context.Background()
	schedule, e := dbwork.NewCheckpointSchedule(ctx, db)
	if e != nil {
		return 0, 0, e
	}
	s := shapeOf(n)
	creditPredicate, genrePredicate := "", ""
	if narrow {
		s.people = max(24, n/100)
		creditPredicate = " WHERE kind=1 OR kind=4 AND i%250=0"
		genrePredicate = " WHERE g=0"
	}
	exec := func(q string, args ...any) error { _, e := db.Exec(q, args...); return e }
	if err = exec(`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/fixture/movies'),('tv','TV','tv','/fixture/tv'),('music','Music','music','/fixture/music')`); err != nil {
		return
	}
	var movies, tv, music int64
	for _, v := range []struct {
		name string
		id   *int64
	}{{"movies", &movies}, {"tv", &tv}, {"music", &music}} {
		if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, v.name).Scan(v.id); err != nil {
			return
		}
	}
	showBase, seasonBase, artistBase, albumBase := n+1, n+1+s.shows, n+1+s.shows*2, n+1+s.shows*2+s.albums/5+2
	// Container identity is fixed before any item refers to it.
	for _, v := range []struct {
		kind         compactcatalog.Kind
		start, count int
		library      int64
		label        string
	}{{compactcatalog.Show, showBase, s.shows, tv, "Show"}, {compactcatalog.Season, seasonBase, s.shows, tv, "Season"}, {compactcatalog.Artist, artistBase, s.albums/5 + 2, music, "Artist"}, {compactcatalog.Album, albumBase, s.albums, music, "Album"}} {
		for off := 0; off < v.count; off += 1000 {
			err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				rows := `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) `
				if _, e := tx.Exec(rows+`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,year) SELECT ?+i,randomblob(16),?,?,printf('%s %06d',?,i),printf('%s %06d',?,i),?,2000 FROM ids`, off, min(v.count, off+1000)-1, v.start, v.library, v.kind, v.label, v.label, string(v.label[0])); e != nil {
					return e
				}
				switch v.kind {
				case compactcatalog.Show:
					_, e := tx.Exec(`INSERT INTO catalog_shows(entity_id,local_key) SELECT id,CAST(id-? AS TEXT) FROM catalog_entities WHERE id>=? AND id<?`, showBase, v.start+off, v.start+min(v.count, off+1000))
					return e
				case compactcatalog.Season:
					if _, e := tx.Exec(`UPDATE catalog_entities SET parent_id=id-?+? WHERE id>=? AND id<?`, seasonBase, showBase, v.start+off, v.start+min(v.count, off+1000)); e != nil {
						return e
					}
					_, e := tx.Exec(`INSERT INTO catalog_seasons(entity_id,show_id,number) SELECT id,parent_id,1 FROM catalog_entities WHERE id>=? AND id<?`, v.start+off, v.start+min(v.count, off+1000))
					return e
				case compactcatalog.Artist:
					_, e := tx.Exec(`INSERT INTO catalog_artists(entity_id,local_key) SELECT id,CAST(id-? AS TEXT) FROM catalog_entities WHERE id>=? AND id<?`, artistBase, v.start+off, v.start+min(v.count, off+1000))
					return e
				case compactcatalog.Album:
					if _, e := tx.Exec(`UPDATE catalog_entities SET parent_id=?+CASE WHEN id-?<417 THEN 0 ELSE (id-?)/5+1 END WHERE id>=? AND id<?`, artistBase, albumBase, albumBase, v.start+off, v.start+min(v.count, off+1000)); e != nil {
						return e
					}
					_, e := tx.Exec(`INSERT INTO catalog_albums(entity_id,artist_id,local_key) SELECT id,parent_id,CAST(id-? AS TEXT) FROM catalog_entities WHERE id>=? AND id<?`, albumBase, v.start+off, v.start+min(v.count, off+1000))
					return e
				}
				return nil
			})
			if err != nil {
				return
			}
		}
	}
	if err = exec(`INSERT INTO catalog_attribute_fields(field) VALUES('contentRating') ON CONFLICT(field) DO NOTHING`); err != nil {
		return
	}
	if err = exec(`INSERT INTO catalog_attribute_terms(field_id,value_key) SELECT id,'g' FROM catalog_attribute_fields WHERE field='contentRating' UNION ALL SELECT id,'r' FROM catalog_attribute_fields WHERE field='contentRating'`); err != nil {
		return
	}
	if err = exec(`INSERT INTO catalog_credit_labels(label) VALUES('Actor'),('Acting') ON CONFLICT(label) DO NOTHING`); err != nil {
		return
	}
	for off := 0; off < s.people; off += 10000 {
		if err = exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_people(id,token,identity_key,name,sort_name) SELECT i+1,printf('fixture-person-%d',i),printf('fixture:person%d',i),printf('Person person%d',i),printf('person person%d',i) FROM ids`, off, min(s.people, off+10000)-1); err != nil {
			return
		}
	}
	if err = exec(`WITH RECURSIVE ids(i) AS(SELECT 0 UNION ALL SELECT i+1 FROM ids WHERE i<4999) INSERT INTO catalog_terms(id,vocab,key,label) SELECT i+1,1,printf('genre %d',i),printf('Genre %d',i) FROM ids`); err != nil {
		return
	}
	for off := 0; off < n; off += 1000 {
		if err = checkScratchBudget(); err != nil {
			return
		}
		schedule.Tick(ctx)
		began := time.Now()
		err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			x := func(q string, args ...any) error { _, e := tx.Exec(q, args...); return e }
			if e := x(`CREATE TEMP TABLE IF NOT EXISTS fixture_items(i INTEGER PRIMARY KEY,library INTEGER,kind INTEGER,parent INTEGER,number INTEGER,added TEXT)`); e != nil {
				return e
			}
			if e := x(`DELETE FROM fixture_items`); e != nil {
				return e
			}
			if e := x(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO fixture_items
 SELECT i,CASE WHEN i<? THEN ? WHEN i<? THEN ? ELSE ? END,
 CASE WHEN i<? THEN 1 WHEN i<? THEN 4 ELSE 7 END,
 CASE WHEN i<? THEN NULL WHEN i<? THEN ?+CASE WHEN i-?<1100 THEN 0 ELSE 1+(i-?-1100)%? END ELSE ?+min(?,(i-?)/12) END,
 CASE WHEN i<? THEN 0 WHEN i<? THEN CASE WHEN i-?<1100 THEN i-?+1 ELSE 1+(i-?-1100)/? END ELSE (i-?)%12+1 END,
 strftime('%Y-%m-%dT%H:%M:%fZ','2026-01-01','+'||(i%86400)||' seconds') FROM ids`, off, min(n, off+1000)-1, s.movies, movies, s.movies+s.episodes, tv, music, s.movies, s.movies+s.episodes, s.movies, s.movies+s.episodes, seasonBase, s.movies, s.movies, s.shows-1, albumBase, s.albums-1, s.movies+s.episodes, s.movies, s.movies+s.episodes, s.movies, s.movies, s.movies, s.shows-1, s.movies+s.episodes); e != nil {
				return e
			}
			for _, q := range []string{
				`INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,title,sort_key,sort_head,year,added_ms) SELECT i+1,randomblob(16),library,kind,parent,printf('Title %09d',i),printf('title %09d',i),'t',1950+i%76,CAST(unixepoch(added)*1000 AS INTEGER) FROM fixture_items`,
				`INSERT INTO catalog_item_details(entity_id,added_text) SELECT i+1,added FROM fixture_items`,
				fmt.Sprintf(`INSERT INTO catalog_episodes(entity_id,show_id,season_id,numbering,number) SELECT i+1,parent-%d+%d,parent,'seasonal',number FROM fixture_items WHERE kind=4`, seasonBase, showBase),
				`INSERT INTO catalog_songs(entity_id,album_id,disc_number,track_number) SELECT i+1,parent,1,number FROM fixture_items WHERE kind=7`,
				`INSERT INTO catalog_song_artists(song_id,artist_id) SELECT f.i+1,a.artist_id FROM fixture_items f JOIN catalog_albums a ON a.entity_id=f.parent WHERE f.kind=7`,
				`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) SELECT (i+1)*3+v,printf('fixture-asset-%d',(i+1)*3+v),printf('/fixture/%d/item%09d.%d.mp4',library,i,v),1048576,1,'mp4','h264','aac',1920,1080,3600 FROM fixture_items CROSS JOIN (SELECT 0 v UNION ALL SELECT 1 UNION ALL SELECT 2) WHERE v=0 OR kind=1 AND v<=i%3`,
				`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT i+1,(i+1)*3+v FROM fixture_items CROSS JOIN (SELECT 0 v UNION ALL SELECT 1 UNION ALL SELECT 2) WHERE v=0 OR kind=1 AND v<=i%3`,
				`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) SELECT i+1,t.id,CASE WHEN i%10=0 THEN 'G' ELSE 'R' END,0 FROM fixture_items JOIN catalog_attribute_fields f ON f.field='contentRating' JOIN catalog_attribute_terms t ON t.field_id=f.id AND t.value_key=CASE WHEN i%10=0 THEN 'g' ELSE 'r' END WHERE i%10<2`,
				fmt.Sprintf(`INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,provider_person_id,source_ordinal) SELECT i+1,c,(i*3+c)%%%d+1,(SELECT id FROM catalog_credit_labels WHERE label='Actor'),(SELECT id FROM catalog_credit_labels WHERE label='Acting'),printf('Person person%%d',(i*3+c)%%%d),'fixture',printf('person%%d',(i*3+c)%%%d),printf('person%%d',(i*3+c)%%%d),c FROM fixture_items CROSS JOIN (SELECT 0 c UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5)`, s.people, s.people, s.people, s.people) + creditPredicate,
				`INSERT INTO catalog_entity_terms(entity_id,term_id) SELECT i+1,(i+g)%5000+1 FROM fixture_items CROSS JOIN (SELECT 0 g UNION ALL SELECT 1 UNION ALL SELECT 2)` + genrePredicate,
				`INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,source_name,ordinal) SELECT i+1,(i+g)%5000+1,'fixture',CAST((i+g)%5000 AS TEXT),printf('Genre %d',(i+g)%5000),g FROM fixture_items CROSS JOIN (SELECT 0 g UNION ALL SELECT 1 UNION ALL SELECT 2)` + genrePredicate,
				`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) SELECT profile,i+1,120000,0,profile!='viewer' AND i%300=0,'fixture' FROM fixture_items CROSS JOIN (SELECT 'viewer' profile UNION ALL SELECT 'viewer-two' UNION ALL SELECT 'viewer-pg13') WHERE i%100=0`,
				`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT profile,l.library_id,i+1,'2026-09-22T12:00:00Z',CASE WHEN profile!='viewer' AND i%300=0 THEN 'ended' ELSE 'paused' END FROM fixture_items JOIN catalog_libraries l ON l.id=library CROSS JOIN (SELECT 'viewer' profile UNION ALL SELECT 'viewer-two' UNION ALL SELECT 'viewer-pg13') WHERE i%100=0`,
				`INSERT INTO catalog_identities(root,source_key,public_id,kind) SELECT l.root,printf('fixture:%d',f.i),e.public_id,e.kind FROM fixture_items f JOIN catalog_entities e ON e.id=f.i+1 JOIN catalog_libraries l ON l.id=e.library_id`,
			} {
				if e := x(q); e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			return
		}
		last = float64(time.Since(began)) / float64(time.Millisecond) / float64(min(n, off+1000)-off)
		if off == 0 {
			first = last
		}
		if off%10000 == 0 || off+1000 >= n {
			fmt.Printf("bulk facts %d/%d latest %.3f ms/item\n", min(n, off+1000), n, last)
		}
	}
	return
}
