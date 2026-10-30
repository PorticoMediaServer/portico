package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSearchCatalogPageRanksBeforeHydrating(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("films", "Films", "movie", "/films")
	private := c.Library("private", "Private", "movie", "/private")
	music := c.Library("music", "Music", "music", "/music")
	tv := c.Library("tv", "TV", "tv", "/tv")
	names := catalogtest.Names{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		path := "/films/" + id + ".mkv"
		var item catalogtest.Item
		if id == "a" {
			item = c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", path, 0), Title: "Harbor", Year: 2020, Added: "2026-01-01T00:00:00.000Z"}, nil)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				asset, err := compactcatalog.CreateAssetTx(ctx, tx, "search-page-a", compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", Width: 1, Height: 1, Duration: 97}, true)
				if err != nil {
					return err
				}
				return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
			})
		} else {
			item = c.Movie(films, path, "Harbor", 2020)
		}
		names[id] = item
		if id == "c" {
			c.Attributes(item.ID, "label", "Adult")
		}
		if id == "a" {
			c.Fields(item.ID, map[string]any{"poster_url": "local:poster-a", "overview": "Selected detail"})
		}
	}
	names["outside"] = c.Movie(private, "/private/outside.mkv", "Harbor", 2020)
	show := c.Show(tv, "Harbor Show", 2020)
	episode := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: show.ID, Key: compactcatalog.EpisodeKey("harbor show:2020", "absolute", 0, 1), Title: "Harbor Episode"}, map[string]any{"show_id": show.ID, "numbering": "absolute", "number": 1})
	c.File(episode.ID, "/tv/Harbor Show/Harbor Episode.mkv", 90)
	names["episode"] = episode
	artist := c.Artist(music, "Harbor Artist")
	oldAlbum := c.Entity(compactcatalog.Entity{Library: music, Kind: compactcatalog.Album, Parent: artist.ID, Key: compactcatalog.AlbumKey("old"), Title: "Harbor Album", Year: 2020}, map[string]any{"artist_id": artist.ID, "local_key": "old"})
	newAlbum := c.Entity(compactcatalog.Entity{Library: music, Kind: compactcatalog.Album, Parent: artist.ID, Key: compactcatalog.AlbumKey("new"), Title: "Harbor Album", Year: 2022}, map[string]any{"artist_id": artist.ID, "local_key": "new"})
	names["album-old"], names["album-new"] = oldAlbum, newAlbum
	names["song"] = c.Song(newAlbum, 1, "/music/Harbor Album/Harbor Song.flac", "Harbor Song")
	c.Drain()
	s := New(db)
	restrictions := identity.ContentRestrictions{BlockedLabels: []string{"Adult"}}
	request := SearchRequest{
		Viewer:   Viewer{Profile: "p", Fence: "f", Libraries: []string{"films", "music", "tv"}, Restrictions: restrictions},
		ServerID: "server", Q: "Harbor", Group: "movies", Limit: 2,
	}
	for _, sortID := range []string{"relevance", "title", "releaseYear", "dateAdded"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(sortID+"/"+direction, func(t *testing.T) {
				r := request
				r.Sort, r.Direction = sortID, direction
				want := names.Publics("a", "b", "d", "e", "f")
				sort.Strings(want)
				// Every sort breaks ties by the internal identity, so a page
				// from an ordered index continues a ranked one.
				{
					internal := map[string]int64{}
					for _, item := range names {
						internal[item.Public] = item.ID
					}
					sort.Slice(want, func(i, j int) bool { return internal[want[i]] < internal[want[j]] })
				}
				if direction == "desc" {
					for left, right := 0, len(want)-1; left < right; left, right = left+1, right-1 {
						want[left], want[right] = want[right], want[left]
					}
				}
				got := []string{}
				for page := 0; page < 3; page++ {
					out, err := s.Search(context.Background(), r)
					if err != nil {
						t.Fatal(err)
					}
					if len(out.Groups) != 1 || out.Groups[0].Status != "success" || out.Groups[0].TotalCount != 5 {
						t.Fatalf("page %d: %+v", page, out.Groups)
					}
					group := out.Groups[0]
					for _, entry := range group.Items {
						got = append(got, entry.ID)
						if entry.ID == names["a"].Public {
							if entry.Overview != "" || entry.Duration == nil || *entry.Duration != 97 || entry.Available == nil || !*entry.Available || entry.Playback == nil || entry.PosterURL != "/v1/items/"+names["a"].Public+"/art/poster" {
								t.Fatalf("selected detail: %+v", entry)
							}
						}
					}
					if page < 2 && (!group.HasMore || group.NextCursor == "") {
						t.Fatalf("missing continuation on page %d: %+v", page, group)
					}
					if page == 2 && (group.HasMore || group.NextCursor != "") {
						t.Fatalf("last page continued: %+v", group)
					}
					r.Cursor = group.NextCursor
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, want %v", got, want)
				}
			})
		}
	}

	request.Group = ""
	request.Groups = []string{"movies", "episodes", "albums", "songs"}
	request.Limit = 40
	out, err := s.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantGroups := map[string]struct {
		count    int
		subtitle string
	}{
		"movies":   {5, ""},
		"episodes": {1, "Harbor Show · Absolute episode 1 (unassigned season)"},
		"albums":   {2, "Harbor Artist"},
		"songs":    {1, "Harbor Album · Harbor Artist"},
	}
	for _, group := range out.Groups {
		want, ok := wantGroups[group.ID]
		if !ok || group.TotalCount != want.count || len(group.Items) != want.count {
			t.Fatalf("mixed group: %+v", group)
		}
		if want.subtitle != "" {
			for _, entry := range group.Items {
				if entry.Subtitle != want.subtitle {
					t.Fatalf("%s subtitle: %+v", group.ID, entry)
				}
			}
		}
		delete(wantGroups, group.ID)
	}
	if len(wantGroups) != 0 {
		t.Fatalf("missing groups: %v", wantGroups)
	}

	request.Group, request.Groups = "albums", nil
	request.Sort, request.Direction, request.Limit = "releaseYear", "desc", 1
	first, err := s.Search(context.Background(), request)
	if err != nil || len(first.Groups) != 1 || len(first.Groups[0].Items) != 1 || first.Groups[0].Items[0].ID != names["album-new"].Public || first.Groups[0].TotalCount != 2 {
		t.Fatalf("album year first page: %+v, %v", first, err)
	}
	request.Cursor = first.Groups[0].NextCursor
	second, err := s.Search(context.Background(), request)
	if err != nil || len(second.Groups) != 1 || len(second.Groups[0].Items) != 1 || second.Groups[0].Items[0].ID != names["album-old"].Public || second.Groups[0].TotalCount != 2 || second.Groups[0].HasMore {
		t.Fatalf("album year continuation: %+v, %v", second, err)
	}
}
