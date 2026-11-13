package api

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist/anilisttest"
)

func TestAniListLibraryMatchingAndIDEntry(t *testing.T) {
	sh := shelfOf(t, db.LibraryAnime, "The Quiet Sky/Season 1/The Quiet Sky - S01E01.mkv")
	ctx := context.Background()
	if p := sh.s.metadata.Providers["anilist"]; p == nil || p.Name() != "anilist" {
		t.Fatal("AniList was not registered at startup")
	}
	stand := anilisttest.New(t)
	sh.s.UseProvider(anilist.New(stand.URL, 0))
	// Seed only this temporary fixture's stored choice; library editing belongs to its own lane.
	conn, err := sql.Open("sqlite", filepath.Join(sh.s.dataDir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider = 'anilist' WHERE public_id = ?`, sh.library); err != nil {
		t.Fatal(err)
	}
	if asks, err := sh.s.metadata.Asks(ctx, sh.library); err != nil || !asks {
		t.Fatalf("availability: %v, %v", asks, err)
	}
	shows := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=show")
	if len(shows.Items) != 1 {
		t.Fatalf("shows: %+v", shows.Items)
	}
	id := shows.Items[0].ID
	episodes := get[ItemPage](t, sh.s, sh.owner, "/v1/items?library="+sh.library+"&kind=episode")
	if len(episodes.Items) != 1 {
		t.Fatalf("episodes: %+v", episodes.Items)
	}
	before := get[Item](t, sh.s, sh.owner, "/v1/items/"+episodes.Items[0].ID)
	for _, ref := range []string{"42", "anilist:42", "https://anilist.co/anime/42/the-quiet-sky/"} {
		found := get[MatchCandidates](t, sh.s, sh.owner, "/v1/admin/items/"+id+"/matches?ref="+url.QueryEscape(ref))
		if len(found.Items) != 1 || found.Items[0].Provider != "anilist" || found.Items[0].ID != "42" || !found.Items[0].Exact {
			t.Fatalf("id %s: %+v", ref, found)
		}
	}
	sh.do("PUT", "/v1/admin/items/"+id+"/match", MatchRequest{Provider: "anilist", ID: "42"}, nil, 200)
	matched := get[Item](t, sh.s, sh.owner, "/v1/items/"+id)
	if matched.Title != "The Quiet Sky" || matched.ExternalIDs["anilist"] != "42" || matched.Year != 2024 {
		t.Fatalf("matched: %+v", matched)
	}
	after := get[Item](t, sh.s, sh.owner, "/v1/items/"+before.ID)
	if after.Title != before.Title || after.ID != before.ID || len(after.ExternalIDs) != len(before.ExternalIDs) {
		t.Fatalf("local episode changed: before %+v after %+v", before, after)
	}
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider = 'tmdb' WHERE public_id = ?`, sh.library); err != nil {
		t.Fatal(err)
	}
	asked, tmdbAsked := stand.Requests.Load(), sh.tmdb.Requests.Load()
	sh.do("GET", "/v1/admin/items/"+id+"/matches?ref="+url.QueryEscape("https://anilist.co/anime/42"), nil, nil, 422)
	if stand.Requests.Load() != asked || sh.tmdb.Requests.Load() != tmdbAsked {
		t.Fatal("foreign AniList id contacted a provider")
	}
	if _, err := conn.Exec(`UPDATE libraries SET metadata_provider = 'missing-provider' WHERE public_id = ?`, sh.library); err != nil {
		t.Fatal(err)
	}
	if asks, err := sh.s.metadata.Asks(ctx, sh.library); err != nil || asks {
		t.Fatalf("unavailable provider silently fell back: %v, %v", asks, err)
	}
}
