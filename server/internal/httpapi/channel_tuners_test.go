package httpapi

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"strings"
	"testing"
	"time"
)

func TestLiveStartAdmissionSharesChannelsAndKeepsRetiringCapacity(t *testing.T) {
	d, _, _ := liveHTTPFixture(t)
	ctx := context.Background()
	s, e := livechannels.New(d.DB)
	if e != nil {
		t.Fatal(e)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source, e := s.Save(ctx, authority, livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Tuner", TunerCount: 1, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://provider.invalid/one\n"})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := d.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, reader := range []string{"first", "second"} {
		_, e = livechannels.AcquireTx(ctx, tx, livechannels.Allocation{SourceID: source.ID, ChannelID: "one", ResourceID: reader, Kind: "live", Owner: livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: reader}}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
	}
	if ok, e := liveTunerAvailable(ctx, tx, source.ID, "one", ""); e != nil || !ok {
		t.Fatalf("shared viewer refused: %v %v", ok, e)
	}
	if ok, e := liveTunerAvailable(ctx, tx, source.ID, "two", ""); e != nil || ok {
		t.Fatalf("another channel admitted: %v %v", ok, e)
	}
	if _, e = tx.Exec(`UPDATE live_allocations SET lease_until_ms=0`); e != nil {
		t.Fatal(e)
	}
	if ok, e := liveTunerAvailable(ctx, tx, source.ID, "one", ""); e != nil || ok {
		t.Fatalf("retiring input reused: %v %v", ok, e)
	}
}
