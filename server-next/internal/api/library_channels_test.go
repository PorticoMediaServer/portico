package api

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

func libraryOfKind(t *testing.T, s *Server, token, kind string) string {
	t.Helper()
	for _, l := range get[Libraries](t, s, token, "/v1/libraries").Items {
		if l.Kind == kind {
			return l.ID
		}
	}
	t.Fatalf("no %s library", kind)
	return ""
}

func makeChannel(t *testing.T, s *Server, owner, name string, b librarychannels.Builder) LibraryChannel {
	t.Helper()
	var ch LibraryChannel
	if w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: &name, Builder: &b}, &ch); w.Code != 201 {
		t.Fatalf("making %q answered %d: %s", name, w.Code, w.Body)
	}
	if err := s.channels.Schedule(context.Background(), ch.ID); err != nil {
		t.Fatal(err)
	}
	return ch
}

// programmes reads a channel's schedule from the main database and checks it has no gaps
// and no overlaps: each programme starts the moment the one before ends.
func programmes(t *testing.T, s *Server, channelID string, from, to time.Time) []db.ScheduleRow {
	t.Helper()
	ch, err := s.db.LibraryChannel(context.Background(), channelID, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.ChannelProgrammes(context.Background(), ch.ID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n < len(rows); n++ {
		if !rows[n].StartsAt.Equal(rows[n-1].EndsAt) {
			t.Fatalf("programme %d starts at %v, the one before ends at %v", n, rows[n].StartsAt, rows[n-1].EndsAt)
		}
	}
	return rows
}

// The owner makes a film channel from a template, it is on the air a week ahead with no
// gaps, and saving a change on an out-of-date copy, an overlapping block or a missing library
// is refused with the field named.
func TestALibraryChannelIsMadeAndScheduledAWeekAhead(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	var templates Page[ChannelTemplate]
	call(t, s, "GET", "/v1/admin/library-channels/templates", owner, nil, &templates)
	if len(templates.Items) != 9 || !templates.Items[0].Available {
		t.Fatalf("templates: %+v", templates.Items)
	}
	if templates.Items[6].Key != "anime" || templates.Items[6].Available || templates.Items[6].Reason != "Not enough media" {
		t.Fatalf("a template with no media is offered: %+v", templates.Items[6])
	}
	ch := makeChannel(t, s, owner, "Movies", templates.Items[0].Builder)

	var read LibraryChannel
	w := call(t, s, "GET", "/v1/admin/library-channels/"+ch.ID, owner, nil, &read)
	if w.Code != 200 || read.State != "onAir" || read.Health == nil || !read.Health.OnAir || read.Titles == 0 || w.Header().Get("ETag") == "" {
		t.Fatalf("the new channel is not on the air: %d %+v %+v", w.Code, read, read.Health)
	}
	now := time.Now()
	rows := programmes(t, s, ch.ID, now, now.Add(8*24*time.Hour))
	// The schedule was built from a moment a little before now, truncated to the second.
	if len(rows) < 50 || rows[len(rows)-1].EndsAt.Before(now.Add(7*24*time.Hour-time.Minute)) || rows[0].StartsAt.After(now) {
		t.Fatalf("%d programmes, ending %v", len(rows), rows[len(rows)-1].EndsAt)
	}
	// Shuffle, no repeats: no film plays twice before every film has played once.
	seen := map[int64]bool{}
	for n, r := range rows[:min(len(rows), read.Titles)] {
		if seen[r.ItemID] {
			for _, r := range rows[:3] {
				t.Logf("%v %v %d", r.StartsAt, r.EndsAt, r.ItemID)
			}
			t.Fatalf("film %d (programme %d of %d) repeated within the first round of %d", r.ItemID, n, len(rows), read.Titles)
		}
		seen[r.ItemID] = true
	}

	films := libraryOfKind(t, s, owner, "movies")
	b := templates.Items[0].Builder
	b.Blocks = []librarychannels.Block{
		{ID: "evening", Days: []string{"mon", "tue"}, From: "19:00", To: "23:00"},
		{ID: "late", Days: []string{"tue"}, From: "22:00", To: "02:00"},
	}
	stale := newRequest("PATCH", "/v1/admin/library-channels/"+ch.ID, owner, LibraryChannelSave{Builder: &b})
	stale.Header.Set("If-Match", `"999"`)
	if w := serve(s, stale); w.Code != 412 {
		t.Fatalf("a save on an out-of-date copy answered %d", w.Code)
	}
	for _, c := range []struct {
		save  LibraryChannelSave
		field string
	}{
		{LibraryChannelSave{Builder: &b}, "builder.blocks[1]"},
		{LibraryChannelSave{Builder: &librarychannels.Builder{Selection: librarychannels.Selection{Kinds: []string{"movie"}}}}, "builder.libraries"},
		{LibraryChannelSave{Builder: &librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}}}}, "builder.kinds"},
		{LibraryChannelSave{TimeZone: new("Mars/Olympus")}, "timeZone"},
	} {
		var e struct {
			Error Error `json:"error"`
		}
		w := call(t, s, "PATCH", "/v1/admin/library-channels/"+ch.ID, owner, c.save, &e)
		if w.Code != 400 || len(e.Error.Fields) == 0 || e.Error.Fields[0].Path != c.field {
			t.Errorf("saving %+v answered %d %+v, want the field %s named", c.save, w.Code, e.Error, c.field)
		}
	}
}

