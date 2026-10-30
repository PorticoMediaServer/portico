package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// Facet counts (catalog_facet_members, catalog_facet_counts). An item's facet
// values are read from its facts; the item is counted once per value, under
// its library and its browse row's restriction keys (rating_key, label_key),
// so a reader decides each count against a viewer's restriction the way the
// browse buckets are decided. A change moves the item's counts by one, never
// recounts a value. Baseline triggers queue the item on every source fact.
//
// In a show library the show holds the facets, not its episodes: Genre, Year
// and the rest then count shows ("Drama 12" beside "24 shows"), which is what a
// Shows grid filters. A show's values are its own genres and year, and whatever
// any of its episodes says about the file or the title (resolution, network,
// studio…), the same facts a filter on the show reaches through its episodes.
// An episode's change reaches its show through what the episode says about
// it (catalog_show_facet_episodes): only a change to that moves the show's
// value counts and derives the show again, and the show reads the counts,
// never its episodes. Episode genre witnesses also support restricted show
// readers; the unrestricted show stage explicitly excludes those genres.

// FacetHolderSQL is the browse rows that can hold facet values, over the
// alias e: shows, and items other than episodes. It is the condition
// the holder stage applies, so a filtered grouping tests its predicate on
// these rows only.
//
// The kind set (shows and the playable kinds other than episodes, whose
// browse rows carry their item) lets a reader seek the (library, kind)
// indexes instead of reading every row of the library.
func FacetHolderSQL(e string) string {
	show, episode := strconv.Itoa(int(Show)), strconv.Itoa(int(Episode))
	return `(` + e + `.kind IN(SELECT id FROM catalog_kinds WHERE id=` + show + ` OR playable=1 AND id<>` + episode + `) AND (` + e + `.kind=` + show + ` OR ` + e + `.item_id IS NOT NULL))`
}

// DomainFacetCounts keeps catalog_facet_members and catalog_facet_counts.
const DomainFacetCounts = 33

// FacetAttributeFields are the exact descriptive source fields counted here.
var FacetAttributeFields = []string{"contentRating", "audioLanguage", "studio", "network", "tag", "label", "series"}

type facetKey struct{ field, value string }
type facetPlace struct {
	library             int64
	ratingKey, labelKey string
}

func adjustFacetCount(ctx context.Context, tx *sql.Tx, at facetPlace, k facetKey, delta int) error {
	if delta > 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_facet_counts(library_id,field,value,rating_key,label_key,total) VALUES(?,?,?,?,?,1)
		 ON CONFLICT(library_id,field,value,rating_key,label_key) DO UPDATE SET total=total+1`, at.library, k.field, k.value, at.ratingKey, at.labelKey)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_counts SET total=total-1 WHERE library_id=? AND field=? AND value=? AND rating_key=? AND label_key=?`,
		at.library, k.field, k.value, at.ratingKey, at.labelKey); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_counts WHERE library_id=? AND field=? AND value=? AND rating_key=? AND label_key=? AND total=0`,
		at.library, k.field, k.value, at.ratingKey, at.labelKey)
	return err
}

func init() {
	Register(DomainFacetCounts, Derivation{
		Version: 7,
		Drain:   drainFacetHolders,
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			// Visit current AND previously accepted owners. Missing browse rows
			// must still withdraw their old counts/episode contributions. Seek
			// each prefix separately instead of DISTINCT/UNION over every value
			// of a holder with arbitrarily many accepted values.
			ids := make([]int64, 0, limit)
			for len(ids) < limit {
				var next sql.NullInt64
				if err := tx.QueryRowContext(ctx, `SELECT min(id) FROM (
 SELECT (SELECT entity_id FROM catalog_browse_rows WHERE entity_id>?1 ORDER BY entity_id LIMIT 1) id
 UNION ALL SELECT (SELECT entity_id FROM catalog_facet_members WHERE entity_id>?1 ORDER BY entity_id LIMIT 1)
 UNION ALL SELECT (SELECT episode_id FROM catalog_show_facet_episodes WHERE episode_id>?1 ORDER BY episode_id LIMIT 1))`, after).Scan(&next); err != nil {
					return nil, err
				}
				if !next.Valid {
					break
				}
				after = next.Int64
				ids = append(ids, after)
			}
			return ids, nil
		},
	})
}

// CheckFacetCounts verifies every facet count against its member rows (for
// tests and diagnostics; it reads both tables whole).
func CheckFacetCounts(ctx context.Context, db *sql.DB) error {
	var diff int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM (
	 SELECT library_id,field,value,rating_key,label_key,total FROM catalog_facet_counts
	 EXCEPT SELECT library_id,field,value,rating_key,label_key,count(*) FROM catalog_facet_members GROUP BY 1,2,3,4,5)`).Scan(&diff)
	if err != nil {
		return err
	}
	var missing int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM (
	 SELECT library_id,field,value,rating_key,label_key,count(*) FROM catalog_facet_members GROUP BY 1,2,3,4,5
	 EXCEPT SELECT library_id,field,value,rating_key,label_key,total FROM catalog_facet_counts)`).Scan(&missing); err != nil {
		return err
	}
	if diff != 0 || missing != 0 {
		return fmt.Errorf("facet counts: %d counts disagree with their members, %d member groups uncounted", diff, missing)
	}
	return nil
}
