package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestDetailFileInfoMemberTechnicalFactsAndOwnerPaths(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(library, "/movies/Private.Title.mkv", "Title", 2020)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}
	member, err := s.FileInfo(viewer, "server", movie.Public, "", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(member.Files) != 1 || member.Files[0].Path != "" || member.Manage != nil || member.Files[0].Duration <= 0 {
		t.Fatalf("member %+v", member)
	}
	owner, err := s.FileInfo(viewer, "server", movie.Public, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Files[0].Path != "/movies/Private.Title.mkv" || owner.Manage == nil || !owner.Manage.Delete {
		t.Fatalf("owner %+v", owner)
	}
	viewer.Libraries = nil
	if _, err = s.FileInfo(viewer, "server", movie.Public, "", 0, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden library: %v", err)
	}
}

func TestFileInfoBatchesChildPageAndPreservesFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Album", 2026)
	items := make([]catalogtest.Item, 201)
	for i := range items {
		items[i] = c.Song(album, i+1, fmt.Sprintf("/music/%03d.flac", i), fmt.Sprintf("Track %03d", i))
	}
	// Multiple versions and one asset linked to two authorized children retain
	// the per-child version order and each child's own track arrays.
	c.File(items[0].ID, "/music/alternate.flac", 120)
	c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,available,part_index,edition) VALUES(?,?,1,0,'shared')`, items[1].ID, items[0].Asset)
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'test' FROM catalog_assets`)
	c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json)
 SELECT token,0,'video','h264','','Picture',0,'',1,0,'bt709','' FROM catalog_assets
 UNION ALL SELECT token,1,'audio','flac','en','English',2,'stereo',1,0,'','' FROM catalog_assets
 UNION ALL SELECT token,2,'audio','aac','fr','French',6,'5.1',0,0,'','' FROM catalog_assets`)
	c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json)
 SELECT token,3,'video','hevc','','Attached cover',0,'',0,0,'smpte2084','' FROM catalog_assets`)
	c.Exec(`UPDATE catalog_assets SET video_codec='',width=0,height=0 WHERE token=?`, items[2].Token)
	c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) SELECT token,1,size,modified_ns,0,1,'known','test' FROM catalog_assets`)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason)
 SELECT token||'-fr',token,'embedded','stream4',4,'srt','fr','French',0,1,'' FROM catalog_assets
 UNION ALL SELECT token||'-en',token,'embedded','stream3',3,'ass','en','English',1,0,'' FROM catalog_assets`)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}}
	for _, owner := range []bool{false, true} {
		for _, limit := range []int{40, 200} {
			var want []TitleFile
			for _, item := range items[:limit] {
				files, err := s.referenceTitleFiles(item.Public, owner)
				if err != nil {
					t.Fatal(err)
				}
				for i := range files {
					files[i].ItemID = item.Public
				}
				want = append(want, files...)
			}
			ctx, cost := dbwork.Measure(context.Background())
			var got FileInfo
			err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
				var err error
				got, err = s.WithContext(ctx).FileInfo(viewer, "server", album.Public, "", limit, owner)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Files, want) || got.NextCursor == "" {
				t.Fatalf("owner=%t limit=%d: facts/order/continuation differ, got %d want %d", owner, limit, len(got.Files), len(want))
			}
			if statements := cost().Statements; statements > 24 {
				t.Fatalf("owner=%t limit=%d: %d statements, child/asset reads are not batched", owner, limit, statements)
			}
		}
	}
	viewer.Libraries = nil
	if got, err := s.FileInfo(viewer, "server", album.Public, "", 40, true); !errors.Is(err, sql.ErrNoRows) || len(got.Files) != 0 {
		t.Fatalf("denied library returned files: %d %v", len(got.Files), err)
	}
}

