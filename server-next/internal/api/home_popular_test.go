package api

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestPopularHomeUsesOnlySharedRecentVisiblePlays(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.setupFile), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	profiles := []int64{}
	for _, token := range members {
		c, err := s.tokens.CheckAccess(token, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, c.ProfileID)
	}
	library := get[Libraries](t, s, owner, "/v1/libraries").Items[0].ID
	films := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=200").Items
	row := get[HomeRow](t, s, members[0], "/v1/home/rows/popular?total=true")
	if len(row.Items) != 0 || row.Total == nil || *row.Total != 0 {
		t.Fatal("new server invented Popular")
	}
	now := time.Now().Add(-time.Minute).UnixMilli()
	play := func(profile int64, item string, at int64, paused int) {
		t.Helper()
		exec(`INSERT INTO play_history(profile_id,profile_name,account_name,item_id,client_event_id,played_at,item_kind,title,hidden_from_profile)
   SELECT ?, 'Private viewer', 'Private account', id, ?, ?, kind,title,? FROM items WHERE public_id=?`, profile, fmt.Sprint(profile, item, at, paused), at, paused, item)
	}
	// One shared profile's single play is enough, including when that profile is the caller.
	exec(`INSERT INTO preferences(profile_id,device_class,key,value,updated_at) VALUES(?,'',?,'true',0)`, profiles[0], db.PrefShareActivity)
	play(profiles[0], films[0].ID, now, 0)
	row = get[HomeRow](t, s, members[0], "/v1/home/rows/popular?total=true")
	if len(row.Items) != 1 || row.Items[0].ID != films[0].ID {
		t.Fatal("single shared caller's play did not show")
	}
	// Nonsharing, paused and old activity never expands the source.
	play(profiles[1], films[1].ID, now, 0)
	play(profiles[0], films[2].ID, now, 1)
	play(profiles[0], films[3].ID, now-int64((8*24*time.Hour)/time.Millisecond), 0)
	for n, it := range films[4:54] {
		play(profiles[0], it.ID, now-int64(n+1), 0)
	}
	play(profiles[0], films[0].ID, now+1, 0)
	play(profiles[0], films[0].ID, now+2, 0)
	for _, who := range members {
		visible := get[ItemPage](t, s, who, "/v1/items?library="+library+"&kind=movie&limit=200").Items
		expected := []string{}
		eligible := map[string]bool{films[0].ID: true}
		for _, it := range films[4:54] {
			eligible[it.ID] = true
		}
		for _, it := range visible {
			if eligible[it.ID] {
				expected = append(expected, it.ID)
			}
		}
		got := []string{}
		for cursor := ""; ; {
			row = get[HomeRow](t, s, who, "/v1/home/rows/popular?limit=7&total=true&cursor="+url.QueryEscape(cursor))
			if row.Total == nil || *row.Total != len(expected) {
				t.Fatalf("visible total %v want %d", row.Total, len(expected))
			}
			for _, it := range row.Items {
				got = append(got, it.ID)
			}
			if row.NextCursor == "" {
				break
			}
			cursor = row.NextCursor
			if len(got) > 100 {
				t.Fatal("row wrapped")
			}
		}
		slices.Sort(got)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			t.Fatal("Popular leaked or lost a title")
		}
	}

	// A tag count must count this row's subset, never the whole tagged library.
	for _, facet := range get[Facets](t, s, owner, "/v1/facets?facet=genre&library="+library+"&kind=movie").Values {
		filtered := get[HomeRow](t, s, members[0], "/v1/home/rows/popular?library="+library+"&kind=movie&tag="+facet.Value+"&limit=200&total=true")
		if filtered.Total == nil || *filtered.Total != len(filtered.Items) {
			t.Fatal("tag total widened Popular")
		}
	}
	home := get[Home](t, s, members[0], "/v1/home")
	var preview HomeRow
	for _, r := range home.Rows {
		if r.ID == "popular" {
			preview = r
		}
	}
	if preview.Items[0].ID != films[0].ID {
		t.Fatal("plays did not rank the row")
	}
	if len(preview.Items) != 40 || preview.NextCursor == "" {
		t.Fatal("missing Popular preview/continuation")
	}
	rest := get[HomeRow](t, s, members[0], "/v1/home/rows/popular?limit=200&cursor="+url.QueryEscape(preview.NextCursor))
	if len(rest.Items) != 11 {
		t.Fatalf("remaining %d want 11", len(rest.Items))
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"home.hiddenRowIds": []string{"popular"}, "home.rowOrder": []string{"popular"}}}
	if status(t, s, "PATCH", "/v1/preferences", members[0], edit) != 200 {
		t.Fatal("save layout")
	}
	editor := get[Home](t, s, members[0], "/v1/home?hidden=include").Rows
	if editor[0].ID != "popular" || !editor[0].Hidden || len(editor[0].Items) != 0 {
		t.Fatal("Popular did not follow layout")
	}
	// Opting out withdraws previous plays immediately, on existing cursors too.
	exec(`UPDATE preferences SET value='false' WHERE profile_id=? AND key=?`, profiles[0], db.PrefShareActivity)
	row = get[HomeRow](t, s, members[0], "/v1/home/rows/popular?total=true&cursor="+url.QueryEscape(preview.NextCursor))
	if len(row.Items) != 0 || *row.Total != 0 {
		t.Fatal("opt-out left activity in Popular")
	}
}
