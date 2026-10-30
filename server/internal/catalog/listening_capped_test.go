package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// cappedSelectionFixture builds a music library with exactly m songs on one
// album (disc 1, tracks 1..m, so cap-01 is first in listening order).
// unavailable marks 1-based tracks whose asset is offline; ratings maps
// 1-based tracks to a contentRating spelling (e.g. "G", "R").
func cappedSelectionFixture(t *testing.T, m int, unavailable map[int]bool, ratings map[int]string) (*Service, *catalogtest.Catalog, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Release", 2020)
	names := catalogtest.Names{"artist": artist, "album": album}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 1; i <= m; i++ {
			name := fmt.Sprintf("cap-%02d", i)
			path := "/music/" + name + ".mp3"
			itemID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album.ID, Key: compactcatalog.ItemKey("/music", path, 0), Title: fmt.Sprintf("Capped Song %02d", i), Added: "2026-01-01T00:00:00.000Z"})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, itemID, map[string]any{"album_id": album.ID, "disc_number": 1, "track_number": i}); err != nil {
				return err
			}
			assetID, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mp3", AudioCodec: "mp3", Duration: 300})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, itemID, assetID, compactcatalog.Link{}); err != nil {
				return err
			}
			if unavailable[i] {
				if err = compactcatalog.SetAssetAvailableTx(ctx, tx, assetID, false); err != nil {
					return err
				}
			}
			if err = compactcatalog.SetSongArtistsTx(ctx, tx, itemID, []int64{artist.ID}); err != nil {
				return err
			}
			if rating, ok := ratings[i]; ok {
				if err = compactcatalog.SetAttributesTx(ctx, tx, itemID, "contentRating", []string{rating}); err != nil {
					return err
				}
			}
			names[name] = catalogtest.Item{ID: itemID, Asset: assetID, Token: token}
		}
		return nil
	})
	for name, item := range names {
		item.Public = c.Public(item.ID)
		names[name] = item
	}
	ageKeys := map[string]bool{}
	for _, rating := range ratings {
		key := strings.ToLower(strings.TrimSpace(rating))
		if ageKeys[key] {
			continue
		}
		ageKeys[key] = true
		age, ok := identity.RatingAge(rating)
		if !ok {
			age = -1
		}
		c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES(?,?)`, key, age)
	}
	c.Drain()
	s := New(c.DB)
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if more, err := s.ClassifyPendingRatings(context.Background()); err != nil || more {
		t.Fatal("pending ratings left unclassified", more, err)
	}
	c.Drain()
	return s, c, names
}

// TestListeningSelectionCountsAreExact: a selection's totals are exact at any
// size (the old 10,000 floor is gone); every available song is listed.
func TestListeningSelectionCountsAreExact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	target := ListeningTarget{"music", "library", "music"}
	for _, songs := range []int{9, 10, 11} {
		s, _, names := cappedSelectionFixture(t, songs, map[int]bool{2: true, 3: true}, nil)
		page, err := s.ListeningSelection(phase34ListeningRequest("music", "browse", ""), target, "ordered", "", false, names["cap-01"].Public)
		if err != nil {
			t.Fatalf("M=%d: %v", songs, err)
		}
		if page.TotalCount != songs-2 || page.UnavailableCount != 2 {
			t.Fatalf("M=%d: counts %d/%d", songs, page.TotalCount, page.UnavailableCount)
		}
		if len(page.Entries) != songs-2 {
			t.Fatalf("M=%d: %d entries, want %d available songs", songs, len(page.Entries), songs-2)
		}
	}
}

// TestListeningSelectionPagesTheWholeWalk pages a selection to the end: the
// concatenated entries are the full walk in order, with exact totals on every
// page and no nextCursor on the last.
func TestListeningSelectionPagesTheWholeWalk(t *testing.T) {
	s, _, names := cappedSelectionFixture(t, 12, nil, nil)
	target := ListeningTarget{"music", "library", "music"}
	full, err := s.ListeningSelection(phase34ListeningRequest("music", "browse", ""), target, "ordered", "", false, names["cap-01"].Public)
	if err != nil || len(full.Entries) != 12 {
		t.Fatalf("full walk: %+v %v", full, err)
	}
	want := []string{}
	for _, entry := range full.Entries {
		want = append(want, entry.ItemID)
	}
	r := phase34ListeningRequest("music", "browse", "")
	r.Limit = 4
	got := []string{}
	pages := 0
	for {
		page, err := s.ListeningSelection(r, target, "ordered", "", false, names["cap-01"].Public)
		if err != nil {
			t.Fatal(err)
		}
		if page.TotalCount != 12 {
			t.Fatalf("page %d: total %d", pages, page.TotalCount)
		}
		for _, entry := range page.Entries {
			got = append(got, entry.ItemID)
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		r.Cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, want) || pages != 3 {
		t.Fatalf("paged %v in %d pages, full walk %v", got, pages, want)
	}
}

// TestListeningSelectionExactBelowTheCapKeepsStaleChecks is the continuation
// fence for an uncapped selection: a library change between pages still
// answers ErrStaleContinuation.
func TestListeningSelectionExactBelowTheCapKeepsStaleChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	r := phase34ListeningRequest("music", "browse", "")
	target := ListeningTarget{"music", "album", names["album"].Public}
	first, err := s.ListeningSelection(r, target, "ordered", "", false, "")
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	r.Cursor = first.NextCursor
	if _, err = c.DB.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('music',?,'genre','embedded','Jazz')`, names["song-001"].Token); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	if _, err = s.ListeningSelection(r, target, "ordered", "", false, ""); err != nil {
		t.Fatalf("library publication prevented continuation: %v", err)
	}
}

// TestListeningSelectionRestrictedProbeCountsOnlyVisible hides songs above a
// rating ceiling from the counts: they use the same restricted scope as the
// page, so hidden songs are never counted.
func TestListeningSelectionRestrictedProbeCountsOnlyVisible(t *testing.T) {
	ratings := map[int]string{}
	for i := 1; i <= 8; i++ {
		ratings[i] = "G"
	}
	ratings[6], ratings[7], ratings[8] = "R", "R", "R"
	s, _, names := cappedSelectionFixture(t, 8, nil, ratings)
	target := ListeningTarget{"music", "library", "music"}
	restricted := phase34ListeningRequest("music", "browse", "")
	restricted.Viewer.Restrictions = phase34RestrictionOf(phase34Ceiling(13), false)
	// Exactly the 5 visible songs.
	if err := s.RebuildVisibilityClass(context.Background(), "music", restricted.Viewer.Restrictions); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListeningSelection(restricted, target, "ordered", "", false, names["cap-01"].Public)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 5 || page.UnavailableCount != 0 {
		t.Fatalf("restricted: %+v", page)
	}
	for _, selection := range []ListeningSelection{page} {
		for _, entry := range selection.Entries {
			if entry.ItemID == names["cap-06"].Public || entry.ItemID == names["cap-07"].Public || entry.ItemID == names["cap-08"].Public {
				t.Fatalf("hidden song %s in selection %+v", entry.ItemID, selection.Entries)
			}
		}
	}
}