func TestFileInfoBatchedDiscFactsKeepObservationFences(t *testing.T) {
	s, db, source, job, object, policy, probe := discPublicationFixture(t)
	var token, revision string
	if err := db.QueryRow(`SELECT asset_id,revision FROM inventory_objects WHERE id=?`, object).Scan(&token, &revision); err != nil {
		t.Fatal(err)
	}
	if receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe); err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c := catalogtest.New(t, db)
	c.Drain()
	c.Exec(`UPDATE catalog_assets SET available=1 WHERE token=?`, token)
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,7,size,modified_ns,'test' FROM catalog_assets WHERE token=?`, token)
	c.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) SELECT token,9,size,modified_ns,'known','test' FROM catalog_assets WHERE token=?`, token)
	c.Exec(`UPDATE inventory_disc_facts SET media_title_key=?,media_stream_revision=7,media_chapter_revision=9 WHERE object_id=?`, probe.Titles[0].Key(probe.Kind), object)
	compare := func(present bool) {
		t.Helper()
		want, err := ReadDiscMediaFacts(context.Background(), s.read(), token)
		if err != nil {
			t.Fatal(err)
		}
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		got, err := s.WithContext(ctx).readDiscMediaFactsBatch([]string{token, "absent"})
		if err != nil || !reflect.DeepEqual(got[token], want) || (want != nil) != present || got["absent"] != nil {
			t.Fatalf("batch changed exact disc facts: got=%+v want=%+v present=%t err=%v", got[token], want, present, err)
		}
		if present {
			for _, query := range trace() {
				if !strings.HasPrefix(query, "SELECT a.token,d.kind") {
					continue
				}
				rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, idsJSON([]string{token, "absent"}))
				if err != nil {
					t.Fatal(err)
				}
				var plan []string
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					plan = append(plan, detail)
					if strings.HasPrefix(detail, "SCAN a") || strings.HasPrefix(detail, "SCAN d") {
						rows.Close()
						t.Fatalf("disc page lost indexed asset/fact lookup: %s", detail)
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				rows.Close()
				if !strings.Contains(strings.Join(plan, "\n"), "inventory_disc_facts_asset") {
					t.Fatalf("disc page did not seek facts by admitted assets: %v", plan)
				}
			}
		}
	}
	compare(true)
	// The owner's title list still comes from the original current observation
	// and the existing fingerprint-checked probe, not the optional display facts.
	err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		got := s.WithContext(ctx).discTitles([]TitleFile{{ID: "absent"}, {ID: token}})
		if got == nil || got.FileID != token || len(got.Titles) != len(probe.Titles) {
			t.Fatalf("owner title list changed: %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, source.ID)
	compare(false)
	c.Exec(`UPDATE library_sources SET generation=generation-1 WHERE id=?`, source.ID)
	c.Exec(`UPDATE inventory_objects SET revision=revision||'-changed' WHERE id=?`, object)
	compare(false)
	c.Exec(`UPDATE inventory_objects SET revision=? WHERE id=?`, revision, object)
	c.Exec(`UPDATE inventory_objects SET state='unavailable' WHERE id=?`, object)
	compare(false)
	c.Exec(`UPDATE inventory_objects SET state='available' WHERE id=?`, object)
	compare(true)
	c.Exec(`UPDATE asset_stream_facts SET revision=8 WHERE asset_id=?`, token)
	compare(false)
	c.Exec(`UPDATE asset_stream_facts SET revision=7 WHERE asset_id=?`, token)
	c.Exec(`UPDATE asset_chapter_facts SET status='unknown' WHERE asset_id=?`, token)
	compare(false)
	c.Exec(`UPDATE asset_chapter_facts SET status='known' WHERE asset_id=?`, token)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job)
 SELECT '!0-first-observation',source_id,asset_id,root_incarnation,'other',object_identity,revision,evidence_json,size,modified_ns,seen_job FROM inventory_objects WHERE id=?`, object)
	err = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		reader := s.WithContext(ctx)
		files := []TitleFile{{ID: token}}
		if got, want := reader.discTitles(files), reader.referenceDiscTitles(files); !reflect.DeepEqual(got, want) {
			t.Fatalf("later observation with facts replaced first observation: got=%+v want=%+v", got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,media_title_key,media_stream_revision,media_chapter_revision)
 SELECT '!0-first-observation',asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc)
 SELECT '!0-first-observation',title_key,selection_token||'-other',title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc FROM inventory_disc_titles WHERE object_id=?`, object)
	for _, table := range []string{"inventory_disc_clips", "inventory_disc_streams", "inventory_disc_chapters"} {
		rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name != "object_id" {
				names = append(names, name)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		columns := strings.Join(names, ",")
		c.Exec(`INSERT INTO `+table+`(object_id,`+columns+`) SELECT '!0-first-observation',`+columns+` FROM `+table+` WHERE object_id=?`, object)
	}
	compare(false) // Two current associations are ambiguous, never first-wins.
	// The lowest-ID current observation now lacks facts; the later original
	// observation still has its probe and must not substitute for it.
	c.Exec(`DELETE FROM inventory_disc_facts WHERE object_id=?`, "!0-first-observation")
	err = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		reader := s.WithContext(ctx)
		files := []TitleFile{{ID: token}}
		if got, want := reader.discTitles(files), reader.referenceDiscTitles(files); !reflect.DeepEqual(got, want) || want != nil {
			t.Fatalf("later observation with facts replaced original empty one: got=%+v want=%+v", got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
