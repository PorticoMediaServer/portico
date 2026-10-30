package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestEpisodeUnmatchedSourceKeepsIdentityWhenAssigned(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("tv", "TV", "tv", "/tv")
	var asset int64
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		asset, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/tv/Unknown.Title.2020.1080p.WEB-DL.x264.mkv", Duration: 90, Container: "mkv", VideoCodec: "h264"})
		return err
	})
	_ = asset
	s := New(c.DB)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return s.commitEpisodes(tx, "tv", token, "Unknown.Title.2020.1080p.WEB-DL.x264.mkv", EpisodeNaming{Issue: "unrecognized_episode_name"}, false)
	})
	c.Drain()
	var id string
	if err := c.DB.QueryRow(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_asset_links a ON a.entity_id=e.id WHERE e.library_id=? AND a.asset_id=?`, library, asset).Scan(&id); err != nil {
		t.Fatal(err)
	}
	item, err := s.Get("p", id)
	if err != nil || item.Kind != "episode" || item.Episode != nil || item.Title != "Unknown Title" || !item.Available {
		t.Fatalf("loose item %+v %v", item, err)
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	browse, err := s.BrowseEntities(viewer, BrowseRequest{Library: "tv", Profile: "p", ViewerFence: "f", Pivot: "episodes", Limit: 37})
	if err != nil || len(browse.Entries) != 1 || browse.Entries[0].ID != id {
		t.Fatalf("loose browse %+v %v", browse, err)
	}
	search, err := s.Search(context.Background(), SearchRequest{Viewer: viewer, ServerID: "server", Profile: "p", ViewerFence: "f", Libraries: []string{"tv"}, Q: "Unknown", Group: "episodes", Limit: 37})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range search.Groups {
		for _, entry := range group.Items {
			if entry.ID == id {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("loose item absent from search", search)
	}
	if _, err = s.Detail(viewer, "server", id, false); err != nil {
		t.Fatal("loose detail is not runnable", err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return s.commitEpisodes(tx, "tv", token, "Unknown.Title.2020.1080p.WEB-DL.x264.mkv", EpisodeNaming{ShowTitle: "Known Show", Numbering: "seasonal", Season: 1, Numbers: []int{2}}, true)
	})
	c.Drain()
	assigned, err := s.Get("p", id)
	if err != nil || assigned.ID != id || assigned.Episode == nil || assigned.Episode.ShowTitle != "Known Show" || assigned.Episode.Number != 2 || !assigned.Available {
		t.Fatalf("identity lost %+v %v", assigned, err)
	}
}
