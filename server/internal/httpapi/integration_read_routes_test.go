package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestIntegrationReadHTTPReadsStayBoundedAndPathsAreOwnerOnly(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/Film.mkv", "Film", 2000)
	c.Exec(`UPDATE direct_memberships SET allowed_libraries='["m"]',revision=revision+1 WHERE account_id='member'`)
	c.Drain()

	for _, test := range []struct {
		token string
		paths bool
	}{{f.owner, true}, {f.admin, false}, {f.member, false}} {
		w := f.call("GET", "/v1/integrations/libraries", test.token, "")
		if w.Code != 200 {
			t.Fatalf("libraries: %d %s", w.Code, w.Body)
		}
		var libraries IntegrationLibraries
		if err := json.Unmarshal(w.Body.Bytes(), &libraries); err != nil {
			t.Fatal(err)
		}
		if len(libraries.Items) != 1 || (len(libraries.Items[0].Paths) > 0) != test.paths {
			t.Fatalf("library paths: %+v", libraries)
		}
		w = f.call("GET", "/v1/integrations/items?includeFiles=true&ids="+item.Public, test.token, "")
		var page catalog.IntegrationItemPage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		if w.Code != 200 || page.Total != 1 || len(page.Items) != 1 || (len(page.Items[0].Paths) > 0) != test.paths {
			t.Fatalf("items: %d %s", w.Code, w.Body)
		}
		w = f.call("GET", "/v1/integrations/items/"+item.Public+"/media-info", test.token, "")
		var info catalog.IntegrationMediaInfo
		_ = json.Unmarshal(w.Body.Bytes(), &info)
		if w.Code != 200 || info.Total != 1 || len(info.MediaSources) != 1 || (info.MediaSources[0].Path != "") != test.paths {
			t.Fatalf("media: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "/v1/media/") || strings.Contains(w.Body.String(), `"url"`) {
			t.Fatalf("delivery data: %s", w.Body)
		}
	}
	for _, query := range []string{"limit=101", "limit=0", "limit=1&limit=2", "unknown=1", "ids=a,,b", "provider=tvdb", "season=1"} {
		if w := f.call("GET", "/v1/integrations/items?"+query, f.owner, ""); w.Code != 400 {
			t.Fatalf("bad query %s: %d %s", query, w.Code, w.Body)
		}
	}
	if w := f.call("GET", "/v1/integrations/items", "", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestIntegrationReadHTTPUsersUseActualScopeAndBoundProfile(t *testing.T) {
	f := integrationFixture(t)
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"integration-read-only","name":"Reader","scopes":["read"]}`)
	for _, test := range []struct {
		token   string
		want    int
		profile string
	}{{f.owner, 3, ""}, {f.admin, 3, ""}, {f.member, 1, "member-profile"}, {read.Secret, 1, "owner-profile"}} {
		w := f.call("GET", "/v1/integrations/users", test.token, "")
		var users IntegrationUsers
		_ = json.Unmarshal(w.Body.Bytes(), &users)
		if w.Code != 200 || len(users.Items) != test.want {
			t.Fatalf("users: %d %s", w.Code, w.Body)
		}
		if test.profile != "" && (users.Items[0].ProfileID != test.profile || users.Items[0].ProfileName == "") {
			t.Fatalf("wrong profile: %+v", users)
		}
		if strings.Contains(w.Body.String(), "email") || strings.Contains(w.Body.String(), "password") {
			t.Fatalf("excess user fields: %s", w.Body)
		}
	}
	app := newApp(t, f, "read-projection")
	token := appLogin(t, f, app)
	w := f.call("GET", "/v1/integrations/users", token.Token, "")
	var users IntegrationUsers
	_ = json.Unmarshal(w.Body.Bytes(), &users)
	if w.Code != 200 || len(users.Items) != 1 || users.Items[0].ProfileID != "member-profile" || users.Items[0].AccountID != "member" {
		t.Fatalf("app scope: %d %s", w.Code, w.Body)
	}
	// Users paging never grants another profile to a bound token, even if its
	// seek cursor was obtained from an admin listing.
	w = f.call("GET", "/v1/integrations/users?limit=1", f.owner, "")
	_ = json.Unmarshal(w.Body.Bytes(), &users)
	w = f.call("GET", "/v1/integrations/users?limit=1&cursor="+url.QueryEscape(users.NextCursor), token.Token, "")
	_ = json.Unmarshal(w.Body.Bytes(), &users)
	if w.Code != 200 {
		t.Fatalf("cursor: %d %s", w.Code, w.Body)
	}
	for _, item := range users.Items {
		if item.ProfileID != "member-profile" {
			t.Fatalf("cursor widened profile: %+v", item)
		}
	}
	// A read-only owner key cannot become a global dashboard key, and the
	// existing own-controls read still requires its native playback grant.
	if w = f.call("GET", "/v1/integrations/sessions", read.Secret, ""); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("read scope widened to playback/admin: %d %s", w.Code, w.Body)
	}
}

func TestIntegrationReadHTTPAllSeasonsAndNativeSeasonResolution(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	lib := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(lib, "Show", 2020)
	one := c.Season(show, 1)
	two := c.Season(show, 2)
	c.Episode(show, one, 1, "/tv/a.mkv")
	second := c.Episode(show, two, 1, "/tv/b.mkv")
	c.Drain()
	var page catalog.IntegrationItemPage
	w := f.call("GET", "/v1/integrations/shows/"+show.Public+"/episodes", f.owner, "")
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 2 {
		t.Fatalf("all seasons: %d %s", w.Code, w.Body)
	}
	w = f.call("GET", "/v1/integrations/shows/"+show.Public+"/episodes?season=2", f.owner, "")
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || page.Total != 1 || page.Items[0].ID != second.Public {
		t.Fatalf("season resolution: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/integrations/shows/"+show.Public+"/episodes?season=99", f.owner, ""); w.Code != 404 {
		t.Fatalf("missing season silently changed meaning: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/integrations/shows/"+show.Public+"/episodes?season=1&seasonId="+two.Public, f.owner, ""); w.Code != 400 {
		t.Fatalf("ambiguous season: %d %s", w.Code, w.Body)
	}
}

func TestIntegrationReadHTTPAuthorityChangeDiscardsComposedFacts(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	lib := c.Library("m", "Movies", "movie", "/m")
	c.Movie(lib, "/m/Film.mkv", "Film", 2000)
	c.Drain()
	request := httptest.NewRequest("GET", "/v1/integrations/items", nil)
	request.Header.Set("Authorization", "Bearer "+f.member)
	err := f.deps.integrationCatalogRead(request, func(_ context.Context, _ identity.Principal, _ *catalog.Service, _ catalog.Viewer, _ bool) error {
		c.Exec(`UPDATE direct_memberships SET disabled=1,revision=revision+1 WHERE account_id='member'`)
		return nil
	})
	if err == nil {
		t.Fatal("authority changed during read but result was released")
	}
}

func TestIntegrationReadHTTPSessionsExposeOnlyReadProjection(t *testing.T) {
	f := newV1Fixture(t, 1)
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "integration-sessions-1"}, startBody(f.items[0], map[string]any{"state": "paused"}), 201, &session)
	settleCompactCatalogue(t, f.db)
	var page IntegrationSessions
	w := f.call("GET", "/v1/integrations/sessions", nil, nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].ID != session.ID || page.Items[0].Item == nil || len(page.Items[0].Actions) == 0 {
		t.Fatalf("native session projection: %s", w.Body)
	}
	for _, forbidden := range []string{`"totals"`, `"streamLimit"`, `"queue"`, `"delivery"`, `"url"`, "/v1/media/"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("session read leaked %s: %s", forbidden, w.Body)
		}
	}
}

func TestIntegrationReadMetadata(t *testing.T) {
	m := integrationReadMetadata("example", "/v1/integrations/example", "Read example", "limit")
	if m.Audience != apikit.Integrations || m.Requires != apikit.ScopeRead || m.Admission != laneBrowsing || m.Access != apikit.Device || m.Method != "GET" {
		t.Fatalf("wrong native contract: %+v", m)
	}
}

func TestIntegrationReadSessionEpisodeFactsAndRedactionAreBatched(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	tv := c.Library("tv", "TV", "tv", "/tv")
	private := c.Library("private", "Private", "movie", "/private")
	show := c.Show(tv, "Actual Series", 2020)
	season := c.Season(show, 2)
	episode := c.Episode(show, season, 7, "/tv/seven.mkv")
	hidden := c.Movie(private, "/private/hidden.mkv", "Hidden", 2000)
	c.Exec(`UPDATE direct_memberships SET allowed_libraries='["tv"]',revision=revision+1 WHERE account_id='member'`)
	c.Drain()
	request := httptest.NewRequest("GET", "/v1/integrations/sessions", nil)
	request.Header.Set("Authorization", "Bearer "+f.member)
	page := IntegrationSessions{Items: []IntegrationSession{{ID: "one", Item: &IntegrationSessionItem{ID: episode.Public}, Actions: []string{"stop"}}, {ID: "two", Item: &IntegrationSessionItem{ID: hidden.Public, Title: "Hidden"}, Actions: []string{"pause", "stop"}, PositionMs: 123, DurationMs: 456}}}
	if err := f.deps.integrationSessionItems(request, &page, false); err != nil {
		t.Fatal(err)
	}
	item := page.Items[0].Item
	if item == nil || item.Kind != "episode" || item.Episode == nil || item.Episode.ShowID != show.Public || item.Episode.ShowTitle != "Actual Series" || item.Episode.SeasonID != season.Public || item.Episode.Number != 7 || item.Episode.SeasonNumber == nil || *item.Episode.SeasonNumber != 2 {
		t.Fatalf("session grouping facts: %+v", item)
	}
	if page.Items[1].Item != nil || strings.Join(page.Items[1].Actions, ",") != "stop" || page.Items[1].PositionMs != 0 || page.Items[1].DurationMs != 0 {
		t.Fatalf("hidden facts released: %+v", page.Items[1])
	}
}

func TestIntegrationReadSecondaryProfileNeverBorrowsOwnerTier(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, err := f.deps.DB.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES('owner-secondary','owner','Secondary',0,1)`); err != nil {
		t.Fatal(err)
	}
	w := f.call("GET", "/v1/integrations/users", f.owner, "")
	var users IntegrationUsers
	_ = json.Unmarshal(w.Body.Bytes(), &users)
	if w.Code != 200 {
		t.Fatalf("users: %d %s", w.Code, w.Body)
	}
	roles := map[string]string{}
	for _, user := range users.Items {
		roles[user.ProfileID] = user.Role
	}
	if roles["owner-profile"] != identity.TierOwner || roles["owner-secondary"] != identity.TierMember {
		t.Fatalf("profile roles: %+v", roles)
	}
}
