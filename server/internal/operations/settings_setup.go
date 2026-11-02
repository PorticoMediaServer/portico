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
func SetupRemoteChoicesTx(ctx context.Context, tx *sql.Tx, access, security, allow string, now time.Time) error {
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
	fields := []string{"remoteAccess", "remoteSecurity"}
	// The lockout warning's Keep allowing this address: the one address setup is reached at,
	// validated as any custom address is.
	if allow != "" {
		origin, ok := AccessOrigin(allow)
		if !ok {
			return ErrSetupRemoteChoice
		}
		present := false
		for _, existing := range values.AccessURLs {
			present = present || existing == origin
		}
		if !present {
			if len(values.AccessURLs) >= MaxAccessURLs {
				return ErrSetupRemoteChoice
			}
			values.AccessURLs = append(append([]string{}, values.AccessURLs...), origin)
		}
		fields = append(fields, "accessUrls")
	}
	if err = writeStoredSettings(tx, values, now.UnixMilli()); err != nil {
		return err
	}
	revision := current.Revision + 1
	if _, err = tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=? WHERE singleton=1`, revision); err != nil {
		return err
	}
	for _, field := range fields {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, field); err != nil {
			return err
		}
		if err := QueueConfigurationProjectionTx(ctx, tx, field, revision); err != nil {
			return err
		}
	}
	return nil
}
