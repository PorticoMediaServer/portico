package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// SR-4, SR-5: a search row carries the facts the shared card model reads, not only the
// joined subtitle. An episode names its show ("Silo · S1 E3"), a song its album, artist
// and the viewer's plays (the Songs table's columns), and a show its season count.
func TestSearchRowsCarryTheCardFactsTheFramesDraw(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	silo := c.Show(tv, "Silo", 2023)
	one, two := c.Season(silo, 1), c.Season(silo, 2)
	machines := c.Episode(silo, one, 3, "/tv/Silo/S01E03.mkv")
	c.Episode(silo, two, 1, "/tv/Silo/S02E01.mkv")
	c.Fields(machines.ID, map[string]any{"title": "Machines"})
	beatles := c.Artist(music, "The Beatles")
	abbey := c.Album(beatles, "Abbey Road", 1969)
	song := c.Song(abbey, 1, "/music/Abbey Road/01.flac", "Come Together")
	c.Drain()
	c.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES('p',?,7)`, song.ID)

	s := New(c.DB)
	request := SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv", "music"}}, ServerID: "server", Limit: 10}
	find := func(q, group string) ContentEntry {
		t.Helper()
		r := request
		r.Q, r.Group = q, group
		out, err := s.Search(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Groups) != 1 || len(out.Groups[0].Items) == 0 {
			t.Fatalf("%s %q: %+v", group, q, out.Groups)
		}
		return out.Groups[0].Items[0]
	}

	episode := find("Silo", "episodes")
	if episode.Show == nil || episode.Show.Name != "Silo" || episode.Show.ID != c.Public(silo.ID) {
		t.Fatalf("episode show: %+v", episode.Show)
	}
	show := find("Silo", "shows")
	if show.SeasonCount == nil || *show.SeasonCount != 2 {
		t.Fatalf("show season count: %v", show.SeasonCount)
	}
	track := find("Come", "songs")
	if track.Album == nil || track.Album.Name != "Abbey Road" || track.Album.ID != c.Public(abbey.ID) {
		t.Fatalf("song album: %+v", track.Album)
	}
	if track.Artist == nil || track.Artist.Name != "The Beatles" || track.Artist.ID != c.Public(beatles.ID) {
		t.Fatalf("song artist: %+v", track.Artist)
	}
	if track.Plays == nil || *track.Plays != 7 {
		t.Fatalf("song plays: %v", track.Plays)
	}
	// Mixed groups read the same projection.
	r := request
	r.Q, r.Groups = "Silo", []string{"shows", "episodes"}
	out, err := s.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range out.Groups {
		for _, entry := range group.Items {
			if entry.Kind == "episode" && entry.Show == nil || entry.Kind == "show" && entry.SeasonCount == nil {
				t.Fatalf("%s in the All view lost its card facts: %+v", group.ID, entry)
			}
		}
	}
}
