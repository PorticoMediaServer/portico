package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestEpisodeThumbnailRepairUsesExistingRouteAndRefusesUnavailableEngine(t *testing.T) {
	h, owner, member, db := editorHTTPFixtureDB(t)
	c := catalogtest.New(t, db)
	library := c.Library("episodes", "Episodes", "tv", "/episodes")
	show := c.Show(library, "A show", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/episodes/Show.S01E01.mkv")
	state := editorState(t, h, owner, episode.Public)
	raw, err := json.Marshal(metadata.RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/metadata/item/" + episode.Public
	for _, token := range []string{"", member} {
		response := editorRequest(t, h, "POST", path, token, "application/json", raw)
		if response.Code != tl6RefusedStatus(token) {
			t.Fatalf("non-owner command status=%d body=%s", response.Code, response.Body.String())
		}
	}
	response := editorRequest(t, h, "POST", path, owner, "application/json", raw)
	if response.Code != 404 || !strings.Contains(response.Body.String(), `"artwork_pending"`) {
		t.Fatalf("unavailable engine did not use existing artwork error: %d %s", response.Code, response.Body.String())
	}
	if after := editorState(t, h, owner, episode.Public); after.Revision != state.Revision {
		t.Fatal("unavailable command mutated artwork or metadata")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM artwork_selections`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unavailable command created artwork", count, err)
	}
}
