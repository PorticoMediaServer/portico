package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// recordUntilTitled books the programme on now, records it until its title exists, and stops it.
func recordUntilTitled(t *testing.T, s *Server, owner, title string) DvrRecording {
	t.Helper()
	var rec DvrRecording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, title)}, &rec); w.Code != 200 {
		t.Fatalf("booking answered %d: %s", w.Code, w.Body)
	}
	s.recorder.Tick(context.Background())
	deadline := time.Now().Add(15 * time.Second)
	for get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID).ItemID == "" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+rec.ID+"?action=stop", owner, nil); code != 204 {
		t.Fatalf("stopping answered %d", code)
	}
	for deadline = time.Now().Add(10 * time.Second); get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID).State == "recording"; {
		if time.Now().After(deadline) {
			t.Fatal("the recording did not stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID)
}

// A recording says how much it takes on disk (its file and the guide's details beside it,
// which is what Delete removes), the library its title is in, and this profile's place in it;
// a booking not yet recorded takes nothing. Storage says what the recordings take, the floor
// and cap, the keep rule behind its words, and about what the bookings ahead will take.
func TestARecordingSaysItsSizeAndPlaceAndStorageItsForecast(t *testing.T) {
	s, owner, folder := recordable(t)
	options := s.dvrOptions()
	options.Floor, options.Cap, options.KeepNewest = 1<<20, 500<<30, 4
	s.setDVROptions(options)
	got := recordUntilTitled(t, s, owner, "Morning News")
	if got.ItemID == "" || got.LibraryID == "" {
		t.Fatalf("the recording reads %+v", got)
	}
	matches, _ := filepath.Glob(filepath.Join(folder, "Morning News", "Morning News - *.ts"))
	if len(matches) != 1 {
		t.Fatalf("recorded files: %v", matches)
	}
	var want int64
	for _, path := range []string{matches[0], strings.TrimSuffix(matches[0], ".ts") + ".nfo"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want += info.Size()
	}
	if got.Bytes != want || got.PlayingNow != 0 {
		t.Fatalf("the recording says %d bytes and %d playing; its files take %d", got.Bytes, got.PlayingNow, want)
	}
	if lib := get[Item](t, s, owner, "/v1/items/"+got.ItemID); lib.LibraryID != got.LibraryID {
		t.Fatalf("the recording names library %q, its title is in %q", got.LibraryID, lib.LibraryID)
	}
	if w := call(t, s, "PUT", "/v1/items/"+got.ItemID+"/state", owner, map[string]any{"positionMs": 1}, nil); w.Code != 200 {
		t.Fatalf("saving a place answered %d: %s", w.Code, w.Body)
	}
	listed := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=history").Items
	if len(listed) != 1 || listed[0].PositionMs != 1 || listed[0].Bytes != want || listed[0].PlayingNow != 0 {
		t.Fatalf("History lists %+v", listed)
	}

	var later DvrRecording
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Late News")}, &later)
	if later.Bytes != 0 {
		t.Fatalf("a booking not yet recorded says %d bytes", later.Bytes)
	}
	overview := get[DvrOverview](t, s, owner, "/v1/dvr")
	if overview.UsedBytes <= 0 || overview.FloorBytes != 1<<20 || overview.CapBytes != 500<<30 || overview.KeepNewest != 4 ||
		overview.KeepDays != 0 || overview.KeepRule != "The newest 4 of each show" {
		t.Fatalf("Storage reads %+v with %d recorded", overview, want)
	}
	if overview.UpcomingHours < 1 || overview.UpcomingBytes <= 0 {
		t.Fatalf("Storage forecasts %d hours, %d bytes for a booking ahead", overview.UpcomingHours, overview.UpcomingBytes)
	}
}

// Editing a series rule previews what the edited rule would record and saves nothing; the
// rule's channel can be changed to another channel of its source; a rule that matches by
// title cannot follow every channel. A rule being made previews its airings too.
func TestEditingASeriesRulePreviewsItAndCanChangeItsChannel(t *testing.T) {
	s, owner, _ := recordable(t)
	var made DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules?preview=true", owner, DvrRuleSave{Programme: programmeID(t, s, owner, "Night Film")}, &made); w.Code != 200 {
		t.Fatalf("previewing a new rule answered %d: %s", w.Code, w.Body)
	}
	if made.Preview == nil || made.Preview.Matches != 1 || made.Preview.Airings[0].Title != "Night Film" || made.Preview.Airings[0].AlreadyBooked ||
		made.Preview.Airings[0].Channel != "Film Four" || made.ID != "" {
		t.Fatalf("a new rule's preview reads %+v", made)
	}
	var rule DvrRule
	call(t, s, "POST", "/v1/dvr/rules", owner, DvrRuleSave{Programme: programmeID(t, s, owner, "Night Film")}, &rule)
	if rule.Channel == "" || rule.Source == "" || rule.Preview != nil {
		t.Fatalf("the saved rule reads %+v", rule)
	}
	var news string
	for _, c := range get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items {
		if c.Name == "Example News" {
			news = c.ID
		}
	}

	var preview DvrRule
	if w := call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID+"?preview=true", owner, DvrRuleSave{Priority: new(1)}, &preview); w.Code != 200 {
		t.Fatalf("previewing an edit answered %d: %s", w.Code, w.Body)
	}
	if preview.Preview == nil || preview.Preview.Matches != 1 || !preview.Preview.Airings[0].AlreadyBooked || preview.Priority != 1 {
		t.Fatalf("the edit's preview reads %+v", preview)
	}
	call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID+"?preview=true", owner, DvrRuleSave{Channel: &news}, &preview)
	if preview.Preview.Matches != 0 || preview.Channel != news {
		t.Fatalf("on Example News the preview reads %+v", preview)
	}
	if listed := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules").Items; len(listed) != 1 || listed[0].Priority != 0 || listed[0].Channel != rule.Channel {
		t.Fatalf("a preview saved the rule: %+v", listed)
	}

	if w := call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{AnyChannel: new(true)}, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "anyChannel") {
		t.Fatalf("every channel for a rule matched by title answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{Channel: new("chn_nothing")}, nil); w.Code != 400 {
		t.Fatalf("an unknown channel answered %d: %s", w.Code, w.Body)
	}
	booked := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings").Items
	if len(booked) != 1 || booked[0].ChannelID != rule.Channel || booked[0].Channel != "Film Four" {
		t.Fatalf("the rule's booking reads %+v", booked)
	}
	var saved DvrRule
	if w := call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{Channel: &news}, &saved); w.Code != 200 || saved.Channel != news ||
		saved.ChannelName != "Example News" {
		t.Fatalf("changing the channel answered %d: %+v", w.Code, saved)
	}
	// Its booking on the channel it left, which nobody else asked for, is let go.
	if got := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+booked[0].ID); got.State != "cancelled" {
		t.Fatalf("after the rule moved channel its old booking reads %+v", got)
	}
	if listed := get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules").Items; listed[0].ChannelName != "Example News" {
		t.Fatalf("the rule list names the channel %q", listed[0].ChannelName)
	}

	// A keep choice is set, and cleared with 0 so the owner's default applies again.
	call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{KeepNewest: new(3), KeepDays: new(7)}, &saved)
	if saved.KeepNewest == nil || *saved.KeepNewest != 3 || saved.KeepDays == nil {
		t.Fatalf("setting the keep choice: %+v", saved)
	}
	var cleared DvrRule
	call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{KeepNewest: new(0), KeepDays: new(0)}, &cleared)
	if cleared.ID != rule.ID || cleared.KeepNewest != nil || cleared.KeepDays != nil {
		t.Fatalf("clearing the keep choice left %+v", cleared)
	}
}

