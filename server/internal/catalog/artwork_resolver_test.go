package catalog

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestResolvedThumbnailURLUsesSelectedArtworkVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "art.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("m", "Movies", "movie", "/m")
	one := c.Movie(films, "/m/One.mkv", "One", 2020)
	full, thumb := strings.Repeat("a", 64), strings.Repeat("b", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',1920,1280,100,'now','ready'),(?, 'image/png',400,267,50,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, full, thumb)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('candidate','item',?,'poster','','fixture','one','local','fixture','fence','now')`, one.ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('item',?,'poster','','candidate',?,?,0,1,'fixture','now'))`, one.ID, full, thumb)
	c.Drain()
	resolved, err := New(db).resolveArtwork([]artworkTarget{{Kind: "item", ID: one.Public}})
	if err != nil {
		t.Fatal(err)
	}
	path := resolved[artworkTarget{Kind: "item", ID: one.Public}].PosterURL
	if !strings.HasPrefix(path, "/v1/metadata/item/"+one.Public+"/art/poster?") {
		t.Fatalf("artwork URL must name the item by its public id: %q", path)
	}
	if path != fixtureArtURL("item", one.Public, "poster", "", full, 1920, 1280, 100) || strings.Contains(path, thumb) {
		t.Fatalf("URL did not carry the selected source and its slots: %q", path)
	}
}

// A populated cache can make SQLite choose ready objects before selections.
// The page must instead seek its selections and then each selection's digest,
// independent of how many unrelated images have accumulated on the server.
func TestArtworkResolutionSeeksSelectedDigestsWithPopulatedCacheStatistics(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(lib, "/movies/One.mkv", "One", 2020)
	ready, deleting := strings.Repeat("a", 64), strings.Repeat("b", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',400,600,100,'now','ready'),(?, 'image/png',400,600,100,'now','deleting'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, ready, deleting)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('poster','item',?,'poster','','fixture','poster','local','fixture','fence','now'),('backdrop','item',?,'backdrop','','fixture','backdrop','local','fixture','fence','now')`, movie.ID, movie.ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('item',?,'poster','','poster',?,?,0,1,'fixture','now'),('item',?,'backdrop','','backdrop',?,?,0,1,'fixture','now'))`, movie.ID, ready, ready, movie.ID, deleting, deleting)
	c.Drain()
	// Representative statistics from a cache of thousands of selected images;
	// no catalogue-wide fixture or timing threshold is required to pin the plan.
	c.Exec(`ANALYZE sqlite_schema`)
	c.Exec(`DELETE FROM sqlite_stat1 WHERE tbl IN('artwork_blobs','artwork_sources','artwork_selections')`)
	c.Exec(`INSERT INTO sqlite_stat1(tbl,idx,stat) VALUES
 ('artwork_blobs','artwork_blobs_created','31896 3 1'),
 ('artwork_blobs','sqlite_autoindex_artwork_blobs_1','31896 1'),
 ('artwork_sources','artwork_sources_created','10632 3 1'),
 ('artwork_sources','artwork_sources_small','10632 1'),
 ('artwork_sources','sqlite_autoindex_artwork_sources_1','10632 1'),
 ('artwork_selections','artwork_selections_candidate','4284 1'),
 ('artwork_selections','artwork_selections_digest','4284 1'),
 ('artwork_selections','sqlite_autoindex_artwork_selections_1','4284 1428 17 8 1')`)
	c.Exec(`ANALYZE sqlite_schema`)
	targets := []artworkTarget{{Kind: "item", ID: movie.Public}}
	raw, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	seekOnlyPlan(t, c.DB, "artwork selected digest", artworkResolutionQuery, []any{string(raw)}, "requested", "resolved", "r", "c")
	resolved, err := New(c.DB).resolveArtwork(targets)
	if err != nil {
		t.Fatal(err)
	}
	art := resolved[targets[0]]
	if art.PosterURL != fixtureArtURL("item", movie.Public, "poster", "", ready, 400, 600, 100) || art.BackdropURL != fixtureArtURL("item", movie.Public, "backdrop", "", deleting, 400, 600, 100) {
		t.Fatalf("selected sources were not described: %+v", art)
	}
}
