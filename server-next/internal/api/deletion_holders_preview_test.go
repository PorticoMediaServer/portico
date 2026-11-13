package api

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Deleting one season's last episode keeps the show; deleting the last episode in the
// remaining season removes both holders. Whole-title and version previews must match it.
func TestDeletionPreviewCountsTheHoldersActuallyRemoved(t *testing.T) {
	for _, mode := range []string{"versions", "items"} {
		t.Run(mode, func(t *testing.T) {
			sh := shelfOf(t, db.LibraryShows, "Lost (2004)/Season 1/Lost - S01E01.mkv", "Lost (2004)/Season 2/Lost - S02E01.mkv")
			sh.do("PATCH", "/v1/admin/libraries/"+sh.library,
				LibraryEdit{Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}, nil, 200)
			var page ItemPage
			sh.do("GET", "/v1/items?library="+sh.library+"&kind=show,season,episode", nil, &page, 200)
			if len(page.Items) != 5 {
				t.Fatalf("wanted show, seasons and episodes: %+v", page)
			}
			var show string
			seasons := map[int]string{}
			all := []string{}
			for _, item := range page.Items {
				all = append(all, item.ID)
				if item.Kind == "show" {
					show = item.ID
				}
				if item.Kind == "season" && item.Index != nil {
					seasons[*item.Index] = item.ID
				}
				if got := status(t, sh.s, "PUT", "/v1/items/"+item.ID+"/state", sh.member, map[string]any{"favorite": true}); got != 200 {
					t.Fatalf("saving state on %s: %d", item.Kind, got)
				}
			}
			episodes := map[int]Item{}
			for _, item := range page.Items {
				if item.Kind != "episode" {
					continue
				}
				for n, season := range seasons {
					if item.ParentID == season {
						episodes[n] = item
					}
				}
			}
			if show == "" || len(seasons) != 2 || len(episodes) != 2 {
				t.Fatalf("unexpected hierarchy: %+v", page)
			}
			var list ListCreated
			sh.do("POST", "/v1/lists", ListCreate{Kind: "collection", Name: "Keep", Items: all}, &list, 201)
			for n := 1; n <= 2; n++ {
				var item Item
				sh.do("GET", "/v1/items/"+episodes[n].ID, nil, &item, 200)
				if len(item.Versions) != 1 {
					t.Fatalf("wanted one version: %+v", item)
				}
				id := item.ID
				if mode == "versions" {
					id = item.Versions[0].ID
				}
				path := "/v1/admin/" + mode + "/" + id
				head, err := sh.s.db.ChangesHead(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var preview DeletionPreview
				sh.do("GET", path+"/deletion", nil, &preview, 200)
				expected := map[string]bool{seasons[n]: true}
				if n == 2 {
					expected[show] = true
				}
				if preview.Titles != 1+len(expected) || preview.ListEntries != preview.Titles || preview.OtherPeople != preview.Titles || preview.Files != 1 {
					t.Fatalf("holder deletion impact was incomplete: %+v", preview)
				}
				if len(preview.AlsoRemoves) != len(expected) {
					t.Fatalf("wrong additional removals: %+v", preview)
				}
				for _, other := range preview.AlsoRemoves {
					if !expected[other.ID] {
						t.Fatalf("unexpected removed holder: %+v", other)
					}
				}
				after, err := sh.s.db.ChangesHead(context.Background())
				if err != nil || after != head {
					t.Fatalf("preview committed change-feed rows: %d -> %d, %v", head, after, err)
				}
				var saved ItemPage
				sh.do("GET", "/v1/items?list="+list.ID, nil, &saved, 200)
				if len(saved.Items) != 7-2*n {
					t.Fatalf("preview removed list entries: %+v", saved)
				}
				for _, saved := range saved.Items {
					var kept Item
					if w := call(t, sh.s, "GET", "/v1/items/"+saved.ID, sh.member, nil, &kept); w.Code != 200 || !kept.State.Favorite {
						t.Fatalf("preview removed saved state: %d %s", w.Code, w.Body)
					}
				}
				file := filepath.Join(sh.root, fmt.Sprintf("Lost (2004)/Season %d/Lost - S%02dE01.mkv", n, n))
				if _, err := os.Stat(file); err != nil {
					t.Fatalf("preview touched a file: %v", err)
				}
				target := path
				if mode == "items" {
					target += "?confirm=" + url.QueryEscape(item.Title)
				}
				var result DeletionResult
				sh.do("DELETE", target, nil, &result, 200)
				if result.FilesDeleted != preview.Files {
					t.Fatalf("delete did not match preview: %+v", result)
				}
				for gone := range expected {
					sh.do("GET", "/v1/items/"+gone, nil, nil, 404)
				}
				sh.do("GET", "/v1/items/"+item.ID, nil, nil, 404)
				sh.do("GET", "/v1/items?list="+list.ID, nil, &saved, 200)
				want := 3
				if n == 2 {
					want = 0
				}
				if len(saved.Items) != want {
					t.Fatalf("deletion did not match lost entries: %+v", saved)
				}
				if n == 1 {
					sh.do("GET", "/v1/items/"+show, nil, nil, 200)
				}
			}
		})
	}
}
