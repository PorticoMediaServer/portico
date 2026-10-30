package compactcatalog

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
)

// ServingTables retain an old relational generation lazily: the first change
// saves the old row (or its absence), without copying a whole library at start.
var ServingTables = []string{
	"catalog_browse_rows", "catalog_browse_blocks", "catalog_browse_buckets",
	"catalog_browse_counted_rows", "catalog_browse_memberships", "catalog_browse_member_metrics",
	"catalog_movie_category_members", "catalog_movie_category_summaries",
	"catalog_home_buckets", "catalog_item_availability", "catalog_facet_counts", "catalog_related_facets",
	"catalog_browse_groups", "catalog_facet_members", "catalog_browse_availability_counts",
}

var servingNames = func() *strings.Replacer {
	pairs := []string{}
	for _, name := range ServingTables {
		pairs = append(pairs, name, name+"_serving")
	}
	return strings.NewReplacer(pairs...)
}()
var servingIndex = regexp.MustCompile(`\s+INDEXED BY (?:catalog_browse_[a-z_]+|catalog_home_[a-z_]+|catalog_related_[a-z_]+|catalog_facet_[a-z_]+|catalog_movie_category_[a-z_]+)`)

// ServingSQL is used only by catalogue readers. Workers keep writing the new
// generation; SQL views select the serving generation in the reader's snapshot.
var browseReadAliases = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+catalog_browse_rows(?:\s+(?:AS\s+)?([a-z_][a-z_0-9]*))?`)

func ServingSQL(query string) string {
	// Compound serving views can merge their two indexed runs only when ORDER
	// BY names output columns. Normalize these equivalent expressions to the
	// view's computed columns, preserving the original indexes and collation.
	aliases := browseReadAliases.FindAllStringSubmatch(query, -1)
	for _, match := range aliases {
		alias := match[1]
		switch strings.ToLower(alias) {
		case "where", "join", "left", "inner", "indexed", "order", "group", "limit", "union", "":
			alias = ""
		}
		prefix := alias
		if prefix != "" {
			prefix += "."
		}
		query = strings.NewReplacer(
			prefix+"sort_key COLLATE NOCASE", prefix+"sort_key",
			"lower("+prefix+"sort_key)", prefix+"sort_key",
			"COALESCE("+prefix+"added_text,'')", prefix+"order_added",
			"COALESCE("+prefix+"duration_max,0)", prefix+"order_duration",
			"COALESCE("+prefix+"rating_max,0)", prefix+"order_rating",
		).Replace(query)
	}
	return servingNames.Replace(servingIndex.ReplaceAllString(query, ""))
}

func searchTables(query string, slot int) string {
	if slot == 0 {
		return query
	}
	return strings.NewReplacer("catalog_search_prefix_rows", "catalog_search_prefix_rows_next", "catalog_search_prefix_counts", "catalog_search_prefix_counts_next", "catalog_search_documents", "catalog_search_documents_next", "catalog_search_titles", "catalog_search_titles_next").Replace(query)
}

// SearchServingSQL resolves the active FTS generation in the same snapshot as
// the search. Both generations have independent documents and inverted indexes.
func SearchServingSQL(ctx context.Context, q ReadQuery, query string) (string, error) {
	if !strings.Contains(query, "catalog_search_titles") && !strings.Contains(query, "catalog_search_documents") && !strings.Contains(query, "catalog_search_prefix_rows") && !strings.Contains(query, "catalog_search_prefix_counts") {
		return query, nil
	}
	var slot int
	err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT active_slot FROM catalog_derivations WHERE domain=17),0)`).Scan(&slot)
	return searchTables(query, slot), err
}

func beginGeneration(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_generations SET generation=generation+1,retaining=1,visibility_sequence=(SELECT sequence FROM compact_visibility_source_state WHERE id=1) WHERE singleton=1 AND retaining=0 AND EXISTS(SELECT 1 FROM catalog_browse_rows WHERE entity_id>0)`)
	return err
}

// Wait for every source/facet quantum before exposing its retained rows. An
// interrupted facet merge can have partial new output, so source/facet poison
// keeps the previous serving image. CaptureFacetProducerFence reports a durable
// ErrFacetProducerFailed (not endless building); normal worker retries recover.
// Other legacy domains keep their existing poisoned-unit publication policy.
func finishGeneration(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_generations SET retaining=0 WHERE singleton=1 AND retaining=1 AND NOT EXISTS(SELECT 1 FROM compact_visibility_source_jobs WHERE first_pending<=catalog_generations.visibility_sequence) AND NOT EXISTS(SELECT 1 FROM catalog_browse_row_work) AND NOT EXISTS(SELECT 1 FROM catalog_browse_row_keys) AND NOT EXISTS(SELECT 1 FROM catalog_item_metric_jobs) AND NOT EXISTS(SELECT 1 FROM catalog_browse_counter_repairs) AND NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE rebuilding=1) AND NOT EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(33,36,37,38,39,40,41,42,43,44)) AND NOT EXISTS(SELECT 1 FROM catalog_dirty d WHERE domain IN(18,19,20,25,26,28,31,33,35,36,37,38,39,40,41,42,43,44) AND NOT EXISTS(SELECT 1 FROM catalog_derivation_failures f WHERE f.domain=d.domain AND f.entity_id=d.entity_id))`)
	return err
}
