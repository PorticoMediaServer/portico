package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestDetailEpisodeFactsFiltersAndSeasonAggregates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE catalog_episodes SET air_date=18262+number WHERE season_id=?`, names["season1"].ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE catalog_shows SET content_rating='TV-14' WHERE entity_id=?`, names["show"].ID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('viewer',?,1,'2026-10-22T00:00:00Z')`, names["ep-001"].ID)
		return err
	})
	c.Drain()
	request := workspaceRequest(names)
	detail, err := s.Detail(request.Viewer, "server", names["ep-002"].Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.EpisodeDetails == nil || detail.EpisodeDetails.ContentRating != "TV-14" || detail.EpisodeDetails.Previous == nil || detail.EpisodeDetails.Previous.ID != names["ep-001"].Public || detail.EpisodeDetails.Next == nil || detail.EpisodeDetails.Next.ID != names["ep-003"].Public {
		t.Fatalf("episode facts %+v", detail.EpisodeDetails)
	}
	workspace, err := s.ShowWorkspace(request)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Show.EpisodeCount != 202 || workspace.Show.AddedAt == "" {
		t.Fatalf("show aggregates %+v", workspace.Show)
	}
	var season *Season
	for i := range workspace.Seasons {
		if workspace.Seasons[i].ID == names["season1"].Public {
			season = &workspace.Seasons[i]
		}
	}
	if season == nil || season.EpisodeCount == nil || *season.EpisodeCount != 200 || season.WatchedCount == nil || *season.WatchedCount != 1 || season.FirstAired == "" || season.LastAired == "" {
		t.Fatalf("season aggregate %+v", season)
	}
	played, _, err := s.Episodes(request.Viewer, "", names["season1"].Public, "", 37, "played")
	if err != nil || len(played) != 1 || played[0].ID != names["ep-001"].Public {
		t.Fatalf("playState %v %v", played, err)
	}
	request.PlayState = "unplayed"
	workspace, err = s.ShowWorkspace(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range workspace.Episodes.Sections {
		for _, entry := range section.Entries {
			if entry.ID == names["ep-001"].Public {
				t.Fatal("watched episode in unplayed window")
			}
		}
	}
	request.PlayState = "unknown"
	if _, err = s.ShowWorkspace(request); err == nil {
		t.Fatal("invalid playState accepted")
	}
}

func TestDetailPersonMergesTitleRolesAndReturnsGroups(t *testing.T) {
	c := catalogtest.Open(t)
	m := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(m, "/m/film.mkv", "Film", 2020)
	compactCredits(c, film.ID, personCredit("tmdb:42", "42", "Person", "Lead", "Acting", 0), personCredit("tmdb:42", "42", "Person", "Executive producer", "Production", 1))
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Series", 2020)
	season := c.Season(show, 1)
	episode := workspaceEpisode(t, c, tv, show, season, "series:2020", "episode", "Episode", "seasonal", 1, 1)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO show_people_credits(show_id,identity_key,provider,credit_id,name,role,department,ordinal) VALUES(?,'tmdb:42','tmdb','show-credit','Person','Lead','Acting',0)`, show.ID)
		return err
	})
	c.Drain()
	s := New(c.DB)
	person := personToken(t, c.DB, "tmdb:42")
	if _, err := c.DB.Exec(`UPDATE catalog_people SET biography='Known biography',birth_date='1980-01-02',birth_place='Halifax',biography_source='TMDB' WHERE token=?`, person); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "tv"}}
	page, err := s.Person(viewer, "server", person, "all", "", 37)
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != 2 || len(page.Credits) != 2 || len(page.Groups) != 2 {
		t.Fatalf("title groups %+v", page)
	}
	if page.Person.Biography != "Known biography" || page.Person.BirthDate != "1980-01-02" || page.Person.BirthPlace != "Halifax" || page.Person.BiographySource != "TMDB" {
		t.Fatalf("known person facts lost %+v", page.Person)
	}
	var merged bool
	for _, credit := range page.Credits {
		if credit.Media.ID == film.Public && len(credit.Roles) == 2 {
			merged = true
		}
	}
	if !merged {
		t.Fatal("same title repeated or lost a role", page.Credits)
	}
	production, err := s.Person(viewer, "server", person, "department:Production", "", 37)
	if err != nil || production.PageInfo.Total != 1 || len(production.Credits) != 1 {
		t.Fatalf("department window %+v %v", production, err)
	}
	c.Attributes(episode.ID, "label", "Hidden")
	c.Drain()
	viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"Hidden"}}
	restricted, err := s.Person(viewer, "server", person, "all", "", 37)
	if err != nil || restricted.PageInfo.Total != 1 || len(restricted.Credits) != 1 {
		t.Fatalf("hidden container credit leaked %+v %v", restricted, err)
	}
	for _, group := range restricted.Groups {
		if group.Total != 1 {
			t.Fatalf("hidden container counted in a person group %+v", group)
		}
	}
}

func TestDetailCollectionMembershipFacts(t *testing.T) {
	c := catalogtest.Open(t)
	m := c.Library("m", "Movies", "movie", "/m")
	a := c.Movie(m, "/m/a.mkv", "A", 2000)
	b := c.Movie(m, "/m/b.mkv", "B", 2020)
	collection := c.Collection(m, "Collection", a, b)
	c.Drain()
	detail, err := New(c.DB).Detail(Viewer{Libraries: []string{"m"}}, "server", b.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Collections) != 1 {
		t.Fatal(detail.Collections)
	}
	got := detail.Collections[0]
	if got.ID != collection.Public || got.MemberCount != 2 || got.Position != 2 || got.YearFrom != 2000 || got.YearTo != 2020 {
		t.Fatalf("membership %+v", got)
	}
}
