package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Accepted-fact model proof; these synthetic content keys make no image decode
// claim. Collector image decoding and source admission have separate tests.
func TestLocalArtworkOrderSurvivesRestartAndCompositeContinuation(t *testing.T) {
	for _, count := range []int{9, 130} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "artwork.sqlite")
			db, err := persistence.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			c := catalogtest.New(t, db)
			lib := c.Library("m", "Movies", "movie", "/m")
			item := c.Movie(lib, "/m/Film.mkv", "Film", 2000)
			s := New(db)
			source, err := s.LibrarySource(ctx, "m")
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('ordered-object',?,?,?,'Film.mkv','r1','{}',1,1)`, source.ID, item.Token, source.Incarnation)
			write := func(values []assets.LocalArtworkCandidate) {
				t.Helper()
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					return s.commitInventoryArtworkCandidatesTx(ctx, tx, source, "ordered-object", "r1", item.Token, "movie", values)
				})
			}
			read := func(after *LocalArtworkCandidateCursor, limit int) []LocalArtworkCandidateCursor {
				t.Helper()
				tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				page, err := ReadLocalArtworkCandidatePageTx(ctx, tx, "item", item.ID, "poster", after, limit)
				if err != nil {
					t.Fatal(err)
				}
				return page
			}
			key := func(n int) string { return fmt.Sprintf("%064x", n) }
			values := []assets.LocalArtworkCandidate{}
			for n := count - 1; n >= 1; n-- {
				values = append(values, assets.LocalArtworkCandidate{Slot: "item/poster", Key: key(n), SourceName: fmt.Sprintf("poster-%d.png", n), SourceRank: assets.LocalArtworkNumberedRankBase + int64(n)})
			}
			// An alternate arrives first. The later canonical image has the
			// largest hash, so a hash-ordered default would be wrong.
			write([]assets.LocalArtworkCandidate{{Slot: "item/poster", Key: key(7), SourceName: "poster-7.png", SourceRank: 1031}})
			canonical := assets.LocalArtworkCandidate{Slot: "item/poster", Key: key(count), SourceName: "poster.jpg", SourceRank: 16}
			// Equal image bytes under two names must retain their best actual
			// source order, regardless of the caller's enumeration order.
			values = append(values, assets.LocalArtworkCandidate{Slot: "item/poster", Key: canonical.Key, SourceName: "poster-777.png", SourceRank: 1801})
			values = append(values, canonical)
			write(values)
			first := read(nil, 8)
			if len(first) != 8 || first[0].Key != canonical.Key || first[0].SourceName != "poster.jpg" {
				t.Fatal("canonical default lost to first arrival/hash", first)
			}
			tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			preferred, err := ReadDefaultLocalArtworkCandidateTx(ctx, tx, "item", item.ID, "poster")
			tx.Rollback()
			if err != nil || preferred == nil || *preferred != first[0] {
				t.Fatal("default disagrees with canonical page", preferred, err)
			}
			c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, canonical.Key)
			c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('owner-choice','item',?,'poster','local','owner',?,'Local file','owner-fence','now')`, item.ID, "local:"+key(7))
			c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) SELECT column1,column2,column3,column4,column5,column7,column8,column9 FROM (VALUES('item',?,'poster','owner-choice',?,?,0,'owner','now'))`, item.ID, canonical.Key, canonical.Key)
			checkDefault := func(blocked bool) {
				t.Helper()
				tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				value, err := ReadDefaultLocalArtworkCandidateTx(ctx, tx, "item", item.ID, "poster")
				if err != nil || blocked != (value == nil) {
					t.Fatal("default ignored owner authority", value, err)
				}
			}
			checkDefault(true) // A deliberately unlocked owner choice still wins.
			c.Exec(`UPDATE artwork_selections SET actor='',locked=1 WHERE entity_id=?`, item.ID)
			checkDefault(true)
			c.Exec(`UPDATE artwork_selections SET actor='',locked=0 WHERE entity_id=?`, item.ID)
			checkDefault(false) // An automatic selection may adopt a better default.
			c.Exec(`INSERT INTO artwork_jobs(id,kind,entity_id,role,candidate_id,origin,provider,source_fence,selection_revision,actor,created_at) VALUES('owner-job','item',?,'poster','owner-choice',?,'local','owner-fence',1,'owner','now')`, item.ID, "local:"+key(7))
			checkDefault(true)
			c.Exec(`DELETE FROM artwork_jobs WHERE id='owner-job'`)
			c.Exec(`DELETE FROM artwork_selections WHERE entity_id=?`, item.ID)
			resume := first[len(first)-1]
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = persistence.Open(path) // same binary/baseline, genuine restart
			if err != nil {
				t.Fatal(err)
			}
			c.DB = db
			s = New(db)
			all := append([]LocalArtworkCandidateCursor(nil), first...)
			for calls := 0; calls < count; calls++ {
				page := read(&resume, 8)
				all = append(all, page...)
				if len(page) < 8 {
					break
				}
				resume = page[len(page)-1]
			}
			if len(all) != count {
				t.Fatalf("restart continuation got %d of %d", len(all), count)
			}
			seen := map[string]bool{}
			for index, value := range all {
				want := key(index)
				if index == 0 {
					want = canonical.Key
				}
				if value.Key != want || seen[value.Key] {
					t.Fatalf("duplicate/skip/order at %d: %+v want %s", index, value, want)
				}
				seen[value.Key] = true
			}
			page := read(nil, 128)
			across := append([]LocalArtworkCandidateCursor(nil), page...)
			if len(page) == 128 {
				cursor := page[127]
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = persistence.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				c.DB = db
				s = New(db)
				across = append(across, read(&cursor, 128)...)
			}
			if !reflect.DeepEqual(all, across) {
				t.Fatal("128-boundary changed composite order")
			}
			c.Exec(`DELETE FROM artwork_dirty`)
			write(values)
			if n := rows(t, c, `SELECT count(*) FROM artwork_dirty`); n != 0 {
				t.Fatal("unchanged ordered set dirtied head", n)
			}
			if !reflect.DeepEqual(first, read(nil, 8)) {
				t.Fatal("repeated publication reordered first page")
			}
			// Name/rank are part of the accepted fact, even with equal pixels.
			values[len(values)-1].SourceName = "Poster.jpg"
			write(values)
			if n := rows(t, c, `SELECT count(*) FROM artwork_dirty WHERE kind='item' AND entity_id=?`, item.ID); n != 1 {
				t.Fatal("order-only change did not dirty affected head", n)
			}
		})
	}
}

func TestLocalArtworkCompositeReaderRejectsIncompleteCursorAndBound(t *testing.T) {
	c := catalogtest.Open(t)
	tx, err := c.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, cursor := range []LocalArtworkCandidateCursor{{SourceRank: -1}, {SourceRank: 0, Key: fmt.Sprintf("%064x", 1)}, {SourceRank: 16, SourceName: "../poster.jpg", Key: fmt.Sprintf("%064x", 1)}, {SourceRank: 16, SourceName: "poster.jpg", Key: "not-a-content-key"}} {
		if _, err := ReadLocalArtworkCandidatePageTx(context.Background(), tx, "item", 1, "poster", &cursor, 8); err == nil {
			t.Fatal("malformed cursor accepted")
		}
	}
	for _, limit := range []int{0, 129} {
		if _, err := ReadLocalArtworkCandidatePageTx(context.Background(), tx, "item", 1, "poster", nil, limit); err == nil {
			t.Fatal("invalid page bound accepted")
		}
	}
}
