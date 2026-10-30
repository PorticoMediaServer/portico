package livechannels

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"strings"
	"testing"
)

func TestHomeChannelMarksUseWholeSourceAndStayIndependentOfOrder(t *testing.T) {
	names := []markName{{"s", "n", "Northwind News", "101"}, {"s", "n24", "Northwind News 24", "102"}, {"s", "s", "Summit Sports", "201"}, {"s", "sse", "Summit Sports Extra", "202"}, {"s", "t", "Tidal Music TV", "601"}, {"s", "h", "Harbor City Local", "701"}, {"other", "n", "Northwind News 24", "1"}}
	got := channelMarks(names)
	for key, want := range map[string]string{"s\x00n": "NN", "s\x00n24": "N24", "s\x00s": "SS", "s\x00sse": "SSE", "s\x00t": "TM", "s\x00h": "HC", "other\x00n": "NN"} {
		if got[key].Text != want {
			t.Fatalf("%s: %+v want %s", key, got[key], want)
		}
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	if !reflect.DeepEqual(got, channelMarks(names)) {
		t.Fatal("marks depend on input order")
	}
}

func TestHomeFavoritesOrderAndMarksBeforeLimitOnTwoThousandChannels(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	input := testInput()
	input.Guide = "<tv/>"
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	for i := 1999; i >= 0; i-- {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"c%d\" tvg-chno=\"%d\" group-title=\"News\",Channel %d\nhttps://fixture.invalid/%d\n", i, i+1, i, i)
	}
	input.Playlist = playlist.String()
	saved, err := s.Save(ctx, testAuthority("owner", true, true), input)
	if err != nil {
		t.Fatal(err)
	}
	viewer := Owner{Authority: "local", AccountID: "owner", ProfileID: "p"}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "f", AllowedLiveSources: []string{saved.ID}, Viewer: viewer, LibraryRows: []DirectoryRow{}}, nil
	}
	all, err := s.ChannelDirectory(ctx, authority, DirectoryQuery{Kind: "all", Sort: "favorites", Limit: 1, Offset: 1999})
	if err != nil {
		t.Fatal(err)
	}
	favorite := all.Channels[0]
	if _, err = s.SetPreference(ctx, testAuthority("owner", true, true), viewer, PreferenceInput{SourceID: saved.ID, ChannelID: favorite.ID, Favorite: true, RequestID: strings.Repeat("de", 24)}); err != nil {
		t.Fatal(err)
	}
	measured, cost := dbwork.Measure(ctx)
	page, err := s.ChannelDirectory(measured, authority, DirectoryQuery{Kind: "all", Sort: "favorites", Limit: 4})
	if err != nil || page.Total != 2000 || len(page.Channels) != 4 || page.Channels[0].ID != favorite.ID || page.Channels[1].Number != "1" || page.Channels[3].Number != "3" {
		t.Fatalf("favorites must sort before LIMIT %+v %v", page, err)
	}
	if cost().Statements > 16 {
		t.Fatalf("directory query count grows per card: %+v", cost())
	}
	firstMark := page.Channels[1].Mark
	if firstMark == nil || firstMark.Seed == "" {
		t.Fatal("no authoritative mark")
	}
	filtered, err := s.ChannelDirectory(ctx, authority, DirectoryQuery{Kind: "all", Sort: "favorites", Limit: 1, Search: "Channel 0"})
	if err != nil || len(filtered.Channels) != 1 || !reflect.DeepEqual(firstMark, filtered.Channels[0].Mark) {
		t.Fatal("filtered card changed mark", filtered, err)
	}
	_, err = s.ChannelDirectory(ctx, authority, DirectoryQuery{Kind: "all", Sort: "favorites", Limit: 4, Offset: 4, Revision: all.Revision})
	if err != ErrCursor {
		t.Fatal("favorites revision did not fence old pages", err)
	}
}
