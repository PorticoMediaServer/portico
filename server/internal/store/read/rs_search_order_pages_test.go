package read

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestRSSearchOrderedKeysetPages(t *testing.T) {
	for _, tc := range []struct{ order, direction string }{{"title", "asc"}, {"releaseYear", "desc"}, {"dateAdded", "asc"}, {"relevance", "asc"}} {
		t.Run(tc.order+"/"+tc.direction, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, _ := newRSSearchFixture(t)
				query, _ := search.PlanQuery("hello", true)
				view := f.input
				view.Kind = 0
				f.exec(t, `CREATE TEMP TABLE rs_search_page_oracle(id INTEGER PRIMARY KEY,sort_key TEXT COLLATE NOCASE,year INTEGER,added TEXT)`)
				var hits []FullSearchHit
				for id := int64(200); id >= 101; id-- {
					h := rsSearchTestHit(1, id, view.Library)
					h.SortKey = fmt.Sprintf("key-%d", id%7)
					h.Year = 1900 + (id*13)%5
					var added any
					if id%4 != 0 {
						h.AddedAt = fmt.Sprintf("2026-11-%02dT00:00:00.000000000Z", id%3+1)
						added = h.AddedAt
					}
					hits = append(hits, h)
					f.exec(t, `INSERT INTO rs_search_page_oracle VALUES(?,?,?,?)`, id, h.SortKey, h.Year, added)
				}
				expression := map[string]string{"title": "sort_key", "releaseYear": "year", "dateAdded": "COALESCE(added,'')", "relevance": "id"}[tc.order]
				rows, e := f.conn.QueryContext(t.Context(), `SELECT id FROM rs_search_page_oracle ORDER BY `+expression+` `+tc.direction+`,id `+tc.direction)
				if e != nil {
					t.Fatal(e)
				}
				var want []int64
				for rows.Next() {
					var id int64
					if e = rows.Scan(&id); e != nil {
						t.Fatal(e)
					}
					want = append(want, id)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				rows.Close()
				calls := 0
				r, _ := NewRSSearchReader(f.authority, f.snapshots)
				r.read = func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
					calls++
					if in.Preview {
						t.Fatal("exact full group downgraded to preview")
					}
					start := 0
					if in.After != nil {
						for start < len(hits) && compareFullRank(hitRank(hits[start]), in.After.after) <= 0 {
							start++
						}
					}
					end := min(start+20, len(hits))
					page := FullSearchPage{Disposition: SearchResults, Exhausted: end == len(hits), Hits: append([]FullSearchHit(nil), hits[start:end]...)}
					sort.Slice(page.Hits, func(i, j int) bool { return compareFullRank(hitRank(page.Hits[i]), hitRank(page.Hits[j])) < 0 })
					if !page.Exhausted {
						page.Next = &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
					}
					rsSearchTestStamp(f, out, req, in, &page, q)
					return page, nil, nil
				}
				input := RSSearchInput{Views: []BrowseInput{view}, Query: query, Group: 1, Full: true, Order: tc.order, Direction: tc.direction, Capacity: 64}
				var got []int64
				for number := 0; number < 3; number++ {
					p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, input)
					if e != nil || ref != nil {
						t.Fatal(e, ref)
					}
					var page RSSearchPage
					for poll := 0; poll < 8; poll++ {
						fg, req, out := liveRequests("rs-sorted-keyset")
						before := calls
						_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
							var err error
							page, _, err = r.Poll(ctx, req, time.Second, out, p)
							return err
						})
						if e != nil || calls-before != 1 {
							t.Fatal("one full call per LIVE poll", e, calls-before)
						}
						if page.Complete {
							break
						}
						if len(page.Groups) != 0 {
							t.Fatal("partial sorted window escaped", page)
						}
					}
					_ = p.Close()
					if !page.Complete || page.Partial || len(page.Groups) != 1 || len(page.Groups[0].Hits) > 40 {
						t.Fatal("sorted group incomplete", page)
					}
					for _, h := range page.Groups[0].Hits {
						got = append(got, h.ID)
					}
					input.After = page.Groups[0].Next
					if number < 2 && (input.After == nil || input.After.order == nil || input.After.anchor == nil) {
						t.Fatal("missing source-bound typed continuation")
					}
				}
				if input.After != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("sorted keyset lost/duplicated boundary", got, want)
				}
			})
		})
	}
}
