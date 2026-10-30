package catalog

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// Every sort, in both directions, for an unrestricted viewer and a restricted
// one (served from its visibility class): a range at any position and a
// cursor walk give exactly the order, which is written out here in Go from the
// rows' own values. Profile sorts put the valued rows first when descending
// (and last when ascending) and the rest in title order.
func TestBrowseEveryOrderPagesMatchTheOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	random := rand.New(rand.NewSource(5))
	type film struct {
		item                     catalogtest.Item
		title                    string
		added                    string
		year                     int
		duration, rating         float64
		mature                   bool
		personalRating           float64
		lastPlayed               string
		hasRating, hasLastPlayed bool
	}
	films2 := []*film{}
	for i := 0; i < 240; i++ {
		f := &film{
			// Few distinct values put ties everywhere, and mixed case checks the
			// title order's case folding.
			title:    fmt.Sprintf("%c%c film", "AbCd"[random.Intn(4)], 'a'+random.Intn(3)),
			added:    fmt.Sprintf("2026-0%d-1%dT00:00:00.000Z", 1+random.Intn(3), random.Intn(3)),
			year:     1990 + random.Intn(4),
			duration: float64(60 * (1 + random.Intn(4))),
			rating:   float64(random.Intn(4)),
			mature:   random.Intn(5) == 0,
		}
		path := fmt.Sprintf("/m/%03d.mkv", i)
		f.item = c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", path, 0), Title: f.title, Year: f.year, Added: f.added}, nil)
		f.item.Asset, f.item.Token = c.File(f.item.ID, path, f.duration)
		if f.mature {
			c.Attributes(f.item.ID, "contentRating", "R")
		}
		films2 = append(films2, f)
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()
	for _, f := range films2 {
		// A rating of 0 reads as none, as the order reads it.
		if f.rating > 0 {
			c.Exec(`UPDATE catalog_browse_rows SET rating_max=? WHERE entity_id=?`, f.rating, f.item.ID)
		} else {
			c.Exec(`UPDATE catalog_browse_rows SET rating_max=NULL WHERE entity_id=?`, f.item.ID)
		}
		if random.Intn(6) == 0 {
			f.personalRating, f.hasRating = float64(1+random.Intn(4)), true
		}
		if random.Intn(5) == 0 {
			f.lastPlayed, f.hasLastPlayed = fmt.Sprintf("2026-09-%02dT00:00:00Z", 1+random.Intn(3)), true
		}
		if f.hasRating || f.hasLastPlayed {
			var rating any
			if f.hasRating {
				rating = f.personalRating
			}
			c.Exec(`INSERT INTO personal_items(profile_id,item_id,rating,revision,last_played_at) VALUES('p',?,?,1,?)`, f.item.ID, rating, f.lastPlayed)
		}
	}
	c.Drain() // Personal sort keys are published by the maintained read model.
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	s := New(c.DB)
	restricted := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "m", restricted); err != nil {
		t.Fatal(err)
	}
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	lower := func(f *film) string { return asciiLower(f.title) }
	compare := map[string]func(a, b *film) int{
		"title": func(a, b *film) int { return cmpString(lower(a), lower(b)) },
		"added": func(a, b *film) int { return cmpString(a.added, b.added) },
		"year":  func(a, b *film) int { return a.year - b.year },
		"duration": func(a, b *film) int {
			return cmpFloat(a.duration, b.duration)
		},
		"communityRating": func(a, b *film) int { return cmpFloat(a.rating, b.rating) },
	}
	order := func(field, direction string, visible []*film) []string {
		out := append([]*film{}, visible...)
		desc := direction == "desc"
		byEntity := func(a, b *film) bool {
			if desc {
				return a.item.ID > b.item.ID
			}
			return a.item.ID < b.item.ID
		}
		if cmp, ok := compare[field]; ok {
			sort.SliceStable(out, func(i, j int) bool {
				if d := cmp(out[i], out[j]); d != 0 {
					return d < 0 != desc
				}
				return byEntity(out[i], out[j])
			})
		} else {
			valued := func(f *film) bool {
				if field == "personalRating" {
					return f.hasRating
				}
				return f.hasLastPlayed
			}
			value := func(a, b *film) int {
				if field == "personalRating" {
					return cmpFloat(a.personalRating, b.personalRating)
				}
				return cmpString(a.lastPlayed, b.lastPlayed)
			}
			withValue, rest := []*film{}, []*film{}
			for _, f := range out {
				if valued(f) {
					withValue = append(withValue, f)
				} else {
					rest = append(rest, f)
				}
			}
			sort.SliceStable(withValue, func(i, j int) bool {
				if d := value(withValue[i], withValue[j]); d != 0 {
					return d < 0 != desc
				}
				return byEntity(withValue[i], withValue[j])
			})
			sort.SliceStable(rest, func(i, j int) bool {
				if d := cmpString(lower(rest[i]), lower(rest[j])); d != 0 {
					return d < 0
				}
				return rest[i].item.ID < rest[j].item.ID
			})
			if desc {
				out = append(withValue, rest...)
			} else {
				out = append(rest, withValue...)
			}
		}
		ids := make([]string, 0, len(out))
		for _, f := range out {
			ids = append(ids, f.item.Public)
		}
		return ids
	}
	for _, viewer := range []Viewer{
		{Profile: "p", Fence: "open", Libraries: []string{"m"}},
		{Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: restricted},
	} {
		visible := []*film{}
		for _, f := range films2 {
			if !viewer.Restrictions.Active() || !f.mature {
				visible = append(visible, f)
			}
		}
		for _, field := range []string{"title", "added", "year", "duration", "communityRating", "personalRating", "lastPlayed"} {
			for _, direction := range []string{"asc", "desc"} {
				want := order(field, direction, visible)
				label := fmt.Sprintf("%s %s %s", viewer.Fence, field, direction)
				request := BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: field, Direction: direction}}, Limit: 17}
				for _, start := range []int{0, 1, 16, 40, 101, len(want) - 5} {
					request.Range = &BrowseRange{Start: start}
					page, err := s.BrowseEntities(viewer, request)
					if err != nil {
						t.Fatalf("%s at %d: %v", label, start, err)
					}
					got := entryIDs(page.Entries)
					if expect := want[start:min(len(want), start+17)]; fmt.Sprint(got) != fmt.Sprint(expect) {
						t.Fatalf("%s at %d:\n got %v\nwant %v", label, start, got, expect)
					}
				}
				request.Range = nil
				walked := []string{}
				for page := 0; ; page++ {
					result, err := s.BrowseEntities(viewer, request)
					if err != nil || page > 40 {
						t.Fatalf("%s cursor page %d: %v", label, page, err)
					}
					walked = append(walked, entryIDs(result.Entries)...)
					if result.PageInfo.NextCursor == "" {
						break
					}
					request.Cursor = result.PageInfo.NextCursor
				}
				if fmt.Sprint(walked) != fmt.Sprint(want) {
					t.Fatalf("%s cursor walk:\n got %v\nwant %v", label, walked, want)
				}
			}
		}
	}
}

