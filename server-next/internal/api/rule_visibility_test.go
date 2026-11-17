package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"slices"
	"testing"
	"time"
)

func TestPersonalRulesFollowCurrentProfileLimits(t *testing.T) {
	s, owner, _ := recordable(t)
	id := programmeID(t, s, owner, "A Grim Film")
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &kid)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 5).Session.AccessToken, ProfileSelect{}, &selected)
	token := selected.AccessToken
	var rule DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules", token, DvrRuleSave{Programme: id}, &rule); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: new(13)}, nil)
	if got := get[Page[DvrRule]](t, s, token, "/v1/dvr/rules"); len(got.Items) != 0 {
		t.Errorf("hidden rule remains: %+v", got.Items)
	}
	if got := get[DvrOverview](t, s, token, "/v1/dvr"); got.Rules != 0 {
		t.Errorf("hidden rule count: %d", got.Rules)
	}
	if got := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules?scope=everyone"); len(got.Items) != 1 || got.Items[0].ID != rule.ID {
		t.Fatal("management lost rule", got)
	}
	if code := status(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID+"?preview=true", token, DvrRuleSave{}); code != 404 {
		t.Errorf("hidden preview: %d", code)
	}
	if code := status(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, token, DvrRuleSave{Paused: new(true)}); code != 404 {
		t.Errorf("hidden change: %d", code)
	}
	if raw, err := s.db.Rule(context.Background(), rule.ID); err != nil || raw.Paused {
		t.Errorf("hidden change mutated rule: %+v %v", raw, err)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/rules/"+rule.ID, token, nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := s.db.Rule(context.Background(), rule.ID); err != nil {
		t.Error("hidden delete mutated rule", err)
	}
}

func TestPersonalRulesUseCurrentAiringsThenSavedRatingFallback(t *testing.T) {
	s, owner, _ := recordable(t)
	ctx := context.Background()
	var adult, unknown DvrRule
	for title, out := range map[string]*DvrRule{"A Grim Film": &adult, "Late News": &unknown} {
		if w := call(t, s, "POST", "/v1/dvr/rules", owner, DvrRuleSave{Programme: programmeID(t, s, owner, title)}, out); w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
	}
	stored, err := s.db.Rule(ctx, adult.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RatingAge == nil {
		t.Fatal("creating rating was not saved")
	}
	source, key, err := s.db.ChannelListings(ctx, stored.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	main := get[Page[Profile]](t, s, owner, "/v1/profiles").Items[0].ID
	caller := CallerOf(t, s, owner)
	limit := func(age *int) {
		t.Helper()
		_, err := s.db.SetProfileRestrictions(ctx, caller.UserID, main, db.Restrictions{AllLibraries: true, MaxRatingAge: age, AllowUnrated: true}, s.now())
		if err != nil {
			t.Fatal(err)
		}
	}

	expect := func(want ...string) {
		t.Helper()
		var ids []string
		for _, rule := range get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules").Items {
			ids = append(ids, rule.ID)
		}
		slices.Sort(ids)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("personal rules: %v want %v", ids, want)
		}
		if got := get[DvrOverview](t, s, owner, "/v1/dvr"); got.Rules != len(want) {
			t.Errorf("personal count: %d want %d", got.Rules, len(want))
		}
		if got := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules?scope=everyone"); len(got.Items) != 2 {
			t.Errorf("management lost rules: %+v", got.Items)
		}
		if got := get[DvrOverview](t, s, owner, "/v1/dvr?scope=everyone"); got.Rules != 2 {
			t.Errorf("management count changed: %d", got.Rules)
		}
	}
	replace := func(ages ...*int) {
		t.Helper()
		if err := s.db.ReplaceSourceGuide(ctx, source, func(add func(db.GuideProgramme) error) error {
			for n, age := range ages {
				at := s.now().Add(time.Duration(n) * time.Hour)
				if err := add(db.GuideProgramme{GuideKey: key, ProgrammeKey: db.NewID("test"), Title: "A Grim Film", StartsAt: at, EndsAt: at.Add(time.Hour), RatingAge: age}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	limit(new(13))
	expect(unknown.ID) // unrated current guide entries are allowed
	replace(new(18), new(6))
	expect(adult.ID) // any current permitted airing makes the rule visible
	replace(nil)
	expect(adult.ID) // current unrated is permitted despite the saved adult rating
	replace(new(18))
	expect() // known current entries are all above the limit
	replace()
	expect() // saved adult and unknown ratings are both hidden at13
	limit(new(18))
	expect(adult.ID) // known saved rating survives an empty guide
	limit(nil)
	expect(adult.ID, unknown.ID) // an unrestricted profile may see an unknown fallback
}

func TestRuleDoesNotUseSavedRatingWhileCurrentAiringIsHidden(t *testing.T) {
	s, owner, _ := recordable(t)
	ctx := context.Background()
	var rule DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules", owner, DvrRuleSave{Programme: programmeID(t, s, owner, "Morning News")}, &rule); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	stored, err := s.db.Rule(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, key, err := s.db.ChannelListings(ctx, stored.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	main := get[Page[Profile]](t, s, owner, "/v1/profiles").Items[0].ID
	if code := status(t, s, "PUT", "/v1/profiles/"+main+"/restrictions", owner, map[string]any{"maxRatingAge": 13}); code != 200 {
		t.Fatal(code)
	}
	if err := s.db.ReplaceSourceGuide(ctx, source, func(add func(db.GuideProgramme) error) error {
		return add(db.GuideProgramme{GuideKey: key, ProgrammeKey: "changed-current-rating", Title: "Morning News", StartsAt: s.now().Add(-time.Minute), EndsAt: s.now().Add(time.Hour), RatingAge: new(18)})
	}); err != nil {
		t.Fatal(err)
	}
	if got := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules"); len(got.Items) != 0 {
		t.Errorf("saved child rating bypassed current adult airing: %+v", got.Items)
	}
	if err := s.db.ReplaceSourceGuide(ctx, source, func(func(db.GuideProgramme) error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules"); len(got.Items) != 1 || got.Items[0].ID != rule.ID {
		t.Errorf("known age0 fallback missing: %+v", got.Items)
	}
}
