package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// A prefix leaf changes at most five rows: itself, two counts and two empty
// count removals. Keep forty rows in reserve under the ordinary 160-row plan.
// The parent document (and its native FTS entry) is removed in a later turn.
const searchCleanupRawRows = 24

func cleanSearchGeneration(ctx context.Context, tx *sql.Tx, slot, limit int) (int, error) {
	var entity int64
	err := tx.QueryRowContext(ctx, searchTables(`SELECT entity_id FROM catalog_search_documents ORDER BY entity_id LIMIT 1`, slot)).Scan(&entity)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET cleaning=0 WHERE domain=17`)
		// The phase transition is a committed turn, not catalogue exhaustion.
		return 1, err
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, searchTables(`SELECT term_id,kind,library_id,tier FROM catalog_search_prefix_rows WHERE entity_id=? ORDER BY term_id LIMIT ?`, slot), entity, min(limit, searchCleanupRawRows))
	if err != nil {
		return 0, err
	}
	type key struct{ term, kind, library, tier int64 }
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.term, &k.kind, &k.library, &k.tier); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(keys) > 0 {
		// Point deletes by the complete primary key avoid a tuple-IN plan that
		// could walk a whole prefix relation. Count-maintenance triggers stay on.
		query := searchTables(`DELETE FROM catalog_search_prefix_rows WHERE term_id=? AND kind=? AND library_id=? AND tier=? AND entity_id=?`, slot)
		for _, k := range keys {
			if _, err = tx.ExecContext(ctx, query, k.term, k.kind, k.library, k.tier, entity); err != nil {
				return 0, err
			}
		}
		return len(keys), nil
	}
	// The empty-leaf witness and document delete share this writer transaction;
	// no FK cascade can expand the leaf work behind the page limit.
	_, err = tx.ExecContext(ctx, searchTables(`DELETE FROM catalog_search_documents WHERE entity_id=?`, slot), entity)
	return 1, err
}
