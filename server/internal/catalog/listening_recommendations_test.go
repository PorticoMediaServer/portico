package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestListeningRelatedRowsUseRealLibraryRelationships(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	// A second release from the same artist; an assetless release must never
	// become a recommendation target, and neither release ever lists songs.
	names["next"] = c.Album(names["artist"], "Another Release", 2021)
	names["missing"] = c.Album(names["artist"], "Unavailable", 2022)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetFactsTx(ctx, tx, names["song-204"].ID, map[string]any{"album_id": names["next"].ID}); err != nil {
			return err
		}
		return compactcatalog.SetFactsTx(ctx, tx, names["song-205"].ID, map[string]any{"album_id": names["missing"].ID})
	})
	c.Drain()
	viewer := Viewer{Profile: "profile", Fence: "fence", Libraries: []string{"music"}}
	d, e := s.Detail(viewer, "server", names["song-001"].Public, false)
	if e != nil {
		t.Fatal(e)
	}
	if d.Related == nil || len(d.Related.Rows) != 1 {
		t.Fatalf("missing music relationships: %+v", d.Related)
	}
	releases := d.Related.Rows[0]
	if releases.Relation != "artist" || releases.Heading != "More releases by Artist" || len(releases.Entries) != 1 || releases.Entries[0].ID != names["next"].Public || releases.Entries[0].Navigation.View != "album" {
		t.Fatal(releases)
	}
	for _, r := range d.Related.Rows {
		for _, v := range r.Entries {
			if v.LibraryID != "music" || v.Available == nil || !*v.Available || v.Playback != nil || v.ID == names["song-001"].Public || v.Kind != "album" {
				t.Fatal(v)
			}
		}
	}
	viewer.Libraries = []string{"books"}
	d, e = s.Detail(viewer, "server", names["part-a1"].Public, false)
	if e != nil {
		t.Fatal(e)
	}
	if d.Related == nil || len(d.Related.Rows) != 1 || d.Related.Rows[0].Relation != "author" || d.Related.Rows[0].Entries[0].ID != names["book-b"].Public || d.Related.Rows[0].Entries[0].Navigation.View != "book" {
		t.Fatal(d.Related)
	}
	for _, r := range d.Related.Rows {
		for _, v := range r.Entries {
			if v.Kind != "book" || v.Playback != nil {
				t.Fatal(v)
			}
		}
	}
	// No other available related works means no recommendation headings.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetAssetAvailableTx(ctx, tx, names["song-002"].Asset, false); err != nil {
			return err
		}
		return compactcatalog.SetAssetAvailableTx(ctx, tx, names["part-b1"].Asset, false)
	})
	c.Drain()
	d, e = s.Detail(viewer, "server", names["part-a1"].Public, false)
	if e != nil || len(d.Related.Rows) != 0 {
		t.Fatal(d.Related, e)
	}
}

func TestListeningEntityIdentityAcrossPagesAndLibraryFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	c.Fields(names["song-150"].ID, map[string]any{"poster_url": "local-cover"})
	c.Drain()
	r := phase34ListeningRequest("music", "album", names["album"].Public)
	r.Limit = 12
	page, e := s.Content(r)
	if e != nil {
		t.Fatal(e)
	}
	if page.Entity == nil || page.Entity.Title != "Release" || page.Entity.Subtitle != "Artist" || *page.Entity.Count != 205 || page.Entity.PosterURL != "/v1/items/"+names["song-150"].Public+"/art/poster" {
		t.Fatal(page.Entity)
	}
	for _, section := range page.Sections {
		if section.ID == "songs" {
			r.Cursor = section.NextCursor
		}
	}
	page, e = s.Content(r)
	if e != nil || page.Entity == nil || *page.Entity.Count != 205 || page.Entity.PosterURL != "/v1/items/"+names["song-150"].Public+"/art/poster" {
		t.Fatal(page.Entity, e)
	}
	r = phase34ListeningRequest("other", "album", names["album"].Public)
	if _, e = s.Content(r); e == nil {
		t.Fatal("cross-library entity exposed")
	}
}
