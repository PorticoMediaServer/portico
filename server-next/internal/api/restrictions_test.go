package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// A parent limits a child's profile by rating, by library and by label, the child is shown
// less at once, and the child's own sign-in can neither read nor lift the limits.
func TestAProfilesRestrictionsAreSetFromTheMainProfile(t *testing.T) {
	s := newTestServer(t)
	parent := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	if _, err := seed.Import(ctx, s.db, seed.Config{Seed: 3, Items: 600}, nil); err != nil {
		t.Fatal(err)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", parent, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	kidSignIn := signIn(t, s, 1)
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", kidSignIn.Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid's profile answered %d", w.Code)
	}
	libraries := get[Libraries](t, s, parent, "/v1/libraries").Items
	films := "/v1/items?library=" + libraries[0].ID + "&kind=movie&total=true"
	all := *get[ItemPage](t, s, onKid.AccessToken, films).Total
	address := "/v1/profiles/" + kid.ID + "/restrictions"
	if got := get[Restrictions](t, s, parent, address); got.MaxRatingAge != nil || !got.AllLibraries || len(got.BlockedLabels) != 0 {
		t.Fatalf("a new profile starts restricted: %+v", got)
	}

	age := 13
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) == 0 {
		t.Fatalf("the seeded library has no labels: %v", err)
	}
	var saved Restrictions
	limits := Restrictions{MaxRatingAge: &age, BlockedLabels: []string{labels[0].PublicID}, Libraries: []string{libraries[0].ID}}
	if w := call(t, s, "PUT", address, parent, limits, &saved); w.Code != 200 || saved.MaxRatingAge == nil || *saved.MaxRatingAge != 13 ||
		len(saved.BlockedLabels) != 1 || saved.AllLibraries || len(saved.Libraries) != 1 || !saved.AccountAllLibraries {
		t.Fatalf("setting limits answered %d %+v", w.Code, saved)
	}
	// The kid's token from before the change is refused, so a title it was playing is
	// checked again; its app renews without signing in and is shown less.
	if code := status(t, s, "GET", "/v1/libraries", onKid.AccessToken, nil); code != 401 {
		t.Fatalf("the kid's token from before the limits answered %d, want 401", code)
	}
	refresh := kidSignIn.Session.RefreshToken
	renewKid := func(id string) {
		t.Helper()
		code, next := renew(t, s, refresh, 1, id)
		if code != 200 {
			t.Fatalf("the kid's app renewing after a change of limits answered %d", code)
		}
		onKid, refresh = next, next.RefreshToken
	}
	renewKid("after-limits")
	if seen := get[Libraries](t, s, onKid.AccessToken, "/v1/libraries").Items; len(seen) != 1 || seen[0].ID != libraries[0].ID {
		t.Fatalf("the kid is shown %d libraries, want only the films", len(seen))
	}
	if fewer := *get[ItemPage](t, s, onKid.AccessToken, films).Total; fewer == 0 || fewer >= all {
		t.Fatalf("the kid is shown %d films of %d under a limit of 13", fewer, all)
	}
	if code := status(t, s, "GET", "/v1/items?library="+libraries[1].ID, onKid.AccessToken, nil); code != 200 {
		t.Fatalf("a library the kid may not see answered %d, want an empty list", code)
	} else if page := get[ItemPage](t, s, onKid.AccessToken, "/v1/items?library="+libraries[1].ID); len(page.Items) != 0 {
		t.Fatalf("the kid was shown %d items of a library it is not given", len(page.Items))
	}

	for _, c := range []struct {
		method, token string
		body          any
		want          int
	}{
		{"GET", onKid.AccessToken, nil, 403},
		{"PUT", onKid.AccessToken, Restrictions{AllLibraries: true}, 403},
		{"PUT", parent, map[string]any{"maxRatingAge": 99, "allLibraries": true}, 400},
		{"PUT", parent, Restrictions{AllLibraries: true, BlockedLabels: []string{"tag_nothing"}}, 400},
		{"PUT", parent, Restrictions{Libraries: []string{"lib_nothing"}}, 400},
	} {
		if code := status(t, s, c.method, address, c.token, c.body); code != c.want {
			t.Errorf("%s %s with %+v answered %d, want %d", c.method, address, c.body, code, c.want)
		}
	}
	if code := status(t, s, "GET", "/v1/profiles/prf_someoneelses/restrictions", parent, nil); code != 404 {
		t.Errorf("another account's profile answered %d, want 404", code)
	}
	// A refused change changed nothing, and lifting the limits shows everything again.
	if kept := get[Restrictions](t, s, parent, address); kept.MaxRatingAge == nil || len(kept.BlockedLabels) != 1 || len(kept.Libraries) != 1 {
		t.Fatalf("a refused change altered the limits: %+v", kept)
	}
	if code := status(t, s, "PUT", address, parent, Restrictions{AllLibraries: true}); code != 200 {
		t.Fatalf("lifting the limits answered %d", code)
	}
	renewKid("after-lifting")
	if again := *get[ItemPage](t, s, onKid.AccessToken, films).Total; again != all {
		t.Fatalf("with the limits lifted the kid is shown %d films of %d", again, all)
	}
}
