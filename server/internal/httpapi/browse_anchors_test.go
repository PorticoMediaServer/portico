package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

// M10: every browse sort publishes position anchors, and jumping to an
// anchor's index with range.start lands on the first row of that bucket.
func TestBrowseAnchorsForEverySort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, call := buildGoldenFixture(t)
	type page struct {
		Entries []struct {
			ID string `json:"id"`
			// A movie row's subtitle is its year.
			Subtitle string `json:"subtitle"`
		} `json:"entries"`
		PageInfo      struct{ Total int } `json:"pageInfo"`
		PositionIndex []struct {
			Key   string `json:"key"`
			Index int    `json:"index"`
		} `json:"positionIndex"`
	}
	browse := func(body string) page {
		status, raw := call("POST", "/v1/libraries/"+f.library.Movies+"/browse", body, "open")
		if status != 200 {
			t.Fatalf("%s: %d %s", body, status, raw)
		}
		var p page
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, sort := range []struct{ field, direction string }{{"year", "desc"}, {"year", "asc"}, {"added", "desc"}, {"title", "desc"}, {"communityRating", "desc"}, {"duration", "asc"}} {
		first := browse(fmt.Sprintf(`{"pivot":"movies","sort":[{"field":%q,"direction":%q}],"limit":4}`, sort.field, sort.direction))
		if len(first.PositionIndex) == 0 {
			t.Fatalf("%s %s: no anchors", sort.field, sort.direction)
		}
		last := -1
		for _, anchor := range first.PositionIndex {
			if anchor.Index <= last || anchor.Index >= first.PageInfo.Total {
				t.Fatalf("%s %s: anchors out of order or range: %+v", sort.field, sort.direction, first.PositionIndex)
			}
			last = anchor.Index
		}
		if sort.field != "year" {
			continue
		}
		// Year buckets are checkable against the rows themselves.
		for _, anchor := range first.PositionIndex {
			at := browse(fmt.Sprintf(`{"pivot":"movies","sort":[{"field":"year","direction":%q}],"limit":1,"range":{"start":%d}}`, sort.direction, anchor.Index))
			if len(at.Entries) != 1 {
				t.Fatalf("no row at anchor %+v", anchor)
			}
			if at.Entries[0].Subtitle != anchor.Key {
				t.Fatalf("anchor %+v lands on a %q row", anchor, at.Entries[0].Subtitle)
			}
			if anchor.Index > 0 {
				before := browse(fmt.Sprintf(`{"pivot":"movies","sort":[{"field":"year","direction":%q}],"limit":1,"range":{"start":%d}}`, sort.direction, anchor.Index-1))
				if len(before.Entries) == 1 && before.Entries[0].Subtitle == anchor.Key {
					t.Fatalf("anchor %+v is not the first row of its bucket", anchor)
				}
			}
		}
	}
}

// M10: a /content grid section opens at any index with `start` and then
// continues by cursor; start is refused on views that are not grids.
func TestContentGridOpensAtAnIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, call := buildGoldenFixture(t)
	type envelope struct {
		Sections []struct {
			ID      string `json:"id"`
			Start   int    `json:"start"`
			Entries []struct {
				ID string `json:"id"`
			} `json:"entries"`
			NextCursor string `json:"nextCursor"`
		} `json:"sections"`
	}
	read := func(library, query, viewer string) envelope {
		status, raw := call("GET", "/v1/libraries/"+library+"/content?"+query, "", viewer)
		if status != 200 {
			t.Fatalf("%s: %d %s", query, status, raw)
		}
		w := httptest.NewRecorder()
		w.Code = status
		w.Body.WriteString(raw)
		assertSpecResponse(t, "GET", "/v1/libraries/{id}/content", w)
		var out envelope
		if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Sections) == 0 {
			t.Fatalf("%s: %v %s", query, err, raw)
		}
		return out
	}
	ids := func(e envelope) []string {
		out := []string{}
		for _, v := range e.Sections[0].Entries {
			out = append(out, v.ID)
		}
		return out
	}
	for _, v := range []struct{ library, view, viewer string }{
		{f.library.Movies, "browse", "open"},
		{f.library.Movies, "browse", "restricted"},
		{f.library.Music, "browse", "open"},
		{f.library.Books, "browse", "open"},
		{f.library.Movies, "collections", "open"},
	} {
		whole := ids(read(v.library, "view="+v.view+"&limit=12", v.viewer))
		if len(whole) < 2 {
			t.Fatalf("%s: fixture too small %v", v.library, whole)
		}
		start := len(whole) / 2
		opened := read(v.library, fmt.Sprintf("view=%s&limit=%d&start=%d", v.view, 2, start), v.viewer)
		got := ids(opened)
		if opened.Sections[0].Start != start || len(got) == 0 || got[0] != whole[start] {
			t.Fatalf("%s %s: start=%d gave %v (start %d), whole %v", v.library, v.viewer, start, got, opened.Sections[0].Start, whole)
		}
		if next := opened.Sections[0].NextCursor; next != "" {
			more := ids(read(v.library, "view="+v.view+"&limit=2&cursor="+next, v.viewer))
			if len(more) == 0 || more[0] != whole[start+len(got)] {
				t.Fatalf("%s %s: continuation after start gave %v", v.library, v.viewer, more)
			}
		}
	}
	for _, query := range []string{"view=discover&start=2", "view=browse&start=-1", "view=browse&start=x"} {
		if status, raw := call("GET", "/v1/libraries/"+f.library.Movies+"/content?"+query, "", "open"); status != 400 {
			t.Fatalf("%s: %d %s", query, status, raw)
		}
	}
}
