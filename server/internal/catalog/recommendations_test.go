package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"
)

type relatedFixture struct {
	db     *sql.DB
	c      *catalogtest.Catalog
	public map[string]catalogtest.Item
	a, b   int64
}

func relatedDB(t *testing.T) *relatedFixture {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	return &relatedFixture{db: db, c: c, public: map[string]catalogtest.Item{},
		a: c.Library("a", "Movies", "movie", "/a"), b: c.Library("b", "Private", "movie", "/b")}
}

func relatedViewer(s *Service, profile, fence, item string) Viewer {
	library, _ := s.LibraryForItem(item)
	return Viewer{Profile: profile, Fence: fence, Libraries: []string{library}}
}

func relatedSeed(t *testing.T, f *relatedFixture, key, library, title, providerID string, year int) catalogtest.Item {
	t.Helper()
	lib := f.a
	if library == "b" {
		lib = f.b
	}
	it := f.c.Movie(lib, "/"+key+".mkv", title, year)
	f.public[key] = it
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetTermsTx(ctx, tx, it.ID, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "16", Name: "Animation"}}); err != nil {
			return err
		}
		return compactcatalog.SetCreditsTx(ctx, tx, it.ID, "tmdb", []compactcatalog.Credit{{PersonName: "Casey Example", ProviderPersonID: "42", CreditID: "credit-" + key, CreditedName: "Casey Example", Role: "Character", Department: "Acting", Ordinal: 0}})
	})
	if providerID != "" {
		f.c.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,'tmdb',?,'https://www.themoviedb.org/movie/test','2026-09-05')`, it.ID, providerID)
	}
	return it
}

func relatedBulk(t *testing.T, f *relatedFixture, rowsN int) {
	t.Helper()
	var rootA, rootB string
	if err := f.db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, f.a).Scan(&rootA); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, f.b).Scan(&rootB); err != nil {
		t.Fatal(err)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for x := 1; x <= rowsN; x++ {
			library, root := f.a, rootA
			if x > rowsN/2 {
				library, root = f.b, rootB
			}
			path := fmt.Sprintf("/bulk%06d.mkv", x)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Film %06d", x), Year: 2000 + x%26, Added: "2026-01-01T00:00:00.000Z"})
			if err != nil {
				return err
			}
			asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 10})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if err = compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "16", Name: "Animation"}}); err != nil {
				return err
			}
		}
		return nil
	})
}

func TestRelatedMoviesRealIdentityScopeAndAvailability(t *testing.T) {
	f := relatedDB(t)
	for _, v := range []struct {
		id, lib, title, provider string
		year                     int
	}{{"source", "a", "Source", "1", 2020}, {"near", "a", "Near", "2", 2019}, {"far", "a", "Far", "3", 2000}, {"same", "a", "Same film", "1", 2021}, {"secret", "b", "Secret", "4", 2020}, {"missing", "a", "Missing", "5", 2020}, {"shared", "a", "Shared source", "6", 2020}, {"unknown", "a", "No provider ID", "", 2020}} {
		relatedSeed(t, f, v.id, v.lib, v.title, v.provider, v.year)
	}
	missing := f.public["missing"]
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetAssetAvailableTx(ctx, tx, missing.Asset, false); err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, f.public["shared"].ID, f.public["source"].Asset, compactcatalog.Link{})
	})
	f.c.Drain()
	source := f.public["source"]
	s := New(f.db)
	out, err := s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	if err != nil || out.Related == nil || len(out.Related.Rows) != 2 {
		t.Fatal(out.Related, err)
	}
	want := [][]string{{"unknown", "near", "far"}, {"unknown", "near", "far"}}
	for i, row := range out.Related.Rows {
		if len(row.Entries) != 3 {
			t.Fatal(row)
		}
		for j, entry := range row.Entries {
			if entry.ID != f.public[want[i][j]].Public || entry.LibraryID != "a" || entry.Playback != nil || entry.Navigation.EntityID != entry.ID {
				t.Fatal(entry)
			}
		}
	}
	if out.Related.Rows[1].EvidenceID != "42" || out.Related.Rows[1].Heading != "With Casey Example" {
		t.Fatal("credit identity used as person", out.Related.Rows[1])
	}
	before := out.Revision.Catalog
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, source.ID, "tmdb", nil)
	})
	f.c.Drain()
	out, err = s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	if err != nil || len(out.Related.Rows) != 1 || out.Revision.Catalog <= before {
		t.Fatal("person removal not revision-fenced", out, err)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, source.ID, compactcatalog.VocabGenre, "tmdb", nil)
	})
	f.c.Drain()
	out, err = s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	if err != nil || len(out.Related.Rows) != 0 {
		t.Fatal("empty catalog relations fabricated", out, err)
	}
}

func TestRelatedMoviesCandidateIndexAndLargeCatalogBound(t *testing.T) {
	testtier.Media(t, "a large catalogue projected per test")
	f := relatedDB(t)
	source := relatedSeed(t, f, "source", "a", "Source", "1", 2020)
	// 2,000 rows in the ordinary suite; release and deep tiers use 20,000.
	rowsN := 2000
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
		rowsN = 20000
	}
	relatedBulk(t, f, rowsN)
	f.c.Drain()
	// Each side of the year walks the facet index in its own order: no sort
	// of the genre's films, and a first page reads only its prefix.
	planText := ""
	for _, side := range [][2]string{{`f.year>=?`, `f.year ASC,f.entity_id DESC`}, {`f.year<? AND f.year>0`, `f.year DESC,f.entity_id`}} {
		plan, err := f.db.Query("EXPLAIN QUERY PLAN "+nearestYearSideSQL(side[0], side[1], "1"), "tmdb", "a", 1, "name", "animation", 2020, source.Public, source.Public, 96)
		if err != nil {
			t.Fatal(err)
		}
		for plan.Next() {
			var a, b, c int
			var detail string
			if err = plan.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			planText += detail + "\n"
		}
		plan.Close()
	}
	if !strings.Contains(planText, "catalog_related_lookup") || strings.Contains(planText, "TEMP B-TREE FOR ORDER BY") {
		t.Fatal(planText)
	}
	seed, err := New(f.db).Get("p", source.Public)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := New(f.db).nearestYearOrder(seed, 1, "name", "animation", "tmdb")(13)
	if err != nil {
		t.Fatal(err)
	}
	n := prefix.len()
	if prefix.complete || n < 13 || n > 3*relationRowPrefix {
		t.Fatal("first page read", n, prefix.complete)
	}
	started := time.Now()
	s := New(f.db)
	f.c.Drain()
	out, err := s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	if err != nil || len(out.Related.Rows) != 1 || len(out.Related.Rows[0].Entries) != 12 {
		t.Fatal(out, err)
	}
	first, _ := json.Marshal(out.Related)
	f.c.Drain()
	again, err := s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	second, _ := json.Marshal(again.Related)
	if err != nil || string(first) != string(second) {
		t.Fatal("unstable ranking", err)
	}
	t.Logf("%d movies,%d same-library candidates: shortlist=%d,returned=%d,two detail reads=%s; indexed materialized plan verified", rowsN+1, rowsN/2+1, n, len(out.Related.Rows[0].Entries), time.Since(started))
}

// Local sidecars use a per-item marker, not a global title identity.
func TestRelatedMoviesLocalSidecarsDoNotMergeDifferentTitles(t *testing.T) {
	f := relatedDB(t)
	source := relatedSeed(t, f, "source", "a", "Source", "1", 2020)
	relatedSeed(t, f, "different", "a", "Different title", "2", 2019)
	relatedSeed(t, f, "same", "a", "Alternate copy", "1", 2020)
	f.c.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) SELECT id,'nfo','local','','2026-09-16' FROM catalog_entities WHERE kind=1`)
	f.c.Drain()
	s := New(f.db)
	detail, err := s.Detail(relatedViewer(s, "p", "fence", source.Public), "server", source.Public, false)
	if err != nil || detail.Related == nil || len(detail.Related.Rows) != 2 {
		t.Fatalf("recommendations: %+v %v", detail.Related, err)
	}
	for _, row := range detail.Related.Rows {
		if len(row.Entries) != 1 || row.Entries[0].ID != f.public["different"].Public {
			t.Fatalf("local sidecar collision or canonical duplicate: %+v", row)
		}
	}
}
