package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// seeded is a server holding a small library, with a token for a profile that sees
// everything and one for a profile limited by rating.
func seeded(t *testing.T) (s *Server, token, limited string) {
	t.Helper()
	s, _, members := seededWith(t, 600, 2)
	return s, members[0], members[1]
}

// seededWith is a server holding a seeded library of some size and the seeder's members, with
// a token for the owner and one for each member, in the seeder's order (see seed.Profiles).
func seededWith(t *testing.T, items, profiles int) (s *Server, owner string, members []string) {
	t.Helper()
	s = newTestServer(t)
	// The seeded members have no password, so their tokens are minted here. A token is only
	// let in if it was made since this server started, and a real sign-in says when that was.
	first := setup(t, s)
	started, err := s.tokens.CheckAccess(first.Session.AccessToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := seed.Import(ctx, s.db, seed.Config{Seed: 3, Items: items}, nil); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, s.db, profiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range viewers {
		token, _ := s.tokens.Access(auth.Claims{UserID: v.UserID, ProfileID: v.ProfileID, Role: "member", Boot: started.Boot},
			time.Now(), time.Now().Add(time.Hour))
		members = append(members, token)
	}
	return s, onMain(t, s, first).AccessToken, members
}

func get[T any](t *testing.T, s *Server, token, path string) T {
	t.Helper()
	var out T
	if w := call(t, s, "GET", path, token, nil, &out); w.Code != 200 {
		t.Fatalf("GET %s answered %d: %s", path, w.Code, w.Body)
	}
	return out
}

// An app can find its libraries, page a list to the end, open a title and a person, count a
// filter's choices and read Home, and a profile limited by rating is shown less of each.
func TestBrowse(t *testing.T) {
	s, token, limited := seeded(t)
	libraries := get[Libraries](t, s, token, "/v1/libraries")
	if len(libraries.Items) != 4 {
		t.Fatalf("%d libraries", len(libraries.Items))
	}
	films := get[Library](t, s, token, "/v1/libraries/"+libraries.Items[0].ID)
	if len(films.Views) != 1 || films.Views[0].Kind != "movie" || len(films.Sorts) == 0 || len(films.Filters) == 0 {
		t.Fatalf("the film library does not say how to browse it: %+v", films)
	}

	list := "/v1/items?library=" + films.ID + "&kind=movie&limit=40"
	page := get[ItemPage](t, s, token, list+"&total=true")
	if page.Total == nil || *page.Total != 90 || len(page.Items) != 40 || page.NextCursor == "" {
		t.Fatalf("first page: %d items, total %v, cursor %q", len(page.Items), page.Total, page.NextCursor)
	}
	seen := len(page.Items)
	for page.NextCursor != "" {
		page = get[ItemPage](t, s, token, list+"&cursor="+page.NextCursor)
		seen += len(page.Items)
	}
	if seen != 90 {
		t.Fatalf("paging to the end sent %d films of 90", seen)
	}
	// The list opens at a position, and the letter rail says where each letter begins.
	whole := get[ItemPage](t, s, token, "/v1/items?library="+films.ID+"&kind=movie&limit=90").Items
	if jump := get[ItemPage](t, s, token, list+"&at=57"); len(jump.Items) != 33 || jump.Items[0].ID != whole[57].ID || jump.NextCursor != "" {
		t.Fatalf("opening at 57 of 90: %d items", len(jump.Items))
	}
	counted := 0
	for _, letter := range get[Facets](t, s, token, "/v1/facets?facet=letter&library="+films.ID+"&kind=movie").Values {
		counted += letter.Count
	}
	if counted != 90 {
		t.Fatalf("the letter rail counts %d films of 90", counted)
	}
	// Highest rated first unless asked otherwise; a film nobody rated is last either way.
	best := get[ItemPage](t, s, token, list+"&sort=communityRating").Items
	worst := get[ItemPage](t, s, token, list+"&sort=communityRating&order=asc").Items
	if best[0].Ratings == nil || worst[0].Ratings == nil || best[0].Ratings.Community <= worst[0].Ratings.Community {
		t.Fatalf("by community rating the list starts with %+v, and ascending with %+v", best[0].Ratings, worst[0].Ratings)
	}
	if fewer := get[ItemPage](t, s, limited, list+"&total=true"); *fewer.Total == 0 || *fewer.Total >= 90 {
		t.Fatalf("the limited profile was shown %d films of 90", *fewer.Total)
	}

	// Find a film the limited profile may not see, then ask for it as that profile.
	var hidden Item
	for _, film := range get[ItemPage](t, s, token, list+"&sort=added").Items {
		if w := call(t, s, "GET", "/v1/items/"+film.ID, limited, nil, nil); w.Code == 404 {
			hidden = film
			break
		}
	}
	if hidden.ID == "" {
		t.Fatal("no film is hidden from the limited profile")
	}
	detail := get[Item](t, s, token, "/v1/items/"+hidden.ID)
	if detail.Summary == "" || len(detail.Versions) == 0 || len(detail.Versions[0].Parts[0].Streams) == 0 || len(detail.Credits) == 0 || len(detail.Tags) == 0 {
		t.Fatalf("the detail is missing parts: %+v", detail)
	}
	credits := get[CreditPage](t, s, token, "/v1/items/"+hidden.ID+"/credits?limit=5")
	if len(credits.Items) != 5 || credits.Total != 14 || credits.NextCursor != "5" {
		t.Fatalf("credits: %d of %d, cursor %q", len(credits.Items), credits.Total, credits.NextCursor)
	}
	cast := get[CreditPage](t, s, token, "/v1/items/"+hidden.ID+"/credits?part=cast&limit=3")
	crew := get[CreditPage](t, s, token, "/v1/items/"+hidden.ID+"/credits?part=crew")
	if cast.Total == 0 || crew.Total == 0 || cast.Total+crew.Total != credits.Total || len(cast.Items) != 3 || cast.NextCursor != "3" ||
		cast.Items[0].Role != "actor" || len(crew.Items) != crew.Total || crew.Items[0].Role == "actor" {
		t.Fatalf("cast %d and crew %d of %d credits: %+v %+v", cast.Total, crew.Total, credits.Total, cast.Items, crew.Items)
	}
	person := get[Person](t, s, token, "/v1/people/"+credits.Items[0].PersonID)
	if titles := get[ItemPage](t, s, token, "/v1/items?person="+person.ID+"&total=true"); person.TitleCount == 0 || *titles.Total != person.TitleCount {
		t.Fatalf("person: %d titles, list total %v", person.TitleCount, titles.Total)
	}
	facets := get[Facets](t, s, token, "/v1/facets?facet=genre&library="+films.ID+"&kind=movie")
	if len(facets.Values) == 0 {
		t.Fatal("no genre choices")
	}
	byGenre := get[ItemPage](t, s, token, "/v1/items?library="+films.ID+"&kind=movie&total=true&tag="+facets.Values[0].Value)
	if *byGenre.Total != facets.Values[0].Count {
		t.Fatalf("the genre count says %d, the filtered list has %d", facets.Values[0].Count, *byGenre.Total)
	}
	if own := get[Facets](t, s, token, "/v1/facets?facet=genre&tag="+facets.Values[0].Value); len(own.Values) == 0 {
		t.Fatal("a genre's own page has no genre choices")
	}
	if home := get[Home](t, s, token, "/v1/home"); len(home.Rows) != 1 || home.Rows[0].ID != "recently_added" || len(home.Rows[0].Items) == 0 {
		t.Fatalf("Home before anything is played: %+v", home.Rows)
	}
	// A card says which library it is from, in a list and on Home, so an app can open its page.
	if page.Items[0].LibraryID != films.ID {
		t.Fatalf("a film in a list says its library is %q, want %q", page.Items[0].LibraryID, films.ID)
	}
	for _, card := range get[Home](t, s, token, "/v1/home").Rows[0].Items {
		if card.LibraryID == "" {
			t.Fatalf("a card on Home does not say its library: %+v", card)
		}
	}

	for _, c := range []struct {
		path   string
		token  string
		status int
		code   string
	}{
		{"/v1/items?library=" + films.ID, "", 401, "unauthorized"},
		{"/v1/items", token, 400, "invalid_request"}, // nothing to list by
		{"/v1/items?library=" + films.ID + "&kind=film", token, 400, "invalid_request"},
		{"/v1/items?library=" + films.ID + "&sort=size", token, 400, "invalid_request"},
		{"/v1/items?library=" + films.ID + "&cursor=xyz", token, 400, "invalid_request"},
		{"/v1/items?library=" + films.ID + "&at=-1", token, 400, "invalid_request"},
		{"/v1/items?library=" + films.ID + "&year=19x", token, 400, "invalid_request"},
		{"/v1/items/itm_nothing", token, 404, "not_found"},
		{"/v1/items/" + hidden.ID + "/credits", limited, 404, "not_found"},
		{"/v1/items/" + hidden.ID + "/credits?part=cast", limited, 404, "not_found"},
		{"/v1/items/" + hidden.ID + "/credits?part=extras", token, 400, "invalid_request"},
		{"/v1/libraries/lib_nothing", token, 404, "not_found"},
		{"/v1/people/per_nothing", token, 404, "not_found"},
		{"/v1/facets?facet=colour&library=" + films.ID, token, 400, "invalid_request"},
	} {
		var got errorBody
		if w := call(t, s, "GET", c.path, c.token, nil, &got); w.Code != c.status || got.Error.Code != c.code {
			t.Errorf("GET %s: %d %s, want %d %s", c.path, w.Code, got.Error.Code, c.status, c.code)
		}
	}
}

// Marking a film and a show, undoing the show, and hearing about each change on the feed.
func TestStateAndEvents(t *testing.T) {
	s, token, limited := seeded(t)
	libraries := get[Libraries](t, s, token, "/v1/libraries")
	film := get[ItemPage](t, s, token, "/v1/items?library="+libraries.Items[0].ID+"&kind=movie&limit=1").Items[0]
	show := get[ItemPage](t, s, token, "/v1/items?library="+libraries.Items[1].ID+"&kind=show&limit=1").Items[0]

	// Open the feed first, as an app does, and read what it says while the changes are made.
	ctx, cancel := context.WithCancel(context.Background())
	stream := httptest.NewServer(s)
	defer stream.Close() // after cancel: an open feed ends when its reader goes away
	defer cancel()
	feed := func(query string) func(want string) {
		request, _ := http.NewRequestWithContext(ctx, "GET", stream.URL+"/v1/events"+query, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("opening the feed: %v, %v", response, err)
		}
		t.Cleanup(func() { response.Body.Close() })
		lines := make(chan string, 100)
		go func() {
			for scanner := bufio.NewScanner(response.Body); scanner.Scan(); {
				lines <- scanner.Text()
			}
			lines <- "<closed>"
		}()
		return func(want string) {
			t.Helper()
			for {
				select {
				case line := <-lines:
					if strings.Contains(line, want) {
						return
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("the feed did not say %q", want)
				}
			}
		}
	}
	expect := feed("")
	expect("event: ready")

	var state StateReply
	if w := call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, map[string]any{"positionMs": 90_000}, &state); w.Code != 200 || state.PositionMs != 90_000 {
		t.Fatalf("progress: %d %+v", w.Code, state)
	}
	expect(fmt.Sprintf(`"topic":"state","op":"updated","id":%q`, film.ID))
	if home := get[Home](t, s, token, "/v1/home"); home.Rows[0].ID != "continue" || home.Rows[0].Items[0].ID != film.ID {
		t.Fatalf("Home after starting a film: first row %q", home.Rows[0].ID)
	}
	// A rating of null clears; a body with only favourite leaves the position alone.
	w := call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, map[string]any{"favorite": true, "rating": 7}, &state)
	if w.Code != 200 || !state.Favorite || state.Rating == nil || state.PositionMs != 90_000 {
		t.Fatalf("favourite and rating: %d %+v", w.Code, state)
	}
	var cleared StateReply
	if call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, map[string]any{"rating": nil}, &cleared); cleared.Rating != nil || !cleared.Favorite {
		t.Fatalf("clearing the rating: %+v", cleared)
	}

	state = StateReply{}
	if w := call(t, s, "PUT", "/v1/items/"+show.ID+"/state", token, map[string]any{"watched": true}, &state); w.Code != 200 || !state.Watched || state.Undo == nil {
		t.Fatalf("marking a show: %d %+v", w.Code, state)
	}
	if listed := get[Item](t, s, token, "/v1/items/"+show.ID); listed.Counts == nil || listed.Counts.Leaves == 0 || listed.Counts.UnwatchedLeaves != 0 {
		t.Fatalf("the show after marking: %+v", listed.Counts)
	}
	if offered, _ := time.Parse(time.RFC3339, state.Undo.ExpiresAt); time.Until(offered) > 31*time.Second {
		t.Fatalf("the undo is offered until %s, more than 30 seconds away", state.Undo.ExpiresAt)
	}
	if w := call(t, s, "POST", "/v1/items/state:undo", limited, UndoRequest{Token: state.Undo.Token}, nil); w.Code != 409 {
		t.Fatalf("another profile's undo answered %d", w.Code)
	}
	if w := call(t, s, "POST", "/v1/items/state:undo", token, UndoRequest{Token: state.Undo.Token}, nil); w.Code != 204 {
		t.Fatalf("undo answered %d: %s", w.Code, w.Body)
	}
	if listed := get[Item](t, s, token, "/v1/items/"+show.ID); listed.Counts.UnwatchedLeaves != listed.Counts.Leaves {
		t.Fatalf("the show after undo: %+v", listed.Counts)
	}
	var refused errorBody
	if w := call(t, s, "POST", "/v1/items/state:undo", token, UndoRequest{Token: state.Undo.Token}, &refused); w.Code != 409 || refused.Error.Code != "undo_expired" {
		t.Fatalf("a second undo: %d %s", w.Code, refused.Error.Code)
	}
	for _, body := range []map[string]any{{"rating": 11}, {"rating": 0}, {"watched": true, "positionMs": 5}, {"madeAgoMs": -1, "favorite": true},
		{"madeAgoMs": 36 * 24 * 3600 * 1000, "favorite": true}} {
		if w := call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, body, &refused); w.Code != 400 {
			t.Errorf("state %v answered %d", body, w.Code)
		}
	}

	// An app that cannot hold a stream asks again and again instead. With no cursor it is told
	// where to start; from there it hears the next change as soon as it is made, and nothing
	// when the wait runs out.
	start := get[EventPage](t, s, token, "/v1/events?poll=true")
	if start.Cursor == "" || len(start.Events) != 0 {
		t.Fatalf("a first poll: %+v", start)
	}
	heard := make(chan EventPage, 1)
	go func() {
		var page EventPage
		call(t, s, "GET", "/v1/events?poll=true&wait=10&cursor="+start.Cursor, token, nil, &page)
		heard <- page
	}()
	time.Sleep(100 * time.Millisecond)
	call(t, s, "PUT", "/v1/items/"+film.ID+"/state", token, map[string]any{"favorite": false}, nil)
	select {
	case page := <-heard:
		if len(page.Events) != 1 || page.Events[0].Topic != "state" || page.Events[0].ID != film.ID || page.Cursor == start.Cursor {
			t.Fatalf("the poll heard %+v", page)
		}
		if quiet := get[EventPage](t, s, token, "/v1/events?poll=true&wait=0&cursor="+page.Cursor); len(quiet.Events) != 0 || quiet.Cursor != page.Cursor {
			t.Fatalf("a poll with nothing new: %+v", quiet)
		}
		if other := get[EventPage](t, s, limited, "/v1/events?poll=true&wait=0&cursor="+start.Cursor); len(other.Events) != 0 {
			t.Fatalf("another profile's poll heard this profile's state: %+v", other.Events)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not answer when something changed")
	}
	if far := get[EventPage](t, s, token, "/v1/events?poll=true&cursor=999999999"); !far.Resync || far.Cursor == "999999999" {
		t.Fatalf("a poll from a cursor this server cannot follow: %+v", far)
	}

	// A stream says when the server ends it, so an app can tell that from a dropped connection.
	s.eventsLifetime = 300 * time.Millisecond
	feed("")("event: end")

	// A reader whose cursor this server cannot follow on from is told to read everything
	// again, with an id to carry on from.
	head, _ := s.db.ChangesHead(ctx)
	lost := feed("?cursor=999999999")
	lost(fmt.Sprintf("id: %d", head))
	lost("event: resync")

	// One sign-in holds at most four feeds: the fifth closes the first.
	for range 3 {
		feed("")("event: ready")
	}
	expect("<closed>")
}
