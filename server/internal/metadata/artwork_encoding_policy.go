package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type artworkEncodingPolicy struct {
	revision      int64
	edge, ceiling int
}
type artworkEncodingPolicyKey struct{}

func readArtworkEncodingPolicyTx(ctx context.Context, tx *sql.Tx) (artworkEncodingPolicy, error) {
	var out artworkEncodingPolicy
	if tx == nil {
		return out, ErrRepairInput
	}
	if err := tx.QueryRowContext(ctx, `SELECT settings.revision,settings.artwork_max_edge FROM metadata_runtime_settings AS settings WHERE settings.singleton=1`).Scan(&out.revision, &out.edge); err != nil {
		return out, err
	}
	if out.revision < 1 {
		return out, ErrRepairInput
	}
	ceiling, ok := artworkLargeCap[out.edge]
	if !ok {
		return out, ErrRepairInput
	}
	out.ceiling = ceiling
	return out, nil
}

// CaptureArtworkContext reads the actual normalized encoder policy. Its guard
// must run in the same write transaction that publishes the encoded picture.
// Missing or damaged policy is an error, never an invented default or grant.
func (s *Service) CaptureArtworkContext(ctx context.Context) (context.Context, func(context.Context, *sql.Tx) error, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, nil, ErrRepairInput
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, nil, err
	}
	defer snapshot.Rollback()
	policy, err := readArtworkEncodingPolicyTx(ctx, snapshot.Tx())
	if err != nil {
		return nil, nil, err
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		current, err := readArtworkEncodingPolicyTx(ctx, tx)
		if err != nil {
			return err
		}
		if current != policy {
			return ErrEpisodeThumbnailEncodingStale
		}
		return nil
	}
	return context.WithValue(ctx, artworkEncodingPolicyKey{}, policy), check, nil
}
