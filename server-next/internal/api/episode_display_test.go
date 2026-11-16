package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestOwnerShowDisplayIsSharedAndEpisodesPageAcrossSeasons(t *testing.T) {
	th := newTheatre(t)
	episode := get[Item](t, th.s, th.owner.AccessToken, "/v1/items/"+th.eps[0])
	show := get[Item](t, th.s, th.owner.AccessToken, "/v1/items/"+episode.RootID)
	showKey, seasonKey := db.ItemKey{Kind: db.KeyFolder, Key: "dark"}, db.ItemKey{Kind: db.KeyName, Key: "dark/2"}
	lib := th.libs[db.LibraryShows]
	if _, err := th.s.db.Import(context.Background(), db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2,
		Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
			{Kind: db.KindSeason, Keys: []db.ItemKey{seasonKey}, Parent: &showKey, Title: "Season 2", IndexNumber: new(2)},
			{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "dark/2/1"}}, Parent: &seasonKey,
				Title: "Then", IndexNumber: new(1), ParentIndex: new(2)},
		}}); err != nil {
		t.Fatal(err)
	}
	if show.EpisodeDisplay == nil || *show.EpisodeDisplay != (EpisodeDisplay{Grouping: "season", Numbering: "season", Order: "oldest"}) {
		t.Fatalf("default show display: %+v", show.EpisodeDisplay)
	}
	path := "/v1/admin/items/" + show.ID + "/episode-display"
	choice := EpisodeDisplay{Grouping: "ranges", Numbering: "absolute", Order: "newest"}
	if w := th.queue("PUT", path, "", choice, nil); w.Code != 200 {
		t.Fatalf("save display: %d %s", w.Code, w.Body)
	}
	for n, role := range []string{"admin", "member"} {
		addMember(t, th.s, th.owner.AccessToken, "viewer-"+role, role)
		token := signInAs(t, th.s, "viewer-"+role, testPassword, 10+n).AccessToken
		if w := call(t, th.s, "PUT", path, token, choice, nil); w.Code != 403 {
			t.Fatalf("%s may write the global display: %d", role, w.Code)
		}
		read := get[Item](t, th.s, token, "/v1/items/"+show.ID)
		if read.EpisodeDisplay == nil || *read.EpisodeDisplay != choice {
			t.Fatalf("%s did not read the shared choice: %+v", role, read.EpisodeDisplay)
		}
	}
	if w := th.queue("PUT", path, "", EpisodeDisplay{Grouping: "anything"}, nil); w.Code != 400 {
		t.Fatalf("invalid display: %d %s", w.Code, w.Body)
	}
	if w := th.queue("PUT", "/v1/admin/items/"+th.film+"/episode-display", "", choice, nil); w.Code != 404 {
		t.Fatalf("film display: %d", w.Code)
	}
	// Both ordinary cursor paging and range offsets use the same natural episode order.
	base := "/v1/items?parent=" + show.ID + "&kind=episode&sort=order&limit=1"
	first := get[ItemPage](t, th.s, th.owner.AccessToken, base)
	next := get[ItemPage](t, th.s, th.owner.AccessToken, base+"&cursor="+first.NextCursor)
	rangePage := get[ItemPage](t, th.s, th.owner.AccessToken, base+"&at=2&order=desc")
	if len(first.Items) != 1 || len(next.Items) != 1 || len(rangePage.Items) != 1 ||
		!slices.Equal([]string{first.Items[0].ID, next.Items[0].ID}, th.eps[:]) || rangePage.Items[0].ID != th.eps[0] {
		t.Fatalf("episode pages: %+v / %+v / %+v", first, next, rangePage)
	}
	tail := get[ItemPage](t, th.s, th.owner.AccessToken, base+"&cursor="+next.NextCursor)
	if len(tail.Items) != 1 || tail.Items[0].Title != "Then" || tail.NextCursor != "" {
		t.Fatalf("the second season was not in the episode list: %+v", tail)
	}
	seasons := get[ItemPage](t, th.s, th.owner.AccessToken, "/v1/items?parent="+show.ID)
	if len(seasons.Items) != 2 || seasons.Items[0].Kind != "season" {
		t.Fatalf("normal show browse lost seasons: %+v", seasons)
	}
}
