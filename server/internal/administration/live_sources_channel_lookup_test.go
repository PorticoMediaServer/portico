package administration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func channelLookupSource(t *testing.T, db *sql.DB) string {
	t.Helper()
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	source, err := store.Save(context.Background(), authority, livechannels.SourceInput{
		ID: strings.Repeat("ab", 24), Name: "Lookup", TunerCount: 1, RequestID: strings.Repeat("cd", 24),
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\" group-title=\"News\",One\nhttps://fixture.invalid/one\n#EXTINF:-1 tvg-id=\"two\" tvg-chno=\"2\" group-title=\"News\",Two\nhttps://fixture.invalid/two\n#EXTINF:-1 tvg-id=\"three\" tvg-chno=\"3\" group-title=\"News\",Three\nhttps://fixture.invalid/three\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return source.ID
}

func TestChannelMapExactLookupMatchesPageEntry(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	source := channelLookupSource(t, db)
	page, err := s.ChannelMap(ctx, allow, source, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var target ChannelMapEntry
	for _, entry := range page.Items {
		if entry.Name == "Two" {
			target = entry
		}
	}
	if target.ChannelID == "" {
		t.Fatalf("fixture page %+v", page)
	}
	before, err := s.LiveSourceConfiguration(ctx, allow, source)
	if err != nil {
		t.Fatal(err)
	}
	// One override exercises every entry field the exact lookup must preserve:
	// the served number, the guide mapping and the hidden state.
	if _, err = s.SaveChannelMap(ctx, allow, source, ChannelMapChange{
		ExpectedRevision: before.Revision, OperationID: "lookup-override-1",
		Overrides: []ChannelMapOverride{{ChannelID: target.ChannelID, Number: "12", GuideChannelID: "guide-two", Hidden: true}},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := s.LiveSourceConfiguration(ctx, allow, source)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("override revision %d after %d", after.Revision, before.Revision)
	}

	page, err = s.ChannelMap(ctx, allow, source, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var want ChannelMapEntry
	for _, entry := range page.Items {
		if entry.ChannelID == target.ChannelID {
			want = entry
		}
	}
	if want.ChannelID != target.ChannelID || want.Number != "12" || want.GuideChannelID != "guide-two" || !want.Hidden || !want.Overridden {
		t.Fatalf("page entry %+v", want)
	}

	exact, err := s.ChannelMap(ctx, allow, source, "", target.ChannelID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if exact.SourceID != page.SourceID || exact.Generation != page.Generation || exact.NextCursor != "" || len(exact.Items) != 1 || exact.Items[0] != want {
		t.Fatalf("exact lookup %+v, page %+v", exact, page)
	}
	// A lookup is a read: it must not move the revision the next write fences.
	afterLookup, err := s.LiveSourceConfiguration(ctx, allow, source)
	if err != nil {
		t.Fatal(err)
	}
	if afterLookup.Revision != after.Revision {
		t.Fatalf("lookup moved revision %d to %d", after.Revision, afterLookup.Revision)
	}

	first, err := s.ChannelMap(ctx, allow, source, "", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	if page, err = s.ChannelMap(ctx, allow, source, first.NextCursor, target.ChannelID, 20); !errors.Is(err, ErrInput) || len(page.Items) != 0 {
		t.Fatalf("channelId with cursor %+v %v", page, err)
	}
	if page, err = s.ChannelMap(ctx, allow, source, "", "absent", 20); !errors.Is(err, ErrNotFound) || len(page.Items) != 0 {
		t.Fatalf("absent channel %+v %v", page, err)
	}
	if page, err = s.ChannelMap(ctx, allow, source, "", strings.Repeat("a", 201), 20); !errors.Is(err, ErrInput) || len(page.Items) != 0 {
		t.Fatalf("oversized channelId %+v %v", page, err)
	}
}

func TestChannelMapExactLookupRefusesDeniedCaller(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	source := channelLookupSource(t, db)
	deny := func(context.Context, *sql.Tx) error { return ErrDenied }
	page, err := s.ChannelMap(ctx, deny, source, "", "channel-id", 20)
	if !errors.Is(err, ErrDenied) || len(page.Items) != 0 {
		t.Fatalf("denied lookup %+v %v", page, err)
	}
}

// Playlist positions start at 0, so the first page must include the first
// channel, and a one-channel page walk must visit every channel once.
func TestChannelMapPagesIncludeTheFirstChannel(t *testing.T) {
	s, db, _ := newService(t)
	ctx := context.Background()
	source := channelLookupSource(t, db)
	page, err := s.ChannelMap(ctx, allow, source, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Name != "One" || page.Items[0].Position != 0 {
		t.Fatalf("first page = %+v", page.Items)
	}
	var walked []string
	token := ""
	for range 4 {
		step, err := s.ChannelMap(ctx, allow, source, token, "", 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range step.Items {
			walked = append(walked, item.Name)
		}
		if step.NextCursor == "" {
			break
		}
		token = step.NextCursor
	}
	if strings.Join(walked, ",") != "One,Two,Three" {
		t.Fatalf("paged walk = %v", walked)
	}
}
