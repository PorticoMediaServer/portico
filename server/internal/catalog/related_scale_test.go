package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// TestRelatedRecommendationsAtScale is an opt-in measurement (ARCH-SRV-04):
// the item page's related rows in a synthetic film library of N items, with
// the bounded related pool, against the previous whole-library scoring.
//
//	PORTICO_RELATED_SCALE=200000 go test ./internal/catalog -run TestRelatedRecommendationsAtScale -count=1 -v
func TestRelatedRecommendationsAtScale(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_RELATED_SCALE"))
	if n <= 0 {
		t.Skip("set PORTICO_RELATED_SCALE to the film count")
	}
	path := os.Getenv("PORTICO_RELATED_DB") // reuse a seeded file across runs
	if path == "" {
		path = filepath.Join(t.TempDir(), "related.sqlite")
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	seeded := time.Now()
	var existing int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE kind=?`, compactcatalog.Movie).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	var films int64
	target := ""
	if existing == 0 {
		films = c.Library("films", "Films", "movie", "/films")
		var root string
		if err = db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, films).Scan(&root); err != nil {
			t.Fatal(err)
		}
		targetID := int64(0)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for i := 0; i < n; i++ {
				path := fmt.Sprintf("/films/f%08d.mkv", i)
				id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Film %d", i), Year: 1950 + i%70, Added: "2026-01-01T00:00:00.000Z"})
				if err != nil {
					return err
				}
				if i == n/2 {
					targetID = id
				}
				asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 6000})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
					return err
				}
				terms := []compactcatalog.Term{}
				for _, offset := range []int{0, 7, 19} {
					genre := (i + offset) % 50
					terms = append(terms, compactcatalog.Term{SourceID: strconv.Itoa(genre), Name: fmt.Sprintf("Genre %d", genre)})
				}
				if err = compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, "tmdb", terms); err != nil {
					return err
				}
				credits := []compactcatalog.Credit{}
				for ordinal := 0; ordinal < 3; ordinal++ {
					person := (i*3 + ordinal) % 20000
					personID := strconv.Itoa(person)
					credits = append(credits, compactcatalog.Credit{PersonKey: "tmdb:" + personID, PersonName: fmt.Sprintf("Person %d", person), ProviderPersonID: personID, CreditID: "c" + strconv.Itoa(ordinal), CreditedName: fmt.Sprintf("Person %d", person), Role: "Actor", Department: "Acting", Ordinal: ordinal})
				}
				if err = compactcatalog.SetCreditsTx(ctx, tx, id, "tmdb", credits); err != nil {
					return err
				}
			}
			return nil
		})
		c.Drain()
		target = c.Public(targetID)
	} else {
		if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='films' AND retired=0`).Scan(&films); err != nil {
			t.Fatal(err)
		}
		path := "/films/f" + leftPad(strconv.Itoa(n/2), 8) + ".mkv"
		var targetID int64
		if err = db.QueryRow(`SELECT e.id FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE a.path=?`, path).Scan(&targetID); err != nil {
			t.Fatal(err)
		}
		target = c.Public(targetID)
	}
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d compact films in %s", n, time.Since(seeded).Round(time.Millisecond))
	s := New(db)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"films"}}
	measure := func(label string, run func() error) {
		best, first := time.Duration(1<<62), time.Duration(0)
		for k := 0; k < 5; k++ {
			began := time.Now()
			if err := run(); err != nil {
				t.Fatal(label, err)
			}
			took := time.Since(began)
			if k == 0 {
				first = took
			}
			best = min(best, took)
		}
		t.Logf("%s: first %s, best of 5 %s", label, first.Round(100*time.Microsecond), best.Round(100*time.Microsecond))
	}
	if os.Getenv("PORTICO_RELATED_PROFILE") != "" {
		item, err := s.Get("p", target)
		if err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		_, err = s.recommendationCandidates(HomeRequest{Libraries: []string{"films"}}, "related", target)
		t.Logf("related candidates %s %v", time.Since(began), err)
		for _, step := range []struct {
			name string
			run  func() error
		}{
			{"relatedPool", func() error {
				_, _, err := s.relatedPool(HomeRequest{Libraries: []string{"films"}}, target)
				return err
			}},
			{"movieRelations", func() error {
				genres, err := s.itemGenreList(target)
				if err != nil {
					return err
				}
				specs, err := s.movieRelations(item, genres)
				for _, spec := range specs {
					if err == nil {
						_, err = spec.order(relatedEntryLimit)
					}
				}
				return err
			}},
			{"filter", func() error {
				rows, _, err := s.ItemRecommendationRows(viewer, target, 12, false)
				if err != nil {
					return err
				}
				_, err = s.filterRecommendationRows(viewer, rows)
				return err
			}},
		} {
			began = time.Now()
			err := step.run()
			t.Logf("%s %s %v", step.name, time.Since(began), err)
		}
		began = time.Now()
		rels, err := s.directorRelation(item)
		t.Logf("directorRelation %s %v", time.Since(began), err)
		for _, rel := range rels {
			began = time.Now()
			_, err := rel.order(relatedEntryLimit)
			t.Logf("relation %s order %s %v", rel.relation, time.Since(began), err)
		}
	}
	measure("item related rows (bounded pool)", func() error {
		_, _, err := s.ItemRecommendationRows(viewer, target, 12, false)
		return err
	})
	measure("previous whole-library related scoring", func() error {
		r := HomeRequest{Libraries: []string{"films"}}
		base, args := recBase(r, scaleWholeLibrary(t, s, r))
		args = append(args, target)
		var count int
		return db.QueryRow(base+` , target AS (SELECT work FROM members WHERE id=?) SELECT count(*) FROM works w WHERE EXISTS(SELECT 1 FROM facets f WHERE f.work=w.work AND f.f IN(SELECT f2.f FROM facets f2 JOIN target t ON t.work=f2.work))`, args...).Scan(&count)
	})
}

func scaleWholeLibrary(t *testing.T, s *Service, r HomeRequest) []string {
	t.Helper()
	ids, whole, err := s.smallLibraryItems(r, 1<<20)
	if err != nil || !whole {
		t.Fatal("whole library", whole, err)
	}
	return ids
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}
