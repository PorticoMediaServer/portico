package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestChangingAnExistingRatingLimitPersistsTheNewAge(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &kid)
	for _, age := range []int{18, 13} {
		var reported Restrictions
		if w := call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": age}, &reported); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		stored := get[Restrictions](t, s, owner, "/v1/profiles/"+kid.ID+"/restrictions")
		if stored.MaxRatingAge == nil || reported.MaxRatingAge == nil {
			t.Fatal("rating disappeared")
		}
		if *stored.MaxRatingAge != age {
			t.Fatalf("save reported %d but reread %d; wanted %d", *reported.MaxRatingAge, *stored.MaxRatingAge, age)
		}
	}
}

func TestReplacingExistingRestrictionListsPersistsTheNewValues(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	libs := get[Libraries](t, s, owner, "/v1/libraries").Items
	tags, err := s.db.Tags(context.Background(), db.TagLabel)
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) < 2 || len(tags) < 2 {
		t.Fatal("fixture needs two libraries and Tags")
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &kid)
	path := "/v1/profiles/" + kid.ID + "/restrictions"
	for field, values := range map[string][]string{"blockedLabels": {tags[0].PublicID, tags[1].PublicID}, "libraries": {libs[0].ID, libs[1].ID}} {
		t.Run(field, func(t *testing.T) {
			first := map[string]any{field: []string{values[0]}}
			if field == "libraries" {
				first["allLibraries"] = false
			}
			if code := status(t, s, "PUT", path, owner, first); code != 200 {
				t.Fatal(code)
			}
			if code := status(t, s, "PUT", path, owner, map[string]any{field: []string{values[1]}}); code != 200 {
				t.Fatal(code)
			}
			got := get[Restrictions](t, s, owner, path)
			stored := got.BlockedLabels
			if field == "libraries" {
				stored = got.Libraries
			}
			if len(stored) != 1 || stored[0] != values[1] {
				t.Errorf("replacement was acknowledged but stored %v; wanted %s", stored, values[1])
			}
		})
	}
}
