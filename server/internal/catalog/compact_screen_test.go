package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestCompactScreenNativeEditAndStableIdentity(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Harbor", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/Harbor/Season 1/Pilot.mkv")
	c.Drain()

	s := New(c.DB)
	request := ShowWorkspaceRequest{
		Viewer:   Viewer{Profile: "viewer", Fence: "fence", Libraries: []string{"tv"}},
		ServerID: "server", Library: "tv", Profile: "viewer", ViewerFence: "fence", ShowID: show.Public, Limit: 7,
	}
	before, err := s.ShowWorkspace(request)
	if err != nil || len(before.Seasons) != 1 || len(before.Episodes.Sections) != 1 || len(before.Episodes.Sections[0].Entries) != 1 {
		t.Fatalf("initial workspace: %+v %v", before, err)
	}
	if before.Show.ID != show.Public || before.Seasons[0].ID != season.Public || before.Episodes.Sections[0].Entries[0].ID != episode.Public {
		t.Fatalf("initial public identities: show=%q season=%q episode=%q", before.Show.ID, before.Seasons[0].ID, before.Episodes.Sections[0].Entries[0].ID)
	}

	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: tv, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey("harbor:2020"), Title: "Harbor Again", Year: 2020,
		})
		return err
	})
	c.Drain()
	after, err := s.ShowWorkspace(request)
	if err != nil || after.Show.Title != "Harbor Again" || len(after.Seasons) != 1 || len(after.Episodes.Sections) != 1 || len(after.Episodes.Sections[0].Entries) != 1 {
		t.Fatalf("edited workspace: %+v %v", after, err)
	}
	if after.Show.ID != before.Show.ID || after.Seasons[0].ID != before.Seasons[0].ID || after.Episodes.Sections[0].Entries[0].ID != before.Episodes.Sections[0].Entries[0].ID {
		t.Fatalf("native edit changed public identity: before=(%q,%q,%q) after=(%q,%q,%q)",
			before.Show.ID, before.Seasons[0].ID, before.Episodes.Sections[0].Entries[0].ID,
			after.Show.ID, after.Seasons[0].ID, after.Episodes.Sections[0].Entries[0].ID)
	}
}