// A booking that loses a tuner says how many tuners the source has and what holds them: the
// account's own recordings by title, except one above the asking profile's rating limit.
func TestAConflictSaysHowManyTunersAndWhatHoldsThem(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	folder := t.TempDir()
	options := s.dvrOptions()
	options.Folder, options.Floor = filepath.Join(folder, "Recorded TV"), 0
	s.setDVROptions(options)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close) // before the provider closes, which waits for the streams the tuners hold
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("One tuner"), TunerCount: new(1),
		Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/recordable.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	_, kid := limitedKid(t, s, owner)
	var grim, late DvrRecording
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "A Grim Film")}, &grim)
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Late News")}, &late)
	got := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+late.ID)
	if !got.Conflict || got.Tuners != 1 || len(got.TunersUsedBy) != 1 || got.TunersUsedBy[0] != "A Grim Film" {
		t.Fatalf("the losing booking reads %+v", got)
	}
	if listed := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings").Items; len(listed) != 2 || listed[1].Tuners != 1 || listed[0].Conflict {
		t.Fatalf("Upcoming lists %+v", listed)
	}
	w := call(t, s, "GET", "/v1/dvr/recordings/"+late.ID, kid, nil, &got)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Grim") || len(got.TunersUsedBy) != 1 || got.TunersUsedBy[0] != "A recording" {
		t.Fatalf("a profile held to 13 reads the conflict as %d: %s", w.Code, w.Body)
	}
}

