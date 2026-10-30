package dvr

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// FEAT-08: a recording row names its channel. The current guide's name wins
// (a rename shows at once); a channel gone from every guide leaves it empty.
func TestRecordingChannelNameFollowsTheCurrentGuide(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	owner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fence", func(string, string) bool { return true }, nil
	}
	playlist := func(name string) string {
		return "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"7\"," + name + "\nhttps://provider.invalid/one\n"
	}
	in := livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Antenna", TunerCount: 1, Playlist: playlist("Channel Seven")}
	first, err := live.Save(ctx, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	var channel string
	if err = db.QueryRow(`SELECT channel_id FROM live_channel_versions WHERE generation_id=?`, first.Generation).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	read := func(generation string) RecordingChannel {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		r := Recording{Occurrence: Occurrence{SourceID: first.ID, ChannelID: channel, Generation: generation}}
		if err = channelTx(ctx, tx, &r); err != nil {
			t.Fatal(err)
		}
		return r.Channel
	}
	if got := read(first.Generation); got.Name != "Channel Seven" || got.Number != "7" || got.ID != channel {
		t.Fatalf("channel %+v", got)
	}
	// A renamed channel: the recording scheduled from the old guide shows the
	// name the owner sees today.
	in.RequestID, in.ExpectedRevision, in.Playlist = strings.Repeat("ef", 24), first.Revision, playlist("Seven HD")
	if _, err = live.Save(ctx, owner, in); err != nil {
		t.Fatal(err)
	}
	if got := read(first.Generation); got.Name != "Seven HD" {
		t.Fatalf("after rename %+v", got)
	}
	r := Recording{Occurrence: Occurrence{SourceID: first.ID, ChannelID: "gone", Generation: first.Generation}}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = channelTx(ctx, tx, &r); err != nil || r.Channel.Name != "" || r.Channel.ID != "gone" {
		t.Fatalf("missing channel %+v %v", r.Channel, err)
	}
}

// FEAT-08 (M7 Q4b): a published recording reports the owner's watched state;
// one not yet published reports none.
func TestRecordingWatchedFollowsPersonalState(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	key := persistence.PersonalOwnerKey(owner.Authority, owner.AccountID, owner.ProfileID)
	c := catalogtest.New(t, db)
	films := c.Library("rec", "Recorded TV", "movie", "/rec")
	watched := c.Movie(films, "/rec/one.mkv", "One", 0)
	fresh := c.Movie(films, "/rec/two.mkv", "Two", 0)
	c.Drain()
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES(?,?,15000,'')`, key, fresh.ID)
	c.Exec(`UPDATE catalog_assets SET duration=3600 WHERE id IN(SELECT asset_id FROM catalog_asset_links WHERE entity_id=?)`, fresh.ID)
	c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,1,'2026-09-24T00:00:00Z')`, key, watched.ID)
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watched,revision) VALUES(?,?,1,1)`, key, watched.ID)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, v := range []struct {
		item string
		want *bool
	}{{"", nil}, {watched.Public, ptr(true)}, {fresh.Public, ptr(false)}} {
		r := Recording{ItemID: v.item}
		if err = decorateTx(ctx, tx, owner, &r); err != nil {
			t.Fatal(err)
		}
		if v.item == fresh.Public && (r.PositionMs != 15000 || r.DurationMs != 3600000) {
			t.Fatalf("recording progress/duration lost: %+v", r)
		}
		if (r.Watched == nil) != (v.want == nil) || r.Watched != nil && *r.Watched != *v.want {
			t.Fatalf("%q watched = %v, want %v", v.item, r.Watched, v.want)
		}
	}
}

func ptr(v bool) *bool { return &v }
