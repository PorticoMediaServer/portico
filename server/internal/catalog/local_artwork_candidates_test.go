package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestLocalArtworkCandidatesKeepMultiplicityFencesAndOwnerPick(t *testing.T) {
	ctx := context.Background()
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/Film (2000).mkv", "Film", 2000)
	s := New(c.DB)
	source, err := s.LibrarySource(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 VALUES('image-object',?,?,?,'Film (2000).mkv','r1','{}',1,1)`, source.ID, item.Token, source.Incarnation)
	primary := strings.Repeat("c", 64)
	c.Exec(`INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES('m','item',?,'poster',?,'now')`, item.ID, primary)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, primary)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('owner-image','item',?,'poster','local','primary',?,'Local file','fence','now')`, item.ID, "local:"+primary)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) SELECT column1,column2,column3,column4,column5,column7,column8,column9 FROM (VALUES('item',?,'poster','owner-image',?,?,1,'owner','now'))`, item.ID, primary, primary)
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	candidates := []assets.LocalArtworkCandidate{{Slot: "item/poster", Key: b}, {Slot: "item/poster", Key: a}, {Slot: "item/poster", Key: a}}
	write := func(binding LibrarySource, revision, kind string, values []assets.LocalArtworkCandidate) error {
		tx, err := c.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.commitInventoryArtworkCandidatesTx(ctx, tx, binding, "image-object", revision, item.Token, kind, values); err != nil {
			return err
		}
		return tx.Commit()
	}
	read := func(after string, limit int) []string {
		t.Helper()
		tx, err := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		keys, err := ReadLocalArtworkCandidatesTx(ctx, tx, "item", item.ID, "poster", after, limit)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	if err = write(source, "r1", "movie", candidates); err != nil {
		t.Fatal(err)
	}
	if got := read("", 1); !reflect.DeepEqual(got, []string{a}) {
		t.Fatal("first bounded candidate page", got)
	}
	if got := read(a, 1); !reflect.DeepEqual(got, []string{b}) {
		t.Fatal("second bounded candidate page", got)
	}
	if got := read(b, 1); len(got) != 0 {
		t.Fatal("candidate page repeated", got)
	}
	// A rejected publication must not replace any accepted candidate rows.
	badSource := source
	badSource.Generation++
	for name, call := range map[string]func() error{
		"revision":          func() error { return write(source, "old", "movie", nil) },
		"source generation": func() error { return write(badSource, "r1", "movie", nil) },
		"library kind":      func() error { return write(source, "r1", "tv", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, storage.ErrInventoryChanged) {
				t.Fatal("stale/cross-source accepted", err)
			}
		})
	}
	for name, values := range map[string][]assets.LocalArtworkCandidate{
		"non-image key": {{Slot: "item/poster", Key: "https://private.invalid/image"}},
		"foreign slot":  {{Slot: "artist/portrait", Key: a}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(source, "r1", "movie", values); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
	if got := read("", 128); !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatal("refused write changed candidates", got)
	}
	c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id='image-object'`)
	if got := read("", 128); len(got) != 0 {
		t.Fatal("stale source revision remained a choice", got)
	}
	if err = write(source, "r2", "movie", candidates[:1]); err != nil {
		t.Fatal(err)
	}
	c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=?`, item.ID)
	if got := read("", 128); len(got) != 0 {
		t.Fatal("candidate outlived its actual asset-to-title link", got)
	}
	c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,edition) SELECT ?,id,0,'' FROM catalog_assets WHERE token=?`, item.ID, item.Token)
	c.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, source.ID)
	if got := read("", 128); len(got) != 0 {
		t.Fatal("offline source remained a choice", got)
	}
	c.Exec(`UPDATE library_sources SET health='unknown' WHERE id=?`, source.ID)
	c.Exec(`UPDATE inventory_objects SET retired=1 WHERE id='image-object'`)
	if got := read("", 128); len(got) != 0 {
		t.Fatal("retired source remained a choice", got)
	}
	c.Exec(`UPDATE inventory_objects SET retired=0 WHERE id='image-object'`)
	if err = write(source, "r2", "movie", nil); err != nil {
		t.Fatal(err)
	}
	if got := read("", 128); len(got) != 0 {
		t.Fatal("complete empty image observation retained old choices", got)
	}
	var key string
	if err = c.DB.QueryRow(`SELECT art_key FROM local_artwork WHERE entity_id=?`, item.ID).Scan(&key); err != nil || key != primary {
		t.Fatal("alternate publication replaced primary carrier", key, err)
	}
	var locked bool
	var actor string
	if err = c.DB.QueryRow(`SELECT locked,actor FROM artwork_selections WHERE entity_id=?`, item.ID).Scan(&locked, &actor); err != nil || !locked || actor != "owner" {
		t.Fatal("alternate publication changed owner pick", locked, actor, err)
	}
}

func TestLocalArtworkCandidateReaderNeedsCallerTransactionAndBound(t *testing.T) {
	if _, err := ReadLocalArtworkCandidatesTx(context.Background(), nil, "item", 1, "poster", "", 1); err == nil {
		t.Fatal("nil transaction accepted")
	}
	c := catalogtest.Open(t)
	tx, err := c.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, limit := range []int{0, 129} {
		if _, err := ReadLocalArtworkCandidatesTx(context.Background(), tx, "item", 1, "poster", "", limit); err == nil {
			t.Fatal("unbounded request accepted", limit)
		}
	}
}

func TestUnmatchedEpisodeArtworkCandidatesDoNotPoisonMediaImport(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "TV", "tv", "/tv")
	_, token := identityFile(t, c, "tv", "/tv/Unknown.Title.2020.1080p.mkv", 1080)
	s := New(c.DB)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return s.commitEpisodes(tx, "tv", token, "Unknown.Title.2020.1080p.mkv", EpisodeNaming{Issue: "unrecognized_episode_name"}, false)
	})
	source, err := s.LibrarySource(context.Background(), "tv")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('unmatched-art',?,?,?,'Unknown.Title.2020.1080p.mkv','r1','{}',1,1)`, source.ID, token, source.Incarnation)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return s.commitInventoryArtworkCandidatesTx(ctx, tx, source, "unmatched-art", "r1", token, "tv", []assets.LocalArtworkCandidate{{Slot: "show/poster", Key: strings.Repeat("a", 64)}, {Slot: "season/1/poster", Key: strings.Repeat("b", 64)}})
	})
	if n := rows(t, c, `SELECT count(*) FROM inventory_artwork_candidates WHERE object_id='unmatched-art'`); n != 0 {
		t.Fatal("unmatched file invented a parent image slot", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE a.token=?`, token); n != 1 {
		t.Fatal("unmatched media was dropped by artwork publication", n)
	}
}