// The Recorded tab reads the recordings that were made, by title and then newest first, page
// by page; bookings not yet made are not in it.
func TestTheRecordedTabReadsTheRecordingsThatWereMade(t *testing.T) {
	s, owner, _ := recordable(t)
	made := recordUntilTitled(t, s, owner, "Morning News")
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Late News")}, nil)
	page := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=recorded")
	if len(page.Items) != 1 || page.Items[0].ID != made.ID || page.NextCursor != "" {
		t.Fatalf("the Recorded tab reads %+v", page)
	}
	if everyone := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=recorded&scope=everyone"); len(everyone.Items) != 1 ||
		everyone.Items[0].BookedBy == "" {
		t.Fatalf("everyone's recordings read %+v", everyone)
	}
}

// A recording that captured nothing, because the source answered but sent no picture, fails
// with the reason and leaves no file and no title behind.
func TestARecordingThatCapturedNothingFailsAndLeavesNoTitle(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	folder := t.TempDir()
	options := s.dvrOptions()
	options.Folder, options.Floor = filepath.Join(folder, "Recorded TV"), 0
	s.setDVROptions(options)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close) // before the provider closes, which waits for the streams the tuners hold
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/silent.m3u" {
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"news.example\" tvg-chno=\"2\",Example News\nhttp://%s/stream/2.ts\n", r.Host)
		}
	}))
	t.Cleanup(silent.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Quiet"), TunerCount: new(1),
		Config: &live.LiveSourceConfig{PlaylistURL: silent.URL + "/silent.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	var rec DvrRecording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Morning News")}, &rec); w.Code != 200 {
		t.Fatalf("booking answered %d: %s", w.Code, w.Body)
	}
	s.recorder.Tick(context.Background())
	// The source is tried again for a minute of the server's clock, and at least three times,
	// before the channel is said to have stopped. From here a minute of that clock passes in
	// each second, so the minute is over two seconds after the third try whenever the tries
	// begin: a slow machine waits for its tries, never for the clock.
	claims, err := s.tokens.CheckAccess(owner, s.now())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	s.setClock(func() time.Time { return started.Add(time.Since(started) * 60) })
	ctx := context.Background()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := s.db.Recording(ctx, rec.ID); err != nil || r.State != "scheduled" && r.State != "recording" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	owner, _ = s.tokens.Access(claims, s.now(), s.now().Add(time.Hour))
	got := get[DvrRecording](t, s, owner, "/v1/dvr/recordings/"+rec.ID)
	if got.State != "failed" || got.Reason == "" || got.ItemID != "" || got.Bytes != 0 {
		t.Fatalf("a recording that captured nothing reads %+v", got)
	}
	if files, _ := filepath.Glob(filepath.Join(options.Folder, "Morning News", "*.ts")); len(files) != 0 {
		t.Fatalf("it left %v", files)
	}
}

