package livechannels

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestDirectoryGloballyOrdersMixedKindsBeforePaging(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	input := testInput()
	input.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"20\" group-title=\"News\",Zulu\nhttps://fixture.invalid/one\n#EXTINF:-1 tvg-id=\"two\" tvg-chno=\"2.5HD\" group-title=\"News\",Alpha\nhttps://fixture.invalid/two\n"
	input.Guide = "<tv/>"
	saved, err := s.Save(ctx, testAuthority("owner", true, true), input)
	if err != nil {
		t.Fatal(err)
	}
	library := DirectoryRow{Channel: Channel{ID: "library", SourceID: "library", Provenance: LibraryChannel, Name: "Middle", Number: "1", Group: "Library Channels", Generation: "library-generation", Programmes: []Programme{}, Favorite: true}, Source: GuideSource{ID: "library", Name: "Middle", Generation: "library-generation", Provenance: LibraryChannel, PublishedAt: "2026-10-09T12:00:00Z", AvailableStart: "2026-10-09T12:00:00Z", AvailableEnd: "2026-10-10T12:00:00Z", RefreshState: "ready"}, Revision: 1}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer-fence", AllowedLiveSources: []string{saved.ID}, LibraryRows: []DirectoryRow{library}, Viewer: Owner{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil
	}
	q := DirectoryQuery{Kind: "all", Sort: "name", Limit: 1}
	first, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || first.Total != 3 || len(first.Channels) != 1 || first.Channels[0].Name != "Alpha" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	q.Offset = 1
	q.Revision = first.Revision
	second, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || second.Revision != first.Revision || second.Channels[0].Name != "Middle" || second.Channels[0].Provenance != LibraryChannel {
		t.Fatalf("mixed second page: %+v %v", second, err)
	}
	q.Offset = 0
	q.Limit = 50
	q.Revision = ""
	q.Sort = "number"
	numbers, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || numbers.Channels[0].Name != "Alpha" || numbers.Channels[1].Name != "Zulu" || numbers.Channels[2].Name != "Middle" {
		t.Fatalf("source-grouped numeric order: %+v %v", numbers, err)
	}
	q.Kind = "library-channel"
	q.SourceID = "library"
	q.Group = "Library Channels"
	q.FavoritesOnly = true
	filtered, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || filtered.Total != 1 || filtered.Channels[0].Name != "Middle" {
		t.Fatalf("filtered library: %+v %v", filtered, err)
	}
	q.Group = "News"
	filtered, err = s.ChannelDirectory(ctx, authority, q)
	if err != nil || filtered.Total != 0 {
		t.Fatalf("wrong-group library: %+v %v", filtered, err)
	}
}

func TestDirectorySummaryNeverReadsProgrammesAndContainsAllSources(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceIDs := []string{}
	for source := 0; source < 2; source++ {
		input := testInput()
		input.ID = strings.Repeat(fmt.Sprintf("%02x", source+1), 24)
		input.RequestID = strings.Repeat(fmt.Sprintf("%02x", source+3), 24)
		input.Name = fmt.Sprintf("Source %d", source)
		input.Guide = "<tv/>"
		var playlist strings.Builder
		playlist.WriteString("#EXTM3U\n")
		for i := 0; i < 70; i++ {
			fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"ch%d\" group-title=\"News\",Channel %d\nhttps://fixture.invalid/%d\n", i, i, i)
		}
		input.Playlist = playlist.String()
		saved, err := s.Save(ctx, testAuthority("owner", true, true), input)
		if err != nil {
			t.Fatal(err)
		}
		sourceIDs = append(sourceIDs, saved.ID)
	}
	// Empty programme tables can be removed: a directory or source summary must
	// not touch them, even though the old guide projection cannot run now.
	if _, err := s.db.Exec(`DROP TABLE live_programmes`); err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", AllowedLiveSources: sourceIDs, LibraryRows: []DirectoryRow{}}, nil
	}
	summary, err := s.ChannelSourceSummary(ctx, authority, false, time.Now())
	if err != nil || len(summary.Sources) != 2 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	for _, source := range summary.Sources {
		if source.ChannelCount != 70 || len(source.GroupCounts) != 1 || source.GroupCounts[0].Count != 70 {
			t.Fatalf("source aggregation: %+v", source)
		}
	}
	page, err := s.ChannelDirectory(ctx, authority, DirectoryQuery{Kind: "all", Sort: "name", Offset: 65, Limit: 14})
	if err != nil || page.Total != 140 || len(page.Channels) != 14 {
		t.Fatalf("bounded directory: %+v %v", page, err)
	}
	for _, channel := range page.Channels {
		if len(channel.Programmes) != 0 {
			t.Fatal("directory leaked programmes")
		}
	}
}

