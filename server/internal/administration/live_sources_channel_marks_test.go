package administration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestChannelMapMarksMatchDirectoryAcrossPagesFiltersAndAuthority(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	input := livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Marks", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\" group-title=\"News\",Northwind News\nhttps://fixture.invalid/one\n#EXTINF:-1 tvg-id=\"two\" tvg-chno=\"2\" group-title=\"News\",Northwind News\nhttps://fixture.invalid/two\n#EXTINF:-1 tvg-id=\"three\" tvg-chno=\"3\" group-title=\"Other\",Northwind News 24\nhttps://fixture.invalid/three\n"}
	source, err := live.Save(ctx, authority, input)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.ChannelMap(ctx, allow, source.ID, "", "", 100)
	if err != nil || len(full.Items) != 3 {
		t.Fatal(full, err)
	}
	// A hidden collision remains in the domain. The effective override supplies
	// its suffix; the provider's number must not win.
	doc, err := s.LiveSourceConfiguration(ctx, allow, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.SaveChannelMap(ctx, allow, source.ID, ChannelMapChange{ExpectedRevision: doc.Revision, OperationID: "marks-hide-one", Overrides: []ChannelMapOverride{{ChannelID: full.Items[0].ChannelID, Number: "9", Hidden: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Items[0].Mark.Text != "NN 9" {
		t.Fatalf("write receipt mark: %+v", receipt.Items[0])
	}
	full, err = s.ChannelMap(ctx, allow, source.ID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]livechannels.ChannelMark{}
	for _, c := range full.Items {
		want[c.ChannelID] = c.Mark
		if c.Mark.Seed != source.ID+":"+c.ChannelID || c.Mark.Text == "" {
			t.Fatal(c)
		}
	}
	if full.Items[1].Mark.Text != "NN 2" || full.Items[2].Mark.Text != "N24" {
		t.Fatal(full.Items)
	}
	cursor := ""
	for i := 0; i < 3; i++ {
		page, e := s.ChannelMap(ctx, allow, source.ID, cursor, "", 1)
		if e != nil || len(page.Items) != 1 || page.Items[0].Mark != want[page.Items[0].ChannelID] {
			t.Fatal(page, e)
		}
		exact, e := s.ChannelMap(ctx, allow, source.ID, "", page.Items[0].ChannelID, 1)
		if e != nil || len(exact.Items) != 1 || exact.Items[0].Mark != page.Items[0].Mark {
			t.Fatal(exact, e)
		}
		cursor = page.NextCursor
	}
	if cursor != "" {
		t.Fatal(cursor)
	}
	viewer := livechannels.Owner{Authority: "local", AccountID: "marks-viewer", ProfileID: "main"}
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO live_channel_preferences VALUES(?,?,?,?,?,1,0,1)`, viewer.Authority, viewer.AccountID, viewer.ProfileID, source.ID, full.Items[1].ChannelID); err != nil {
		t.Fatal(err)
	}
	for _, fence := range []string{"owner", "restricted-viewer"} {
		scope := func(context.Context, *sql.Tx) (livechannels.DirectoryScope, error) {
			return livechannels.DirectoryScope{Fence: fence, Viewer: viewer, AllowedLiveSources: []string{source.ID}}, nil
		}
		for _, q := range []livechannels.DirectoryQuery{
			{Kind: "all", Sort: "name", Limit: 1},
			{Kind: "all", Sort: "number", Offset: 1, Limit: 1},
			{Kind: "all", Sort: "name", FavoritesOnly: true, Limit: 1},
			{Kind: "all", Sort: "name", Search: "24", Group: "Other", Limit: 1},
		} {
			directory, e := live.ChannelDirectory(ctx, scope, q)
			if e != nil || len(directory.Channels) != 1 {
				t.Fatal(directory, e)
			}
			for _, c := range directory.Channels {
				if c.ID == full.Items[0].ChannelID || c.Mark == nil || *c.Mark != want[c.ID] {
					t.Fatalf("filtered directory: %+v", c)
				}
			}
		}
	}
	// Guide channel-level authority can list just one collision while the mark
	// still agrees with the owner's full map.
	q := livechannels.GuideQuery{NoProgrammes: true, Limit: 10, Kind: livechannels.LiveSource, Start: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC), Timezone: "UTC"}
	q.MarkChannels = func(ctx context.Context, tx *sql.Tx, channels []livechannels.Channel) error {
		return live.ChannelMarksTx(ctx, tx, func(context.Context, *sql.Tx) (livechannels.DirectoryScope, error) {
			return livechannels.DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{source.ID}}, nil
		}, channels)
	}
	restricted := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "viewer", func(_ string, channel string) bool { return channel == "" || channel == full.Items[1].ChannelID }, nil
	}
	guide, e := live.Guide(ctx, restricted, q)
	if e != nil || len(guide.Channels) != 1 || guide.Channels[0].Mark == nil || *guide.Channels[0].Mark != want[guide.Channels[0].ID] {
		t.Fatal(guide, e)
	}
	// Revision change invalidates the warm cache and also handles reset/removal.
	doc, err = s.LiveSourceConfiguration(ctx, allow, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveChannelMap(ctx, allow, source.ID, ChannelMapChange{ExpectedRevision: doc.Revision, OperationID: "marks-reset-one", Overrides: []ChannelMapOverride{{ChannelID: full.Items[0].ChannelID}}})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := s.ChannelMap(ctx, allow, source.ID, "", full.Items[0].ChannelID, 1)
	if err != nil || reset.Items[0].Mark.Text != "NN 1" {
		t.Fatal(reset, err)
	}
	// Replacing the active generation changes the domain even at the same
	// override revision, while preserving the existing source:channel seed.
	input.RequestID = strings.Repeat("ef", 24)
	input.ExpectedRevision = source.Revision
	input.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",Northwind News\nhttps://fixture.invalid/one\n"
	_, err = live.Save(ctx, authority, input)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := s.ChannelMap(ctx, allow, source.ID, "", "", 100)
	if err != nil || len(replaced.Items) != 1 || replaced.Items[0].Mark.Text != "NN" || replaced.Items[0].Mark.Seed != full.Items[0].Mark.Seed {
		t.Fatal(replaced, err)
	}
}

// A channel-map read reports the same combined revision a write is fenced by,
// even after the source itself has moved (enabled, refreshed, reconfigured), so
// an exact lookup followed by Hide never conflicts with itself.
func TestChannelMapRevisionMatchesItsWriteFence(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source, err := live.Save(ctx, authority, livechannels.SourceInput{ID: strings.Repeat("ef", 24), RequestID: strings.Repeat("aa", 24), Name: "Fence", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",One\nhttps://fixture.invalid/one\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE live_sources SET revision=revision+3 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.ChannelMap(ctx, allow, source.ID, "", "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	exact, err := s.ChannelMap(ctx, allow, source.ID, "", page.Items[0].ChannelID, 1)
	if err != nil || exact.Revision != page.Revision {
		t.Fatal(exact, err)
	}
	saved, err := s.SaveChannelMap(ctx, allow, source.ID, ChannelMapChange{ExpectedRevision: exact.Revision, OperationID: "fence-hide", Overrides: []ChannelMapOverride{{ChannelID: exact.Items[0].ChannelID, Hidden: true}}})
	if err != nil || !saved.Items[0].Hidden || saved.Items[0].Mark.Text == "" || saved.Revision != exact.Revision+1 {
		t.Fatal(saved, err)
	}
}
