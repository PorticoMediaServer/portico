package administration

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"time"
)

type BackgroundPauseChange struct {
	Paused      bool   `json:"paused"`
	OperationID string `json:"operationId"`
}

// PauseBackground is idempotent: retrying the same action does not extend the
// hour. It is durable across restarts; resume clears the deadline immediately.
func (s *Service) PauseBackground(ctx context.Context, auth Authorize, change BackgroundPauseChange) (MaintenanceDocument, error) {
	var out MaintenanceDocument
	if !validOperationID(change.OperationID) {
		return out, ErrInput
	}
	digest := digestOf(change)
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		saved, replayed, err := receipt[MaintenanceDocument](ctx, tx, "background-pause", change.OperationID, digest)
		if err != nil {
			return err
		}
		if replayed {
			out = saved
			return nil
		}
		settings, revision, err := readMaintenanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		normalizeMaintenance(&settings)
		if err = maintenanceRuntime(ctx, tx, &settings); err != nil {
			return err
		}
		until := int64(0)
		if change.Paused {
			until = s.milliseconds() + time.Hour.Milliseconds()
		}
		if _, err = tx.ExecContext(ctx, `UPDATE maintenance_policy SET paused_until_ms=?,revision=revision+1 WHERE singleton=1`, until); err != nil {
			return err
		}
		stored := settings
		stored.Windows = nil
		if err = writeMaintenanceSettings(ctx, tx, revision+1, stored, s.milliseconds()); err != nil {
			return err
		}
		out = maintenanceDocument(Document[MaintenanceSettingsDocument]{Revision: revision + 1, Digest: digestOf(settings), Settings: settings})
		out.PausedUntil = until
		return saveReceipt(ctx, tx, "background-pause", change.OperationID, digest, out, s.milliseconds())
	})
	if err == nil {
		s.ApplyBackgroundPolicy(ctx)
	}
	return out, err
}

// ApplyBackgroundPolicy refreshes the process projection from durable authority.
// It is called at startup and after every maintenance mutation.
func (s *Service) ApplyBackgroundPolicy(ctx context.Context) {
	// The write may already have committed when the HTTP caller disconnects.
	// Finish projecting durable controls without borrowing that cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return
	}
	defer snapshot.Rollback()
	p, err := workpolicy.ReadTx(ctx, snapshot.Tx())
	if err != nil {
		return
	}
	settings, _, err := readMaintenanceSettings(ctx, snapshot.Tx())
	if err != nil {
		return
	}
	dbwork.SetBackgroundPolicy(settings.BackgroundSpeed, p.PausedUntil)
}
