package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Small physical fixture only: 4,096 shared directory values, 32 genres,
// 100 ready targets/role/genre, four genre memberships per retained target.
// No million-title catalogue or full candidate graph is constructed.
func TestGenreMaintainedRepresentationPhysicalSize(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	for first := 0; first < 4096; first += 32 {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for i := first; i < first+32; i++ {
				if _, err := tx.Exec(`INSERT INTO catalog_facet_base_values(library_id,field,value) VALUES(?,'genre',?)`, library, fmt.Sprintf("Genre %04d", i)); err != nil {
					return err
				}
			}
			return nil
		})
	}
	type sizes struct{ candidates, reverse, targets, ready, walk int64 }
	measure := func() sizes {
		t.Helper()
		var s sizes
		for name, dest := range map[string]*int64{"catalog_search_genre_candidates": &s.candidates, "catalog_search_genre_candidate_target": &s.reverse, "catalog_search_genre_targets": &s.targets, "catalog_search_genre_ready": &s.ready, "catalog_search_genre_value_walk": &s.walk} {
			if err := c.DB.QueryRow(`SELECT COALESCE(sum(pgsize),0) FROM dbstat WHERE name=?`, name).Scan(dest); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	empty := measure()
	b := &FacetBases{DB: c.DB, Producer: &facetFixtureProducer{}, MaxBytes: 128 << 20}
	scope := FacetBaseScope{LibraryID: library, Library: "movies", Field: "genre", Policy: FacetBasePolicy(Viewer{}.Restrictions), Source: SearchVideoGenreSource + `:[]`}
	seed := func(witness string) {
		var base int64
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			base, err = b.Begin(ctx, tx, scope, witness, "authority")
			return err
		})
		for i := 0; i < 800; i += 8 {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				for n := i; n < i+8; n++ {
					if _, err := tx.Exec(`INSERT INTO catalog_search_genre_targets(base_id,entity_id,public_id,kind,rating,sort_key,backdrop,poster) VALUES(?,?,zeroblob(16),1,5,?,zeroblob(32),zeroblob(32))`, base, n+1, fmt.Sprintf("ordinary title sort key %025d", n)); err != nil {
						return err
					}
				}
				return nil
			})
		}
		for g := 0; g < 32; g++ {
			var value int64
			if err := c.DB.QueryRow(`SELECT id FROM catalog_facet_base_values WHERE library_id=? AND field='genre' AND value=?`, library, fmt.Sprintf("Genre %04d", g)).Scan(&value); err != nil {
				t.Fatal(err)
			}
			c.Exec(`INSERT INTO catalog_search_genre_ready(base_id,value_id,last_target) VALUES(?,?,800)`, base, value)
			for role := 1; role <= 2; role++ {
				for first := 0; first < 100; first += 8 {
					c.Write(func(ctx context.Context, tx *sql.Tx) error {
						for i := first; i < first+8 && i < 100; i++ {
							entity := (g/4)*100 + i + 1
							if _, err := tx.Exec(`INSERT INTO catalog_search_genre_candidates(base_id,value_id,role,position,entity_id) VALUES(?,?,?,?,?)`, base, value, role, int64(i+1)*genrePositionGap, entity); err != nil {
								return err
							}
						}
						return nil
					})
				}
			}
		}
	}
	seed("one")
	one := measure()
	seed("retained-two")
	two := measure()
	total := func(s sizes) int64 { return s.candidates + s.reverse + s.targets + s.ready }
	t.Logf("dbstat bytes empty=%+v one=%+v two=%+v", empty, one, two)
	t.Logf("one-basis candidate+target+ready incremental=%d bytes for 6400 refs/800 shared targets/32 genres; second retained increment=%d; shared ID-walk=%d bytes/4096 directory values (%.2f B/value)", total(one)-total(empty), total(two)-total(one), one.walk, float64(one.walk)/4096)
	// 256 ordinary genres at the same four-membership/target coefficient gives
	// 51,200 capped refs and 6,400 retained targets, independent of catalogue N.
	projection := 8 * (total(one) - total(empty))
	t.Logf("256-genre projection one basis=%d bytes (%.2f MiB), two=%.2f MiB; shared value-walk excluded and charged separately", projection, float64(projection)/(1<<20), 8*float64(total(two)-total(empty))/(1<<20))
	if projection > 20<<20 {
		t.Fatal("candidate projection exceeds root review threshold", projection)
	}
}
