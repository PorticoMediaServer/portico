package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func readingBrowseFixture(t *testing.T, kind string) (*catalogtest.Catalog, *Service, Viewer, []catalogtest.Item) {
	t.Helper()
	c := catalogtest.Open(t)
	root := t.TempDir()
	library := c.Library("reading", "Reading", kind, root)
	c.Library("other", "Other", "book", t.TempDir())
	leaf := compactcatalog.Ebook
	if kind == "comic" {
		leaf = compactcatalog.Comic
	}
	var items []catalogtest.Item
	for i := 0; i < 4; i++ {
		path := filepath.Join(root, fmt.Sprintf("Publication-%d.pdf", i))
		if err := os.WriteFile(path, readingtest.PDF(), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: leaf, Key: path, Title: fmt.Sprintf("Title %d", i)}, nil)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "pdf"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item.ID, asset)
			return err
		})
		items = append(items, item)
	}
	for i, value := range []string{"Café Author", "CAFÉ  AUTHOR", "Cafe Author"} {
		c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author',?,'',1,'owner','now')`, items[i].ID, value)
		c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'series','Real Series','',1,'owner','now')`, items[i].ID)
	}
	c.Exec(`UPDATE catalog_assets SET available=0`)
	return c, New(c.DB), Viewer{Profile: "local:account:profile", Fence: "viewer", Libraries: []string{"reading"}}, items
}

func TestReadingBrowseLeavesFacetsAndCompilerShareTheReadyScope(t *testing.T) {
	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c, s, viewer, _ := readingBrowseFixture(t, kind)
			if _, err := s.BrowseCapabilitiesForViewer(viewer, "reading", ""); !errors.Is(err, ErrStaleContinuation) {
				t.Fatalf("unready capabilities: %v", err)
			}
			c.Drain()
			caps, err := s.BrowseCapabilitiesForViewer(viewer, "reading", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(caps.Pivots) != 3 {
				t.Fatalf("facet pivots: %+v", caps.Pivots)
			}
			for _, pivot := range caps.Pivots {
				if pivot.ID == "authors" || pivot.ID == "series" {
					if pivot.Browsable || pivot.FacetSource == nil {
						t.Fatalf("facet pivot: %+v", pivot)
					}
				}
			}
			if kind == "comic" && (caps.Pivots[0].ID != "series" || caps.Pivots[1].ID != "comics" || caps.Pivots[1].LabelKey != "library.tab.volumesAndIssues") {
				t.Fatalf("drawn Comic order: %+v", caps.Pivots)
			}
			query := &BrowseNode{All: []BrowseNode{{Field: "author", Operator: "contains", Value: "  café\tAUTHOR  "}, {Field: "series", Operator: "equals", Value: "real series"}}}
			page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Query: query, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			want := "ebook"
			if kind == "comic" {
				want = "comic"
			}
			if page.PageInfo.Total != 2 || len(page.Entries) != 1 || page.Entries[0].Kind != want || page.Entries[0].Playback != nil || page.Entries[0].Duration != nil || page.Entries[0].Navigation == nil || page.Entries[0].Navigation.View != "item" {
				t.Fatalf("Reading leaf projection: %+v", page)
			}
			if page.Entries[0].Author != "Café Author" || page.Entries[0].Subtitle != page.Entries[0].Author {
				t.Fatalf("accepted Reading author was not a semantic card fact: %+v", page.Entries[0])
			}
			if page.Entries[0].Available == nil || *page.Entries[0].Available {
				t.Fatal("unavailable title was hidden or given read availability")
			}
			next, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Query: query, Cursor: page.PageInfo.NextCursor, Limit: 1})
			if err != nil || len(next.Entries) != 1 || next.Entries[0].ID == page.Entries[0].ID {
				t.Fatalf("continuation %+v %v", next, err)
			}
			facets, err := s.Facets(viewer, FacetRequest{Library: "reading", Field: "author", Pivot: "authors", Limit: 1})
			if err != nil || facets.Total != 2 || len(facets.Values) != 1 {
				t.Fatalf("exact facets %+v %v", facets, err)
			}
			nextFacet, err := s.Facets(viewer, FacetRequest{Library: "reading", Field: "author", Pivot: "authors", Limit: 1, Offset: 1})
			if err != nil || nextFacet.Total != 2 || len(nextFacet.Values) != 1 || nextFacet.Values[0].Value == facets.Values[0].Value || nextFacet.Revision != facets.Revision {
				t.Fatalf("bounded facet continuation %+v %v", nextFacet, err)
			}
			counted, err := s.BrowseCounts(viewer, "reading", BrowseCountRequest{Queries: []BrowseCountQuery{{ID: "same", Query: query}}}, nil)
			if err != nil || len(counted.Counts) != 1 || counted.Counts[0].Total != 2 || counted.Counts[0].TotalIsLowerBound {
				t.Fatalf("Reading filter counts differ from browse %+v %v", counted, err)
			}
			if facets.TotalIsLowerBound || facets.Values[0].CountIsLowerBound {
				t.Fatal("small exact facet became a lower bound")
			}
			seriesFacets, err := s.Facets(viewer, FacetRequest{Library: "reading", Field: "series"})
			if err != nil || len(seriesFacets.Values) != 1 || string(seriesFacets.Values[0].SeriesID) != compactcatalog.ReadingSeriesID("reading", "Real Series") || facets.Values[0].SeriesID != "" {
				t.Fatalf("opaque series navigation identity %+v %v", seriesFacets, err)
			}
			filtered, err := s.Facets(viewer, FacetRequest{Library: "reading", Field: "author", Query: query})
			if err != nil || filtered.Total != 1 || filtered.Values[0].Count != 2 {
				t.Fatalf("query facets %+v %v", filtered, err)
			}
			if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Pivot: "authors"}); !errors.Is(err, ErrBrowsePivot) && err == nil {
				t.Fatal("author pivot manufactured entity cards")
			}
			if _, err = s.Facets(Viewer{}, FacetRequest{Library: "reading", Field: "author"}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("unauthorized facets %v", err)
			}
			if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "other"}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("foreign Reading library %v", err)
			}
		})
	}
}

func TestReadingBrowseCursorBindsUnboundedSortAndRejectsInputDrift(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Exec(`UPDATE catalog_entities SET sort_key=? WHERE id=?`, "a"+strings.Repeat("x", 70000), items[0].ID)
	c.Drain()
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.PageInfo.NextCursor) == 0 || len(page.PageInfo.NextCursor) > 4096 {
		t.Fatalf("unbounded sort leaked into cursor: %d", len(page.PageInfo.NextCursor))
	}
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 1, Cursor: page.PageInfo.NextCursor}); err != nil {
		t.Fatal(err)
	}
	signed, err := s.readSignedBrowseCursor(page.PageInfo.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	signed.Expires = time.Now().Add(-time.Second).Unix()
	expired, err := s.encodeCursor(signed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 1, Cursor: expired}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("expired Reading cursor renewed: %v", err)
	}
	c.Exec(`UPDATE metadata_owner_fields SET value='Changed Series' WHERE entity_id=? AND field='series'`, items[0].ID)
	if _, err = s.Facets(viewer, FacetRequest{Library: "reading", Field: "series"}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("unready facets returned facts: %v", err)
	}
	c.Drain()
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 1, Cursor: page.PageInfo.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("stale cursor %v", err)
	}
	err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		r, err := s.WithContext(ctx).readingBrowseScope("reading")
		if err != nil {
			return err
		}
		for _, driven := range []bool{false, true} {
			compiler := &browseCompiler{reading: r}
			if driven {
				compiler.count = s.WithContext(ctx).countRows
				compiler.ids = s.WithContext(ctx).entityIDs
			}
			query := &BrowseNode{Field: "author", Operator: "equals", Value: "Cafe Author"}
			clause, err := compiler.compile(query)
			if err != nil {
				return err
			}
			var n int
			if err = s.WithContext(ctx).read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+clause, compiler.args...).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				t.Fatalf("driven=%v accent identity widened to %d", driven, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadingBrowseAvailabilityUsesOnlyTheSelectedReadingPublication(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 2; i++ {
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: filepath.Join(t.TempDir(), fmt.Sprintf("extra-%d.pdf", i)), Size: 10, Container: "pdf"})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, items[0].ID, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if i == 0 {
				if _, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, items[0].ID, asset); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE catalog_assets SET available=0 WHERE id=?`, asset); err != nil {
					return err
				}
			}
			// i=1 is an available linked asset, but not a Reading publication.
			// Its availability must not become the Reading leaf's availability.
		}
		return nil
	})
	c.Drain()
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		if entry.ID == items[0].Public {
			if entry.Available == nil || *entry.Available {
				t.Fatal("unrelated available asset made unavailable publications readable")
			}
			return
		}
	}
	t.Fatal("eligible unavailable Reading title was hidden")
}

func TestReadingBrowseRelativeFacetBindsItsEvaluationInstant(t *testing.T) {
	c, s, viewer, items := readingBrowseFixture(t, "book")
	c.Exec(`INSERT INTO catalog_item_details(entity_id,added_text) VALUES(?,?) ON CONFLICT(entity_id) DO UPDATE SET added_text=excluded.added_text`, items[0].ID, "2026-10-24T12:00:00Z")
	c.Drain()
	now := time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
	s.Clock = func() time.Time { return now }
	request := FacetRequest{Library: "reading", Field: "author", Query: &BrowseNode{Field: "dateAdded", Operator: "within-last-days", Value: float64(1)}}
	first, err := s.Facets(viewer, request)
	if err != nil || first.Total != 1 || len(first.Values) != 1 || first.Values[0].Count != 1 {
		t.Fatalf("current relative facet %+v %v", first, err)
	}
	now = now.Add(48 * time.Hour)
	later, err := s.Facets(viewer, request)
	if err != nil || later.Total != 0 || len(later.Values) != 0 || later.Revision == first.Revision {
		t.Fatalf("relative facet reused an older instant %+v %v", later, err)
	}
}
