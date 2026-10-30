package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOnNowCursorExhaustionStableRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s := testStore(t)
	ctx := context.Background()
	input := testInput()
	var playlist, guide strings.Builder
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)
	end := now.Add(4 * time.Hour)
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString("<tv>")
	for i := 0; i < 2003; i++ {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-chno=\"%d\" group-title=\"News\",Channel %d\nhttps://fixture.invalid/%d\n", i, i+1, i, i)
		fmt.Fprintf(&guide, "<channel id=\"ch%d\"><display-name>Channel %d</display-name></channel><programme channel=\"ch%d\" start=\"%s\" stop=\"%s\"><title>Program %d</title></programme>", i, i, i, start.Format("20060102150405 -0700"), end.Format("20060102150405 -0700"), i)
	}
	guide.WriteString("</tv>")
	input.Playlist = playlist.String()
	input.Guide = guide.String()
	saved, e := s.Save(ctx, testAuthority("owner", true, true), input)
	if e != nil {
		t.Fatal(e)
	}
	restricted := testInput()
	restricted.ID = strings.Repeat("c1", 24)
	restricted.RequestID = strings.Repeat("c2", 24)
	restricted.Name = "Private source"
	if _, e = s.Save(ctx, testAuthority("owner", true, true), restricted); e != nil {
		t.Fatal(e)
	}
	viewer := Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	fence := "viewer-1"
	permitted := true
	a := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		if !permitted {
			return DirectoryScope{}, ErrDenied
		}
		return DirectoryScope{Fence: fence, AllowedLiveSources: []string{saved.ID}, Viewer: viewer}, nil
	}
	read := func(ctx context.Context, q GuideQuery) (Guide, error) {
		q.Viewer = viewer
		return s.Guide(ctx, testAuthority("viewer", true, true), q)
	}
	var favorite string
	if e = s.db.QueryRow(`SELECT channel_id FROM live_channel_versions WHERE generation_id=? AND number='2003'`, saved.Generation).Scan(&favorite); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO live_channel_preferences VALUES('local','account','profile',?,?,1,0,1)`, saved.ID, favorite); e != nil {
		t.Fatal(e)
	}

	q := GuidePageQuery{Directory: DirectoryQuery{Kind: "all", Group: "News", Limit: 37}, At: now, OnNow: true}
	first, e := s.NowNextPage(ctx, a, read, q)
	if e != nil {
		t.Fatal(e)
	}
	if len(first.Channels) != 37 || first.Total != 2003 {
		t.Fatalf("first %d total %d", len(first.Channels), first.Total)
	}
	if first.Channels[0].ID != favorite {
		t.Fatal("favorites not first")
	}
	prefix := append([]Channel{}, first.Channels...)
	ids := map[string]bool{}
	all := []string{}
	page := first
	for {
		for _, ch := range page.Channels {
			if ids[ch.ID] {
				t.Fatal("duplicate", ch.ID)
			}
			ids[ch.ID] = true
			all = append(all, ch.ID)
			if len(ch.Programmes) != 1 {
				t.Fatalf("programmes for %s: %+v", ch.ID, ch.Programmes)
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
		page, e = s.NowNextPage(ctx, a, read, q)
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(ids) != 2003 {
		t.Fatalf("exhaustion: %d", len(ids))
	}
	for i, ch := range prefix {
		if all[i] != ch.ID {
			t.Fatal("loaded prefix changed")
		}
	}
	q.Cursor = first.NextCursor
	replay, e := s.NowNextPage(ctx, a, read, q)
	if e != nil || replay.Offset != 37 || replay.Channels[0].ID != all[37] {
		t.Fatalf("cursor replay: %+v %v", replay, e)
	}
	if _, e = s.db.Exec(`UPDATE live_sources SET revision=revision+1 WHERE id=?`, saved.ID); e != nil {
		t.Fatal(e)
	}
	fence = "viewer-2"
	if _, e = s.NowNextPage(ctx, a, read, q); !errors.Is(e, ErrCursor) {
		t.Fatalf("revision change accepted: %v", e)
	}
	q.Cursor = ""
	if _, e = s.NowNextPage(ctx, a, read, q); e != nil {
		t.Fatal("restart", e)
	}
	permitted = false
	blocked, e := s.NowNextPage(ctx, a, read, q)
	if !errors.Is(e, ErrDenied) || len(blocked.Channels) != 0 {
		t.Fatalf("no Live TV leaked: %+v %v", blocked, e)
	}
}

func TestGuideProgrammeSearchRemindersAndCompactReads(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	input := testInput()
	now := time.Now().UTC().Truncate(time.Second)
	var guide strings.Builder
	guide.WriteString(`<tv><channel id="one"><display-name>One</display-name></channel>`)
	for i := 0; i < 7; i++ {
		fmt.Fprintf(&guide, `<programme channel="one" start="%s" stop="%s"><title>News</title><sub-title>Episode %d</sub-title><live/></programme>`, now.Add(time.Duration(i)*3*time.Hour-time.Hour).Format("20060102150405 -0700"), now.Add(time.Duration(i+1)*3*time.Hour-time.Hour).Format("20060102150405 -0700"), i)
	}
	guide.WriteString("</tv>")
	input.Guide = guide.String()
	saved, e := s.Save(ctx, testAuthority("owner", true, true), input)
	if e != nil {
		t.Fatal(e)
	}
	viewer := Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	scope := DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}, Viewer: viewer}
	a := func(context.Context, *sql.Tx) (DirectoryScope, error) { return scope, nil }
	read := func(ctx context.Context, q GuideQuery) (Guide, error) {
		q.Viewer = viewer
		return s.Guide(ctx, testAuthority("viewer", true, true), q)
	}
	compact, e := s.NowNextPage(ctx, a, read, GuidePageQuery{Directory: DirectoryQuery{Kind: "all", Limit: 37}, At: now})
	if e != nil {
		t.Fatal(e)
	}
	if len(compact.Channels[0].Programmes) != 3 {
		t.Fatalf("compact: %+v", compact)
	}
	q := AiringsQuery{Start: now, End: now.Add(24 * time.Hour), Search: "Episode 4", Limit: 1}
	page, e := s.Airings(ctx, a, nil, q)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Groups) != 1 || len(page.Groups[0].Airings) != 1 || page.Groups[0].Airings[0].Programme.Subtitle != "Episode 4" || !page.Groups[0].Airings[0].Programme.Flags.Live {
		t.Fatalf("episode name search: %+v", page)
	}
	v := page.Groups[0].Airings[0]
	in := ReminderInput{SourceID: v.Channel.SourceID, ChannelID: v.Channel.ID, Generation: v.Channel.Generation, ProgrammeID: v.Programme.ID, Kind: v.Channel.Provenance}
	reminder, e := s.SaveReminder(ctx, a, nil, in, nil)
	if e != nil {
		t.Fatal(e)
	}
	listed, e := s.Reminders(ctx, a, nil, "", 30, nil)
	if e != nil || len(listed.Items) != 1 || listed.Items[0].ID != reminder.ID {
		t.Fatalf("reminders %+v %v", listed, e)
	}
	scope.Viewer.ProfileID = "other"
	listed, e = s.Reminders(ctx, a, nil, "", 30, nil)
	if e != nil || len(listed.Items) != 0 {
		t.Fatal("cross-profile reminder", listed, e)
	}
	if e = s.DeleteReminder(ctx, a, reminder.ID, 1); !errors.Is(e, ErrConflict) {
		t.Fatal("cross-profile deletion", e)
	}
	scope.Viewer = viewer
	if e = s.DeleteReminder(ctx, a, reminder.ID, 1); e != nil {
		t.Fatal(e)
	}
	q.Search = "News"
	q.ProgrammeAllowed = func(string) bool { return false }
	page, e = s.Airings(ctx, a, nil, q)
	if e != nil || len(page.Groups) != 0 {
		t.Fatal("restricted programme search leaked", page, e)
	}
	scope.AllowedLiveSources = nil
	page, e = s.Airings(ctx, a, nil, q)
	if e != nil || len(page.Groups) != 0 {
		t.Fatal("unauthorized sources leaked", page, e)
	}
}
