package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
)

// A cursor now carries the sort key of the row the last page ended on, so a
// continuation seeks to it instead of skipping to it. That is only a safe change
// if the sequence a client walks is the same sequence it walked before — the
// same rows, in the same order, with the same absolute `start` on every page.
//
// This walks every browsable pivot of every library kind twice: once by
// following `nextCursor`, and once by asking for the same absolute ranges. The
// two sequences must be identical, and the paged walk must cover the row's whole
// total exactly once.
func TestBrowseCursorPagingMatchesAbsoluteRanges(t *testing.T) {
	f, call := buildGoldenFixture(t)
	type page struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
		PageInfo struct {
			Start      int    `json:"start"`
			Total      int    `json:"total"`
			HasMore    bool   `json:"hasMore"`
			NextCursor string `json:"nextCursor"`
		} `json:"pageInfo"`
	}
	read := func(t *testing.T, library, body string) page {
		t.Helper()
		status, raw := call("POST", "/v1/libraries/"+library+"/browse", body, "open")
		if status != 200 {
			t.Fatalf("browse %s: %d %s", body, status, raw)
		}
		var out page
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	for _, pivot := range []struct{ library, pivot, sort, direction string }{
		{f.library.Movies, "movies", "title", "asc"},
		{f.library.Movies, "movies", "added", "desc"},
		{f.library.Movies, "movies", "year", "desc"},
		{f.library.Movies, "collections", "title", "asc"},
		{f.library.Shows, "episodes", "title", "asc"},
		{f.library.Music, "songs", "added", "desc"},
		{f.library.Music, "albums", "year", "desc"},
		{f.library.Books, "books", "title", "asc"},
		// An expensive sort is a correlated aggregate rather than a column, so it
		// keeps paging by offset; the sequence must still match.
		{f.library.Movies, "movies", "duration", "desc"},
	} {
		name := pivot.library + "/" + pivot.pivot + "/" + pivot.sort + ":" + pivot.direction
		t.Run(name, func(t *testing.T) {
			const limit = 7
			body := func(extra string) string {
				return fmt.Sprintf(`{"pivot":%q,"sort":[{"field":%q,"direction":%q}],"limit":%d%s}`,
					pivot.pivot, pivot.sort, pivot.direction, limit, extra)
			}
			first := read(t, pivot.library, body(""))
			if first.PageInfo.Total == 0 {
				t.Skip("this pivot is empty in the fixture")
			}
			byCursor := []string{}
			starts := []int{}
			current := first
			for guard := 0; ; guard++ {
				if guard > 200 {
					t.Fatal("the cursor walk did not terminate")
				}
				starts = append(starts, current.PageInfo.Start)
				for _, entry := range current.Entries {
					byCursor = append(byCursor, entry.ID)
				}
				if current.PageInfo.NextCursor == "" {
					break
				}
				cursor, _ := json.Marshal(current.PageInfo.NextCursor)
				current = read(t, pivot.library, body(`,"cursor":`+string(cursor)))
			}
			if len(byCursor) != first.PageInfo.Total {
				t.Fatalf("the cursor walk covered %d rows against a total of %d", len(byCursor), first.PageInfo.Total)
			}
			byRange := []string{}
			for index, start := range starts {
				ranged := read(t, pivot.library, body(fmt.Sprintf(`,"range":{"start":%d}`, start)))
				if ranged.PageInfo.Start != start {
					t.Fatalf("page %d reported start %d against %d", index, ranged.PageInfo.Start, start)
				}
				for _, entry := range ranged.Entries {
					byRange = append(byRange, entry.ID)
				}
			}
			if len(byCursor) != len(byRange) {
				t.Fatalf("the cursor walk returned %d rows and the absolute ranges returned %d", len(byCursor), len(byRange))
			}
			for index := range byCursor {
				if byCursor[index] != byRange[index] {
					t.Fatalf("row %d is %q by cursor and %q by absolute range", index, byCursor[index], byRange[index])
				}
			}
			seen := map[string]bool{}
			for _, id := range byCursor {
				if seen[id] {
					t.Fatalf("%q appeared twice in the paged walk", id)
				}
				seen[id] = true
			}
		})
	}
}
