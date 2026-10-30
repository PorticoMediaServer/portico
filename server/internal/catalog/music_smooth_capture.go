package catalog

import (
	"context"
	"database/sql"
)

func (s *Service) MusicSmoothOrderTx(ctx context.Context, tx *sql.Tx, viewer Viewer, ids []string) (MusicSmoothOrder, error) {
	if tx == nil {
		return MusicSmoothOrder{}, ErrAdminQuery
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	// A window's first ordinary song remains its anchor. Depending on a
	// previously fetched window would make capture depend on request order.
	return clone.MusicSmoothOrder(viewer, ids, "")
}
