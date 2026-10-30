package operations

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// Protocol settings use only the normalized tiny ledger. These legacy helpers
// are reached after CheckContext has reserved capacity in the same transaction.
func settingsLegacyReceipt(ctx context.Context, tx *sql.Tx, scope, key string, body any, now int64) (string, string, error) {
	if _, _, ok := usermutation.RequestFrom(ctx); ok {
		return "", "", nil
	}
	return Receipt(tx, scope, key, body, now)
}
func saveSettingsReceipt(ctx context.Context, tx *sql.Tx, scope, key, digest string, out any, target string, revision, now int64) error {
	if _, _, ok := usermutation.RequestFrom(ctx); ok {
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: target, Revision: revision}, now)
	}
	return SaveReceipt(tx, scope, key, digest, out, now)
}
