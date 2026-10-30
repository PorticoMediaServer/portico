package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// FEAT-08 (server data): DVR rows carry a stable show-grouping key. Two
// recordings of one series and one of another come back with the right
// seriesId values; one without series metadata has none. Paging order is
// unchanged: the client groups what it has.
func TestRecordingsCarrySeriesGrouping(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recordingProfiles(t, db, "account", "profile")
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db, live: live, now: time.Now, captureAvailable: true, durable: func(context.Context, *sql.Tx, livechannels.Owner, string, string) error {
		return nil
	}}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fence", func(string, string) bool { return true }, nil
	}
	src := strings.Repeat("ab", 24)
	if _, err = db.Exec(`INSERT INTO live_source_identities VALUES(?)`, src); err != nil {
		t.Fatal(err)
	}
	ch := strings.Repeat("cd", 32)
	gen := strings.Repeat("ef", 24)
	now := time.Now().UnixMilli()
	insert := func(id, series, title string, created int64) {
		t.Helper()
		prog := livechannels.Programme{ID: id, ChannelID: ch, Title: title, Start: time.UnixMilli(now).UTC().Format(time.RFC3339Nano), End: time.UnixMilli(now + 3600000).UTC().Format(time.RFC3339Nano), Lineage: "interval-only", SeriesID: series}
		pb, _ := json.Marshal(prog)
		ob, _ := json.Marshal(Options{})
		if _, err = db.Exec(`INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,rule_id,rule_revision,manual,options_json,start_ms,end_ms,priority,revision,state,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,'completed',?,?)`,
			id, owner.Key(), owner.Authority, owner.AccountID, owner.ProfileID, src, ch, id, gen, string(pb), "", 0, 0, string(ob), now, now+3600000, 0, created, created); err != nil {
			t.Fatal(err)
		}
	}
	// Two of one series, one of another, one with no series metadata. Created
	// order is deliberately not grouped: the list must keep paging order.
	idA1, idA2 := strings.Repeat("a1", 32), strings.Repeat("a2", 32)
	idB, idNone := strings.Repeat("b1", 32), strings.Repeat("c1", 32)
	insert(idB, "show-b", "Show B", now+30)
	insert(idNone, "", "One-off special", now+20)
	insert(idA2, "show-a", "Show A", now+10)
	insert(idA1, "show-a", "Show A", now)
	page, err := s.List(ctx, auth, owner, Query{State: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Recordings) != 4 {
		t.Fatalf("recordings = %d, want 4", len(page.Recordings))
	}
	// Paging order unchanged: created_ms DESC, not grouped.
	wantOrder := []string{idB, idNone, idA2, idA1}
	for i, want := range wantOrder {
		if page.Recordings[i].ID != want {
			t.Fatalf("order[%d] = %s, want %s", i, page.Recordings[i].ID, want)
		}
	}
	byID := map[string]Recording{}
	for _, r := range page.Recordings {
		byID[r.ID] = r
	}
	if got := byID[idA1].SeriesID; got != "show-a" {
		t.Fatalf("A1 seriesId = %q", got)
	}
	if got := byID[idA2].SeriesID; got != "show-a" {
		t.Fatalf("A2 seriesId = %q", got)
	}
	if got := byID[idB].SeriesID; got != "show-b" {
		t.Fatalf("B seriesId = %q", got)
	}
	if got := byID[idNone].SeriesID; got != "" {
		t.Fatalf("unmetadata seriesId = %q, want none", got)
	}
	if got := byID[idA1].SeriesTitle; got != "Show A" {
		t.Fatalf("A1 seriesTitle = %q", got)
	}
	if got := byID[idB].SeriesTitle; got != "Show B" {
		t.Fatalf("B seriesTitle = %q", got)
	}
	if got := byID[idNone].SeriesTitle; got != "" {
		t.Fatalf("unmetadata seriesTitle = %q, want none", got)
	}
	// Detail carries the same grouping.
	detail, err := s.Detail(ctx, auth, owner, idA1, "")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Recording.SeriesID != "show-a" || detail.Recording.SeriesTitle != "Show A" {
		t.Fatalf("detail grouping = %q %q", detail.Recording.SeriesID, detail.Recording.SeriesTitle)
	}
}

