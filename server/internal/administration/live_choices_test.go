package administration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestSourceChoicesRetainExcludedGroupsAndPageChannels(t *testing.T) {
	s, db, _ := newService(t)
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source, err := store.Save(context.Background(), auth, livechannels.SourceInput{ID: strings.Repeat("ab", 24), Name: "Choices", TunerCount: 1, RequestID: strings.Repeat("cd", 24), EnabledGroups: []string{"News"}, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" group-title=\"News\",News\nhttps://fixture.invalid/news\n#EXTINF:-1 tvg-id=\"sport\" group-title=\"Sports\",Sports\nhttps://fixture.invalid/sport\n"})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := s.SourceChoices(context.Background(), allow, source.ID, "groups", "", "", 1)
	if err != nil || groups.Total != 2 || len(groups.Items) != 1 || groups.NextCursor == "" {
		t.Fatal(groups, err)
	}
	next, err := s.SourceChoices(context.Background(), allow, source.ID, "groups", groups.NextCursor, "", 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].Name != "Sports" {
		t.Fatal(next, err)
	}
	channels, err := s.SourceChoices(context.Background(), allow, source.ID, "channels", "", "Sports", 20)
	if err != nil || len(channels.Items) != 1 || channels.Items[0].ID != "sport" {
		t.Fatal(channels, err)
	}
}
