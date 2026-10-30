package catalog

import (
	"context"
	"database/sql"
	"time"
)

// MusicMixKeysTx shares the queue/playlist's admitted transaction. It never
// opens another snapshot or returns an earlier profile's cached selection.
func (s *Service) MusicMixKeysTx(ctx context.Context, tx *sql.Tx, viewer Viewer, at time.Time, id string) ([]string, error) {
	if tx == nil {
		return nil, ErrAdminQuery
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	return clone.MusicMixKeys(viewer, at, id)
}

// SaveMusicMix copies through the sole ordinary playlist writer, including its
// authorization, bounds, creation receipt, lifetime and revision semantics.
// Name is the caller's localized display name from the canonical formatter.
// The mix identifier remains in the original create request hash, so a retry
// can replay a successful copy after its recommendation has become stale.
func (s *Service) SaveMusicMix(ctx context.Context, actor ResourceActor, id, operation, name string, at time.Time, currentViewer func(*sql.Tx) (Viewer, error), authorize func(*sql.Tx) error) (PlaylistReceipt, error) {
	if currentViewer == nil || authorize == nil {
		return PlaylistReceipt{}, ErrAdminQuery
	}
	return s.mutatePlaylistCaptured(actor, id, "create", "", PlaylistMutation{OperationID: operation, Name: &name}, authorize, func(tx *sql.Tx) ([]string, error) {
		viewer, err := currentViewer(tx)
		if err != nil {
			return nil, err
		}
		bound, ok := viewerFromPersonalKey(viewer.Profile)
		if !ok || bound.Authority != actor.Authority || bound.AccountID != actor.AccountID || bound.ProfileID != actor.ProfileID {
			return nil, sql.ErrNoRows
		}
		return s.MusicMixKeysTx(ctx, tx, viewer, at, id)
	})
}
