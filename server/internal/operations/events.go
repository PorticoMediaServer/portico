package operations

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// AppendOperationTx publishes a committed operation revision in the same
// transaction as the operation change. An unchanged revision has no event.
func AppendOperationTx(tx *sql.Tx, id string) error {
	var revision int64
	var state string
	if err := tx.QueryRow(`SELECT revision,state FROM console_operations WHERE id=?`, id).Scan(&revision, &state); err != nil {
		return err
	}
	return AppendActivityProgressTx(tx, "operation.updated", "operation", id, strconv.FormatInt(revision, 10), state != "running" && state != "reconciling")
}

func updateOperationTx(tx *sql.Tx, id, query string, args ...any) (bool, error) {
	result, err := tx.Exec(query, args...)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, AppendOperationTx(tx, id)
}

func updateOperation(ctx context.Context, db *sql.DB, class dbwork.Class, id, query string, args ...any) (bool, error) {
	updated := false
	err := dbwork.WithWriteTx(ctx, db, class, func(tx *sql.Tx) error {
		var err error
		updated, err = updateOperationTx(tx, id, query, args...)
		return err
	})
	return updated, err
}

// AppendActivityProgressTx mirrors only an invalidation hint to administrators.
// In-flight progress is bounded to once a second per resource. Terminal and
// explicit state transitions are immediate; the read remains the authority.
func AppendActivityProgressTx(tx *sql.Tx, event, kind, id, revision string, immediate bool) error {
	now := time.Now().UnixMilli()
	if !immediate {
		var recent bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM api_events WHERE audience='admin' AND type=? AND resource_kind=? AND resource_id=? AND at_ms>?)`, event, kind, id, now-1000).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return nil
		}
	}
	return apievents.Append(tx, apievents.AdminAudience, event, kind, id, revision, nil)
}