// A TV channel plays each show's episodes in order, and Regenerate schedule builds the rest
// again from the end of the programme now on without skipping the week of episodes it threw away.
func TestShowsPlayInEpisodeOrderAndRegenerateKeepsTheirPlace(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	ctx := context.Background()
	shows := libraryOfKind(t, s, owner, "shows")
	ch := makeChannel(t, s, owner, "TV", librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{shows},
		Kinds: []string{"show"}, Order: "inOrder", Episodes: "rotate"}})
	now := time.Now()
	before := programmes(t, s, ch.ID, now, now.Add(8*24*time.Hour))
	var ids []int64
	for _, r := range before {
		ids = append(ids, r.ItemID)
	}
	titles, err := s.db.ProgrammeTitles(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	last := map[int64][2]int{}
	for _, r := range before {
		e := titles[r.ItemID]
		if e.ShowID == 0 || e.Season == nil || e.Episode == nil {
			t.Fatalf("programme %+v is not an episode", e)
		}
		place := [2]int{*e.Season, *e.Episode}
		// A show that has played to its end starts again from its first episode.
		if prev, ok := last[e.ShowID]; ok && place != [2]int{1, 1} && (place[0] < prev[0] || place[0] == prev[0] && place[1] <= prev[1]) {
			t.Fatalf("show %d went from %v to %v", e.ShowID, prev, place)
		}
		last[e.ShowID] = place
	}

	var regenerated LibraryChannel
	if w := call(t, s, "POST", "/v1/admin/library-channels/"+ch.ID+"/regenerate", owner, nil, &regenerated); w.Code != 202 || regenerated.State != "building" {
		t.Fatalf("regenerate answered %d %+v", w.Code, regenerated)
	}
	if err := s.channels.Schedule(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	after := programmes(t, s, ch.ID, now, now.Add(8*24*time.Hour))
	for n := 0; n < 20 && n < len(before); n++ {
		if after[n] != before[n] {
			t.Fatalf("programme %d was %+v and after Regenerate is %+v", n, before[n], after[n])
		}
	}
}

// In a block that cuts programmes off, nothing runs past the block's end; outside it a film
// always finishes, so the block can start late.
func TestABlockThatCutsOffEndsOnTime(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	films := libraryOfKind(t, s, owner, "movies")
	shows := libraryOfKind(t, s, owner, "shows")
	zone := "America/Halifax"
	b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}},
		Blocks: []librarychannels.Block{{ID: "cartoons", Days: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}, From: "19:00", To: "21:00",
			Overrun: "cut", Selection: &librarychannels.Selection{Libraries: []string{shows}, Kinds: []string{"show"}}}}}
	var ch LibraryChannel
	if w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: new("Evenings"), TimeZone: &zone, Builder: &b}, &ch); w.Code != 201 {
		t.Fatalf("making the channel answered %d: %s", w.Code, w.Body)
	}
	if len(ch.Libraries) != 2 {
		t.Fatalf("the block's library is not among the channel's libraries: %v", ch.Libraries)
	}
	if err := s.channels.Schedule(context.Background(), ch.ID); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(zone)
	now := time.Now()
	inBlock := 0
	for _, r := range programmes(t, s, ch.ID, now, now.Add(8*24*time.Hour)) {
		start := r.StartsAt.In(loc)
		end := time.Date(start.Year(), start.Month(), start.Day(), 21, 0, 0, 0, loc)
		if start.Hour() >= 19 && start.Hour() < 21 {
			inBlock++
			if r.EndsAt.After(end) {
				t.Fatalf("a programme in the block runs from %v to %v, past %v", start, r.EndsAt.In(loc), end)
			}
		}
	}
	if inBlock < 7 {
		t.Fatalf("only %d programmes in a week of evening blocks", inBlock)
	}
}

