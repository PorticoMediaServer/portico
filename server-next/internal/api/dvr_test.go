package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/dvr"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// recordable is a server with a source whose channels stream from the fake provider, a guide
// for them, and recordings written into a folder of the test's own.
func recordable(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, owner, _ := seededWith(t, 50, 0)
	folder := t.TempDir()
	options := dvr.Defaults(folder)
	options.Folder, options.Floor = filepath.Join(folder, "Recorded TV"), 0
	s.setDVROptions(options)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2),
		Config: &live.SourceConfig{PlaylistURL: fake.URL + "/recordable.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	return s, owner, options.Folder
}

func programmeID(t *testing.T, s *Server, token, title string) string {
	t.Helper()
	stretch := "/v1/live/guide?from=" + s.now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + s.now().Add(6*time.Hour).UTC().Format(time.RFC3339)
	for _, row := range get[Guide](t, s, token, stretch).Items {
		for _, p := range row.Programmes {
			if p.Title == title {
				return p.ID
			}
		}
	}
	t.Fatalf("no %q in the guide", title)
	return ""
}

// One airing is booked once however many ask; when the booker cancels or their profile is
// deleted, the booking passes to the next person who asked, and it is cancelled only when
// nobody is left.
func TestABookingPassesToTheNextPersonWhoAsked(t *testing.T) {
	s, owner, _ := recordable(t)
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid answered %d", w.Code)
	}
	late := programmeID(t, s, owner, "Late News")
	var first, second Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: late}, &first)
	call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, RecordingBook{Programme: late}, &second)
	if first.ID == "" || second.ID != first.ID || first.State != "scheduled" {
		t.Fatalf("two bookings of one airing: %+v and %+v", first, second)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+first.ID, owner, nil); code != 204 {
		t.Fatalf("cancelling answered %d", code)
	}
	if got := get[Recording](t, s, onKid.AccessToken, "/v1/dvr/recordings/"+first.ID); got.State != "scheduled" || got.BookedBy != "Kid" {
		t.Fatalf("after the booker cancelled the booking reads %+v", got)
	}

	// The kid's booking, which the owner's profile also asked for, passes on when the kid's
	// profile is deleted; one nobody else asked for is cancelled.
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: late}, nil)
	var alone Recording
	call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, RecordingBook{Programme: programmeID(t, s, owner, "Night Film")}, &alone)
	if code := status(t, s, "DELETE", "/v1/profiles/"+kid.ID, owner, nil); code != 204 {
		t.Fatalf("deleting the kid's profile answered %d", code)
	}
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+first.ID); got.State != "scheduled" || got.BookedBy == "Kid" {
		t.Fatalf("after the booker's profile was deleted the booking reads %+v", got)
	}
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+alone.ID); got.State != "cancelled" {
		t.Fatalf("a booking nobody else wanted reads %+v after its profile was deleted", got)
	}
}

