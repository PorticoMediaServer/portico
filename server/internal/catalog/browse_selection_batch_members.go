package catalog

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A serving view can hide arbitrarily many freshly inserted/tombstoned keys
// before producing its LIMIT. Merge the two raw indexed runs explicitly so
// even those rejected keys and both EOF probes consume preparation units.
type selectionBatchMemberCursor struct {
	afterItem, afterSource int64
	item, source           int64
	stage                  int
}

func (s *Service) selectionBatchNextMember(entity int64, c *selectionBatchMemberCursor) (done, ready bool, item int64, err error) {
	tx := dbwork.Snapshot(s.Context())
	switch c.stage {
	case 0:
		c.item, c.source = 0, 0
		err = tx.QueryRowContext(s.Context(), `SELECT item_id,source FROM catalog_browse_memberships INDEXED BY sqlite_autoindex_catalog_browse_memberships_1 WHERE entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, entity, c.afterItem, c.afterSource).Scan(&c.item, &c.source)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		c.stage = 1
		return
	case 1:
		var old, source int64
		err = tx.QueryRowContext(s.Context(), `SELECT item_id,source FROM catalog_browse_memberships_previous INDEXED BY sqlite_autoindex_catalog_browse_memberships_previous_1 WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, entity, c.afterItem, c.afterSource).Scan(&old, &source)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, false, 0, err
		}
		if old > 0 && (c.item == 0 || old < c.item || old == c.item && source < c.source) {
			c.item, c.source = old, source
		}
		if c.item == 0 {
			return true, false, 0, nil
		}
		c.stage = 2
		return false, false, 0, nil
	case 2:
		var present bool
		err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_browse_memberships WHERE entity_id=? AND item_id=? AND source=?)`, entity, c.item, c.source).Scan(&present)
		if err != nil {
			return false, false, 0, err
		}
		c.afterItem, c.afterSource = c.item, c.source
		c.stage = 0
		return false, present, c.item, nil
	}
	return false, false, 0, ErrSelectionBatchIncomplete
}