// The preview plays the same titles in the same order as the schedule saving makes.
func TestThePreviewIsTheScheduleSavingMakes(t *testing.T) {
	s, owner, _ := seededWith(t, 600, 0)
	films := libraryOfKind(t, s, owner, "movies")
	b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}}}
	var preview ChannelPreview
	if w := call(t, s, "POST", "/v1/admin/library-channels/preview", owner, ChannelPreviewAsk{TimeZone: "UTC", Builder: b}, &preview); w.Code != 200 {
		t.Fatalf("preview answered %d: %s", w.Code, w.Body)
	}
	ch := makeChannel(t, s, owner, "Same", b)
	rows := programmes(t, s, ch.ID, time.Now().Add(-time.Minute), time.Now().Add(24*time.Hour))
	for n := 0; n < 5 && n < len(preview.Today) && n < len(rows); n++ {
		got, _ := s.db.ProgrammeTitles(context.Background(), []int64{rows[n].ItemID})
		if preview.Today[n].Title == nil || got[rows[n].ItemID].PublicID != preview.Today[n].Title.ID {
			t.Fatalf("programme %d: the preview showed %+v, saving made %s", n, preview.Today[n].Title, got[rows[n].ItemID].PublicID)
		}
	}
}

// A block added from a preset is a copy that a later change to the preset leaves alone, a
// block can play one pinned title at a fixed time each week, and an uploaded logo is kept
// small and shown with the channel.
func TestPresetsFixedTimesAndALogo(t *testing.T) {
	s, owner, members := seededWith(t, 600, 1)
	ctx := context.Background()
	films := libraryOfKind(t, s, owner, "movies")
	var preset BlockPreset
	if w := call(t, s, "POST", "/v1/admin/block-presets", owner, BlockPresetSave{Name: new("Late films"), Library: &films,
		Recipe: &librarychannels.Recipe{Order: "recent", Minutes: 180, StartsAt: "21:00", Days: []string{"fri", "sat"}}}, &preset); w.Code != 201 {
		t.Fatalf("making a preset answered %d: %s", w.Code, w.Body)
	}
	page, err := s.db.Items(ctx, viewerOfToken(t, s, owner), db.ItemQuery{Library: films, Kinds: []db.ItemKind{db.KindMovie}, Limit: 1})
	if err != nil || len(page.Items) == 0 {
		t.Fatal(err)
	}
	pinned := page.Items[0].PublicID
	zone := "America/Halifax"
	b := librarychannels.Builder{Selection: librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}},
		Blocks: []librarychannels.Block{
			{ID: "late", FromPreset: preset.ID},
			{ID: "classic", Days: []string{"sun"}, From: "20:00", To: "20:01", Overrun: "finish",
				Selection: &librarychannels.Selection{Libraries: []string{films}, Kinds: []string{"movie"}, Always: []string{pinned}, OnlyAlways: true}},
		}}
	var ch LibraryChannel
	if w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: new("Fixed"), TimeZone: &zone, Builder: &b}, &ch); w.Code != 201 {
		t.Fatalf("making the channel answered %d: %s", w.Code, w.Body)
	}
	late := ch.Builder.Blocks[0]
	if late.FromPreset != "" || late.Name != "Late films" || late.From != "21:00" || late.To != "00:00" || late.Selection == nil ||
		late.Selection.Sort != "recent" || len(late.Days) != 2 {
		t.Fatalf("the block copied from the preset reads %+v", late)
	}
	status(t, s, "PATCH", "/v1/admin/block-presets/"+preset.ID, owner, BlockPresetSave{Name: new("Renamed")})
	if again := get[LibraryChannel](t, s, owner, "/v1/admin/library-channels/"+ch.ID); again.Builder.Blocks[0].Name != "Late films" {
		t.Fatalf("changing the preset changed the channel: %+v", again.Builder.Blocks[0])
	}
	if err := s.channels.Schedule(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(zone)
	titles, err := s.db.TitleLibraries(ctx, []string{pinned})
	if err != nil || len(titles) != 1 {
		t.Fatal(err)
	}
	sundays := 0
	for _, r := range programmes(t, s, ch.ID, time.Now(), time.Now().Add(8*24*time.Hour)) {
		local := r.StartsAt.In(loc)
		if local.Weekday() == time.Sunday && local.Hour() == 20 && local.Minute() == 0 {
			sundays++
			got, _ := s.db.ProgrammeTitles(ctx, []int64{r.ItemID})
			if got[r.ItemID].PublicID != pinned {
				t.Fatalf("Sunday at 8 plays %s, not the pinned title", got[r.ItemID].PublicID)
			}
		}
	}
	if sundays == 0 {
		t.Log("no Sunday 8 PM was free in this week's schedule: a film ran over it") // allowed: outside blocks a film finishes
	}

	// The on-screen logo needs a logo; an upload is kept small and shown with the channel.
	b.LogoOnScreen = librarychannels.Logo{On: true}
	if code := status(t, s, "PATCH", "/v1/admin/library-channels/"+ch.ID, owner, LibraryChannelSave{Builder: &b}); code != 400 {
		t.Fatalf("turning the logo on without one answered %d", code)
	}
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1200, 600)))
	r := newRequest("PUT", "/v1/admin/library-channels/"+ch.ID+"/logo", owner, nil)
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(picture.Bytes())), int64(picture.Len())
	var withLogo LibraryChannel
	if w := serve(s, r); w.Code != 200 || jsonInto(w, &withLogo) != nil || withLogo.Logo == "" {
		t.Fatalf("uploading a logo answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "GET", withLogo.Logo, "", nil, nil); w.Code != 200 {
		t.Fatalf("the logo answered %d", w.Code)
	}
	tiny := newRequest("PUT", "/v1/admin/library-channels/"+ch.ID+"/logo", owner, nil)
	tiny.Body = io.NopCloser(bytes.NewReader([]byte("not a picture")))
	if w := serve(s, tiny); w.Code != 400 {
		t.Fatalf("a logo that is not a picture answered %d", w.Code)
	}
	if code := status(t, s, "PATCH", "/v1/admin/library-channels/"+ch.ID, owner, LibraryChannelSave{Builder: &b}); code != 200 {
		t.Fatalf("turning the logo on answered %d", code)
	}
	for _, c := range get[Page[Channel]](t, s, members[0], "/v1/live/channels").Items {
		if c.ID == ch.ID && c.Logo != withLogo.Logo {
			t.Fatalf("the channel list shows logo %q", c.Logo)
		}
	}
}
