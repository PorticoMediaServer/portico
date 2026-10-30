package administration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func saveSettingsReceipt(ctx context.Context, tx *sql.Tx, scope, key, digest string, out any, target string, revision, now int64) error {
	if _, _, ok := usermutation.RequestFrom(ctx); ok {
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: target, Revision: revision}, now)
	}
	return saveReceipt(ctx, tx, scope, key, digest, out, now)
}

// Fresh live-source preflight reservations are deliberately rolled back before
// network preparation. The accepting writer checks and reserves again.
var errSettingsPrepared = errors.New("settings preparation complete")
