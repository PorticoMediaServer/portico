package networking

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var ErrClaimOwnerRequired = errors.New("current local owner required")

// ClaimRequestGuard reauthorizes the captured current local owner token family
// in the caller transaction. Root binds its real SessionFamilyTx repository.
// This fences an interactive request; it does not renew durable owner consent.
type ClaimRequestGuard func(context.Context, *sql.Tx) error
type claimRequestScope struct {
	store *SQLiteStore
	guard ClaimRequestGuard
}
type claimRequestKey struct{}

func guardedClaimRequest(ctx context.Context, s *SQLiteStore, g ClaimRequestGuard) (context.Context, error) {
	if s == nil || g == nil {
		return nil, ErrInvalid
	}
	return context.WithValue(ctx, claimRequestKey{}, claimRequestScope{s, g}), nil
}
func guardClaimRequestTx(ctx context.Context, tx *sql.Tx) error {
	scope, ok := ctx.Value(claimRequestKey{}).(claimRequestScope)
	if !ok {
		return ctx.Err()
	}
	if scope.store == nil || scope.guard == nil {
		return ErrUnavailable
	}
	return scope.guard(ctx, tx)
}
func checkClaimRequest(ctx context.Context) error {
	scope, ok := ctx.Value(claimRequestKey{}).(claimRequestScope)
	if !ok {
		return ctx.Err()
	}
	if scope.store == nil || scope.guard == nil {
		return ErrUnavailable
	}
	gated, e := dbwork.BeginSnapshot(ctx, scope.store.db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return scope.guard(ctx, tx)
}
func commitClaimDatabaseTx(ctx context.Context, gatedArg *dbwork.Write) error {
	tx := gatedArg.Tx()
	return commitClaimTx(ctx, func() error {
		if e := guardClaimRequestTx(ctx, tx); e != nil {
			return e
		}
		return gatedArg.Commit()
	})
}
