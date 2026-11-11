package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func limitedKid(t *testing.T, s *Server, owner string) (Profile, string) {
	t.Helper()
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	age := 13
	if code := status(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{MaxRatingAge: &age, AllLibraries: true}); code != 200 {
		t.Fatalf("limiting the kid answered %d", code)
	}
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	return kid, onKid.AccessToken
}

// A profile held to 13 never learns of a TV-MA booking someone else made: not in everyone's
// upcoming list, not by its id.
func TestALimitedProfileNeverSeesAnotherPersonsHiddenBooking(t *testing.T) {
	s, owner, _ := recordable(t)
	_, kid := limitedKid(t, s, owner)
	var grim Recording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "A Grim Film")}, &grim); w.Code != 200 {
		t.Fatalf("booking answered %d: %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/dvr/recordings?scope=everyone", "/v1/dvr/recordings?scope=everyone&state=history"} {
		w := call(t, s, "GET", path, kid, nil, nil)
		if strings.Contains(w.Body.String(), "A Grim Film") {
			t.Fatalf("%s showed a 13 profile a TV-MA booking: %s", path, w.Body)
		}
	}
	if code := status(t, s, "GET", "/v1/dvr/recordings/"+grim.ID, kid, nil); code != 404 {
		t.Fatalf("a TV-MA booking read by its id answered %d to a 13 profile", code)
	}
}

// Deleting a series rule with its bookings passes a booking someone else also asked for on to
// them, as cancelling it would.
func TestDeletingARuleWithItsBookingsPassesOnOnesOthersWanted(t *testing.T) {
	s, owner, _ := recordable(t)
	kid := signIn(t, s, 1).Session.AccessToken
	var other Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Sam"}, &other)
	var onOther Session
	call(t, s, "POST", "/v1/profiles/"+other.ID+"/select", kid, ProfileSelect{}, &onOther)
	night := programmeID(t, s, owner, "Night Film")
	var rule Rule
	if w := call(t, s, "POST", "/v1/dvr/rules", owner, RuleSave{Programme: night}, &rule); w.Code != 201 {
		t.Fatalf("making a rule answered %d: %s", w.Code, w.Body)
	}
	var booked Recording
	call(t, s, "POST", "/v1/dvr/recordings", onOther.AccessToken, RecordingBook{Programme: night}, &booked)
	if code := status(t, s, "DELETE", "/v1/dvr/rules/"+rule.ID+"?bookings=cancel", owner, nil); code != 204 {
		t.Fatalf("deleting the rule answered %d", code)
	}
	if got := get[Recording](t, s, onOther.AccessToken, "/v1/dvr/recordings/"+booked.ID); got.State != "scheduled" || got.BookedBy != "Sam" {
		t.Fatalf("a booking Sam also asked for reads %+v after the rule's owner deleted the rule", got)
	}
}

// A recording stops before the recordings folder's disk falls below the floor, not only when
// it starts: a three-hour recording must not fill the disk the database is on.
func TestARecordingStopsWhenSpaceRunsOutWhileItRecords(t *testing.T) {
	s, owner, _ := recordable(t)
	options := s.dvrOptions()
	var rec Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Morning News")}, &rec)
	s.recorder.Tick(context.Background())
	deadline := time.Now().Add(10 * time.Second)
	for get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID).State != "recording" && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	options.Floor = 1 << 62 // the disk is now below the floor
	s.setDVROptions(options)
	deadline = time.Now().Add(15 * time.Second)
	for {
		got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID)
		if got.State != "recording" {
			if got.Reason != "Space ran out" {
				t.Fatalf("the recording ended as %+v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			status(t, s, "DELETE", "/v1/dvr/recordings/"+rec.ID+"?action=stop", owner, nil)
			t.Fatal("a recording kept writing for 15 seconds with the disk below the floor")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// With two tuners, a series rule's airing that overlaps two back-to-back single bookings on
// one other channel fits: that channel never uses more than one tuner at a time.
func TestBackToBackBookingsOnOneChannelUseOneTuner(t *testing.T) {
	s, owner, _ := recordable(t)
	var news, film Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Late News")}, &news)
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Night Film")}, &film)
	ctx := context.Background()
	n, _ := s.db.Recording(ctx, news.ID)
	f, _ := s.db.Recording(ctx, film.ID)
	at := s.now().Add(24 * time.Hour).Truncate(time.Hour)
	list := []db.Recording{
		{ID: 1001, ChannelID: n.ChannelID, StartsAt: at, EndsAt: at.Add(time.Hour)},                                         // 8 to 9 on news
		{ID: 1002, ChannelID: n.ChannelID, StartsAt: at.Add(time.Hour), EndsAt: at.Add(2 * time.Hour)},                      // 9 to 10 on news
		{ID: 1003, RuleID: 7, ChannelID: f.ChannelID, StartsAt: at.Add(30 * time.Minute), EndsAt: at.Add(90 * time.Minute)}, // 8:30 to 9:30 on film
	}
	losers, err := s.recorder.Clashes(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(losers) != 0 {
		t.Fatalf("two tuners, at most two channels at once, yet these lose a tuner: %v", losers)
	}
}
