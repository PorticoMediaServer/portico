package remotesources

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type verificationListingContext struct{}

func withVerificationListing(ctx context.Context) context.Context {
	return context.WithValue(dbwork.WithClass(ctx, dbwork.ClassMaintenance), verificationListingContext{}, true)
}
func isVerificationListing(ctx context.Context) bool {
	v, _ := ctx.Value(verificationListingContext{}).(bool)
	return v
}
func verificationListingPause(ctx context.Context, started time.Time) error {
	if !isVerificationListing(ctx) {
		return nil
	}
	timer := time.NewTimer(6 * time.Since(started))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func verificationListingCommit(ctx context.Context, w *dbwork.Write, started time.Time) error {
	if err := w.Commit(); err != nil {
		return err
	}
	return verificationListingPause(ctx, started)
}
func listingExec(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	started := time.Now()
	result, err := dbwork.ExecWrite(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia), query, args...)
	if err == nil {
		err = verificationListingPause(ctx, started)
	}
	return result, err
}
func listingContext(ctx context.Context, key string) context.Context {
	if strings.HasPrefix(key, verificationKeyPrefix) {
		return withVerificationListing(ctx)
	}
	return ctx
}
