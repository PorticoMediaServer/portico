package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// readingDiscoverGet decodes one Discover answer or fails the test.
func readingDiscoverGet(t *testing.T, r *readingFixture, query url.Values) ReadingDiscoverResponse {
	t.Helper()
	w := readingHTTP(t, New(r.d), "GET", "/v1/reading/discover?"+query.Encode(), r.f.owner.AccessToken, "", nil)
	var out ReadingDiscoverResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("discover %s: %d %s", query.Encode(), w.Code, w.Body.String())
	}
	return out
}

func TestReadingDiscoverRowsContinueAloneWithoutRepeatOrSkip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	var series []string
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("Saga %c", 'A'+i)
		path := readingTreeFile(t, r, fmt.Sprintf("Author/%s/Title %d.epub", name, i))
		readingScan(t, r.f, r.library)
		readingSeriesOwnerField(t, r, readingPathItem(t, r, path), "series", name)
		series = append(series, compactcatalog.ReadingSeriesID(r.library, name))
	}
	readingSeriesDrain(t, r)

	first := readingDiscoverGet(t, r, url.Values{"libraryId": {r.library}, "limit": {"2"}, "seriesLimit": {"2"}})
	if len(first.Rows) != 2 || first.Rows[0].Key != "recent" || first.Rows[1].Key != "series" || first.Rows[0].NextCursor == "" || first.Rows[1].NextCursor == "" {
		t.Fatalf("first page rows %+v", first.Rows)
	}
	// Each row walks on alone; together its pages are its whole ordered membership once.
	walk := func(row string, page ReadingDiscoverRow, ids func(ReadingDiscoverRow) []string) []string {
		var all []string
		for {
			all = append(all, ids(page)...)
			if page.NextCursor == "" {
				return all
			}
			next := readingDiscoverGet(t, r, url.Values{"libraryId": {r.library}, "row": {row}, "cursor": {page.NextCursor}, "limit": {"2"}})
			if len(next.Rows) != 1 || next.Rows[0].Key != row {
				t.Fatalf("continued %s answered other rows %+v", row, next.Rows)
			}
			page = next.Rows[0]
		}
	}
	recent := walk("recent", first.Rows[0], func(row ReadingDiscoverRow) []string {
		var out []string
		for _, item := range row.Items {
			out = append(out, item.ID)
		}
		return out
	})
	whole := readingDiscoverGet(t, r, url.Values{"libraryId": {r.library}, "limit": {"100"}}).Rows[0].Items
	if len(recent) != len(whole) || len(whole) != 6 {
		t.Fatalf("recent pages %v whole %d", recent, len(whole))
	}
	for i := range whole {
		if recent[i] != whole[i].ID {
			t.Fatalf("recent pages reorder %v", recent)
		}
	}
	got := walk("series", first.Rows[1], func(row ReadingDiscoverRow) []string {
		var out []string
		for _, summary := range row.Series {
			if summary.Count != 1 || len(summary.Preview) != 1 {
				t.Fatalf("selected series not evaluated whole %+v", summary)
			}
			out = append(out, summary.ID)
		}
		return out
	})
	if strings.Join(got, ",") != strings.Join(series, ",") {
		t.Fatalf("series pages %v want key order %v", got, series)
	}

	h := New(r.d)
	for _, bad := range []string{
		"cursor=" + url.QueryEscape(first.Rows[0].NextCursor),
		"row=recent&seriesLimit=2",
		"row=series&limit=21&libraryId=" + r.library,
		"row=newspaper&libraryId=" + r.library,
		"row=recent&cursor=1.x",
		"row=series&libraryId=" + r.library + "&cursor=" + url.QueryEscape(first.Rows[0].NextCursor),
	} {
		if w := readingHTTP(t, h, "GET", "/v1/reading/discover?"+bad, r.f.owner.AccessToken, "", nil); w.Code < 400 || w.Code >= 500 {
			t.Fatalf("%s accepted: %d %s", bad, w.Code, w.Body.String())
		}
	}
	// A rebuilt generation retires its series cursor rather than seeking stale keys.
	readingSeriesOwnerField(t, r, whole[0].ID, "series", "Changed")
	readingSeriesDrain(t, r)
	if w := readingHTTP(t, h, "GET", "/v1/reading/discover?row=series&libraryId="+r.library+"&cursor="+url.QueryEscape(first.Rows[1].NextCursor), r.f.owner.AccessToken, "", nil); w.Code != 412 {
		t.Fatalf("stale series cursor %d %s", w.Code, w.Body.String())
	}
}