func TestGuideProgrammeLookupSeeksOnlySelectedChannel(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, testAuthority("owner", true, true), testInput()); err != nil {
		t.Fatal(err)
	}
	guide, err := s.Guide(ctx, testAuthority("owner", true, true), testQuery())
	if err != nil {
		t.Fatal(err)
	}
	query, args := guideProgrammeQuery(guide.Channels, guide.End, guide.Start)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 {
		t.Fatalf("selected channel query read %d rows instead of one: %v", count, err)
	}
	rows, err = s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seek := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SEARCH p USING INDEX live_programmes_window (generation_id=? AND channel_id=? AND start_utc<?)") {
			seek = true
		}
		if strings.HasPrefix(detail, "SCAN p") {
			t.Errorf("programme lookup scans beyond selected channels: %s", detail)
		}
	}
	if !seek {
		t.Fatal("programme lookup did not use generation/channel/window index")
	}
}

func TestDirectoryRevisionRejectsPermissionAndPreferenceChanges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	saved, err := s.Save(ctx, testAuthority("owner", true, true), testInput())
	if err != nil {
		t.Fatal(err)
	}
	viewer := Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	fence := "first"
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: fence, AllowedLiveSources: []string{saved.ID}, Viewer: viewer}, nil
	}
	q := DirectoryQuery{Kind: "all", Sort: "number", Limit: 1}
	page, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Revision = page.Revision
	fence = "revoked"
	if _, err = s.ChannelDirectory(ctx, authority, q); err != ErrCursor {
		t.Fatalf("old permission revision admitted: %v", err)
	}
	fence = "first"
	if _, err = s.db.Exec(`INSERT INTO live_channel_preferences VALUES(?,?,?,?,?,?,?,?)`, viewer.Authority, viewer.AccountID, viewer.ProfileID, saved.ID, page.Channels[0].ID, 1, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChannelDirectory(ctx, authority, q); err != ErrCursor {
		t.Fatalf("old preference revision admitted: %v", err)
	}
	q.Revision = ""
	q.FavoritesOnly = true
	favorite, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || favorite.Total != 1 {
		t.Fatalf("favorite count: %+v %v", favorite, err)
	}
}

func BenchmarkDirectoryTwoThousandChannels(b *testing.B) {
	db, err := persistence.Open(filepath.Join(b.TempDir(), "guide.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	s, err := New(db)
	if err != nil {
		b.Fatal(err)
	}
	input := testInput()
	input.Guide = "<tv/>"
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-chno=\"%d\" group-title=\"News\",Channel %d\nhttps://fixture.invalid/%d\n", i, i, i, i)
	}
	input.Playlist = playlist.String()
	saved, err := s.Save(context.Background(), testAuthority("owner", true, true), input)
	if err != nil {
		b.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}, LibraryRows: []DirectoryRow{}}, nil
	}
	q := DirectoryQuery{Kind: "all", Sort: "number", Offset: 1000, Limit: 50}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := s.ChannelDirectory(context.Background(), authority, q)
		if err != nil || page.Total != 2000 || len(page.Channels) != 50 {
			b.Fatal(page, err)
		}
	}
}

