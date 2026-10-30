package downloads

import (
	"context"
	"database/sql"
	"time"
)

// RevokeForRetirement is the narrow receipt hook used by the catalogue's
// SeparateFolder retirement units. It keeps the existing revokeReceipt
// sequence allocation, the source_changed reason and the revocation feed
// intact. It never selects or rewrites a preparation, grant or request.
func RevokeForRetirement(_ context.Context, tx *sql.Tx, id string) error {
	return revokeReceipt(tx, id, ReasonSourceChanged, time.Now().UnixMilli())
}
