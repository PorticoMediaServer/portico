package catalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// An artist page lists the artist's own songs and the songs that credit them,
// and releases that are theirs or carry such a song — and nothing else in
// the library.
func TestArtistPageReadsTheArtistsSets(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("mu", "Music", "music", "/mu")
	a, b, other := c.Artist(music, "Alpha"), c.Artist(music, "Beta"), c.Artist(music, "Gamma")
	own := c.Album(a, "Alpha Album", 2020)
	c.Song(own, 1, "/mu/a1.flac", "One")
	c.Song(own, 2, "/mu/a2.flac", "Two")
	guest := c.Album(b, "Beta Album", 2021)
	featured := c.Song(guest, 1, "/mu/b1.flac", "Featuring Alpha")
	c.Song(guest, 2, "/mu/b2.flac", "Beta alone")
	c.Song(c.Album(other, "Gamma Album", 2022), 1, "/mu/g1.flac", "Gamma song")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, featured.ID, []int64{b.ID, a.ID})
	})
	c.Drain()
	s := New(c.DB)
	out, err := s.Content(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"mu"}}, Library: "mu", Profile: "p", ViewerFence: "f", View: "artist", EntityID: a.Public, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, section := range out.Sections {
		counts[section.ID] = section.TotalCount
	}
	if counts["songs"] != 3 || counts["releases"] != 2 {
		t.Fatalf("artist sections %v", counts)
	}
	if out.Entity == nil || out.Entity.Count == nil || *out.Entity.Count != 3 {
		t.Fatalf("artist header %+v", out.Entity)
	}
	// A restricted viewer's header counts only what it may see.
	c.Attributes(featured.ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()
	restricted := classesRestrictionOf(classesCeiling(13), false)
	out, err = s.Content(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "kid", Libraries: []string{"mu"}, Restrictions: restricted}, Library: "mu", Profile: "p", ViewerFence: "kid", View: "artist", EntityID: a.Public, Limit: 10})
	if err != nil || out.Entity == nil || out.Entity.Count == nil || *out.Entity.Count != 2 {
		t.Fatalf("restricted artist header %+v %v", out.Entity, err)
	}
}

// A restricted viewer's library song list walks its visibility class in title
// order: every visible song once, by cursor, and never the hidden one.
func TestRestrictedSongListWalksItsClass(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("mu", "Music", "music", "/mu")
	album := c.Album(c.Artist(music, "Alpha"), "Album", 2020)
	hidden := c.Song(album, 3, "/mu/3.flac", "Charlie")
	for i, title := range []string{"Echo", "Bravo", "Delta", "Alpha song"} {
		c.Song(album, 10+i, "/mu/s"+title+".flac", title)
	}
	c.Attributes(hidden.ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()
	s := New(c.DB)
	restricted := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "mu", restricted); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "kid", Libraries: []string{"mu"}, Restrictions: restricted}
	request := ContentRequest{Viewer: viewer, Library: "mu", Profile: "p", ViewerFence: "kid", View: "songs", Limit: 2}
	titles := []string{}
	for page := 0; page < 5; page++ {
		out, err := s.Content(request)
		if err != nil || len(out.Sections) != 1 {
			t.Fatalf("page %d: %+v %v", page, out.Sections, err)
		}
		for _, e := range out.Sections[0].Entries {
			titles = append(titles, e.Title)
		}
		if out.Sections[0].NextCursor == "" {
			break
		}
		request.Cursor = out.Sections[0].NextCursor
	}
	if got := strings.Join(titles, ","); got != "Alpha song,Bravo,Delta,Echo" {
		t.Fatalf("restricted songs %s", got)
	}
}
