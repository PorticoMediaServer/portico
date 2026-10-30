package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func (d Dependencies) captureOwnControlsRecipientTx(ctx context.Context, tx *sql.Tx, c playbackv1.Caller) (identity.ControlsRecipient, error) {
	if d.Identity == nil || tx == nil {
		return identity.ControlsRecipient{}, identity.ErrUnauthorized
	}
	original, err := d.Identity.CaptureControlsRecipientTx(ctx, tx, c.Principal)
	if err != nil {
		return identity.ControlsRecipient{}, err
	}
	if !original.MatchesDevice(c.DeviceID) {
		return identity.ControlsRecipient{}, identity.ErrUnauthorized
	}
	return original, nil
}

func (d Dependencies) captureOwnControlsRecipient(ctx context.Context, c playbackv1.Caller) (identity.ControlsRecipient, error) {
	if d.DB == nil {
		return identity.ControlsRecipient{}, identity.ErrUnauthorized
	}
	tx, done, err := dbwork.BeginRead(dbwork.WithClass(ctx, dbwork.ClassSecurityFence), d.DB)
	if err != nil {
		return identity.ControlsRecipient{}, err
	}
	defer done()
	return d.captureOwnControlsRecipientTx(ctx, tx, c)
}

// A composed controls response must still belong to its opening family and
// device after paging, metadata redaction and the separate count snapshot.
func (d Dependencies) checkOwnControlsRecipient(r *http.Request, c playbackv1.Caller, original identity.ControlsRecipient) error {
	if d.Identity == nil || d.DB == nil || !original.MatchesDevice(c.DeviceID) {
		return identity.ErrUnauthorized
	}
	if err := d.controlsRouteAdmission(r); err != nil {
		return err
	}
	fresh, err := d.strictPrincipalFor(r)
	if err != nil {
		return err
	}
	tx, done, err := dbwork.BeginRead(dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence), d.DB)
	if err != nil {
		return err
	}
	defer done()
	return d.Identity.CheckControlsRecipientTx(r.Context(), tx, original, fresh)
}
