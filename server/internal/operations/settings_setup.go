package operations

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrSetupRemoteChoice: setup asked for a Remote access or Remote security
// value this build doesn't have.
var ErrSetupRemoteChoice = errors.New("unknown remote access or remote security choice")

// SetupRemoteChoicesTx writes the Remote access and Remote security choices
// made in first-run setup (O-169), inside setup's own transaction, so a server
// is never set up without them. The console revision moves and the file
// projection is queued for the two fields, as for any settings save.
func SetupRemoteChoicesTx(ctx context.Context, tx *sql.Tx, access, security string, now time.Time) error {
	if !oneOf(access, RemoteAccessModes) || !oneOf(security, RemoteSecurityModes) {
		return ErrSetupRemoteChoice
	}
	// Written as any first settings save is: the whole stored document, with
	// these two set (a missing settings row means "all defaults"; once the row
	// exists, every value is read from it).
	current, err := readSettings(tx)
	if err != nil {
		return err
	}
	values := current.Requested
	values.RemoteAccess, values.RemoteSecurity = access, security
	if err = writeStoredSettings(tx, values, now.UnixMilli()); err != nil {
		return err
	}
	revision := current.Revision + 1
	if _, err = tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=? WHERE singleton=1`, revision); err != nil {
		return err
	}
	for _, field := range []string{"remoteAccess", "remoteSecurity"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, field); err != nil {
			return err
		}
		if err := QueueConfigurationProjectionTx(ctx, tx, field, revision); err != nil {
			return err
		}
	}
	return nil
}
