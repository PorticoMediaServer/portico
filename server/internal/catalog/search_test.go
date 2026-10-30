package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"
)

func searchTestCatalog(t *testing.T, path string) (*catalogtest.Catalog, error) {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return catalogtest.New(t, db), nil
}

func searchBulkMovies(t *testing.T, c *catalogtest.Catalog, library int64, root string, n int, title func(int) string) []catalogtest.Item {
	t.Helper()
	items := make([]catalogtest.Item, n)
	ids := make([]int64, n)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("%s/%03d.mkv", strings.TrimRight(root, "/"), i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: title(i), Year: 2026})
			if err != nil {
				return err
			}
			ids[i] = id
		}
		return nil
	})
	for i, id := range ids {
		items[i] = catalogtest.Item{ID: id, Public: c.Public(id)}
	}
	return items
}

func TestSearchGroupsPagingAndIndexUpdates(t *testing.T) {
	tier := filepath.Join(t.TempDir(), "db")
	db, err := persistence.Open(tier)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	movies := c.Library("m", "Movies", "movie", "/m")
	hidden := c.Library("hidden", "Private", "movie", "/h")
	tv := c.Library("tv", "TV", "tv", "/tv")
	anime := c.Library("an", "Anime", "anime", "/an")
	music := c.Library("music", "Music", "music", "/mu")
	books := c.Library("b", "Books", "audiobook", "/b")
	show := c.Show(tv, "Harbor Story", 2020)
	c.Show(anime, "Harbor Anime", 2021)
	showSeason := c.Season(show, 1)
	artist := c.Artist(music, "Harbor Artist")
	album := c.Album(artist, "Harbor Album", 2026)
	c.Book(books, "Harbor Book", "Author")
	c.Movie(hidden, "/h/private.mkv", "Harbor private", 2020)
	episode := c.Episode(show, showSeason, 1, "/tv/episode.mkv")
	var tvRoot string
	if err = c.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, tv).Scan(&tvRoot); err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: showSeason.ID, Key: compactcatalog.ItemKey(tvRoot, "/tv/episode.mkv", 0), Title: "Harbor Episode"})
		return err
	})
	c.Song(album, 1, "/mu/song.flac", "Harbor Song")
	movieFixtures := searchBulkMovies(t, c, movies, "/m", 43, func(i int) string { return fmt.Sprintf("Harbor Movie %02d", i) })
	selected, thumb := strings.Repeat("a", 64), strings.Repeat("b", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?, 'image/png',1920,1280,100,'now','ready'),(?, 'image/png',400,267,50,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, selected, thumb)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('search-poster','show',?,'poster','','fixture','show','local','fixture','fence','now')`, show.ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('show',?,'poster','','search-poster',?,?,0,1,'fixture','now'))`, show.ID, selected, thumb)
	c.Drain()
	s := New(db)
	libraries := []string{"m", "tv", "an", "music", "b"}
	r := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "  Harbor\t ", Limit: 40, Libraries: libraries}
	out, err := s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// A request without Live TV permission publishes only nonlive groups.
	if len(out.Groups) != len(searchGroups)-1 || out.Query.Q != "Harbor" || out.Groups[0].TotalCount != 43 || len(out.Groups[0].Items) != SearchPreviewLimit {
		t.Fatalf("groups %+v", out)
	}
	for _, group := range out.Groups {
		if group.ID == "shows" || group.ID == "episodes" {
			wanted := show.Public
			if group.ID == "episodes" {
				wanted = episode.Public
			}
			found := false
			for _, item := range group.Items {
				if item.ID == wanted && strings.Contains(item.PosterURL, "?"+fixtureArtQuery(selected, 1920, 1280, 100)) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s did not inherit selected poster in search: %+v", group.ID, group.Items)
			}
		}
	}
	for _, group := range out.Groups {
		for _, entry := range group.Items {
			if entry.LibraryID == "hidden" || entry.Navigation == nil || entry.Navigation.EntityID != entry.ID {
				t.Fatal("projection leak/target", entry)
			}
		}
	}
	cursor := out.Groups[0].NextCursor
	r.Group, r.Cursor = "movies", cursor
	page, err := s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Groups) != 1 || len(page.Groups[0].Items) != 43-SearchPreviewLimit || page.Groups[0].NextCursor != "" {
		t.Fatal("continuation", page)
	}
	for _, change := range []func(*SearchRequest){func(r *SearchRequest) { r.ViewerFence = "other" }, func(r *SearchRequest) { r.Group = "songs" }, func(r *SearchRequest) { r.Q = "Movie" }, func(r *SearchRequest) { r.Limit = 1 }, func(r *SearchRequest) { r.Libraries = []string{"hidden"} }} {
		bad := r
		change(&bad)
		if _, err = s.Search(context.Background(), bad); !errors.Is(err, ErrCursor) {
			t.Fatal("cursor scope", err)
		}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: movies, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", "/m/000.mkv", 0), Title: "Renamed", Year: 2026})
		return err
	})
	c.Drain()
	if _, err = s.Search(context.Background(), r); err != nil {
		t.Fatal("continuation failed after publication", err)
	}
	r.Cursor, r.Q = "", "renam"
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 1 {
		t.Fatal("rename index", out, err)
	}
	c.Delete(movieFixtures[0].ID)
	c.Drain()
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 0 {
		t.Fatal("delete index", out, err)
	}
	db.Close()
	db, err = persistence.Open(tier)
	if err != nil {
		t.Fatal(err)
	}
	c = catalogtest.New(t, db)
	c.Drain()
	s = New(db)
	r.Q = "movie harbor"
	out, err = s.Search(context.Background(), r)
	if err != nil || len(out.Groups[0].Items) != 40 || !out.Groups[0].HasMore || out.Groups[0].TotalCountExact {
		t.Fatal("AND/restart", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Search(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	_ = artist
	_ = album
}

func TestSearchInputAndLiteralOperators(t *testing.T) {
	for _, q := range []string{"", " ", "***", "OR * NEAR() a b c d e f g", strings.Repeat("界", 129)} {
		if _, _, err := NormalizeSearch(q); err == nil {
			t.Fatal("accepted", q)
		}
	}
	q, expr, err := NormalizeSearch("  café\u2003Moon-STAR\"  ")
	if err != nil || q != "café Moon-STAR\"" || expr != `"cafe"* AND "moonstar"*` {
		t.Fatal(q, expr, err)
	}
	_, expr, err = NormalizeSearch(`harbor OR "movie"* -secret`)
	if err != nil || expr != `"harbor"* AND "or"* AND "movie"* AND "secret"*` {
		t.Fatal("operator injection", expr, err)
	}
}

func TestSearchPersistenceLibraryMoveAndScale(t *testing.T) {
	testtier.Media(t, "20,000 titles projected and indexed")
	path := filepath.Join(t.TempDir(), "db")
	c, err := searchTestCatalog(t, path)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Library("a", "A", "movie", "/a")
	b := c.Library("b", "B", "movie", "/b")
	move := c.Movie(a, "/a/move.mkv", "Café Harbor", 2020)
	db := c.DB
	db.Close()
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Search derivation resumes from compact writes after reopening the file.
	c = catalogtest.New(t, db)
	c.Drain()
	s := New(db)
	r := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, Profile: "p", ViewerFence: "f", Q: "cafe harb", Group: "movies", Limit: 1, Libraries: []string{"a"}}
	out, err := s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 1 {
		t.Fatal("backfill/diacritic", out, err)
	}
	c.Delete(move.ID)
	destination := c.Movie(b, "/b/move.mkv", "Café Harbor", 2020)
	c.Drain()
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 0 {
		t.Fatal("moved authorization", out, err)
	}
	_ = destination
	rowsN := 20000
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
		rowsN = 100000
	}
	var root string
	if err = db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, a).Scan(&root); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	scaleItem := int64(0)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 1; i <= rowsN; i++ {
			title := fmt.Sprintf("Other Fixture %06d", i)
			if i%(rowsN/100) == 0 {
				title = fmt.Sprintf("Harbor Beacon %06d", i)
			}
			itemPath := fmt.Sprintf("/a/scale%06d.mkv", i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: a, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, itemPath, 0), Title: title, Year: 2025})
			if err != nil {
				return err
			}
			if i == 1000 {
				scaleItem = id
			}
		}
		return nil
	})
	c.Drain()
	t.Log(rowsN, "compact entities and search index inserted", time.Since(start))
	r.Q, r.Limit = "beacon harbor", 40
	start = time.Now()
	out, err = s.Search(context.Background(), r)
	if err != nil || len(out.Groups) != 1 || out.Groups[0].TotalCountExact || out.Groups[0].TotalCount != 40 || len(out.Groups[0].Items) != 40 {
		t.Fatal("scale token-AND window", out, err)
	}
	t.Log("cold process-indexed token AND 40-page", time.Since(start))
	// Several words have a bounded window count, not a maintained
	// intersection total. Prove all 100 expected matches by exhausting the
	// real cursor stream; a first-page integer cannot establish completeness.
	walk, current := r, out
	seen := map[string]bool{}
	pages := 0
	for {
		pages++
		if pages > 3 || len(current.Groups) != 1 {
			t.Fatal("scale traversal did not exhaust in three pages", pages, current)
		}
		group := current.Groups[0]
		if group.TotalCountExact || group.TotalCount != len(group.Items) || group.HasMore != (group.NextCursor != "") {
			t.Fatal("incorrect bounded intersection count or continuation", group)
		}
		for _, item := range group.Items {
			if seen[item.ID] || !strings.HasPrefix(item.Title, "Harbor Beacon ") {
				t.Fatal("duplicate or nonmatching scale item", item)
			}
			seen[item.ID] = true
		}
		if !group.HasMore {
			break
		}
		walk.Cursor = group.NextCursor
		current, err = s.Search(context.Background(), walk)
		if err != nil {
			t.Fatal("scale traversal", err)
		}
	}
	if len(seen) != 100 || pages != 3 {
		t.Fatal("scale traversal lost expected matches", len(seen), pages)
	}
	// A single indexed word still has the exact maintained total, independently
	// of its 40-row page and of the multiword continuation above.
	single := r
	single.Q = "beacon"
	exact, err := s.Search(context.Background(), single)
	if err != nil || len(exact.Groups) != 1 || !exact.Groups[0].TotalCountExact || exact.Groups[0].TotalCount != 100 || len(exact.Groups[0].Items) != 40 {
		t.Fatal("exact maintained scale count", exact, err)
	}
	start = time.Now()
	_, err = s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("steady query", time.Since(start))
	explain, err := db.Query(`EXPLAIN QUERY PLAN SELECT rowid FROM catalog_search_titles WHERE catalog_search_titles MATCH 'kind : k1 AND search : ("beacon"* AND "harbor"*)'`)
	if err != nil {
		t.Fatal(err)
	}
	used := false
	for explain.Next() {
		var node, parent, unused int
		var detail string
		if err = explain.Scan(&node, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(detail, "VIRTUAL TABLE INDEX") {
			used = true
		}
	}
	explain.Close()
	if !used {
		t.Fatal("FTS index unused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	broad := r
	broad.Q = "Other"
	_, cancelErr := s.Search(ctx, broad)
	cancel()
	if !errors.Is(cancelErr, context.DeadlineExceeded) {
		t.Fatal("in-flight SQL cancellation", cancelErr)
	}
	r.Cursor = out.Groups[0].NextCursor
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES('p',?,1,'playback')`, scaleItem)
	c.Exec(`INSERT INTO progress_activity(profile_id,item_id,library_id,updated_at,state) VALUES('p',?,'a','2026-09-04','paused')`, scaleItem)
	if _, err = s.Search(context.Background(), r); err != nil {
		t.Fatal("viewer progress fence", err)
	}
	c.Exec(`INSERT INTO catalog_search_titles(catalog_search_titles) VALUES('integrity-check')`)
}

func TestSearchSharedSourceKeepsLogicalPermissionAndAvailability(t *testing.T) {
	c := catalogtest.Open(t)
	a := c.Library("a", "A", "movie", "/a")
	b := c.Library("b", "B", "movie", "/b")
	visible := c.Entity(compactcatalog.Entity{Library: a, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", "/a/visible.mkv", 0), Title: "Same Title", Year: 2020}, nil)
	private := c.Entity(compactcatalog.Entity{Library: b, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/b", "/b/private.mkv", 0), Title: "Same Title", Year: 2020}, nil)
	asset, _ := c.File(visible.ID, "/source/shared.mkv", 90)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.LinkAssetTx(ctx, tx, private.ID, asset, compactcatalog.Link{}); err != nil {
			return err
		}
		return compactcatalog.SetFactsTx(ctx, tx, visible.ID, map[string]any{"poster_url": "local:private-art-key"})
	})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, private.ID, map[string]any{"poster_url": "https://provider.invalid/private.jpg"})
	})
	c.Drain()
	s := New(c.DB)
	r := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, Profile: "p", ViewerFence: "f", Q: "same", Group: "movies", Libraries: []string{"a"}}
	out, err := s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 1 {
		t.Fatal(out, err)
	}
	entry := out.Groups[0].Items[0]
	if entry.ID != visible.Public || entry.PosterURL != "/v1/items/"+visible.Public+"/art/poster" || entry.Available == nil || !*entry.Available || entry.Playback == nil || entry.Playback.ItemID != visible.Public {
		t.Fatal(entry)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, asset, false)
	})
	c.Drain()
	out, err = s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	entry = out.Groups[0].Items[0]
	if entry.Available == nil || *entry.Available || entry.Playback != nil {
		t.Fatal("unavailable source offered playback", entry)
	}
	r.AllLibraries = true
	r.Viewer.Libraries, r.Libraries = []string{"a", "b"}, []string{"a", "b"}
	out, err = s.Search(context.Background(), r)
	if err != nil || out.Groups[0].TotalCount != 2 {
		t.Fatal("same-title logical identities flattened", out, err)
	}
}

func TestSearchSameTitleEpisodeHierarchyContext(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	hidden := c.Library("hidden", "Private", "anime", "/private")
	show := c.Show(tv, "Test Harbor", 2020)
	privateShow := c.Show(hidden, "Private Show", 2020)
	specials := c.Season(show, 0)
	first := c.Season(show, 1)
	s0 := c.Episode(show, specials, 1, "/tv/s0.mkv")
	s1 := c.Episode(show, first, 1, "/tv/s1.mkv")
	var showLocalKey string
	if err := c.DB.QueryRow(`SELECT local_key FROM catalog_shows WHERE entity_id=?`, show.ID).Scan(&showLocalKey); err != nil {
		t.Fatal(err)
	}
	absolute := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: show.ID, Key: compactcatalog.EpisodeKey(showLocalKey, "absolute", 0, 1), Title: "Episode 1"}, map[string]any{"show_id": show.ID, "numbering": "absolute", "number": 1})
	c.File(absolute.ID, "/tv/absolute.mkv", 60)
	c.Episode(privateShow, c.Season(privateShow, 1), 1, "/private/hidden.mkv")
	c.Drain()
	s := New(c.DB)
	request := SearchRequest{Viewer: Viewer{Profile: "profile", Fence: "viewer", Libraries: []string{"tv"}}, ServerID: "server", Profile: "profile", ViewerFence: "viewer", Q: "Episode", Limit: 40, Group: "episodes", Libraries: []string{"tv"}}
	out, err := s.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 1 || len(out.Groups[0].Items) != 3 {
		t.Fatal(out)
	}
	expected := map[string]string{s0.Public: "Test Harbor · Specials · Episode 1", s1.Public: "Test Harbor · Season 1 · Episode 1", absolute.Public: "Test Harbor · Absolute episode 1 (unassigned season)"}
	for _, entry := range out.Groups[0].Items {
		if entry.Subtitle != expected[entry.ID] || entry.Navigation == nil || entry.Navigation.View != "item" || entry.Navigation.EntityID != entry.ID || entry.LibraryID != "tv" {
			t.Fatal("context/link", entry)
		}
	}
	paged := request
	paged.Limit = 1
	page, err := s.Search(context.Background(), paged)
	if err != nil {
		t.Fatal(err)
	}
	paged.Cursor = page.Groups[0].NextCursor
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: tv, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(showLocalKey), Title: "Renamed Harbor", Year: 2020})
		return err
	})
	c.Drain()
	if _, err = s.Search(context.Background(), paged); err != nil {
		t.Fatal("parent rename prevented continuation", err)
	}
	out, err = s.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range out.Groups[0].Items {
		if !strings.HasPrefix(entry.Subtitle, "Renamed Harbor · ") {
			t.Fatal("stale parent title", entry)
		}
	}
}

func TestSearchDuplicateAlbumsCarryActualAlbumArtist(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	firstArtist := c.Artist(music, "First Artist")
	secondArtist := c.Artist(music, "Second Artist")
	firstAlbum := c.Album(firstArtist, "Shared Title", 0)
	secondAlbum := c.Album(secondArtist, "Shared Title", 0)
	song := c.Song(secondAlbum, 1, "/music/song.flac", "Shared Song")
	_ = firstAlbum
	_ = song
	c.Drain()
	s := New(c.DB)
	out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"music"}}, ServerID: "s", Profile: "p", ViewerFence: "f", Q: "Shared", Limit: 40, Libraries: []string{"music"}})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{firstAlbum.Public: "First Artist", secondAlbum.Public: "Second Artist", song.Public: "Shared Title · Second Artist"}
	seen := 0
	for _, section := range out.Groups {
		for _, entry := range section.Items {
			if entry.Subtitle != expected[entry.ID] || entry.Navigation.EntityID != entry.ID {
				t.Fatal(entry)
			}
			seen++
		}
	}
	if seen != 3 {
		t.Fatal(out)
	}
}
