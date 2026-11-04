package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Explicit point publication uses the same typed latest head as the worker.
func publishReadingPathValuesTx(ctx context.Context, tx *sql.Tx, item, asset int64, object, revision string, v compactcatalog.ReadingValues) (bool, error) {
	changed, err := compactcatalog.ReadingPublishPathTx(ctx, tx, item, asset, object, revision, v)
	if err == nil {
		err = compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
	}
	return changed, err
}