// The programme on now is recorded into the Recorded TV library's folder, laid out for other
// tools with the guide's details beside it, and becomes a title; stopped by hand it is kept
// as Partly recorded. A booking whose whole time passed while the server was off goes to
// History saying so.
func TestAProgrammeIsRecordedIntoTheRecordedTVLibrary(t *testing.T) {
	s, owner, folder := recordable(t)
	var rec Recording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Morning News")}, &rec); w.Code != 200 {
		t.Fatalf("booking answered %d: %s", w.Code, w.Body)
	}
	s.recorder.Tick(context.Background())
	deadline := time.Now().Add(15 * time.Second)
	for get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID).ItemID == "" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+rec.ID+"?action=stop", owner, nil); code != 204 {
		t.Fatalf("stopping answered %d", code)
	}
	deadline = time.Now().Add(10 * time.Second)
	for get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID).State == "recording" {
		if time.Now().After(deadline) {
			t.Fatal("the recording did not stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID)
	if got.State != "partial" || got.Reason != "Stopped by hand" || got.ItemID == "" {
		t.Fatalf("the stopped recording reads %+v", got)
	}
	matches, _ := filepath.Glob(filepath.Join(folder, "Morning News", "Morning News - *.ts"))
	if len(matches) != 1 {
		t.Fatalf("recorded files: %v", matches)
	}
	if info, err := os.Stat(matches[0]); err != nil || info.Size() < 188 {
		t.Fatalf("the recording's file: %v %v", info, err)
	}
	if nfo, err := os.ReadFile(strings.TrimSuffix(matches[0], ".ts") + ".nfo"); err != nil || !strings.Contains(string(nfo), "Morning News") {
		t.Fatalf("the guide's details beside it: %s %v", nfo, err)
	}
	title := get[Item](t, s, owner, "/v1/items/"+got.ItemID)
	if title.ContentRating != "TV-G" {
		t.Fatalf("the recorded title carries rating %q, want the guide's TV-G", title.ContentRating)
	}
	if title.RootID != "" {
		if show := get[Item](t, s, owner, "/v1/items/"+title.RootID); show.ContentRating != "TV-G" {
			t.Fatalf("the recorded show carries rating %q, want the guide's TV-G", show.ContentRating)
		}
	}

	var later Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Late News")}, &later)
	claims, err := s.tokens.CheckAccess(owner, s.now())
	if err != nil {
		t.Fatal(err)
	}
	s.setClock(func() time.Time { return time.Now().Add(6 * time.Hour) })
	owner, _ = s.tokens.Access(claims, s.now(), s.now().Add(time.Hour))
	s.recorder.Resume(context.Background())
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+later.ID); got.State != "failed" || got.Reason != "The server was off" {
		t.Fatalf("a booking whose time passed while the server was off reads %+v", got)
	}
}

// A reminder is kept for the profile and listed until its programme ends; a programme's page
// says whether it is booked or has a reminder, and one above the profile's rating limit says
// only that it is not available and takes no reminder and no booking.
func TestRemindersAndAProgrammesPage(t *testing.T) {
	s, owner, _ := recordable(t)
	late := programmeID(t, s, owner, "Late News")
	if code := status(t, s, "PUT", "/v1/live/reminders/"+late, owner, nil); code != 204 {
		t.Fatalf("setting a reminder answered %d", code)
	}
	if got := get[Page[ReminderOut]](t, s, owner, "/v1/live/reminders").Items; len(got) != 1 || got[0].Title != "Late News" {
		t.Fatalf("reminders: %+v", got)
	}
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: late}, nil)
	page := get[ProgrammeDetail](t, s, owner, "/v1/live/programmes/"+late)
	if page.Title != "Late News" || !page.Reminder || page.Recording == nil || !page.CanRecord {
		t.Fatalf("the programme's page reads %+v", page)
	}
	status(t, s, "DELETE", "/v1/live/reminders/"+late, owner, nil)
	if got := get[Page[ReminderOut]](t, s, owner, "/v1/live/reminders").Items; len(got) != 0 {
		t.Fatalf("after removing it, reminders: %+v", got)
	}

	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	age := 13
	if code := status(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{MaxRatingAge: &age, AllLibraries: true}); code != 200 {
		t.Fatalf("limiting the kid answered %d", code)
	}
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	grim := programmeID(t, s, owner, "A Grim Film")
	if p := get[ProgrammeDetail](t, s, onKid.AccessToken, "/v1/live/programmes/"+grim); !p.NotAvailable || p.Title != "" || p.CanRecord {
		t.Fatalf("a TV-MA programme's page for a 13 profile reads %+v", p)
	}
	if code := status(t, s, "PUT", "/v1/live/reminders/"+grim, onKid.AccessToken, nil); code != 404 {
		t.Fatalf("a reminder on a hidden programme answered %d", code)
	}
	if code := status(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, RecordingBook{Programme: grim}); code != 404 {
		t.Fatalf("booking a hidden programme answered %d", code)
	}
}

