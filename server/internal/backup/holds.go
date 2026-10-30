package backup

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A backup copies the database first and the files it names afterwards. If a
// collector deleted one of those files in between, the backup would either
// fail or carry a database that points at artwork it does not have. So, from
// before the first database step until the backup is published or abandoned,
// one row in backup_blob_holds tells every physical blob collector not to
// delete anything. It stops deletion only: writes, imports and revocations
// carry on.
//
// The row is durable so a restart cannot guess: a hold left by a process that
// is no longer running is that process's interrupted backup. Start-up removes
// it (ReleaseInterruptedHold) after the scratch it named has been cleaned, and
// a new backup replaces it in any case. There is no expiry: a hold that is
// still owned is never timed out from under a running copy.

// Holds are taken and moved in the backup's own class (copyLimits.class), so
// a scheduled backup's bookkeeping yields to users like its steps do, and a
// manual one is not stuck behind them before its first step. Each wait is
// bounded by the attempt's no-progress budget: a scheduled backup that cannot
// get a turn gives up with ErrBackupPressure rather than waiting forever.
//
// A service runs one backup at a time (Service.copyMu), and only one process
// opens the database, so any hold already there when a backup starts is stale:
// an earlier attempt's release failed, or an earlier process stopped. The new
// hold replaces it.

func holdWait(ctx context.Context, limits copyLimits) (context.Context, context.CancelFunc) {
	return context.WithTimeout(dbwork.WithClass(ctx, limits.class), limits.noProgress)
}

// holdError reports a refused or exhausted hold admission as backup pressure.
func holdError(ctx context.Context, err error) error {
	if err != nil && ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dbwork.ErrWriterCapacity)) {
		return ErrBackupPressure
	}
	return err
}

// holdBlobs records the running backup's hold. It runs before the first
// database step: under hard WAL pressure it waits like any writer, so copying
// never starts without the hold.
func (s *Service) holdBlobs(ctx context.Context, limits copyLimits, backupID, scratch string) error {
	wait, cancel := holdWait(ctx, limits)
	defer cancel()
	_, err := dbwork.ExecWrite(wait, s.db, limits.class,
		`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,?,?,?,?,'copying')
		 ON CONFLICT(singleton) DO UPDATE SET backup_id=excluded.backup_id,process_epoch=excluded.process_epoch,scratch_token=excluded.scratch_token,created_ms=excluded.created_ms,state='copying'`,
		backupID, s.epoch, scratch, s.now().UnixMilli())
	return holdError(ctx, err)
}

// advanceHold moves the hold to the backup's next phase.
func (s *Service) advanceHold(ctx context.Context, limits copyLimits, backupID, state string) error {
	wait, cancel := holdWait(ctx, limits)
	defer cancel()
	wait = dbwork.WithPlan(wait, dbwork.WritePlan{Name: dbwork.BackupHoldPhasePlanName, Rows: 1, Pages: 4, VMOps: 250_000, Why: "one singleton state update; no indexed state column, foreign-key action or trigger"})
	for {
		_, err := dbwork.ExecWrite(wait, s.db, limits.class,
			`UPDATE backup_blob_holds SET state=? WHERE singleton=1 AND backup_id=? AND process_epoch=?`, state, backupID, s.epoch)
		if !errors.Is(err, dbwork.ErrWriterCapacity) || wait.Err() != nil {
			return holdError(ctx, err)
		}
		// A WAL reservation refusal has no effect. Keep the finished copy and
		// retry this idempotent phase within the original no-progress deadline.
		if err = sleepContext(wait, backupBusyBackoff); err != nil {
			return holdError(ctx, err)
		}
	}
}

// releaseHold ends the hold whatever happened to the backup, after a
// cancellation too. It is a one-row delete taken as an interactive ticket, so
// it gets a turn among users; a release that still fails leaves the hold for
// the next backup (which replaces it) or the next start, which only delays
// artwork clean-up.
func (s *Service) releaseHold(backupID string) {
	ctx, cancel := context.WithTimeout(dbwork.WithClass(context.Background(), dbwork.ClassInteractive), time.Minute)
	defer cancel()
	for {
		_, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassInteractive,
			`DELETE FROM backup_blob_holds WHERE singleton=1 AND backup_id=? AND process_epoch=?`, backupID, s.epoch)
		if err == nil {
			return
		}
		if !errors.Is(err, dbwork.ErrWriterCapacity) || sleepContext(ctx, time.Second) != nil {
			log.Printf("warn: Backup could not release its artwork hold; artwork clean-up waits for the next backup or restart: %v", err)
			return
		}
	}
}

// ReleaseInterruptedHold removes a hold left by an earlier process. Start-up
// calls it after CleanupScratch, once the interrupted backup's scratch and
// partial folders are gone; a published backup is never part of a hold.
func (s *Service) ReleaseInterruptedHold(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("backup: no database")
	}
	_, err := dbwork.ExecWrite(dbwork.WithClass(ctx, dbwork.ClassMaintenance), s.db, dbwork.ClassMaintenance,
		`DELETE FROM backup_blob_holds WHERE process_epoch<>?`, s.epoch)
	return err
}
