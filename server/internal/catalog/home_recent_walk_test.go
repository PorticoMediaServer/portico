package catalog

import (
	"fmt"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The newest-first walk returns exactly the full query's rows, page by page,
// over editions folded by provider id (including an undated edition), undated
// works, and ties on the added time; and it declines while the recent order is
// still being derived.
func TestHomeRecentWalkMatchesTheFullQuery(t *testing.T) {
	f := recommendationFixtureDB(t)
	c := f.c
	library := f.libs["movies"]
	movies := []catalogtest.Item{}
	for i := 0; i < 40; i++ {
		path := fmt.Sprintf("/movies/w%02d.mkv", i)
		added := fmt.Sprintf("2026-02-%02dT10:00:00.000Z", 1+i%9)
		if i%13 == 7 {
			added = ""
		}
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/movies", path, 0), Title: fmt.Sprintf("Work %d", i), Year: 1980 + i, Added: added}, nil)
		item.Asset, item.Token = c.File(item.ID, path, 5400)
		movies = append(movies, item)
	}
	for n, edition := range []struct {
		a, b     int
		provider string
	}{{0, 9, "tmdb"}, {3, 31, "tmdb"}, {5, 7, "tmdb"}, {12, 20, "tvdb"}, {21, 22, "anilist"}, {24, 33, "tmdb"}} {
		for _, i := range []int{edition.a, edition.b} {
			c.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,?,?,'','')`, movies[i].ID, edition.provider, fmt.Sprint(9000+n))
		}
	}
	c.Drain()
	s := New(f.db)
	defer func(batch int) { homeWalkBatch = batch }(homeWalkBatch)
	for _, batch := range []int{1, 2, 3, 64} {
		homeWalkBatch = batch
		for _, libraries := range [][]string{{"movies", "tv", "music", "books"}, {"movies"}} {
			t.Run(fmt.Sprintf("batch %d %v", batch, libraries), func(t *testing.T) { checkHomeRecentWalk(t, s, c, movies, libraries) })
		}
	}
}

func checkHomeRecentWalk(t *testing.T, s *Service, c *catalogtest.Catalog, movies []catalogtest.Item, libraries []string) {
	{
		r := engineRequest(libraries...).scoped()
		src, err := s.homeRecentPagedSource(r, false)
		if err != nil || src.walk == nil {
			t.Fatal("no walk on the recent row:", err)
		}
		type row struct{ id, ord string }
		full := []row{}
		rows, err := s.read().Query(`SELECT id,ord FROM (`+src.base+`) ORDER BY ord DESC,id DESC`, src.args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var x row
			if err = rows.Scan(&x.id, &x.ord); err != nil {
				t.Fatal(err)
			}
			full = append(full, x)
		}
		rows.Close()
		if len(full) < 30 {
			t.Fatalf("fixture too small: %d works", len(full))
		}
		check := func(name string, want []row, ids, ords []string) {
			t.Helper()
			got := []row{}
			for i := range ids {
				got = append(got, row{ids[i], ords[i]})
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%v %s:\n got  %v\n want %v", libraries, name, got, want)
			}
		}
		walked := 0
		for _, limit := range []int{1, 2, 3, 5, 7, 12} {
			for _, skip := range []int{0, 1, 4, 10} {
				ids, ords, ok, err := src.walk(homeWalkRequest{skip: skip, take: limit + 1})
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					walked++
					check(fmt.Sprintf("skip %d take %d", skip, limit+1), full[skip:skip+limit+1], ids, ords)
				}
			}
			// The continuation chain, from each page's last row.
			for at := limit - 1; at+1 < len(full); at += limit {
				after := [2]string{full[at].ord, full[at].id}
				ids, ords, ok, err := src.walk(homeWalkRequest{take: limit + 1, after: &after})
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					continue
				}
				walked++
				end := min(len(full), at+1+limit+1)
				check(fmt.Sprintf("after %d take %d", at, limit+1), full[at+1:end], ids, ords)
			}
		}
		if walked < 40 {
			t.Fatalf("%v: the walk answered only %d pages", libraries, walked)
		}

		// The whole row through the public pager is the full query, page by page.
		paged := []row{}
		cursor := ""
		for {
			got, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 4, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range got.DisplayTargets {
				paged = append(paged, row{id: target.MembershipID})
			}
			if cursor = got.NextCursor; cursor == "" {
				break
			}
		}
		want := []row{}
		for _, x := range full {
			want = append(want, row{id: x.id})
		}
		if !slices.Equal(paged, want) {
			t.Fatalf("%v paged row:\n got  %v\n want %v", libraries, paged, want)
		}

		// While a container refresh is pending, recent_text may trail its
		// members: the walk declines.
		c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,?,1)`, compactcatalog.DomainContainerRefresh, movies[1].ID)
		if _, _, ok, err := src.walk(homeWalkRequest{take: 3}); err != nil || ok {
			t.Fatalf("the walk ran over a pending refresh: ok=%v %v", ok, err)
		}
		c.Exec(`DELETE FROM catalog_dirty WHERE domain=? AND entity_id=?`, compactcatalog.DomainContainerRefresh, movies[1].ID)
	}
}
