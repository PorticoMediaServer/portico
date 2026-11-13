package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestSelectionPreviewCountsHoldersEmptiedByTheWholeSelection(t *testing.T) {
	sh := shelfOf(t, db.LibraryShows, "Lost (2004)/Season 1/Lost - S01E01.mkv", "Lost (2004)/Season 2/Lost - S02E01.mkv")
	sh.do("PATCH", "/v1/admin/libraries/"+sh.library,
		LibraryEdit{Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}, nil, 200)
	var page ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=show,season,episode", nil, &page, 200)
	if len(page.Items) != 5 {
		t.Fatalf("wanted five items: %+v", page)
	}
	var selected, all []string
	for _, item := range page.Items {
		all = append(all, item.ID)
		if item.Kind == "episode" {
			selected = append(selected, item.ID)
		}
		if code := status(t, sh.s, "PUT", "/v1/items/"+item.ID+"/state", sh.member, map[string]any{"favorite": true}); code != 200 {
			t.Fatalf("saving state: %d", code)
		}
	}
	var list ListCreated
	sh.do("POST", "/v1/lists", ListCreate{Kind: "collection", Name: "Keep", Items: all}, &list, 201)
	oversized := make([]string, 201)
	for n := range oversized {
		oversized[n] = fmt.Sprintf("item_over_limit_%d", n)
	}
	for _, route := range []string{"deletion", "delete?confirm=201"} {
		w := call(t, sh.s, "POST", "/v1/admin/items/"+route, sh.owner, SelectionDelete{IDs: oversized}, nil)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "Delete at most 200 titles at once") {
			t.Fatalf("server did not enforce its selection cap before looking up titles: %d %s", w.Code, w.Body)
		}
	}
	head, err := sh.s.db.ChangesHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var preview SelectionDeletion
	req := SelectionDelete{IDs: selected}
	sh.do("POST", "/v1/admin/items/deletion", req, &preview, 200)
	if preview.Count != 2 || preview.Titles != 5 || preview.Files != 2 || preview.ListEntries != 5 || preview.OtherPeople != 5 || len(preview.AlsoRemoves) != 3 {
		t.Fatalf("the combined selection must also remove both seasons and their show: %+v", preview)
	}
	for _, other := range preview.AlsoRemoves {
		for _, id := range selected {
			if other.ID == id {
				t.Fatalf("selected title named as additional: %+v", other)
			}
		}
	}
	after, err := sh.s.db.ChangesHead(context.Background())
	if err != nil || head != after {
		t.Fatalf("preview committed feed changes: %d -> %d, %v", head, after, err)
	}
	var saved ItemPage
	sh.do("GET", "/v1/items?list="+list.ID, nil, &saved, 200)
	if len(saved.Items) != 5 {
		t.Fatalf("preview removed entries: %+v", saved)
	}
	for _, item := range saved.Items {
		var kept Item
		if w := call(t, sh.s, "GET", "/v1/items/"+item.ID, sh.member, nil, &kept); w.Code != 200 || !kept.State.Favorite {
			t.Fatalf("preview removed state: %d %s", w.Code, w.Body)
		}
	}
	var result SelectionDeleted
	sh.do("POST", "/v1/admin/items/delete?confirm=2", req, &result, 200)
	if result.Removed != 2 || result.FilesDeleted != preview.Files {
		t.Fatalf("delete differs from preview: %+v", result)
	}
	for _, id := range all {
		sh.do("GET", "/v1/items/"+id, nil, nil, 404)
	}
	sh.do("GET", "/v1/items?list="+list.ID, nil, &saved, 200)
	if len(saved.Items) != 0 {
		t.Fatalf("entries survived the deletion: %+v", saved)
	}
}
