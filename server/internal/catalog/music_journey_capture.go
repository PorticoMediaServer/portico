package catalog

import (
	"context"
	"database/sql"
)

// Journey capture reads the current profile, model and source facts from the
// queue's admitted transaction, without a nested snapshot or cached selection.
func (s *Service) MusicJourneyTx(ctx context.Context, tx *sql.Tx, viewer Viewer, from, to string, length int) (MusicJourney, error) {
	if tx == nil {
		return MusicJourney{}, ErrAdminQuery
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	return clone.MusicJourney(viewer, from, to, length)
}