// A series rule made from a programme the guide gives no series id follows the same title on
// the same channel and books its airings; deleted with its bookings, they are cancelled.
func TestASeriesRuleBooksItsAirings(t *testing.T) {
	s, owner, _ := recordable(t)
	var rule Rule
	if w := call(t, s, "POST", "/v1/dvr/rules", owner, RuleSave{Programme: programmeID(t, s, owner, "Night Film")}, &rule); w.Code != 201 || !rule.ByTitle {
		t.Fatalf("making a rule answered %d: %s", w.Code, w.Body)
	}
	upcoming := get[Page[Recording]](t, s, owner, "/v1/dvr/recordings").Items
	if len(upcoming) != 1 || upcoming[0].Title != "Night Film" || !upcoming[0].FromRule {
		t.Fatalf("the rule booked %+v", upcoming)
	}
	var again Rule
	call(t, s, "POST", "/v1/dvr/rules", owner, RuleSave{Programme: programmeID(t, s, owner, "Night Film")}, &again)
	if again.ID != rule.ID {
		t.Fatal("the same rule asked again made a second one")
	}
	if code := status(t, s, "DELETE", "/v1/dvr/rules/"+rule.ID+"?bookings=cancel", owner, nil); code != 204 {
		t.Fatalf("deleting the rule answered %d", code)
	}
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+upcoming[0].ID); got.State != "cancelled" {
		t.Fatalf("its booking reads %+v", got)
	}
}

// With every tuner busy, a viewer keeps watching by default; when the owner chose to stop
// their stream and record, the viewer's session shows a countdown with the reason, then its
// stream ends and the recording takes the tuner.
func TestAClashStopsAViewerAfterACountdownWhenTheOwnerChoseTo(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	folder := t.TempDir()
	options := dvr.Defaults(folder)
	// A one-second countdown on the real clock: the recorder's own loop may tick at any moment,
	// and a countdown of nothing would let that tick stop the viewer before the warning is read.
	options.Folder, options.Floor, options.Countdown, options.CountdownFloor = filepath.Join(folder, "Recorded TV"), 0, time.Second, time.Second
	s.setDVROptions(options)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("One tuner"), TunerCount: new(1),
		Config: &live.SourceConfig{PlaylistURL: fake.URL + "/recordable.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	channels := get[Page[Channel]](t, s, owner, "/v1/live/channels").Items
	var watching PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[1].ID}, &watching); w.Code != 201 {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	var rec Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Morning News")}, &rec)
	ctx := context.Background()
	s.recorder.Tick(ctx)
	if got := get[PlaybackSession](t, s, owner, "/v1/playback/sessions/"+watching.ID); got.Countdown != nil || got.State != "playing" {
		t.Fatalf("by default the viewer keeps watching, but the session reads %+v", got)
	}

	options.StopViewers = true
	s.setDVROptions(options)
	s.recorder.Tick(ctx)
	got := get[PlaybackSession](t, s, owner, "/v1/playback/sessions/"+watching.ID)
	if got.Countdown == nil || !strings.Contains(got.Countdown.Reason, "Morning News") {
		t.Fatalf("with stop-and-record the viewer's session reads %+v", got)
	}
	time.Sleep(time.Until(got.Countdown.EndsAt))
	s.recorder.Tick(ctx) // the countdown is over: the viewer is stopped
	s.recorder.Tick(ctx) // and the recording takes the tuner
	if code := status(t, s, "GET", "/v1/playback/sessions/"+watching.ID, owner, nil); code != 404 {
		t.Fatalf("after the countdown the viewer's session answered %d", code)
	}
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID); got.State != "recording" {
		t.Fatalf("after the countdown the recording reads %+v", got)
	}
	status(t, s, "DELETE", "/v1/dvr/recordings/"+rec.ID+"?action=stop", owner, nil)
}

