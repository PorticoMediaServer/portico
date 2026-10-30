package catalog

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Episode range membership is normally a whole-show ROW_NUMBER window. For
// preparation, compute a candidate's exact rank by reducing raw episode keys;
// retain only its order tuple, one raw cursor and the count. This is deliberately
// conservative cold work, not a claim of cheap arbitrary show-range selection.
type selectionBatchRangeCursor struct {
	stage                        int
	show, season, number, target int64
	after, rank, item            int64
	check                        selectionBatchItemCheck
}

func (s *Service) selectionBatchRange(p *selectionBatchPlan, test selectionBatchTest, entity int64, c *selectionBatchRangeCursor, scalarBytes *int) (done, yes bool, err error) {
	switch c.stage {
	case 0:
		var eligible bool
		err = s.read().QueryRow(`SELECT ep.show_id,COALESCE(sd.number,1),ep.number,ep.entity_id,EXISTS(SELECT 1 FROM catalog_asset_links link WHERE link.entity_id=ep.entity_id) FROM catalog_episodes ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.entity_id=? AND ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, entity, test.rangeShow).Scan(&c.show, &c.season, &c.number, &c.target, &eligible)
		if errors.Is(err, sql.ErrNoRows) {
			return true, false, nil
		}
		if err != nil {
			return false, false, err
		}
		if !eligible || c.season == 0 {
			return true, false, nil
		}
		c.stage = 1
		return false, false, nil
	case 1:
		var show, season, number int64
		var available bool
		// The raw episode PK precedes show/order/asset/policy tests. Every
		// unrelated and rejected episode advances, including missing files.
		err = dbwork.Snapshot(s.Context()).QueryRowContext(s.Context(), `SELECT ep.entity_id,ep.show_id,COALESCE(sd.number,1),ep.number,EXISTS(SELECT 1 FROM catalog_asset_links link WHERE link.entity_id=ep.entity_id) FROM (SELECT entity_id,show_id,season_id,number FROM catalog_episodes WHERE entity_id>? ORDER BY entity_id LIMIT 1) ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id`, c.after).Scan(&c.item, &show, &season, &number, &available)
		if errors.Is(err, sql.ErrNoRows) {
			return true, c.rank > test.rangeStart && c.rank <= test.rangeStart+showRangeSize, nil
		}
		if err != nil {
			return false, false, err
		}
		c.after = c.item
		if show != c.show || season == 0 || !available || season > c.season || season == c.season && number > c.number || season == c.season && number == c.number && c.item > c.target {
			return false, false, nil
		}
		c.check = selectionBatchItemCheck{item: c.item}
		c.stage = 2
		return false, false, nil
	case 2:
		err = s.selectionBatchCheckItem(p, &c.check, scalarBytes)
		if err != nil {
			return false, false, err
		}
		if c.check.done {
			if c.check.visible {
				c.rank++
			}
			c.stage = 1
		}
		return false, false, nil
	}
	return false, false, ErrSelectionBatchIncomplete
}