func TestDVRGroupsSummaryTitleRulesAndRetainedPictures(t *testing.T) {
	ctx := context.Background()
	s, source, p, a := sharingStore(t)
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	config := RuleConfig{Name: p.Title, SourceID: source.ID, MatchKind: "title", Title: p.Title, ChannelID: p.ChannelID, Enabled: true, Episodes: "all", AllowedChannels: []string{}, BlockedChannels: []string{}, Keywords: []string{}}
	rule, e := s.SaveRule(ctx, a, owner, RuleInput{Mutation: Mutation{RequestID: strings.Repeat("1", 48)}, ID: strings.Repeat("2", 64), Anchor: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}, Config: config})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		worked, e := s.ReconcileOne(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			break
		}
	}
	page, e := s.List(ctx, a, owner, Query{State: "upcoming", Limit: 50})
	if e != nil || len(page.Recordings) != 1 {
		t.Fatalf("runnable title rule: %+v %v", page, e)
	}
	recording := page.Recordings[0]
	if recording.Programme.SeriesID != "" {
		t.Fatal("fabricated provider id")
	}
	rules, e := s.List(ctx, a, owner, Query{State: "rules", Limit: 50})
	if e != nil || len(rules.Rules) != 1 || rules.Rules[0].Summary.Upcoming != 1 || rules.Rules[0].Summary.Next == nil {
		t.Fatalf("rule summary %+v %v", rules, e)
	}
	config.Enabled = false
	rule, e = s.SaveRule(ctx, a, owner, RuleInput{Mutation: Mutation{RequestID: strings.Repeat("3", 48), ExpectedRevision: rule.Revision}, ID: rule.ID, Config: config})
	if e != nil {
		t.Fatal(e)
	}
	rules, e = s.List(ctx, a, owner, Query{State: "rules", Limit: 50})
	if e != nil || rules.Rules[0].Summary.MatchingTonight != 1 {
		t.Fatalf("paused rule summary %+v %v", rules, e)
	}
	// A late image import is retained by the recording, independently of the
	// provider generation. GC must preserve both the image row and byte digest.
	digest := strings.Repeat("4", 64)
	url := "https://images.invalid/program.png"
	if _, e = s.db.Exec(`INSERT INTO live_programme_icons(generation_id,programme_id,url) VALUES(?,?,?)`, source.Generation, p.ID, url); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) VALUES(?,?,?,'image/png',16,16,10,16,16,20,1)`, url, digest, digest); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`DELETE FROM live_programme_icons WHERE generation_id=?`, source.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`DELETE FROM live_programme_images WHERE url=?`, url); e != nil {
		t.Fatal(e)
	}
	image, mime, e := s.ImageRendition(ctx, a, owner, recording.ID, false)
	if e != nil || image != digest || mime != "image/png" {
		t.Fatalf("retained picture %q %v", image, e)
	}
	if _, _, e = s.ImageRendition(ctx, a, livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "other"}, recording.ID, true); e == nil {
		t.Fatal("foreign profile image")
	}
	if _, e = s.db.Exec(`UPDATE dvr_recordings SET state='completed' WHERE id=?`, recording.ID); e != nil {
		t.Fatal(e)
	}
	summary, e := s.Summary(ctx, a, owner)
	if e != nil || summary.Recorded != 1 || summary.Groups != 1 || summary.Rules != 1 {
		t.Fatalf("summary %+v %v", summary, e)
	}
	groups, e := s.Groups(ctx, a, owner, "recent", "", 1)
	if e != nil || len(groups.Groups) != 1 || groups.Groups[0].Recordings != 1 || !strings.HasPrefix(groups.Groups[0].Key, "title:") {
		t.Fatalf("groups %+v %v", groups, e)
	}
	episodes, e := s.List(ctx, a, owner, Query{State: "recorded", Sort: "title", Group: groups.Groups[0].Key, Limit: 50})
	if e != nil || len(episodes.Recordings) != 1 || episodes.Recordings[0].Programme.Image != "/v1/dvr/recordings/"+recording.ID+"/image?sd=16x16.10."+digest[:16]+"&ld=16x16.20."+digest[:16] {
		t.Fatalf("group episodes %+v %v", episodes, e)
	}
	if _, e = s.db.Exec(`UPDATE dvr_recordings SET state='deleted' WHERE id=?`, recording.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.ImageRendition(ctx, a, owner, recording.ID, false); e == nil {
		t.Fatal("deleted image remained visible")
	}
}

func TestDVRUpcomingNamesConflictsFirstAndFencesCompetitionChanges(t *testing.T) {
	ctx := context.Background()
	s, src, p, a := sharingStore(t)
	o := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	high, e := s.Schedule(ctx, a, o, ScheduleInput{RequestID: strings.Repeat("5", 48), Occurrence: Occurrence{src.ID, p.ChannelID, src.Generation, p.ID}, Options: Options{Priority: 10}})
	if e != nil {
		t.Fatal(e)
	}
	channel, programme := strings.Repeat("6", 64), strings.Repeat("7", 64)
	if _, e = s.db.Exec(`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) SELECT generation_id,?,'two','Two','2',group_name,position+1,locator_sealed,available_start,available_end FROM live_channel_versions WHERE generation_id=? AND channel_id=?`, channel, src.Generation, p.ChannelID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO live_programmes SELECT generation_id,?,?,'two','Competing program',start_utc,end_utc,lineage FROM live_programmes WHERE generation_id=? AND id=?`, programme, channel, src.Generation, p.ID); e != nil {
		t.Fatal(e)
	}
	low, e := s.Schedule(ctx, a, o, ScheduleInput{RequestID: strings.Repeat("8", 48), Occurrence: Occurrence{src.ID, channel, src.Generation, programme}})
	if e != nil {
		t.Fatal(e)
	}
	page, e := s.List(ctx, a, o, Query{State: "upcoming", Limit: 1})
	if e != nil || len(page.Recordings) != 1 || page.Recordings[0].ID != low.ID || page.Recordings[0].State != "conflicted" || len(page.Recordings[0].Overlaps) != 1 || page.Recordings[0].Overlaps[0].ID != high.ID || page.Recordings[0].Overlaps[0].Channel.Name != "One" || page.NextCursor == "" {
		t.Fatalf("conflicts-first inline names %+v %v", page, e)
	}
	summary, e := s.Summary(ctx, a, o)
	if e != nil || summary.Conflicts != 1 {
		t.Fatalf("conflict summary %+v %v", summary, e)
	}
	if _, e = s.db.Exec(`UPDATE live_reservations SET priority=-10 WHERE id=?`, high.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(ctx, a, o, Query{State: "upcoming", Limit: 1, Cursor: page.NextCursor}); !errors.Is(e, livechannels.ErrCursor) {
		t.Fatal("competition changed without cursor refusal", e)
	}
	foreign := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "other"}
	other, e := s.Schedule(ctx, a, foreign, ScheduleInput{RequestID: strings.Repeat("9", 48), Occurrence: Occurrence{src.ID, p.ChannelID, src.Generation, p.ID}, Options: Options{Priority: 20}})
	if e != nil {
		t.Fatal(e)
	}
	detail, e := s.Detail(ctx, a, o, low.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, overlap := range detail.Overlaps {
		if overlap.ID == other.ID {
			t.Fatal("foreign profile conflict details leaked")
		}
	}
}
