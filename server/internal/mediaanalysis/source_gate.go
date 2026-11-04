package mediaanalysis

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func sourceStageAllowedTx(ctx context.Context, tx *sql.Tx, source, operation string) (bool, error) {
	return catalog.SourceScanOperationAllowedTx(ctx, tx, source, operation)
}