func TestDirectoryRejectsAmbiguousChannelIdentifiersWithoutLosingTotals(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	saved, err := s.Save(ctx, testAuthority("owner", true, true), testInput())
	if err != nil {
		t.Fatal(err)
	}
	native, err := s.Guide(ctx, testAuthority("owner", true, true), testQuery())
	if err != nil {
		t.Fatal(err)
	}
	library := DirectoryRow{Channel: Channel{ID: native.Channels[0].ID, SourceID: saved.ID, Provenance: LibraryChannel, Name: "Library", Generation: "libgen", Programmes: []Programme{}}, Source: GuideSource{ID: saved.ID, Name: "Library", Generation: "libgen", Provenance: LibraryChannel}}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}, LibraryRows: []DirectoryRow{library}}, nil
	}
	q := DirectoryQuery{Kind: "all", Sort: "name", Limit: 50}
	if _, err = s.ChannelDirectory(ctx, authority, q); err != ErrChannelIdentifierConflict {
		t.Fatalf("ambiguous directory admitted: %v", err)
	}
	q.Kind = string(LiveSource)
	page, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || page.Total != 2 {
		t.Fatalf("unambiguous source unavailable: %+v %v", page, err)
	}
	// Source ids may collide across provenances without creating ambiguous row ids.
	library.Channel.ID = "unique-library-id"
	q.Kind = "all"
	page, err = s.ChannelDirectory(ctx, authority, q)
	if err != nil || page.Total != 3 || len(page.Sources) != 2 {
		t.Fatalf("source provenance lost: %+v %v", page, err)
	}
}

func TestDirectoryNeighborsWrapSkipUnavailableAndKeepViewRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	rows := []DirectoryRow{}
	for i, name := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
		generation := "generation"
		if i == 1 {
			generation = ""
		}
		rows = append(rows, DirectoryRow{Channel: Channel{ID: name, SourceID: name, Name: name, Provenance: LibraryChannel, Number: fmt.Sprint(i), Generation: generation, Group: "Library Channels", Programmes: []Programme{}}, Source: GuideSource{ID: name, Name: name, Generation: generation, Provenance: LibraryChannel}, Revision: 1})
	}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", LibraryRows: rows}, nil
	}
	normal := DirectoryQuery{Kind: "all", Sort: "name", Limit: 50}
	directory, err := s.ChannelDirectory(ctx, authority, normal)
	if err != nil {
		t.Fatal(err)
	}
	q := normal
	q.Limit = 1
	q.Revision = directory.Revision
	q.AnchorProvenance = string(LibraryChannel)
	q.DeliveryAvailable = true
	for _, test := range []struct {
		anchor, direction, want string
		offset                  int
	}{{"Alpha", "next", "Charlie", 2}, {"Alpha", "previous", "Delta", 3}, {"Delta", "next", "Alpha", 0}, {"Bravo", "next", "Charlie", 2}, {"Bravo", "previous", "Alpha", 0}} {
		q.AnchorChannelID = test.anchor
		q.AnchorSourceID = test.anchor
		q.Direction = test.direction
		neighbor, err := s.ChannelDirectory(ctx, authority, q)
		if err != nil || len(neighbor.Channels) != 1 || neighbor.Channels[0].ID != test.want || neighbor.Offset != test.offset || neighbor.Total != 4 || neighbor.Revision != directory.Revision {
			t.Fatalf("%+v: %+v %v", test, neighbor, err)
		}
	}
	q.DeliveryAvailable = false
	none, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || len(none.Channels) != 0 || none.Total != 4 {
		t.Fatalf("unavailable runtime produced neighbor: %+v %v", none, err)
	}
	q.DeliveryAvailable = true
	q.AnchorChannelID = "missing"
	q.AnchorSourceID = "missing"
	if _, err = s.ChannelDirectory(ctx, authority, q); err != ErrDirectoryAnchorNotFound {
		t.Fatalf("unknown anchor admitted: %v", err)
	}
	q.AnchorChannelID = "Alpha"
	q.AnchorSourceID = "Alpha"
	q.Group = "not-in-view"
	q.Revision = ""
	if _, err = s.ChannelDirectory(ctx, authority, q); err != ErrDirectoryAnchorNotFound {
		t.Fatalf("filtered anchor admitted: %v", err)
	}
	q.Group = ""
	rows = rows[:1]
	single, err := s.ChannelDirectory(ctx, authority, q)
	if err != nil || len(single.Channels) != 0 || single.Total != 1 {
		t.Fatalf("only playable channel retuned itself: %+v %v", single, err)
	}
}
