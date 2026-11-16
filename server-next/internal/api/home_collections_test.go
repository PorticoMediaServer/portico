package api

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHomeCollectionsPageOnlyVisibleMembers(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	previous := viewRowsBudget
	viewRowsBudget = time.Hour
	t.Cleanup(func() { viewRowsBudget = previous })
	libraries := get[Libraries](t, s, owner, "/v1/libraries").Items
	library := libraries[0].ID
	films := get[ItemPage](t, s, owner, "/v1/items?library="+library+"&kind=movie&limit=70").Items
	ids := func(items []Item) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	var made CollectionCreated
	if w := call(t, s, "POST", "/v1/admin/libraries/"+library+"/collections", owner, CollectionCreate{Title: "Home collection", ItemIDs: ids(films)}, &made); w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	rowID := "collection:" + made.ID
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == rowID {
			t.Fatal("collection on Home before promotion")
		}
	}
	// Add a collection through the existing layout save; each profile chooses independently.
	for _, who := range members {
		edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"home.rowOrder": []string{rowID}}}
		if status(t, s, "PATCH", "/v1/preferences", who, edit) != 200 {
			t.Fatal("promote collection")
		}
	}
	for _, who := range members {
		want := get[ItemPage](t, s, who, "/v1/items?collection="+made.ID+"&limit=200&total=true")
		var got []Item
		for cursor := ""; ; {
			row := get[HomeRow](t, s, who, "/v1/home/rows/"+rowID+"?limit=7&total=true&cursor="+url.QueryEscape(cursor))
			if row.Title != "Home collection" || row.Total == nil || *row.Total != *want.Total {
				t.Fatal("collection header/count")
			}
			got = append(got, row.Items...)
			if row.NextCursor == "" {
				break
			}
			cursor = row.NextCursor
			if len(got) > 70 {
				t.Fatal("collection did not end")
			}
		}
		if !slices.Equal(ids(got), ids(want.Items)) {
			t.Fatal("collection changed source/order")
		}
		found := false
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			if row.ID == rowID {
				found = true
				if len(row.Items) != min(20, len(want.Items)) || row.Library != library {
					t.Fatal("collection preview")
				}
				rest := get[HomeRow](t, s, who, "/v1/home/rows/"+rowID+"?limit=200&cursor="+url.QueryEscape(row.NextCursor))
				if !slices.Equal(ids(append(row.Items, rest.Items...)), ids(want.Items)) {
					t.Fatal("preview continuation")
				}
			}
		}
		if !found {
			t.Fatal("collection absent from Home")
		}
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"home.rowOrder": []string{rowID}, "home.hiddenRowIds": []string{rowID}}}
	if status(t, s, "PATCH", "/v1/preferences", members[0], edit) != 200 {
		t.Fatal("save layout")
	}
	editor := get[Home](t, s, members[0], "/v1/home?hidden=include").Rows
	if editor[0].ID != rowID || !editor[0].Hidden || editor[0].Deferred || len(editor[0].Items) != 0 {
		t.Fatal("collection editor layout")
	}
	// Person-made collections appear on landing; automatic ones need explicit Home promotion.
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.setupFile), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`INSERT INTO item_keys(library_id,kind,key,item_id) SELECT library_id,?, 'tmdb:collection:fixture',id FROM items WHERE public_id=?`, db.KeyProvider, made.ID); err != nil {
		t.Fatal(err)
	}
	for _, row := range get[Home](t, s, members[0], "/v1/libraries/"+library+"/landing").Rows {
		if row.ID == rowID {
			t.Fatal("automatic collection on landing")
		}
	}
	// Removing the promoted row id resets Home to having no collection row.
	if status(t, s, "PATCH", "/v1/preferences", members[0], PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{"home.rowOrder": nil, "home.hiddenRowIds": nil}}) != 200 {
		t.Fatal("reset layout")
	}
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == rowID {
			t.Fatal("collection survived reset")
		}
	}
	// A collection in a library the profile cannot see has neither a row nor a readable name.
	shows := get[ItemPage](t, s, owner, "/v1/items?library="+libraries[1].ID+"&kind=show&limit=2").Items
	var secret CollectionCreated
	if w := call(t, s, "POST", "/v1/admin/libraries/"+libraries[1].ID+"/collections", owner, CollectionCreate{Title: "Private shelf", ItemIDs: ids(shows)}, &secret); w.Code != 200 {
		t.Fatalf("create show collection: %d", w.Code)
	}
	if status(t, s, "GET", "/v1/home/rows/collection:"+secret.ID, members[2], nil) != 404 {
		t.Fatal("hidden library collection exposed")
	}
	for _, row := range get[Home](t, s, members[2], "/v1/home").Rows {
		if strings.Contains(row.ID, secret.ID) {
			t.Fatal("hidden collection on Home")
		}
	}
	if status(t, s, "DELETE", "/v1/admin/collections/"+made.ID, owner, nil) != 204 {
		t.Fatal("delete collection")
	}
	if status(t, s, "GET", "/v1/home/rows/"+rowID, members[0], nil) != 404 {
		t.Fatal("deleted collection row persisted")
	}
}
