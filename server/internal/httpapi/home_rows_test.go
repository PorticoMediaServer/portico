package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func homeAPIFixture(t *testing.T) (http.Handler, identity.Envelope, catalogtest.Names) {
	t.Helper()
	handler, owner, names, _, _ := homeAPIFixtureWithConsole(t)
	return handler, owner, names
}

func homeAPIFixtureWithConsole(t *testing.T) (http.Handler, identity.Envelope, catalogtest.Names, *operations.Store, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	c, names := fixtureTemplates.Open(t, "home-api", buildHomeAPICatalogue)
	db := c.DB
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); e != nil {
		t.Fatal(e)
	}
	owner, e := id.Issue("owner", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	console := operations.New(db)
	return New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Console: console}), owner, names, console, db
}

func TestItemRecommendationsReadStoredPolicyWithoutToolValidation(t *testing.T) {
	handler, owner, names, console, db := homeAPIFixtureWithConsole(t)
	// Prime the router's one-time connection-policy cache before observing
	// the recommendations consumer. Cold router setup reads its own settings
	// independently of this handler; the regression isolates repeated requests.
	if warm := homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/system", ""); warm.Code != 200 {
		t.Fatalf("router policy warm-up: %d", warm.Code)
	}
	toolCalls := 0
	console.FFmpegValidation = func(context.Context, string) operations.FFmpegStatus {
		toolCalls++
		return operations.FFmpegStatus{}
	}
	viewers := []struct{ account, profile, name string }{
		{"b", "q", "B"}, {"c", "r", "C"}, {"d", "s", "D"},
	}
	for _, viewer := range viewers {
		if _, e := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, viewer.account, viewer.account, viewer.profile); e != nil {
			t.Fatal(e)
		}
		// The accounts trigger already creates the primary profile/membership.
		// Update and prove those actual rows instead of inserting duplicates.
		for _, update := range []struct {
			query string
			args  []any
		}{
			{`UPDATE direct_profiles SET name=? WHERE id=? AND account_id=?`, []any{viewer.name, viewer.profile, viewer.account}},
			{`UPDATE direct_memberships SET allowed_libraries='["movies"]',disabled=0 WHERE account_id=?`, []any{viewer.account}},
		} {
			result, e := db.Exec(update.query, update.args...)
			if e != nil {
				t.Fatal(e)
			}
			if count, e := result.RowsAffected(); e != nil || count != 1 {
				t.Fatalf("fixture row update: %d %v", count, e)
			}
		}
	}
	auth := func(context.Context, *sql.Tx, string) error { return nil }
	for _, viewer := range viewers {
		principal := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: viewer.account, ProfileID: viewer.profile}}
		current, e := console.Preferences(context.Background(), principal, "web", auth)
		if e != nil || len(current.Documents) == 0 {
			t.Fatal("preference document", e)
		}
		if _, e = console.ApplyPreferences(context.Background(), principal, auth, operations.PreferenceChange{
			Scope: operations.ScopeProfileServer, DeviceClass: "web",
			ExpectedRevision: current.Documents[0].Revision, IdempotencyKey: identity.Token(),
			Values: operations.PreferencePatch{"privacy.showActivityToMembers": true},
		}); e != nil {
			t.Fatal(e)
		}
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, viewer := range viewers {
		key := persistence.PersonalOwnerKey("local", viewer.account, viewer.profile)
		for _, item := range []string{"m1", "m2"} {
			if _, e := db.Exec(`INSERT INTO rec_profile_signals(profile_id,work_id,weight,engaged,hidden,finished,at,long,short) VALUES(?,?,1,1,0,1,?,1,1)`, key, names[item].ID, now); e != nil {
				t.Fatal(e)
			}
		}
	}
	setCommunity := func(enabled bool) {
		value := 0
		if enabled {
			value = 1
		}
		if _, e := db.Exec(`INSERT INTO server_settings(singleton,"home.communityActivityEnabled",updated_ms) VALUES(1,?,0) ON CONFLICT(singleton) DO UPDATE SET "home.communityActivityEnabled"=excluded."home.communityActivityEnabled",updated_ms=excluded.updated_ms`, value); e != nil {
			t.Fatal(e)
		}
	}
	request := func() []catalog.HomeRow {
		toolCalls = 0
		w := homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/items/"+names["m1"].Public+"/recommendations", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var page struct {
			Rows []catalog.HomeRow `json:"rows"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
			t.Fatal(e)
		}
		if toolCalls != 0 {
			t.Fatalf("recommendations ran FFmpeg validation %d times", toolCalls)
		}
		return page.Rows
	}
	hasCommunityRow := func(rows []catalog.HomeRow) bool {
		for _, row := range rows {
			if row.Relation == "viewers_also_watched" {
				return true
			}
		}
		return false
	}
	setCommunity(false)
	if hasCommunityRow(request()) {
		t.Fatal("community recommendations offered while the stored policy is off")
	}
	setCommunity(true)
	if !hasCommunityRow(request()) {
		t.Fatal("stored community policy did not change the offered recommendation rows")
	}
}

func homeAPIRequest(t *testing.T, h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The home surface, its per-row paging and its customisation are all reachable
// over HTTP, and the layout endpoint refuses required rows by name.
func TestHomeRoutesComposePageAndCustomise(t *testing.T) {
	handler, owner, names := homeAPIFixture(t)
	w := homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home?limit=2", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var document catalog.HomeDocument
	if e := json.Unmarshal(w.Body.Bytes(), &document); e != nil {
		t.Fatal(e)
	}
	if len(document.Rows) == 0 || document.GeneratedAt == "" {
		t.Fatal("empty home", w.Body.String())
	}
	var recent *catalog.HomeRow
	for index := range document.Rows {
		if document.Rows[index].ID == "new_on_server" {
			recent = &document.Rows[index]
		}
	}
	if recent == nil || !recent.Deferred || len(recent.Entries) != 0 || recent.Endpoint != "/v1/home/rows/new_on_server" {
		t.Fatalf("recent row: %+v", recent)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/new_on_server?limit=2", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	*recent = catalog.HomeRow{}
	if err := json.Unmarshal(w.Body.Bytes(), recent); err != nil || recent.Deferred || len(recent.Entries) != 2 || recent.Total != 3 {
		t.Fatalf("preview %+v %v", recent, err)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/new_on_server?limit=2&cursor="+recent.NextCursor, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page catalog.HomeRow
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || len(page.Entries) != 1 || page.Start != 2 {
		t.Fatal(e, w.Body.String())
	}
	if w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/nonexistent", ""); w.Code != 404 {
		t.Fatal("unknown row", w.Code, w.Body.String())
	}
	if w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/trending", ""); w.Code != 404 {
		t.Fatal("gated community row reachable", w.Code)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"k1","rowOrder":["new_on_server"],"hiddenRowIds":["continue"]}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_home_layout") {
		t.Fatal("required row hidden", w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"k2","rowOrder":["trending_now","listen_next"],"hiddenRowIds":["new_on_server"]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var layout operations.HomeLayoutDocument
	if e := json.Unmarshal(w.Body.Bytes(), &layout); e != nil || layout.Revision != 2 || len(layout.HiddenRowIDs) != 1 {
		t.Fatal(e, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home", "")
	if e := json.Unmarshal(w.Body.Bytes(), &document); e != nil {
		t.Fatal(e)
	}
	// trending_now is empty for this viewer and "watchlist" is a row Home no longer
	// has (My List is a Saved tab): the layout saves and the server's order leads.
	if document.Rows[0].ID != "trending_now" || document.Layout.Revision != 2 {
		t.Fatalf("saved layout not applied: %+v", document.Layout)
	}
	for _, row := range document.Rows {
		if row.ID == "new_on_server" {
			t.Fatal("hidden row published over HTTP")
		}
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"k3","rowOrder":[],"hiddenRowIds":[]}`)
	if w.Code != 409 {
		t.Fatal("stale layout revision accepted", w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "POST", "/v1/home/layout/reset", `{"idempotencyKey":"k4"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &layout); e != nil || len(layout.RowOrder) != 0 || len(layout.HiddenRowIDs) != 0 {
		t.Fatal("reset did not clear the layout", e, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/suggestions?limit=5", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "generatedAt") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/items/"+names["m1"].Public+"/recommendations", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"rows\"") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = homeAPIRequest(t, handler, "", "GET", "/v1/home", ""); w.Code != 401 {
		t.Fatal("home served without a session", w.Code)
	}
	if w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home?limit=999", ""); w.Code != 200 {
		t.Fatal("oversized limit was not clamped", w.Code)
	}
}

// CON-23: the layout lists every row the viewer can arrange, including ones with no entries
// (trending_now here), in the saved order, and marks the hidden ones.
func TestHomeLayoutListsEveryArrangeableRow(t *testing.T) {
	handler, owner, _ := homeAPIFixture(t)
	w := homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"k1","rowOrder":["trending_now","new_on_server"],"hiddenRowIds":["recommended"]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/layout", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	tl11AssertHomeSpecResponse(t, "GET", "/v1/home/layout", w)
	var view catalog.HomeLayoutView
	if e := json.Unmarshal(w.Body.Bytes(), &view); e != nil {
		t.Fatal(e)
	}
	if view.Revision != 2 || len(view.RowOrder) != 2 || len(view.HiddenRowIDs) != 1 {
		t.Fatalf("layout: %+v", view)
	}
	ids := map[string]catalog.HomeLayoutRow{}
	for _, row := range view.Rows {
		ids[row.ID] = row
	}
	if len(view.Rows) < 2 || view.Rows[0].ID != "trending_now" || view.Rows[1].ID != "new_on_server" {
		t.Fatalf("saved order first: %+v", view.Rows)
	}
	if _, ok := ids["trending_now"]; !ok {
		t.Fatal("an empty row is missing from the layout")
	}
	if !ids["recommended"].Hidden || ids["continue"].Hidden || !ids["continue"].Required || ids["new_on_server"].LibraryID != "" || ids["new_on_server"].Hidden {
		t.Fatalf("row flags: %+v", ids)
	}
	// Home publishes the endpoint independently of its empty preview.
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home", "")
	if !strings.Contains(w.Body.String(), `"id":"trending_now"`) || !strings.Contains(w.Body.String(), `"deferred":true`) {
		t.Fatal("home did not publish the deferred row")
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/trending_now", "")
	var empty catalog.HomeRow
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil || w.Code != 200 || len(empty.Entries) != 0 || empty.Deferred {
		t.Fatal("empty preview", w.Code, w.Body.String())
	}
	if w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/layout?x=1", ""); w.Code != 400 {
		t.Fatal("unknown query accepted", w.Code)
	}
}

// M5: Home entries carry the year, the content rating, the first two genres and the viewer's
// My List state, so the hero can show its meta line and a My List toggle.
func TestHomeEntriesCarryHeroFields(t *testing.T) {
	handler, owner, names := homeAPIFixture(t)
	w := homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/rows/new_on_server", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	tl11AssertHomeSpecResponse(t, "GET", "/v1/home/rows/{id}", w)
	var row catalog.HomeRow
	if e := json.Unmarshal(w.Body.Bytes(), &row); e != nil || len(row.Entries) == 0 {
		t.Fatal(e, w.Body.String())
	}
	sawRated := false
	for _, entry := range row.Entries {
		sawRated = sawRated || entry.ID == names["m1"].Public
		if entry.Year == nil || *entry.Year < 2001 || entry.Watchlisted == nil || !*entry.Watchlisted {
			t.Fatalf("entry without its hero fields: %+v", entry)
		}
		if entry.ID == names["m1"].Public && (entry.ContentRating != "PG-13" || len(entry.Genres) != 2 || entry.Genres[0] != "Drama" || entry.Genres[1] != "Comedy") {
			t.Fatalf("m1 rating and genres: %+v", entry)
		}
	}
	if !sawRated {
		t.Fatal("the row lost m1")
	}
}

func tl11AssertHomeSpecResponse(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); method != "HEAD" && err != nil {
		t.Fatalf("%s %s %d: %v", method, path, w.Code, err)
	}
	doc, schema, err := apispec.Response(method, path, w.Code)
	if err != nil {
		t.Fatal(err)
	}
	if problems := doc.ValidateJSON(schema, w.Body.Bytes()); len(problems) > 0 {
		t.Fatalf("%s %s %d does not match %s: %v\n%s", method, path, w.Code, doc.File, problems, w.Body.String())
	}
}

func TestHomeHasOneComposedEndpoint(t *testing.T) {
	h, viewer, _ := homeAPIFixture(t)
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}
	legacy := call("/v1/content?view=home&limit=12")
	if legacy.Code != 400 {
		t.Fatal(legacy.Code, legacy.Body.String())
	}
	current := call("/v1/home?limit=12")
	var home catalog.HomeDocument
	if e := json.Unmarshal(current.Body.Bytes(), &home); e != nil || current.Code != 200 {
		t.Fatal(current.Code, current.Body.String(), e)
	}
	for _, row := range home.Rows {
		if row.SeeAll == nil {
			t.Fatal("missing explicit destination", row.ID)
		}
	}
}

func TestHomeSavedSummaryShapeAndLayoutConflictHTTP(t *testing.T) {
	handler, owner, _ := homeAPIFixture(t)
	w := homeAPIRequest(t, handler, "", "GET", "/v1/me/saved/summary", "")
	if w.Code != 401 {
		t.Fatal("anonymous summary", w.Code)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/me/saved/summary", "")
	var summary catalog.SavedSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || w.Code != 200 || summary.MyList.Posters != 3 || summary.MyList.Squares != 0 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/content?view=watchlist&shape=squares&limit=1", "")
	var content catalog.ContentEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &content); err != nil || w.Code != 200 || len(content.Sections) != 0 || content.ShapeCounts == nil || content.ShapeCounts.Posters != 3 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"first-layout","rowOrder":["continue_books"],"hiddenRowIds":[]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "PUT", "/v1/home/layout", `{"expectedRevision":1,"idempotencyKey":"stale-layout","rowOrder":["continue"],"hiddenRowIds":[]}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "home_layout_conflict") {
		t.Fatal("layout conflict", w.Code, w.Body.String())
	}
	w = homeAPIRequest(t, handler, owner.AccessToken, "GET", "/v1/home/layout", "")
	var layout catalog.HomeLayoutView
	if err := json.Unmarshal(w.Body.Bytes(), &layout); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	found := false
	for _, row := range layout.Rows {
		if row.ID == "continue_books" {
			found = true
			if row.AvailabilityText == nil || row.AvailabilityText.Fallback != "Shown when a book is in progress" {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatal("empty books descriptor absent")
	}
}

func buildHomeAPICatalogue(t *testing.T) (*catalogtest.Catalog, catalogtest.Names) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	names := catalogtest.Names{
		"m1": c.Movie(movies, "/movies/m1.mp4", "First", 2001),
		"m2": c.Movie(movies, "/movies/m2.mp4", "Second", 2002),
		"m3": c.Movie(movies, "/movies/m3.mp4", "Third", 2003),
	}
	for i, name := range []string{"m1", "m2", "m3"} {
		added := []string{"2026-01-01T00:00:00.000Z", "2026-01-02T00:00:00.000Z", "2026-01-03T00:00:00.000Z"}[i]
		c.Fields(names[name].ID, map[string]any{"backdrop_url": "/b", "added_text": added})
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES(?,?,1,0,1)`, identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "profile"}), names[name].ID)
	}
	c.Attributes(names["m1"].ID, "contentRating", "PG-13")
	c.Genres(names["m1"].ID, "fixture", "Drama", "Comedy", "Thriller")
	c.Drain()
	return c, names
}