// Groups are chosen before they are evaluated: book series beside the
// magazines never become groups, and each page counts its groups whole.
func TestReadingPeriodicalGroupsPageSelectedGroupsInKeyOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	names := []string{"Delta Monthly", "alpha weekly", "Charlie Review"}
	for i, name := range names {
		for j := 0; j <= i; j++ {
			path := readingTreeFile(t, r, fmt.Sprintf("%s/Issue 1930-%02d.epub", name, j+1))
			readingScan(t, r.f, r.library)
			item := readingPathItem(t, r, path)
			readingSeriesOwnerField(t, r, item, "series", name)
			readingSeriesOwnerField(t, r, item, "publicationType", "magazine")
		}
	}
	book := readingTreeFile(t, r, "Novelist/Bravo Saga/One.epub")
	readingScan(t, r.f, r.library)
	readingSeriesOwnerField(t, r, readingPathItem(t, r, book), "series", "Bravo Saga")
	readingSeriesDrain(t, r)
	h := New(r.d)
	var got []string
	counts := map[string]int64{}
	path := "/v1/libraries/" + r.library + "/reading/periodicals?publicationType=magazine&limit=1"
	for cursor := ""; ; {
		target := path
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		w := readingHTTP(t, h, "GET", target, r.f.owner.AccessToken, "", nil)
		var page ReadingPeriodicalGroupsResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Groups) != 1 {
			t.Fatalf("groups page %d %s", w.Code, w.Body.String())
		}
		got = append(got, page.Groups[0].Name)
		counts[page.Groups[0].Name] = page.Groups[0].IssueCount
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if strings.Join(got, ",") != "alpha weekly,Charlie Review,Delta Monthly" || counts["alpha weekly"] != 2 || counts["Charlie Review"] != 3 || counts["Delta Monthly"] != 1 {
		t.Fatalf("groups %v counts %v", got, counts)
	}
}

// Owner text can make each item tens of kilobytes after escaping. A page stops
// inside the client's budget and continues from the last item it kept.
func TestReadingItemsPagesStayInsideTheirByteBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	long := strings.Repeat("<&>", 1364) // 4092 bytes; owner values hold at most 4096
	for i := 0; i < 11; i++ {
		path := readingTreeFile(t, r, fmt.Sprintf("Author/Long %d.epub", i))
		readingScan(t, r.f, r.library)
		item := readingPathItem(t, r, path)
		readingSeriesOwnerField(t, r, item, "author", long)
		readingSeriesOwnerField(t, r, item, "series", long+fmt.Sprint(i))
	}
	readingSeriesDrain(t, r)
	h := New(r.d)
	seen := map[string]bool{}
	pages := 0
	for cursor := ""; ; pages++ {
		target := "/v1/reading/items?limit=100&libraryId=" + r.library
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		w := readingHTTP(t, h, "GET", target, r.f.owner.AccessToken, "", nil)
		var page ReadingItemsResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) == 0 {
			t.Fatalf("items page %d %.200s", w.Code, w.Body.String())
		}
		if w.Body.Len() > 256<<10 {
			t.Fatalf("page of %d bytes passes the client budget", w.Body.Len())
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("item %s repeated", item.ID)
			}
			seen[item.ID] = true
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 12 || pages < 1 {
		t.Fatalf("budgeted pages saw %d items over %d continuations", len(seen), pages)
	}
}