// A booking follows its programme when the guide moves it, and one the guide no longer lists
// says so and is never recorded in its place.
func TestABookingFollowsItsProgrammeInTheGuide(t *testing.T) {
	s, owner, _ := recordable(t)
	var rec Recording
	call(t, s, "POST", "/v1/dvr/recordings", owner, RecordingBook{Programme: programmeID(t, s, owner, "Night Film")}, &rec)
	ctx := context.Background()
	stored, _ := s.db.Recording(ctx, rec.ID)
	source, key, err := s.db.ChannelListings(ctx, stored.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	moved := stored.StartsAt.Add(30 * time.Minute)
	if err := s.db.ReplaceSourceGuide(ctx, source, func(add func(db.GuideProgramme) error) error {
		// As the guide parser writes a programme with no id of its own: the channel and the start.
		return add(db.GuideProgramme{GuideKey: key, StartsAt: moved, EndsAt: moved.Add(time.Hour), ProgrammeKey: fmt.Sprintf("%s@%d", key, moved.Unix()),
			Title: "Night Film"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.recorder.FollowRules(ctx); err != nil {
		t.Fatal(err)
	}
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID); !got.StartsAt.Equal(moved.UTC()) || !got.InGuide {
		t.Fatalf("after the guide moved it the booking reads %+v", got)
	}
	s.db.ReplaceSourceGuide(ctx, source, func(func(db.GuideProgramme) error) error { return nil })
	s.recorder.FollowRules(ctx)
	if got := get[Recording](t, s, owner, "/v1/dvr/recordings/"+rec.ID); got.InGuide {
		t.Fatalf("a booking the guide no longer lists reads %+v", got)
	}
}

// The owner's default keep rule keeps the newest few recordings of each show and deletes the
// older ones; a recording marked Keep is never deleted by it.
func TestTheDefaultKeepRuleKeepsTheNewestOfEachShow(t *testing.T) {
	s, owner, _ := recordable(t)
	ctx := context.Background()
	channels := get[Page[Channel]](t, s, owner, "/v1/live/channels").Items
	ch, err := s.db.LiveChannelByID(ctx, viewerOfToken(t, s, owner), channels[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	day := s.now().Add(-72 * time.Hour)
	var ids []int64
	for n := range 3 {
		r, _, err := s.db.BookAiring(ctx, db.Booking{ProfileID: 1, ProfileName: "Justin", ProfilePub: "prf", ChannelID: ch.ID, ChannelName: ch.Name,
			ProgrammeKey: fmt.Sprint("news-", n), Title: "News", StartsAt: day.Add(time.Duration(n) * time.Hour), EndsAt: day.Add(time.Duration(n)*time.Hour + 30*time.Minute),
			Now: s.now()})
		if err != nil {
			t.Fatal(err)
		}
		s.db.RecordingFinished(ctx, r.ID, "recorded", "", s.now())
		ids = append(ids, r.ID)
	}
	keep := true
	oldest, _ := s.db.Recording(ctx, recordingPublic(t, s, ids[0]))
	s.db.ChangeRecording(ctx, oldest.PublicID, db.RecordingChange{Keep: &keep}, s.now())
	options := s.dvrOptions()
	options.KeepNewest = 1
	s.setDVROptions(options)
	if err := s.recorder.Keep(ctx); err != nil {
		t.Fatal(err)
	}
	for n, id := range ids {
		_, err := s.db.Recording(ctx, recordingPublic(t, s, id))
		if gone := err != nil; gone != (n == 1) {
			t.Fatalf("recording %d gone=%v: the newest and the kept one stay, the one between goes", n, gone)
		}
	}
}

func recordingPublic(t *testing.T, s *Server, id int64) string {
	t.Helper()
	rows, err := s.db.Recordings(context.Background(), db.RecordingQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == id {
			return r.PublicID
		}
	}
	return "rec_gone"
}
