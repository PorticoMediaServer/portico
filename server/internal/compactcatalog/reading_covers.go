package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
)

// ReadingCoverQueriesTx projects ready selected covers for an already admitted
// bounded set of entities, as the artwork query naming each cover's source and
// describing its two slots. It supplies no visibility or source-access grant.
func ReadingCoverQueriesTx(ctx context.Context, tx *sql.Tx, entities []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(entities) == 0 {
		return out, nil
	}
	if tx == nil || len(entities) > 4096 {
		return nil, fmt.Errorf("invalid Reading cover projection")
	}
	encoded, err := json.Marshal(entities)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.entity_id,a.digest,`+artworkref.Columns+` FROM json_each(?) selected
 CROSS JOIN artwork_selections a ON a.kind='item' AND a.entity_id=selected.value AND a.role='cover' AND a.subject=''`+artworkref.Joins("a.digest"), string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entity int64
		var digest string
		var d artworkref.Descriptor
		if err = rows.Scan(append([]any{&entity, &digest}, d.ScanTargets()...)...); err != nil {
			return nil, err
		}
		out[entity] = d.Query(digest)
	}
	return out, rows.Err()
}
