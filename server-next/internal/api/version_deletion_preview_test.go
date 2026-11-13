package api

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestVersionDeletionPreviewUsesOnlyTheSelectedVersion(t *testing.T) {
	sh := newShelf(t, "Heat (1995)/Heat (1995) {edition-Theatrical} - pt1.mkv",
		"Heat (1995)/Heat (1995) {edition-Theatrical} - pt2.mkv", "Heat (1995)/Heat (1995) {edition-Other}.mkv")
	var page ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("wanted one film: %+v", page.Items)
	}
	var item Item
	sh.do("GET", "/v1/items/"+page.Items[0].ID, nil, &item, 200)
	if len(item.Versions) != 2 {
		t.Fatalf("wanted two versions: %+v", item.Versions)
	}
	var whole DeletionPreview
	sh.do("GET", "/v1/admin/items/"+item.ID+"/deletion", nil, &whole, 200)
	if whole.Files != 3 || whole.Titles != 1 {
		t.Fatalf("the shared formatter changed the whole-title preview: %+v", whole)
	}
	for _, version := range item.Versions {
		path := "/v1/admin/versions/" + version.ID + "/deletion"
		var preview DeletionPreview
		sh.do("GET", path, nil, &preview, 200)
		if preview.Title != item.Title || preview.Files != len(version.Parts) || preview.Bytes != version.Size || preview.Titles != 0 || len(preview.AlsoRemoves) != 0 {
			t.Fatalf("preview for only %s: %+v, version %+v", version.ID, preview, version)
		}
		for _, token := range []string{sh.member, sh.limitedAdmin} {
			for _, id := range []string{version.ID, "ver_unknown"} {
				if got := status(t, sh.s, "GET", "/v1/admin/versions/"+id+"/deletion", token, nil); got != 403 {
					t.Fatalf("a non-owner got %d for %s", got, id)
				}
			}
		}
	}
	sh.do("GET", "/v1/admin/versions/ver_unknown/deletion", nil, nil, 404)
	if _, err := os.Stat(filepath.Join(sh.root, "Heat (1995)/Heat (1995) {edition-Other}.mkv")); err != nil {
		t.Fatalf("preview touched the files: %v", err)
	}
}

func TestVersionDeletionPreviewCountsOnlySharedTitlesThatLoseTheirLastVersion(t *testing.T) {
	sh := shelfOf(t, db.LibraryShows, "Lost (2004)/Season 1/Lost - S01E01-E03.mkv", "Lost (2004)/Season 1/Lost - S01E02 {edition-Other}.mkv")
	var page ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=episode", nil, &page, 200)
	ids := map[int]string{}
	for _, item := range page.Items {
		if item.Index != nil {
			ids[*item.Index] = item.ID
		}
	}
	if len(ids) != 3 {
		t.Fatalf("wanted three episodes: %+v", page.Items)
	}
	for _, index := range []int{1, 2, 3} {
		if got := status(t, sh.s, "PUT", "/v1/items/"+ids[index]+"/state", sh.member, map[string]any{"positionMs": 1000}); got != 200 {
			t.Fatalf("saving the member's place: %d", got)
		}
	}
	var list ListCreated
	sh.do("POST", "/v1/lists", ListCreate{Kind: "collection", Name: "Keep", Items: []string{ids[1], ids[2], ids[3]}}, &list, 201)
	var item Item
	sh.do("GET", "/v1/items/"+url.PathEscape(ids[1]), nil, &item, 200)
	var preview DeletionPreview
	sh.do("GET", "/v1/admin/versions/"+item.Versions[0].ID+"/deletion", nil, &preview, 200)
	if preview.Files != 1 || preview.Titles != 2 || preview.ListEntries != 2 || preview.OtherPeople != 2 || len(preview.AlsoRemoves) != 1 || preview.AlsoRemoves[0].ID != ids[3] {
		t.Fatalf("episodes 1 and 3 and their state go; episode 2 keeps its other version and state: %+v", preview)
	}
	sh.do("PATCH", "/v1/admin/libraries/"+sh.library, LibraryEdit{Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}, nil, 200)
	var result DeletionResult
	sh.do("DELETE", "/v1/admin/versions/"+item.Versions[0].ID, nil, &result, 200)
	if result.FilesDeleted != preview.Files || len(result.AlsoRemoved) != 1 || result.AlsoRemoved[0].ID != ids[3] {
		t.Fatalf("delete did not match its preview: %+v", result)
	}
	sh.do("GET", "/v1/items/"+ids[1], nil, nil, 404)
	sh.do("GET", "/v1/items/"+ids[3], nil, nil, 404)
	var kept Item
	if got := call(t, sh.s, "GET", "/v1/items/"+ids[2], sh.member, nil, &kept); got.Code != 200 || kept.State.PositionMs != 1000 || len(kept.Versions) != 1 {
		t.Fatalf("the surviving version/state was lost: %d %s", got.Code, got.Body)
	}
	var saved ItemPage
	sh.do("GET", "/v1/items?list="+list.ID, nil, &saved, 200)
	if len(saved.Items) != 1 || saved.Items[0].ID != ids[2] {
		t.Fatalf("the remaining list entry was lost: %+v", saved)
	}

}
