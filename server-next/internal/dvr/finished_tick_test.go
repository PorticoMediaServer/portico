package dvr

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A tick's upcoming list can outlive the recording worker. Once that worker has
// finished and left the running map, its old snapshot must not start it again.
func TestATickCannotRestartARecordingThatFinishedAfterItsRead(t *testing.T) {
	ctx, now := t.Context(), time.Now()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	owner, err := d.CreateOwner(ctx, db.NewOwner{Username: "owner", PasswordHash: "test-hash", ProfileName: "Owner", Now: now,
		Session: db.NewSession{ID: db.NewSessionID(), RefreshHash: "test-refresh", ExpiresAt: now.Add(time.Hour),
			Device: db.DeviceInfo{InstallationID: "test", Name: "Test", Platform: "web", App: "web", AppVersion: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := d.Profiles(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := d.CreateLiveSource(ctx, owner.UserID, db.LiveSourceSave{Kind: "playlist", Name: "Test", Config: "{}", Audience: "everyone",
		Secret: []byte{}, TunerCount: 1, IdempotencyKey: "source", RequestHash: "source", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ImportSourceChannels(ctx, source.ID, []db.SourceChannel{{Key: "one", Name: "One", Number: 1, BroughtIn: true}}, now); err != nil {
		t.Fatal(err)
	}
	channels, err := d.SourceChannels(ctx, source.ID, 0, 0, 10)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels: %+v %v", channels, err)
	}
	rec, _, err := d.BookAiring(ctx, db.Booking{ProfileID: profiles[0].ID, ProfileName: "Owner", ProfilePub: profiles[0].PublicID,
		ChannelID: channels[0].ID, ChannelName: "One", ProgrammeKey: "now", Title: "News", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RecordingStarted(ctx, rec.ID, "", now); err != nil {
		t.Fatal(err)
	}
	upcoming, err := d.Upcoming(ctx, now.Add(2*time.Hour))
	if err != nil || len(upcoming) != 1 {
		t.Fatalf("upcoming: %+v %v", upcoming, err)
	}
	if err := d.RecordingFinished(ctx, rec.ID, db.RecordingRecorded, "Finished before the tick reached it", now); err != nil {
		t.Fatal(err)
	}
	// The destination/floor changed after it finished. Entering start again would overwrite
	// its recorded state with a new failure, exactly what a stale background tick must avoid.
	folder := t.TempDir()
	s := &Service{db: d, now: func() time.Time { return now }, options: func() Options { return Options{Folder: folder, Floor: math.MaxInt64} }}
	s.start(ctx, upcoming[0], upcoming[0].EndsAt)
	got, err := d.Recording(ctx, rec.PublicID)
	if err != nil || got.State != db.RecordingRecorded || got.Reason != "Finished before the tick reached it" {
		t.Fatalf("a stale tick overwrote the finished recording: %+v (%v)", got, err)
	}
}