func entryIDs(entries []ContentEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// The media tier's share: every ordering's blocks stay exact through splits,
// and positions across block boundaries match the order in both directions.
func TestBrowseEveryOrderAcrossBlockSplits(t *testing.T) {
	testtier.Media(t, "4,500 movies across several blocks per ordering")
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	random := rand.New(rand.NewSource(13))
	for i := 0; i < 4500; i++ {
		path := fmt.Sprintf("/m/%05d.mkv", i)
		it := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", path, 0),
			Title: fmt.Sprintf("%c film %03d", 'A'+random.Intn(26), random.Intn(300)), Year: 1950 + random.Intn(70),
			Added: fmt.Sprintf("2026-0%d-%02dT00:00:00.000Z", 1+random.Intn(9), 1+random.Intn(28))}, nil)
		c.File(it.ID, path, float64(60*(1+random.Intn(200))))
	}
	c.Drain()
	if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, sorting := range []struct{ field, expression string }{{"title", "e.sort_key COLLATE NOCASE"}, {"added", "COALESCE(e.added_text,'')"}, {"year", "e.year"}, {"duration", "COALESCE(e.duration_max,0)"}} {
		for _, direction := range []string{"ASC", "DESC"} {
			for _, start := range []int{1023, 2048, 3001, 4490} {
				page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: sorting.field, Direction: map[string]string{"ASC": "asc", "DESC": "desc"}[direction]}}, Limit: 40, Range: &BrowseRange{Start: start}})
				if err != nil {
					t.Fatal(err)
				}
				rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id
				 WHERE e.kind=1 ORDER BY `+sorting.expression+` `+direction+`,e.entity_id `+direction+` LIMIT 40 OFFSET ?`, start)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{}
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					want = append(want, id)
				}
				rows.Close()
				if got := entryIDs(page.Entries); fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("%s %s at %d: got %d starting %v, want %d starting %v", sorting.field, direction, start, len(got), first(got), len(want), first(want))
				}
			}
		}
	}
}
