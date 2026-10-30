package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestCompactPageHydrationAndWholePageFence(t *testing.T) {
	c := catalogtest.Open(t)
	db := c.DB
	films := c.Library("films", "Films", "movie", "/films")
	a := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", "/films/a.mkv", 0), Title: "Alpha", Added: "2026-09-23T12:34:56.123456789Z"}, nil)
	b := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", "/films/b.mkv", 0), Title: "Bravo", Added: "2026-09-23T12:34:56.123456789Z"}, nil)
	c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Extra, Key: "file:extra", Title: "Behind the scenes", Added: "2026-09-23T12:34:56.123456789Z"}, nil)
	assetZ, tokenZ := int64(0), ""
	assetX, tokenX := int64(0), ""
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		assetZ, tokenZ, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/z.mkv", Size: 1, ModifiedNS: 1, Container: "matroska", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 12.345678901234567})
		if err != nil {
			return err
		}
		assetX, tokenX, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/x.mkv", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "av1", AudioCodec: "ac3", Width: 3840, Height: 2160, Duration: 23.456789012345678})
		if err != nil {
			return err
		}
		for _, link := range []struct {
			entity, asset int64
			part          int
		}{{a.ID, assetX, 2}, {a.ID, assetZ, 1}, {b.ID, assetZ, 0}} {
			if err = compactcatalog.LinkAssetTx(ctx, tx, link.entity, link.asset, compactcatalog.Link{Part: link.part}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('viewer',?,4250,0,'playback')`, a.ID)
	c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('viewer',?,1,'2026-09-23T12:00:00Z')`, a.ID)
	s := New(db)
	c.Drain()
	page, err := s.moviePage("viewer", []string{b.Public, a.Public})
	if err != nil {
		t.Fatal(err)
	}
	// The evidence row holds whatever SQLite parsed from the literal (x86
	// extended precision and ARM differ in the last bit); the page must carry
	// that exact value through.
	var stored float64
	if err = db.QueryRow(`SELECT duration FROM catalog_assets WHERE id=?`, assetX).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != b.Public || page[1].ID != a.Public ||
		page[1].AddedAt == nil || *page[1].AddedAt != "2026-09-23T12:34:56.123456789Z" ||
		page[1].Watched == nil || !*page[1].Watched || page[1].ProgressSeconds != 4.25 ||
		len(page[1].Sources) != 2 || page[1].Sources[0].ID != tokenZ || page[1].Sources[1].ID != tokenX ||
		math.Float64bits(page[1].Duration) != math.Float64bits(stored) {
		added, watched := "<nil>", "<nil>"
		if len(page) == 2 && page[1].AddedAt != nil {
			added = *page[1].AddedAt
		}
		if len(page) == 2 && page[1].Watched != nil {
			watched = fmt.Sprint(*page[1].Watched)
		}
		t.Fatalf("batched compact facts changed page order or wire fields: added=%q watched=%s duration=%v stored=%v: %+v", added, watched, page[1].Duration, stored, page)
	}
	list, next, err := s.List(Viewer{Profile: "viewer", Libraries: []string{"films"}}, "films", "", 2)
	if err != nil || len(list) != 2 || list[0].ID != a.Public || list[1].ID != b.Public || next != fmt.Sprint(b.ID) || list[0].Sources != nil || list[0].Watched != nil {
		t.Fatalf("list page or omitted fields changed: %+v next=%q err=%v", list, next, err)
	}
	c.Fields(b.ID, map[string]any{"title": "Bravo revised"})
	// Facts are synchronous: the request sees the committed title immediately.
	if page, err = s.moviePage("viewer", []string{a.Public, b.Public}); err != nil || len(page) != 2 || page[1].Title != "Bravo revised" {
		t.Fatalf("synchronous title change: %+v %v", page, err)
	}
	if list, _, err = s.List(Viewer{Profile: "viewer", Libraries: []string{"films"}}, "films", "", 2); err != nil || len(list) != 2 {
		t.Fatalf("a queued title change failed the list: %+v %v", list, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/z.mkv", Size: 1, ModifiedNS: 1, Container: "matroska", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 99.125})
		return err
	})
	page, err = s.moviePage("viewer", []string{a.Public, b.Public})
	if err != nil || page[0].Duration != 99.125 || page[1].Duration != 99.125 {
		t.Fatalf("synchronous shared asset edit was not visible to both items: %+v %v", page, err)
	}
	c.Delete(b.ID)
	if _, err = s.moviePage("viewer", []string{a.Public, b.Public}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("deleted entity remained readable before derived work drained: %v", err)
	}
	c.Drain()
	if _, err = s.moviePage("viewer", []string{a.Public, b.Public}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("deleted source remained in the page after tombstone projection: %v", err)
	}
}

func TestMissingFlagSurvivesPageAndCardProjection(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	item := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: "file:missing-film", Title: "Missing film"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return compactcatalog.SettleMissingTx(ctx, tx, item.ID) })
	s := New(c.DB)
	rows, err := s.compactPageItems("", []string{item.Public}, false)
	if err != nil || rows[item.Public] == nil || !rows[item.Public].Missing {
		t.Fatal(rows, err)
	}
	card := contentItem(*rows[item.Public])
	if !card.Missing || card.Playback != nil {
		t.Fatal(card)
	}
	single, err := s.Get("", item.Public)
	if err != nil || !single.Missing {
		t.Fatal(single, err)
	}
}
