package metadata

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type lookupServiceKey struct{}
type lookupCapture struct {
	service *Service
	epoch   int64
}

func (s *Service) lookupContext(ctx context.Context) context.Context {
	if captured, ok := ctx.Value(lookupServiceKey{}).(lookupCapture); ok && captured.service == s {
		return ctx
	}
	// ReadHandle returns a *sql.DB pool, not the caller's Snapshot transaction.
	// Calling its QueryRowContext directly keeps these live IO fences outside any
	// caller-held read snapshot; dbwork.QueryRow would deliberately join it.
	allowed, epoch, capturedErr := localonly.Online(ctx, dbwork.ReadHandle(ctx, s.db))
	check := func() error {
		if capturedErr != nil {
			return capturedErr
		}
		if !allowed {
			return localonly.Err
		}
		current, revision, err := localonly.Online(ctx, dbwork.ReadHandle(ctx, s.db))
		if err != nil {
			return err
		}
		if !current || revision != epoch {
			return localonly.Err
		}
		return nil
	}
	return context.WithValue(metadataprovider.WithNetworkAdmission(ctx, func(context.Context) (func() error, error) {
		if err := check(); err != nil {
			return nil, err
		}
		return check, nil
	}), lookupServiceKey{}, lookupCapture{service: s, epoch: epoch})
}
func onlineLookupPolicyTx(ctx context.Context, tx *sql.Tx) (bool, int64, error) {
	return localonly.Online(ctx, tx)
}
func invalidateLookupWorkTx(ctx context.Context, tx *sql.Tx) error {
	return requestLookupFanoutTx(ctx, tx, "screen", "mb", "tvdb", "artwork", "subtitles")
}

func checkLookupContextTx(ctx context.Context, tx *sql.Tx) error {
	captured, ok := ctx.Value(lookupServiceKey{}).(lookupCapture)
	if !ok {
		return localonly.Err
	}
	return localonly.CheckOnline(ctx, tx, captured.epoch)
}
