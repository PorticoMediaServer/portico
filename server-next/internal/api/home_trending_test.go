package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrendingFetchesOnScheduleAndIntersectsTMDBLibraries(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	libraries := get[Libraries](t, s, owner, "/v1/libraries").Items
	library := libraries[0].ID
	movies := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=55").Items
	type result struct {
		ID        int64  `json:"id"`
		MediaType string `json:"media_type"`
	}
	provider := []result{{ID: 999999999, MediaType: "person"}}
	wantOrder := []string{}
	for _, movie := range movies {
		detail := get[Item](t, s, owner, "/v1/items/"+movie.ID)
		id, err := strconv.ParseInt(detail.ExternalIDs["tmdb"], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		provider = append(provider, result{ID: id, MediaType: "movie"})
		wantOrder = append(wantOrder, movie.ID)
	}
	showLibrary := libraryOfKind(t, s, owner, "shows")
	show := get[ItemPage](t, s, owner, "/v1/items?library="+showLibrary+"&kind=show&limit=1").Items[0]
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.setupFile), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const showID = 999999997
	if _, err := conn.Exec(`INSERT OR REPLACE INTO item_external_ids(item_id,provider,external_id)
  SELECT id,'tmdb',? FROM items WHERE public_id=?`, strconv.Itoa(showID), show.ID); err != nil {
		t.Fatal(err)
	}
	provider = append(provider, result{ID: showID, MediaType: "tv"}, result{ID: 999999998, MediaType: "tv"})
	wantOrder = append(wantOrder, show.ID)
	var calls atomic.Int64
	var fail, deep atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/3/trending/all/day" || len(r.URL.Query()) != 1 || r.Header.Get("Authorization") == "" {
			t.Error("unexpected provider request; no library/profile facts may be sent")
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if deep.Load() {
			_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "total_pages": 500,
				"results": []result{{ID: int64(8000000 + page), MediaType: "movie"}}})
			return
		}
		if page < 1 || page > 2 {
			t.Error("unexpected upstream page")
		}
		rows := provider[:20]
		if page == 2 {
			rows = provider[20:]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"page": page, "total_pages": 2, "results": rows})
	}))
	defer upstream.Close()
	s.UseTMDBHost(upstream.URL)
	ctx := context.Background()
	now := time.Now()
	// Home never fetches upstream, even before the first scheduled update.
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == "trending" {
			t.Fatal("invented Trending before fetch")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("Home fetched provider")
	}
	if err := s.metadata.RefreshTrending(ctx, now); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("scheduled fetch did not reach provider end")
	}
	for _, who := range members {
		visible := map[string]bool{}
		for _, it := range get[ItemPage](t, s, who, "/v1/items?library="+library+"&kind=movie&limit=200").Items {
			visible[it.ID] = true
		}
		if status(t, s, "GET", "/v1/items/"+show.ID, who, nil) == 200 {
			visible[show.ID] = true
		}
		expected := []string{}
		for _, id := range wantOrder {
			if visible[id] {
				expected = append(expected, id)
			}
		}
		got := []string{}
		for cursor := ""; ; {
			row := get[HomeRow](t, s, who, "/v1/home/rows/trending?limit=7&total=true&cursor="+url.QueryEscape(cursor))
			if row.Total == nil || *row.Total != len(expected) {
				t.Fatal("Trending counted an invisible/unowned title")
			}
			for _, it := range row.Items {
				got = append(got, it.ID)
			}
			if row.NextCursor == "" {
				break
			}
			cursor = row.NextCursor
			if len(got) > len(wantOrder) {
				t.Fatal("Trending wrapped")
			}
		}
		if !slices.Equal(got, expected) {
			t.Fatal("provider order or visible intersection changed")
		}
	}
	preview := HomeRow{}
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == "trending" {
			preview = row
		}
	}
	if len(preview.Items) != 40 || preview.NextCursor == "" {
		t.Fatal("Trending preview")
	}
	if calls.Load() != 2 {
		t.Fatal("row reads fetched provider")
	}
	if err := s.metadata.RefreshTrending(ctx, now.Add(time.Hour)); err != nil || calls.Load() != 2 {
		t.Fatal("fresh list fetched again", err)
	}
	fail.Store(true)
	if err := s.metadata.RefreshTrending(ctx, now.Add(25*time.Hour)); err == nil {
		t.Fatal("upstream outage accepted")
	}
	if row := get[HomeRow](t, s, members[0], "/v1/home/rows/trending?total=true"); row.Total == nil || *row.Total != len(wantOrder) {
		t.Fatal("outage lost previous complete list")
	}
	failedCalls := calls.Load()
	if err := s.metadata.RefreshTrending(ctx, now.Add(25*time.Hour+5*time.Minute)); err != nil || calls.Load() != failedCalls {
		t.Fatal("failed refresh retried on the next five-minute tick", err)
	}
	if code := status(t, s, "GET", "/v1/home/rows/trending?cursor="+url.QueryEscape(preview.NextCursor), members[0], nil); code != 200 {
		t.Fatal("failed attempt expired the last complete input's cursor")
	}
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider='local' WHERE public_id=?`, showLibrary); err != nil {
		t.Fatal(err)
	}
	if row := get[HomeRow](t, s, members[0], "/v1/home/rows/trending?total=true"); row.Total == nil || *row.Total != len(movies) {
		t.Fatal("local-only show library contributed while TMDB films remained enabled")
	}
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider='local'`); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if err := s.metadata.RefreshTrending(ctx, now.Add(26*time.Hour)); err != nil || calls.Load() != before {
		t.Fatal("disabled TMDB still fetched", err)
	}
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == "trending" {
			t.Fatal("local-only library appeared")
		}
	}
	// Re-enable one library: the previous provider input is usable, then a successful refresh expires old cursors.
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider='tmdb' WHERE public_id=?`, library); err != nil {
		t.Fatal(err)
	}
	fail.Store(false)
	if err := s.metadata.RefreshTrending(ctx, now.Add(50*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "GET", "/v1/home/rows/trending?cursor="+url.QueryEscape(preview.NextCursor), members[0], nil); code != 400 {
		t.Fatal("old provider-order cursor did not expire")
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"home.rowOrder": []string{"trending"}, "home.hiddenRowIds": []string{"trending"}}}
	if code := status(t, s, "PATCH", "/v1/preferences", members[0], edit); code != 200 {
		t.Fatal(fmt.Sprint("save layout ", code))
	}
	editor := get[Home](t, s, members[0], "/v1/home?hidden=include").Rows
	if editor[0].ID != "trending" || !editor[0].Hidden || len(editor[0].Items) != 0 {
		t.Fatal("Trending did not follow layout")
	}
	deep.Store(true)
	before = calls.Load()
	if err := s.metadata.RefreshTrending(ctx, now.Add(75*time.Hour)); err != nil || calls.Load()-before != 50 {
		t.Fatal("daily provider walk did not stop at 50 pages", err, calls.Load()-before)
	}
}
