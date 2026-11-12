package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Two devices have a child's limits open. A save from one names only what it changed, so it
// leaves what the other saved in the meantime: a Tag blocked on the phone is still blocked
// after the TV saves a rating, and the other way round. null lifts the rating and nothing else.
func TestARestrictionsSaveChangesOnlyWhatItNames(t *testing.T) {
	s := newTestServer(t)
	phone := onMain(t, s, setup(t, s)).AccessToken
	tv := onMain(t, s, signIn(t, s, 1)).AccessToken
	ctx := context.Background()
	if _, err := seed.Import(ctx, s.db, seed.Config{Seed: 3, Items: 600}, nil); err != nil {
		t.Fatal(err)
	}
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) < 2 {
		t.Fatalf("the seeded library has %d labels: %v", len(labels), err)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", phone, ProfileCreate{Name: "Kid"}, &kid)
	address := "/v1/profiles/" + kid.ID + "/restrictions"
	save := func(token, body string) Restrictions {
		t.Helper()
		var out Restrictions
		if w := call(t, s, "PUT", address, token, json.RawMessage(body), &out); w.Code != 200 {
			t.Fatalf("saving %s answered %d: %s", body, w.Code, w.Body)
		}
		return out
	}
	holds := func(got Restrictions, what string, age int, unrated bool, blocked ...string) {
		t.Helper()
		ok := (age < 0) == (got.MaxRatingAge == nil) && (age < 0 || *got.MaxRatingAge == age) && got.AllowUnrated == unrated && len(got.BlockedLabels) == len(blocked)
		for n := range blocked {
			ok = ok && n < len(got.BlockedLabels) && got.BlockedLabels[n] == blocked[n]
		}
		if !ok {
			t.Errorf("%s: the limits are age %v, unrated %v, blocked %v", what, got.MaxRatingAge, got.AllowUnrated, got.BlockedLabels)
		}
	}
	one, two := labels[0].PublicID, labels[1].PublicID

	holds(save(phone, `{"blockedLabels":["`+one+`"]}`), "the phone blocks a Tag", -1, false, one)
	holds(save(tv, `{"maxRatingAge":13}`), "the TV, whose sheet is older, sets a rating", 13, false, one)
	holds(save(phone, `{"allowUnrated":true}`), "the phone allows unrated titles", 13, true, one)
	holds(save(tv, `{"maxRatingAge":null}`), "the TV lifts the rating", -1, true, one)
	holds(save(tv, `{"blockedLabels":["`+two+`"],"maxRatingAge":8}`), "the TV replaces the list of Tags and sets a rating", 8, true, two)
	// A library choice saved on one device is not undone by a save of something else.
	libraries := get[Libraries](t, s, phone, "/v1/libraries").Items
	if got := save(phone, `{"allLibraries":false,"libraries":["`+libraries[0].ID+`"]}`); got.AllLibraries || len(got.Libraries) != 1 {
		t.Fatalf("choosing one library read back as %+v", got)
	}
	if got := save(tv, `{"allowUnrated":false}`); got.AllLibraries || len(got.Libraries) != 1 || got.MaxRatingAge == nil || len(got.BlockedLabels) != 1 {
		t.Errorf("a save of one switch changed the rest: %+v", got)
	}
	holds(get[Restrictions](t, s, phone, address), "read back on the phone", 8, false, two)
	// A save that sends everything still replaces everything.
	holds(save(phone, `{"maxRatingAge":null,"allowUnrated":false,"blockedLabels":[],"allLibraries":true,"libraries":[]}`), "a whole save", -1, false)

	for name, bad := range map[string]string{"an age of 99": `{"maxRatingAge":99}`, "a Tag nobody has": `{"blockedLabels":["tag_nothing"]}`} {
		if code := status(t, s, "PUT", address, phone, json.RawMessage(bad)); code != 400 {
			t.Errorf("%s answered %d, want 400", name, code)
		}
	}
}

// A save that names nothing, or names only what is already so,
// changes nothing: the profile's access marker, which every open app compares to decide
// whether to throw away what it has cached, stays as it was.
func TestARestrictionsSaveThatChangesNothingMovesNoMarker(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	address := "/v1/profiles/" + kid.ID + "/restrictions"
	marker := func() string {
		t.Helper()
		return get[Profile](t, s, owner, "/v1/profiles/"+kid.ID).AccessMarker
	}
	if code := status(t, s, "PUT", address, owner, json.RawMessage(`{"maxRatingAge":13}`)); code != 200 {
		t.Fatalf("setting a rating answered %d", code)
	}
	before := marker()
	clock := time.Now().Add(time.Minute)
	s.setClock(func() time.Time { return clock })
	for _, body := range []string{`{}`, `{"maxRatingAge":13}`, `{"allowUnrated":null}`} {
		if code := status(t, s, "PUT", address, owner, json.RawMessage(body)); code != 200 {
			t.Fatalf("saving %s answered %d", body, code)
		}
		if after := marker(); after != before {
			t.Errorf("a save of %s, which changes nothing, moved the profile's access marker from %s to %s", body, before, after)
		}
	}
}
