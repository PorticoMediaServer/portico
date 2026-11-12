package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A guide that gives its programmes no id of their own (the XMLTV form most feeds use: the
// key is the channel and the start) still moves a booking when it shifts the programme by a
// few minutes: one programme of the same title now covers most of the booked time.
func TestReviewABookingFollowsAProgrammeWhoseKeyIsItsStart(t *testing.T) {
	s, owner, _ := recordable(t)
	var rec DvrRecording
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Night Film")}, &rec)
	ctx := context.Background()
	stored, _ := s.db.Recording(ctx, rec.ID)
	if strings.HasPrefix(stored.ProgrammeKey, "id:") {
		t.Fatalf("the fixture guide was expected to give no programme ids, key %q", stored.ProgrammeKey)
	}
	source, key, err := s.db.ChannelListings(ctx, stored.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	moved := stored.StartsAt.Add(5 * time.Minute)
	// What the next refresh of that guide writes: the key follows the new start.
	newKey := fmt.Sprintf("%s@%d", key, moved.Unix())
	if err := s.db.ReplaceSourceGuide(ctx, source, func(add func(db.GuideProgramme) error) error {
		return add(db.GuideProgramme{GuideKey: key, StartsAt: moved, EndsAt: stored.EndsAt.Add(5 * time.Minute), ProgrammeKey: newKey, Title: "Night Film"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.recorder.FollowRules(ctx); err != nil {
		t.Fatal(err)
	}
	if got := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID); !got.InGuide || !got.StartsAt.Equal(moved.UTC()) {
		t.Fatalf("a programme moved five minutes leaves its booking reading %+v (in guide %v)", got.StartsAt, got.InGuide)
	}
}

// When a guide gives one id to more than one airing (an episode id repeated at each rerun),
// a booking stays on the airing it was made for: it never moves to another airing of that
// id, and never to one that is already over.
func TestReviewABookingNeverFollowsToAnotherAiringOfTheSameKey(t *testing.T) {
	s, owner, _ := recordable(t)
	var rec DvrRecording
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Night Film")}, &rec)
	ctx := context.Background()
	stored, _ := s.db.Recording(ctx, rec.ID)
	source, key, _ := s.db.ChannelListings(ctx, stored.ChannelID)
	length := stored.EndsAt.Sub(stored.StartsAt)
	earlier := s.now().Add(-3 * time.Hour) // an earlier airing still held in the guide
	if err := s.db.ReplaceSourceGuide(ctx, source, func(add func(db.GuideProgramme) error) error {
		if err := add(db.GuideProgramme{GuideKey: key, StartsAt: earlier, EndsAt: earlier.Add(length), ProgrammeKey: stored.ProgrammeKey, Title: "Night Film"}); err != nil {
			return err
		}
		return add(db.GuideProgramme{GuideKey: key, StartsAt: stored.StartsAt, EndsAt: stored.EndsAt, ProgrammeKey: stored.ProgrammeKey, Title: "Night Film"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.recorder.FollowRules(ctx); err != nil {
		t.Fatal(err)
	}
	if got := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID); !got.StartsAt.Equal(stored.StartsAt.UTC()) {
		t.Fatalf("the booking for %v moved to %v, another airing of the same key", stored.StartsAt.UTC(), got.StartsAt)
	}
}

// A series rule set to keep every episode replaces the owner's default keep rule (definition
// rule 54): the default never deletes that rule's recordings.
func TestReviewTheDefaultKeepRuleLeavesARuleThatKeepsEveryEpisode(t *testing.T) {
	s, owner, _ := recordable(t)
	ctx := context.Background()
	var rule DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules", owner, DvrRuleSave{Programme: programmeID(t, s, owner, "Night Film")}, &rule); w.Code != 201 {
		t.Fatalf("making a rule answered %d: %s", w.Code, w.Body)
	}
	if rule.KeepNewest != nil || rule.KeepDays != nil {
		t.Fatalf("a rule made with no keep choice reads %+v", rule)
	}
	rules, err := s.db.Rules(ctx, 0)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules %v, %v", rules, err)
	}
	channels := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	ch, err := s.db.LiveChannelByID(ctx, viewerOfToken(t, s, owner), channels[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	day := s.now().Add(-72 * time.Hour)
	var ids []int64
	for n := range 3 {
		r, _, err := s.db.BookAiring(ctx, db.Booking{ProfileID: 1, ProfileName: "Justin", ProfilePub: "prf", RuleID: rules[0].ID, ChannelID: ch.ID,
			ChannelName: ch.Name, ProgrammeKey: fmt.Sprint("ep-", n), Title: "Night Film", StartsAt: day.Add(time.Duration(n) * time.Hour),
			EndsAt: day.Add(time.Duration(n)*time.Hour + 30*time.Minute), Now: s.now()})
		if err != nil {
			t.Fatal(err)
		}
		s.db.RecordingFinished(ctx, r.ID, "recorded", "", s.now())
		ids = append(ids, r.ID)
	}
	options := s.dvrOptions()
	options.KeepNewest = 1
	s.setDVROptions(options)
	if err := s.recorder.Keep(ctx); err != nil {
		t.Fatal(err)
	}
	for n, id := range ids {
		if recordingPublic(t, s, id) == "rec_gone" {
			t.Errorf("episode %d of a rule that keeps every episode was deleted by the owner's default", n)
		}
	}
}

// A profile the owner keeps out of the Recorded TV library does not read everyone's bookings
// and recordings (definition rule 48: Everyone's is for those who may see the library).
func TestReviewAProfileWithoutRecordedTVSeesNoOneElsesBookings(t *testing.T) {
	s, owner, _ := recordable(t)
	var news DvrRecording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Morning News")}, &news); w.Code != 200 {
		t.Fatalf("booking answered %d: %s", w.Code, w.Body)
	}
	films := libraryOfKind(t, s, owner, "movies")
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Films only"}, &kid)
	if code := status(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{Libraries: []string{films}}); code != 200 {
		t.Fatalf("restricting the profile answered %d", code)
	}
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	w := call(t, s, "GET", "/v1/dvr/recordings?scope=everyone", onKid.AccessToken, nil, nil)
	if w.Code == 200 && strings.Contains(w.Body.String(), "Morning News") {
		t.Fatalf("a profile kept out of Recorded TV reads everyone's bookings: %s", w.Body)
	}
	if code := status(t, s, "GET", "/v1/dvr/recordings/"+news.ID, onKid.AccessToken, nil); code == 200 {
		t.Fatalf("a profile kept out of Recorded TV reads a booking by its id")
	}
	t.Logf("everyone's list answered %d", w.Code)
}
