package dvr

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

type sharingDriver struct{ reclaimingDriver }

func (*sharingDriver) Share(context.Context, CaptureRequest, CaptureRequest, CaptureCheckpoint) (CaptureResult, error) {
	return CaptureResult{}, ErrUnavailable
}
func sharingStore(t *testing.T) (*Store, livechannels.Source, livechannels.Programme, livechannels.Authority) {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	recordingProfiles(t, db, "account", "profile", "0", "1", "other", "other-profile")
	live, e := livechannels.New(db)
	if e != nil {
		t.Fatal(e)
	}
	a := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(-time.Minute), now.Add(time.Hour)
	in := livechannels.SourceInput{ID: strings.Repeat("a", 48), RequestID: strings.Repeat("b", 48), Name: "Antenna", TunerCount: 1, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://provider.invalid/one\n", Guide: fmt.Sprintf(`<tv><programme channel="one" start="%s" stop="%s"><title>Show</title><sub-title>The episode</sub-title><episode-num system="xmltv_ns">1.4.</episode-num><desc>A description.</desc></programme></tv>`, start.Format("20060102150405 -0700"), end.Format("20060102150405 -0700"))}
	source, e := live.Save(context.Background(), a, in)
	if e != nil {
		t.Fatal(e)
	}
	guide, e := live.Guide(context.Background(), a, livechannels.GuideQuery{Kind: livechannels.LiveSource, Start: start, End: end, Timezone: "UTC", Limit: 50})
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db, live, func(context.Context, *sql.Tx, livechannels.Owner, string, string) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	s.captureAvailable = true
	s.driver = &sharingDriver{}
	s.instance = "fixture"
	return s, source, guide.Channels[0].Programmes[0], a
}
func TestTwoProfilesScheduleOnePhysicalCapture(t *testing.T) {
	s, source, p, a := sharingStore(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: fmt.Sprint(i)}
		_, e := s.Schedule(ctx, a, owner, ScheduleInput{RequestID: strings.Repeat(fmt.Sprint(i+1), 48), Occurrence: Occurrence{SourceID: source.ID, ChannelID: p.ChannelID, Generation: source.Generation, ProgrammeID: p.ID}, Options: Options{}})
		if e != nil {
			t.Fatal(e)
		}
	}
	first, found, e := s.claimNext(ctx)
	if e != nil || !found || first == nil {
		t.Fatalf("leader: %+v %v", first, e)
	}
	second, found, e := s.claimNext(ctx)
	if e != nil || !found || second != nil {
		t.Fatalf("follower should wait: %+v %v", second, e)
	}
	var allocations, shares int
	if e = s.db.QueryRow(`SELECT count(*) FROM live_allocations WHERE state='active'`).Scan(&allocations); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM dvr_recording_shares`).Scan(&shares); e != nil {
		t.Fatal(e)
	}
	if allocations != 1 || shares != 1 {
		t.Fatalf("allocations=%d shares=%d", allocations, shares)
	}
}
func TestRecordedEpisodePublishesShowSeasonAndGuideFacts(t *testing.T) {
	s, source, p, a := sharingStore(t)
	ctx := context.Background()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	_, e := s.Schedule(ctx, a, owner, ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}})
	if e != nil {
		t.Fatal(e)
	}
	c, _, e := s.claimNext(ctx)
	if e != nil || c == nil {
		t.Fatalf("claim: %v", e)
	}
	root := filepath.Join(t.TempDir(), owner.Key())
	hash := strings.Repeat("d", 64)
	result := CaptureResult{CaptureCheckpoint: CaptureCheckpoint{Object: mediaartifact.Object{Digest: hash, Size: 1880}, First: time.Now().Add(-time.Minute), Last: time.Now(), Gaps: 1, WriterRetired: true}, Root: root, Path: filepath.Join(root, "captures", c.request.Recording.ID, "objects", hash), Container: "mpegts", VideoCodec: "mpeg2video", Duration: 60, Width: 160, Height: 90}
	if e = s.publish(ctx, c, result); e != nil {
		t.Fatal(e)
	}
	var title, description, show string
	var season, episode, kind int
	e = s.db.QueryRow(`SELECT e.title,d.overview,sh.title,se.number,ep.number,e.kind FROM dvr_catalog_provenance p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_episodes ep ON ep.entity_id=e.id JOIN catalog_entities sh ON sh.id=ep.show_id JOIN catalog_seasons se ON se.entity_id=ep.season_id JOIN catalog_item_details d ON d.entity_id=e.id`).Scan(&title, &description, &show, &season, &episode, &kind)
	if e != nil {
		t.Fatal(e)
	}
	if title != "The episode" || show != "Show" || season != 2 || episode != 5 || kind != 4 || !strings.Contains(description, "A description.") {
		t.Fatalf("%s/%s %d/%d kind %d %s", show, title, season, episode, kind, description)
	}
}

func TestOneTimeRecordingFollowsUniqueGuideCorrection(t *testing.T) {
	s, source, p, a := sharingStore(t)
	ctx := context.Background()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	r, e := s.Schedule(ctx, a, owner, ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}})
	if e != nil {
		t.Fatal(e)
	}
	oldStart, _ := time.Parse(time.RFC3339Nano, p.Start)
	oldEnd, _ := time.Parse(time.RFC3339Nano, p.End)
	in := livechannels.SourceInput{ID: source.ID, ExpectedRevision: source.Revision, RequestID: strings.Repeat("d", 48), Name: source.Name, TunerCount: 1, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://provider.invalid/one\n", Guide: fmt.Sprintf(`<tv><programme channel="one" start="%s" stop="%s"><title>Show</title><sub-title>The episode</sub-title><episode-num system="xmltv_ns">1.4.</episode-num><desc>A description.</desc></programme></tv>`, oldStart.Add(5*time.Minute).Format("20060102150405 -0700"), oldEnd.Add(5*time.Minute).Format("20060102150405 -0700"))}
	if _, e = s.live.Save(ctx, a, in); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileScheduled(ctx); e != nil {
		t.Fatal(e)
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	updated, e := loadTx(ctx, tx, owner, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if updated.State != "scheduled" || updated.Start != oldStart.Add(5*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("%+v", updated)
	}
}

// recordingProfiles makes the fixture's owners real profiles whose Recordings
// switch is on: who may record is the profile's own switch, and a profile that
// does not exist records nothing.
func recordingProfiles(t *testing.T, db *sql.DB, account string, profiles ...string) {
	t.Helper()
	if _, e := db.Exec(`INSERT OR IGNORE INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, account, account, profiles[0]); e != nil {
		t.Fatal(e)
	}
	for _, profile := range profiles {
		if _, e := db.Exec(`INSERT OR IGNORE INTO direct_profiles(id,account_id,name) VALUES(?,?,?)`, profile, account, profile); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES(?,1) ON CONFLICT(profile_id) DO UPDATE SET allow_dvr=1`, profile); e != nil {
			t.Fatal(e)
		}
	}
}
