package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ReadingOwnerValuesTx projects only accepted item owner rows for a bounded
// set of Reading titles. It performs no adoption/writes or automatic fallback.
// Callers must select authorized titles in this same transaction before calling.
func ReadingOwnerValuesTx(ctx context.Context, tx *sql.Tx, items []int64) (map[int64]ReadingValues, error) {
	out := make(map[int64]ReadingValues, len(items))
	if len(items) == 0 {
		return out, nil
	}
	if len(items) > 100 {
		return nil, ErrReadingValue
	}
	args := make([]any, 0, len(items))
	marks := make([]string, 0, len(items))
	for _, id := range items {
		if id < 1 {
			return nil, ErrReadingValue
		}
		if _, exists := out[id]; exists {
			return nil, ErrReadingValue
		}
		out[id] = ReadingValues{}
		args = append(args, id)
		marks = append(marks, "?")
	}
	// The accepted-format set matches readingAutomaticValuesTx exactly, so an
	// owner value is not dropped for the same title whose automatic value is
	// kept. This is only a projection over caller-selected titles; it neither
	// enables MOBI/AZW3 conversion nor admits a title into a Reading scope.
	query := `SELECT e.id,m.field,m.value,m.locked
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN libraries lib ON lib.id=cl.library_id
 JOIN metadata_owner_fields m ON m.kind='item' AND m.entity_id=e.id
 AND m.field IN('author','series','seriesNumber','publicationType')
 WHERE e.id IN (` + strings.Join(marks, ",") + `) AND e.retired=0 AND cl.retired=0
 AND ((e.kind=20 AND lib.kind='book') OR (e.kind=21 AND lib.kind='comic'))
 AND EXISTS(SELECT 1 FROM reading_publications p JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 WHERE p.item_id=e.id AND ((e.kind=20 AND p.format IN('epub','pdf','mobi','azw3')) OR (e.kind=21 AND p.format IN('cbz','pdf','cb7','cbr'))))`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[int64]map[string]ReadingOwnerValue{}
	for rows.Next() {
		var id int64
		var field, value string
		var locked bool
		if err = rows.Scan(&id, &field, &value, &locked); err != nil {
			return nil, err
		}
		if owners[id] == nil {
			owners[id] = map[string]ReadingOwnerValue{}
		}
		owners[id][field] = ReadingOwnerValue{value, locked}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for id, fields := range owners {
		v, err := ReadingEffectiveValues(ReadingValues{}, fields)
		if err != nil {
			return nil, fmt.Errorf("reading owner projection: %w", err)
		}
		out[id] = v
	}
	return out, nil
}
