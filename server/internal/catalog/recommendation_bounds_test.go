package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// wholeLibrary is the explicit key list of every scorable item in a small
// test library: recBase has no whole-library form.
func wholeLibrary(t testing.TB, s *Service, r HomeRequest) []string {
	t.Helper()
	ids, whole, err := s.smallLibraryItems(r, 1<<20)
	if err != nil || !whole {
		t.Fatal("whole library", whole, err)
	}
	return ids
}

func TestRecommendationCandidateSetsStayBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "bounds.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("films", "Films", "movie", "/films")
	n := recommendationPoolCutover + 200
	var targetID int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("/films/f%08d.mkv", i)
			entity, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", path, 0), Title: "Film " + strconv.Itoa(i), Year: 1950 + i%70, Added: "2026-01-01T00:00:00.000Z"})
			if err != nil {
				return err
			}
			if i == n/2 {
				targetID = entity
			}
			token := compactcatalog.NewAssetToken()
			asset, err := compactcatalog.CreateAssetTx(ctx, tx, token, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: int64(i + 1), Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 6000}, true)
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			g1, g2 := i%5, (i+2)%5
			if err = compactcatalog.SetTermsTx(ctx, tx, entity, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{Name: "Genre " + strconv.Itoa(g1)}, {Name: "Genre " + strconv.Itoa(g2)}}); err != nil {
				return err
			}
			credits := []compactcatalog.Credit{}
			for p := 0; p < 2; p++ {
				person := (i + p) % 3
				credits = append(credits, compactcatalog.Credit{PersonKey: "tmdb:person:" + strconv.Itoa(person), PersonName: "Person " + strconv.Itoa(person), PersonSortName: "Person " + strconv.Itoa(person), ProviderPersonID: "p" + strconv.Itoa(person), CreditID: "c" + strconv.Itoa(p), CreditedName: "Actor", Role: "Actor", Department: "Acting", Ordinal: p})
			}
			if err = compactcatalog.SetCreditsTx(ctx, tx, entity, "tmdb", credits); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	r := HomeRequest{Profile: "p", Libraries: []string{"films"}}
	target := c.Public(targetID)
	if _, whole, err := s.smallLibraryItems(r, recommendationPoolCutover); err != nil || whole {
		t.Fatal("fixture must exceed the pool cutover", whole, err)
	}
	related, _, err := s.relatedPool(r, target)
	if err != nil || len(related) == 0 || len(related) > relatedPoolLimit || related[0] != target {
		t.Fatal("related pool", len(related), err)
	}
	recommended, _, err := s.recommendationPool(r)
	if err != nil || len(recommended) == 0 || len(recommended) > recommendationPoolLimit {
		t.Fatal("recommendation pool", len(recommended), err)
	}
	allowed := regexp.MustCompile(`^(candidate|json_each|CONSTANT|permitted|members|m|works|w|wp|facets|personal|seeds|interest|negative_facets|defaults|pn|j|\(subquery-\d+\))$`)
	scan := regexp.MustCompile(`^SCAN (\S+)`)
	for label, pool := range map[string][]string{"related": related, "recommended": recommended} {
		base, args := recBase(r, pool)
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+base+` SELECT (SELECT count(*) FROM facets),(SELECT count(*) FROM seeds),(SELECT count(*) FROM negative_facets)`, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var node, parent, unused int
			var detail string
			if err = rows.Scan(&node, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if m := scan.FindStringSubmatch(detail); m != nil && !allowed.MatchString(m[1]) {
				t.Errorf("%s scorer scans %q: %s", label, m[1], detail)
			}
		}
		rows.Close()
	}
	for _, row := range []string{"recommended", "related"} {
		got, err := s.recommendationCandidates(r, row, target)
		if err != nil {
			t.Fatal(row, err)
		}
		limit := recommendationPoolLimit
		if row == "related" {
			limit = relatedPoolLimit
		}
		if len(got) > limit {
			t.Fatal(row, "scored beyond its pool", len(got))
		}
	}
	if _, err = s.recommendationCandidates(r, "recent", ""); err != ErrHomeRowUnknown {
		t.Fatal("a row without a bounded candidate source must not score the library", err)
	}
	base, args := recBase(r, nil)
	var members int
	if err = db.QueryRow(base+` SELECT count(*) FROM members`, args...).Scan(&members); err != nil || members != 0 {
		t.Fatal("nil candidates widened to the library", members, err)
	}
}

func TestSmallLibraryPoolIsTheWholeLibrary(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "small.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	c.Movie(movies, "/movies/one.mkv", "One", 2020)
	c.Movie(movies, "/movies/two.mkv", "Two", 2021)
	c.Drain()
	s := New(db)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "fence", Libraries: []string{"movies"}}, Profile: "p", Libraries: []string{"movies"}}
	pool, bounded, err := s.recommendationPool(r)
	if err != nil || !bounded {
		t.Fatal(bounded, err)
	}
	whole := wholeLibrary(t, s, r)
	if len(pool) == 0 || len(pool) != len(whole) {
		t.Fatal("small pool is not the whole library", len(pool), len(whole))
	}
}
