package playbackruntime

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// What a live session is fenced on must hold still across a guide refresh that
// leaves its channel's stream alone, and must move when the stream's address
// or the source's tuner limit does.
func TestLiveSourceFenceSurvivesAGuideRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.Open(filepath.Join(t.TempDir(), "guide.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	live, e := livechannels.New(db)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	owner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fixture", func(string, string) bool { return true }, nil
	}
	in := livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("c0", 24), Name: "Aerial", TunerCount: 2,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",Channel one\nhttps://provider.invalid/one\n",
		Guide:    `<?xml version="1.0"?><tv><channel id="one"/><programme channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Afternoon</title></programme></tv>`}
	publish := func(request string, change func(*livechannels.SourceInput)) (fence string, source livechannels.Source) {
		t.Helper()
		in.RequestID = strings.Repeat(request, 24)
		if change != nil {
			change(&in)
		}
		source, e := live.Save(ctx, owner, in)
		if e != nil {
			t.Fatal(e)
		}
		in.ExpectedRevision = source.Revision
		gated, e := dbwork.BeginSnapshot(ctx, db)
		if e != nil {
			t.Fatal(e)
		}
		defer gated.Rollback()
		var channel string
		var tuners int
		if e = gated.Tx().QueryRowContext(ctx, `SELECT c.channel_id,s.tuner_count FROM live_sources s JOIN live_channel_versions c ON c.generation_id=s.active_generation WHERE s.id=?`, source.ID).Scan(&channel, &tuners); e != nil {
			t.Fatal(e)
		}
		ref := playback.LinearReference{Kind: "live-source", SourceID: source.ID, ChannelID: channel, Generation: source.Generation}
		// With no readable input the fence is the source revision, as it always was.
		if without := liveSourceFence(ctx, gated.Tx(), nil, ref, source.Revision, tuners); without != strconv.FormatInt(source.Revision, 10) {
			t.Fatalf("without a source store the fence is not the source revision: %q", without)
		}
		return liveSourceFence(ctx, gated.Tx(), live, ref, source.Revision, tuners), source
	}
	first, published := publish("c1", nil)
	refreshed, republished := publish("c2", func(v *livechannels.SourceInput) { v.Guide = strings.Replace(v.Guide, "Afternoon", "Evening", 1) })
	if republished.Generation == published.Generation || republished.Revision == published.Revision {
		t.Fatal("the refresh did not publish a new generation and revision")
	}
	if refreshed != first {
		t.Fatalf("a guide refresh moved the fence of an unchanged stream: %q then %q", first, refreshed)
	}
	if retuned, _ := publish("c3", func(v *livechannels.SourceInput) { v.TunerCount = 1 }); retuned == first {
		t.Fatal("a changed tuner limit did not move the fence")
	}
	narrowed, _ := publish("c4", nil)
	if moved, _ := publish("c5", func(v *livechannels.SourceInput) {
		v.Playlist = strings.Replace(v.Playlist, "https://provider.invalid/one", "https://provider.invalid/moved", 1)
	}); moved == narrowed {
		t.Fatal("a changed stream address did not move the fence")
	}
}
