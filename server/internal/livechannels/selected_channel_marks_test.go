package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestSelectedChannelMarksKeepAuthorizedFullSourceDomain(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := testInput()
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" tvg-chno=\"1\",Northwind News\nhttps://private.invalid/news\n#EXTINF:-1 tvg-id=\"news24\" tvg-chno=\"2\",Northwind News 24\nhttps://private.invalid/news24\n"
	in.Guide = "<tv/>"
	saved, e := s.Save(ctx, testAuthority("owner", true, true), in)
	if e != nil {
		t.Fatal(e)
	}
	scope := DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}, Viewer: Owner{Authority: "local", AccountID: "owner", ProfileID: "p"}}
	a := func(context.Context, *sql.Tx) (DirectoryScope, error) { return scope, nil }
	directory, e := s.ChannelDirectory(ctx, a, DirectoryQuery{Kind: "all", Sort: "name", Limit: 50})
	if e != nil {
		t.Fatal(e)
	}
	if len(directory.Channels) != 2 {
		t.Fatal(directory)
	}
	for _, channel := range directory.Channels {
		tx, e := s.db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		selected := []Channel{{SourceID: channel.SourceID, ID: channel.ID, Name: "Not the canonical name"}, {SourceID: "denied-source", ID: channel.ID, Mark: &ChannelMark{Text: "stale", Seed: "stale"}}, {SourceID: channel.SourceID, ID: "not-a-channel"}}
		e = s.ChannelMarksTx(ctx, tx, a, selected)
		tx.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(selected[0].Mark, channel.Mark) || selected[1].Mark != nil || selected[2].Mark != nil {
			t.Fatal("page/name/unauthorized domain affected marks", selected, channel)
		}
	}
	// A library channel belongs to the same complete authorized library source
	// domain, even when only one of its colliding names is on the session page.
	scope.LibraryRows = []DirectoryRow{{Channel: Channel{SourceID: "library", ID: "one", Name: "Northwind News", Number: "1", Provenance: LibraryChannel}}, {Channel: Channel{SourceID: "library", ID: "two", Name: "Northwind News 24", Number: "2", Provenance: LibraryChannel}}}
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	selected := []Channel{{SourceID: "library", ID: "two"}}
	if e = s.ChannelMarksTx(ctx, tx, a, selected); e != nil || selected[0].Mark == nil || selected[0].Mark.Text != "N24" || selected[0].Mark.Seed != "library:two" {
		t.Fatal(selected, e)
	}
}

func TestSelectedChannelMarksFailClosedAcrossAuthorityChange(t *testing.T) {
	s := testStore(t)
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	n := 0
	a := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		n++
		fence := "before"
		if n > 1 {
			fence = "after"
		}
		return DirectoryScope{Fence: fence, LibraryRows: []DirectoryRow{{Channel: Channel{SourceID: "library", ID: "one", Name: "News", Provenance: LibraryChannel}}}}, nil
	}
	selected := []Channel{{SourceID: "library", ID: "one"}}
	if e = s.ChannelMarksTx(context.Background(), tx, a, selected); !errors.Is(e, ErrDenied) || selected[0].Mark != nil {
		t.Fatal("stale authority returned mark", selected, e)
	}
	if e = s.ChannelMarksTx(context.Background(), tx, nil, selected); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if e = s.ChannelMarksTx(context.Background(), tx, a, make([]Channel, 201)); !errors.Is(e, ErrDenied) {
		t.Fatal("unbounded selection admitted", e)
	}
}

func TestGuideMarksUseFullSourceWithinItsReadTransaction(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := testInput()
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"news\" tvg-chno=\"1\",Northwind News\nhttps://private.invalid/news\n#EXTINF:-1 tvg-id=\"news24\" tvg-chno=\"2\",Northwind News 24\nhttps://private.invalid/news24\n"
	in.Guide = "<tv/>"
	saved, err := s.Save(ctx, testAuthority("owner", true, true), in)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}}, nil
	}
	directory, err := s.ChannelDirectory(ctx, authority, DirectoryQuery{Kind: "all", Sort: "name", Limit: 50})
	if err != nil || len(directory.Channels) != 2 {
		t.Fatal(directory, err)
	}
	q := testQuery()
	q.NoProgrammes, q.Search = true, "News 24"
	called := 0
	q.MarkChannels = func(ctx context.Context, tx *sql.Tx, channels []Channel) error {
		called++
		return s.ChannelMarksTx(ctx, tx, authority, channels)
	}
	guide, err := s.Guide(ctx, testAuthority("viewer", true, true), q)
	if err != nil || len(guide.Channels) != 1 || called != 1 {
		t.Fatal(guide, called, err)
	}
	for _, channel := range directory.Channels {
		if channel.ID == guide.Channels[0].ID && (!reflect.DeepEqual(channel.Mark, guide.Channels[0].Mark) || channel.Mark == nil || channel.Mark.Text != "N24") {
			t.Fatalf("filtered guide changed full-source mark: %#v vs %#v", guide.Channels[0].Mark, channel.Mark)
		}
	}
	q.MarkChannels = func(context.Context, *sql.Tx, []Channel) error { return ErrDenied }
	if _, err = s.Guide(ctx, testAuthority("viewer", true, true), q); !errors.Is(err, ErrDenied) {
		t.Fatalf("guide ignored mark authority failure: %v", err)
	}
}