// Each row says what this profile may do with it, as the routes decide: the booker and the
// owner change and cancel; another profile that only reads it may do neither until it asks
// for the booking too, and then may cancel its own ask. The header says whether Everyone's
// shows more than the profile's own.
func TestARowSaysWhatThisProfileMayDoWithIt(t *testing.T) {
	s, owner, _ := recordable(t)
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)
	var late DvrRecording
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programmeID(t, s, owner, "Late News")}, &late)
	if !late.MayChange || !late.MayCancel {
		t.Fatalf("the booker's own booking reads %+v", late)
	}
	if !get[DvrOverview](t, s, owner, "/v1/dvr").EveryonesRecordings || !get[DvrOverview](t, s, onKid.AccessToken, "/v1/dvr").EveryonesRecordings {
		t.Fatal("a profile that may see Recorded TV is told Everyone's shows nothing more")
	}
	seen := get[Page[DvrRecording]](t, s, onKid.AccessToken, "/v1/dvr/recordings?scope=everyone").Items
	if len(seen) != 1 || seen[0].MayChange || seen[0].MayCancel {
		t.Fatalf("another profile's booking reads to the kid as %+v", seen)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+late.ID, onKid.AccessToken, nil); code != 403 {
		t.Fatalf("the kid cancelling someone else's booking answered %d", code)
	}
	call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, DvrRecordingBook{Programme: programmeID(t, s, owner, "Late News")}, nil)
	if got := get[DvrRecording](t, s, onKid.AccessToken, "/v1/dvr/recordings/"+late.ID); got.MayChange || !got.MayCancel {
		t.Fatalf("once the kid asked too the booking reads %+v", got)
	}

	if code := status(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{AllLibraries: false, Libraries: []string{}}); code != 200 {
		t.Fatalf("limiting the kid's libraries answered %d", code)
	}
	if get[DvrOverview](t, s, onKid.AccessToken, "/v1/dvr").EveryonesRecordings {
		t.Fatal("a profile that may not see Recorded TV is offered Everyone's")
	}
}

// Recording space obeys both the drive floor and remaining cap, while the drive fact stays raw.
func TestRecordingSpaceRespectsTheCapAndFreeSpaceFloor(t *testing.T) {
	s, owner, _ := seededWith(t, 20, 0)
	opts := s.dvrOptions()
	opts.Folder, opts.Floor, opts.Cap = t.TempDir(), 0, 1<<20
	s.setDVROptions(opts)
	got := get[DvrOverview](t, s, owner, "/v1/dvr")
	if !got.FolderOK || got.FreeBytes != 1<<20 || got.FreeOnDriveBytes < got.FreeBytes || got.HoursLeft != 0 {
		t.Fatalf("capped space: %+v", got)
	}
	opts.Cap, opts.Floor = 0, 1<<62
	s.setDVROptions(opts)
	got = get[DvrOverview](t, s, owner, "/v1/dvr")
	if !got.FolderOK || got.FreeBytes != 0 || got.FreeOnDriveBytes <= 0 || got.HoursLeft != 0 {
		t.Fatalf("space below the floor: %+v", got)
	}
	opts.Floor = 0
	s.setDVROptions(opts)
	got = get[DvrOverview](t, s, owner, "/v1/dvr")
	if got.FreeBytes != got.FreeOnDriveBytes || got.FreeBytes <= 0 {
		t.Fatalf("uncapped space with no floor: %+v", got)
	}
}
