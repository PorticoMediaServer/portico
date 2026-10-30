package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestDetailAssetSummaryPreservesFactsWithoutSourceAllocation(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	tv := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	small := c.Movie(films, "/films/small.mkv", "Small", 2020)
	many := c.Movie(films, "/films/many.mkv", "Many", 2021)
	empty := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Extra, Key: "empty", Title: "Empty"}, nil)
	negative := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: "negative", Title: "Negative"}, nil)
	negativeAsset, _ := c.File(negative.ID, "/films/negative.mkv", -3)
	zero := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: "zero", Title: "Zero"}, nil)
	c.File(zero.ID, "/films/zero.mkv", 0)
	show := c.Show(tv, "Series", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/series/one.mkv")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	song := c.Song(album, 1, "/music/one.flac", "Song")
	book := c.Entity(compactcatalog.Entity{Library: books, Kind: compactcatalog.Book, Key: "book", Title: "Book"}, map[string]any{"library_id": books, "local_key": "book"})
	part := c.BookFile(book, 1, "/books/one.m4b")
	const versions = 256
	const longest = 9999.123456789013
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for n := 1; n < versions; n++ {
			duration := float64(n)
			if n == versions-1 {
				duration = longest
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
				Path: fmt.Sprintf("/films/version-%03d.mkv", n), Container: "mkv",
				VideoCodec: "h264", AudioCodec: "aac", Duration: duration,
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, many.ID, asset, compactcatalog.Link{Part: n % 3}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"films", "tv", "music", "books"}}

	assertParity := func(item catalogtest.Item) {
		t.Helper()
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.CompleteInfoAvailabilityTx(ctx, tx, item.ID)
		})
		full, err := s.Get("p", item.Public)
		if err != nil {
			t.Fatal(err)
		}
		if full.Sources != nil {
			t.Fatal("Get retained the eager source array")
		}
		sources, available, duration, err := s.compactItemAssets(item.Public)
		if err != nil || full.Available != available || math.Float64bits(full.Duration) != math.Float64bits(duration) {
			t.Fatalf("maintained summary differs from independent raw oracle: %+v %v %v %v", full, available, duration, err)
		}
		_ = sources
		scalar, err := s.getItem("p", item.Public, false)
		if err != nil {
			t.Fatal(err)
		}
		if scalar.Sources != nil {
			t.Fatal("the Detail item read materialized sources")
		}
		full.Sources = nil
		if !reflect.DeepEqual(full, scalar) {
			t.Fatalf("%s: source-free item facts differ from Get: full=%+v scalar=%+v", item.Public, full, scalar)
		}
		if math.Float64bits(full.Duration) != math.Float64bits(scalar.Duration) {
			t.Fatal("duration lost precision")
		}
		detail, err := s.Detail(viewer, "server", item.Public, false)
		if err != nil {
			t.Fatal(err)
		}
		// Detail selects ready art independently of the item's legacy hints.
		full.PosterURL, full.BackdropURL, full.StillURL = detail.Item.PosterURL, detail.Item.BackdropURL, detail.Item.StillURL
		if !reflect.DeepEqual(full, detail.Item) {
			t.Fatalf("%s: ordinary Detail changed item facts: full=%+v detail=%+v", item.Public, full, detail.Item)
		}
	}

	for _, item := range []catalogtest.Item{small, many, empty, negative, zero, episode, song, part} {
		assertParity(item)
	}
	full, err := s.Get("p", many.Public)
	if err != nil || full.Sources != nil || full.Duration != longest {
		t.Fatalf("Get summary/source separation changed: %+v %v", full, err)
	}
	summary, err := compactcatalog.ReadInfoSummary(context.Background(), c.DB, many.ID)
	if err != nil || summary.Files != versions {
		t.Fatalf("maintained exact file count %d want%d: %v", summary.Files, versions, err)
	}
	// A missing link still contributes its duration. Asset availability is
	// deliberately distinct: the original source reader uses link.available.
	c.Exec("UPDATE catalog_asset_links SET available=0 WHERE entity_id=?", many.ID)
	c.Exec("UPDATE catalog_asset_links SET available=1 WHERE entity_id=? AND asset_id=?", many.ID, many.Asset)
	c.Exec("UPDATE catalog_assets SET available=0 WHERE id=?", many.Asset)
	c.Exec("INSERT INTO catalog_missing_titles(entity_id,library_id,missing_since_ms) VALUES(?,?,1000)", many.ID, films)
	assertParity(many)
	got, err := s.getItem("p", many.Public, false)
	if err != nil || !got.Available || !got.Missing || got.Duration != longest {
		t.Fatalf("mixed-link/missing maximum changed: %+v %v", got, err)
	}
	c.Exec("UPDATE catalog_asset_links SET available=0 WHERE entity_id=?", many.ID)
	assertParity(many)
	got, err = s.getItem("p", many.Public, false)
	if err != nil || got.Available || got.Duration != longest {
		t.Fatalf("unavailable versions no longer contribute duration: %+v %v", got, err)
	}
	c.Exec("UPDATE catalog_asset_links SET available=0 WHERE entity_id=?", negative.ID)
	c.Exec("UPDATE catalog_assets SET available=1 WHERE id=?", negativeAsset)
	assertParity(negative)
	got, err = s.getItem("p", negative.Public, false)
	if err != nil || got.Duration != 0 || got.Available {
		t.Fatalf("negative duration zero floor/link authority changed: %+v %v", got, err)
	}
	_, fullErr := s.Get("p", "absent")
	_, scalarErr := s.getItem("p", "absent", false)
	if !errors.Is(fullErr, sql.ErrNoRows) || !errors.Is(scalarErr, sql.ErrNoRows) {
		t.Fatalf("missing item error changed: full=%v scalar=%v", fullErr, scalarErr)
	}

	ctx, queries := dbwork.TraceQueryPlans(context.Background())
	if _, err = s.WithContext(ctx).Detail(viewer, "server", many.Public, false); err != nil {
		t.Fatal(err)
	}
	summaries := 0
	for _, query := range queries() {
		shape := strings.Join(strings.Fields(query), " ")
		if strings.Contains(shape, "SELECT a.token,a.container,a.video_codec,a.audio_codec") {
			t.Fatal("ordinary Detail still reads every source DTO")
		}
		if strings.Contains(shape, "e.info_desired_epoch") && strings.Contains(shape, "catalog_info_duration") {
			summaries++
		}
	}
	if summaries != 1 {
		t.Fatalf("ordinary Detail made %d scalar asset reads, want one", summaries)
	}

	allocations := func(item string, sources bool) float64 {
		t.Helper()
		var lastErr error
		count := testing.AllocsPerRun(3, func() {
			if sources {
				_, _, _, lastErr = s.compactItemAssets(item)
			} else {
				_, _, lastErr = s.compactItemAssetSummary(item)
			}
		})
		if lastErr != nil {
			t.Fatal(lastErr)
		}
		return count
	}
	smallSummary := allocations(small.Public, false)
	largeSummary := allocations(many.Public, false)
	largeSources := allocations(many.Public, true)
	if largeSummary > smallSummary+32 || largeSummary*4 >= largeSources {
		t.Fatalf("summary allocations grew with versions or retained source DTO cost: one=%.0f many=%.0f sources=%.0f", smallSummary, largeSummary, largeSources)
	}
	t.Logf("asset-read allocations: scalar 1 version=%.0f; scalar %d versions=%.0f; full sources=%.0f", smallSummary, versions, largeSummary, largeSources)
}
