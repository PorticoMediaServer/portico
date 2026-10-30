package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A library built from empty in one go — every title's facts written, then
// all derived work drained together, the derived worker's bulk order — must
// end with exactly the derived data the incremental path keeps, where each
// title is written and derived before the next arrives. Films, a show's
// episodes and an album artist's songs with credits and genres cover every
// container kind the bulk order defers.
func TestBulkBuildDerivesWhatIncrementalBuildDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const n = 240
	open := func(name string) *sql.DB {
		db, err := persistence.Open(filepath.Join(t.TempDir(), name+".sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	ctx := context.Background()
	incremental, bulk := open("incremental"), open("bulk")
	if _, _, err := seedReference(incremental, n, 0, n, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := seedReference(bulk, n, 0, n, n, false); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err := bulk.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&queued); err != nil || queued < n {
		t.Fatalf("bulk fixture queued %d derived units before draining: %v", queued, err)
	}
	for _, db := range []*sql.DB{incremental, bulk} {
		if err := compactcatalog.Drain(ctx, db); err != nil {
			t.Fatal(err)
		}
		// The fence records mutation history, which differs between build
		// paths. Both paths must advance it; their final numbers need not match.
		var revision int64
		if err := db.QueryRow(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`).Scan(&revision); err != nil || revision <= 1 {
			t.Fatalf("browse predicate fence did not advance: %d %v", revision, err)
		}
		if err := db.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&revision); err != nil || revision <= 1 {
			t.Fatalf("selection predicate fence did not advance: %d %v", revision, err)
		}
		// Informational epochs count how often a path published an item's
		// facts, not which facts it holds: the bulk path accepts the same
		// inputs through more intermediate publications, so its epoch numbers
		// are legitimately higher. Readiness is the semantic claim and is
		// required of each database on its own, not inferred by comparing two
		// histories.
		var unready int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_entities
 WHERE info_desired_epoch<>info_applied_epoch OR info_source_blockers<>0
  OR info_availability_pending<>0 OR info_blocked_children<>0`).Scan(&unready); err != nil {
			t.Fatal(err)
		}
		if unready != 0 {
			t.Errorf("%d entities are not Ready after draining: want applied=desired with zero source blockers, no pending availability and no blocked children", unready)
		}
	}
	tables := derivedTables(t, incremental)
	t.Logf("comparing %d derived tables", len(tables))
	if len(tables) < 20 {
		t.Fatalf("only %d derived tables compared", len(tables))
	}
	for _, table := range tables {
		read := func(db *sql.DB) string {
			if query, ok := normalizedTables[table]; ok {
				return queryContent(t, db, query)
			}
			return tableContent(t, db, table)
		}
		want, got := read(incremental), read(bulk)
		if want != got {
			t.Errorf("%s differs between the incremental and the bulk build\nincremental:\n%s\nbulk:\n%s", table, clip(want), clip(got))
		}
	}
	// Counted blocks may split at different rows; the counts they hold may not.
	const blocks = `SELECT library_id,kind,ordering,sum(total) FROM catalog_browse_blocks GROUP BY 1,2,3`
	if want, got := queryContent(t, incremental, blocks), queryContent(t, bulk, blocks); want != got {
		t.Errorf("counted blocks differ\nincremental:\n%s\nbulk:\n%s", clip(want), clip(got))
	}
}

// artistFacet masks an artist facet, which is named by the artist's public
// id (random per database).
const artistFacet = `CASE WHEN facet LIKE 'a:%' THEN 'a:' ELSE facet END`

// normalizedTables compare through the values they stand for: search prefix
// terms are numbered in the order they were first seen.
var normalizedTables = map[string]string{
	"catalog_rec_facets":           `SELECT entity_id,` + artistFacet + ` FROM catalog_rec_facets`,
	"catalog_rec_member_counts":    `SELECT work_id,` + artistFacet + `,total,eligible FROM catalog_rec_member_counts`,
	"catalog_rec_postings":         `SELECT ` + artistFacet + `,library_id,quality,entity_id FROM catalog_rec_postings`,
	"catalog_rec_df":               `SELECT ` + artistFacet + `,sum(works) FROM catalog_rec_df GROUP BY 1`,
	"catalog_search_prefix_terms":  `SELECT prefix FROM catalog_search_prefix_terms`,
	"catalog_search_prefix_rows":   `SELECT t.prefix,r.entity_id,r.kind,r.library_id,r.tier,r.container,r.rating_key,r.label_key FROM catalog_search_prefix_rows r JOIN catalog_search_prefix_terms t ON t.id=r.term_id`,
	"catalog_search_prefix_counts": `SELECT t.prefix,c.kind,c.library_id,c.container,c.rating_key,c.label_key,c.total FROM catalog_search_prefix_counts c JOIN catalog_search_prefix_terms t ON t.id=c.term_id`,
}

// derivedTables are the tables derived work writes. Queues, cursors, the
// generation bookkeeping and the previous-generation copies record how the
// work went rather than what it produced; the title index's own storage
// depends on the order its documents arrived in (the documents themselves are
// compared); counted blocks are compared by their totals.
func derivedTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND sql NOT LIKE 'CREATE VIRTUAL%'
 AND (name LIKE 'catalog\_browse\_%' ESCAPE '\' OR name LIKE 'catalog\_search\_%' ESCAPE '\' OR name LIKE 'catalog\_home\_%' ESCAPE '\'
  OR name LIKE 'catalog\_facet\_%' ESCAPE '\' OR name LIKE 'catalog\_show\_facet\_%' ESCAPE '\' OR name LIKE 'catalog\_rec\_%' ESCAPE '\'
  OR name LIKE 'catalog\_related\_%' ESCAPE '\' OR name LIKE 'catalog\_movie\_category\_%' ESCAPE '\' OR name LIKE 'catalog\_item\_%' ESCAPE '\'
  OR name LIKE 'catalog\_content\_eligible%' ESCAPE '\' OR name LIKE 'catalog\_visibility\_%' ESCAPE '\' OR name LIKE 'catalog\_personal\_sort\_%' ESCAPE '\'
  OR name LIKE 'catalog\_container\_%' ESCAPE '\' OR name IN('catalog_entities','catalog_asset_links'))
 AND name NOT LIKE '%\_previous' ESCAPE '\' AND name NOT LIKE '%\_jobs' ESCAPE '\' AND name NOT LIKE '%\_next' ESCAPE '\' AND name NOT LIKE '%\_state' ESCAPE '\'
 AND name NOT LIKE 'catalog\_search\_titles%' ESCAPE '\'
 AND name NOT IN('catalog_browse_blocks','catalog_browse_metric_cursors','catalog_browse_predicate_epoch','catalog_selection_predicate_epoch','catalog_item_attribute_edges','catalog_item_details')
 ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// pathColumns differ between two databases by construction (random public
// ids and what is keyed on them) or record when, not what.
var pathColumns = map[string]bool{"public_id": true, "random_key": true, "entity_pid": true, "sequence": true, "added_ms": true, "updated_ms": true, "created_ms": true}

// historyColumns count how many times a build path published an item's facts,
// not what it published. Each path reaches a different epoch number (the bulk
// path coalesces and re-accepts inputs), so the epochs are not comparable
// between databases; every other column of these tables is. The readiness the
// epochs stand for is asserted per database before the comparison.
var historyColumns = map[string]map[string]bool{
	"catalog_entities": {"info_desired_epoch": true, "info_applied_epoch": true},
}

func tableContent(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !pathColumns[name] && !historyColumns[table][name] {
			columns = append(columns, `"`+name+`"`)
		}
	}
	rows.Close()
	if len(columns) == 0 {
		return ""
	}
	return queryContent(t, db, `SELECT `+strings.Join(columns, ",")+` FROM `+table)
}

// queryContent is a query's rows as sorted lines.
func queryContent(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
		}
		out = append(out, fmt.Sprint(values...))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func clip(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 12 {
		lines = append(lines[:12], fmt.Sprintf("… %d rows", len(lines)))
	}
	return strings.Join(lines, "\n")
}
