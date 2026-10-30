package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Item 3: a restricted viewer's whole-library listening totals come from the
// published class and follow availability through the dirty-mark journal.
func TestRestrictedListeningCountsComeFromTheClass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2020)
	songs := make([]catalogtest.Item, 205)
	for i := range songs {
		n := i + 1
		songs[i] = c.Song(album, n, fmt.Sprintf("/music/%03d.mp3", n), fmt.Sprintf("Song %03d", 206-n))
	}
	c.Attributes(songs[0].ID, "label", "adult")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, songs[204].Asset, false)
	})
	c.Drain()

	db := c.DB
	s := New(db)
	ctx := context.Background()
	r := visibilityRestrictionOf(nil, false, "adult")
	if err := s.RebuildVisibilityClass(ctx, "music", r); err != nil {
		t.Fatal(err)
	}
	request := ContentRequest{
		Viewer:      Viewer{Profile: "profile", Fence: "viewer", Libraries: []string{"music"}},
		ServerID:    "server",
		Library:     "music",
		Profile:     "profile",
		ViewerFence: "viewer",
		View:        "browse",
		Limit:       100,
	}
	request.Viewer.Restrictions = r
	target := ListeningTarget{"music", "library", "music"}
	counts := func(label string) (int, int) {
		t.Helper()
		page, err := s.ListeningSelection(request, target, "ordered", "", false, "")
		if err != nil {
			t.Fatal(label, err)
		}
		var total, missing int
		counted, err := s.classListeningCounts(target, r, "", &total, &missing)
		if err != nil || !counted {
			t.Fatal(label, "class did not answer the counts", counted, err)
		}
		return page.TotalCount, page.UnavailableCount
	}
	// 205 songs, one restricted, one unavailable.
	if total, missing := counts("published"); total != 203 || missing != 1 {
		t.Fatalf("class totals %d/%d, want 203/1", total, missing)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, songs[1].Asset, false)
	})
	c.Drain()
	if _, err := visibilityRefreshForTest(ctx, s, "music", r); err != nil {
		t.Fatal(err)
	}
	if total, missing := counts("after a file went offline"); total != 202 || missing != 2 {
		t.Fatalf("class totals %d/%d after refresh, want 202/2", total, missing)
	}
}

// Item 3: a show whose episodes lost their files leaves the class once the
// derived catalogue work and per-class dirty mark are processed.
func TestShowWithoutFilesLeavesTheClass(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Harbor", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/episode-1.mkv")
	c.Drain()

	s := New(c.DB)
	ctx := context.Background()
	r := visibilityRestrictionOf(nil, false, "adult")
	if err := s.RebuildVisibilityClass(ctx, "tv", r); err != nil {
		t.Fatal(err)
	}
	classID, _, generation := visibilityClassForTest(t, c, "tv", r)
	listed := func() bool {
		var n int
		if err := c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?`, classID, generation, show.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !listed() {
		t.Fatal("show with files missing from the class")
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, episode.ID, episode.Asset)
	})
	c.Drain()
	var dirty int
	if err := c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_dirty WHERE class_id=? AND entity_id=?`, classID, show.ID).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal("unlinking files did not mark the show dirty", dirty, err)
	}
	if _, err := visibilityRefreshForTest(ctx, s, "tv", r); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("a show without files stayed in the class")
	}
}

// visibilityClassForTest reads the current per-library class identifiers.
func visibilityClassForTest(t *testing.T, c *catalogtest.Catalog, library string, r identity.ContentRestrictions) (classID, libraryID, generation int64) {
	t.Helper()
	key, _ := visibilityClassKey(library, r)
	if err := c.DB.QueryRow(`SELECT id,library_id,active_generation FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&classID, &libraryID, &generation); err != nil {
		t.Fatal(err)
	}
	return
}

// visibilityRestrictionOf is a local copy of the package-wide restrictionOf
// test helper so this file remains self-contained under test-only.sh.
func visibilityRestrictionOf(max *int, blockUnrated bool, labels ...string) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, BlockedLabels: labels, Revision: 2}
}

// visibilityRefreshForTest applies one incremental refresh of the published class.
func visibilityRefreshForTest(ctx context.Context, s *Service, library string, r identity.ContentRestrictions) (bool, error) {
	key, _ := visibilityClassKey(library, r)
	var classID, libraryID, generation int64
	if err := s.db.QueryRow(`SELECT id,library_id,active_generation FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&classID, &libraryID, &generation); err != nil {
		return false, err
	}
	return true, s.refreshVisibilityClass(ctx, library, r, classID, libraryID, generation)
}
