package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/idempotency"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/tableretention"
)

// Two pieces of housekeeping that were missing entirely, and one that is
// deliberately opt-in.
//
// The write-ahead log was checkpointed exactly once in the process's life — a
// PASSIVE pass at startup. `wal_autocheckpoint` covers about four mebibytes and
// a PASSIVE checkpoint does nothing at all while any reader holds a snapshot
// older than the log head, which during a scan with viewers browsing is
// continuously. The log grows onto the same volume as the generated media until
// the disk fills. It is now checkpointed on a timer, escalating to TRUNCATE only
// when the log is long AND nothing is writing or waiting to write.
//
// And nothing ever asked the database whether it was structurally sound.
// quick_check and foreign_key_check are both the size of the database, so they
// are opt-in and never near the startup path.

const (
	// integrityIntervalDefault is how often the opt-in structural check runs.
	integrityIntervalDefault  = 24 * time.Hour
	plannerStatisticsInterval = 5 * time.Minute
)

// Focused lifecycle knobs. Production keeps the real retention loop, the real
// planner refresh and the shipped intervals; tests may replace them to prove the
// housekeeping body advances while retention is blocked and that cancellation
// stops both supervised loops.
var (
	retentionRun                  = tableretention.Run
	plannerRefresh                = persistence.RefreshPlannerStatistics
	plannerRefreshInterval        = plannerStatisticsInterval
	housekeepingPollInterval      = dbwork.CheckpointPollInterval
	housekeepingAncillaryInterval = dbwork.CheckpointRegularInterval
	housekeepingTickHook          = func() {}
)

// integritySchedule reads the owner's choice. Off unless asked for, because on a
// multi-gigabyte library this is minutes of I/O and the owner should be the one
// deciding when to spend it.
func integritySchedule() (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PORTICO_INTEGRITY_CHECK"))) {
	case "", "off", "0", "false":
		return 0, false
	case "startup":
		return 0, true
	case "hourly":
		return time.Hour, true
	case "daily", "1", "true", "on":
		return integrityIntervalDefault, true
	case "weekly":
		return 7 * integrityIntervalDefault, true
	}
	if parsed, err := time.ParseDuration(strings.TrimSpace(os.Getenv("PORTICO_INTEGRITY_CHECK"))); err == nil && parsed > 0 {
		return parsed, true
	}
	return 0, false
}

// runDatabaseHousekeeping is the recurring half of the database's care: the
// checkpoint and planner refresh every start needs, and the structural check
// an owner asked for.
func runDatabaseHousekeeping(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	// How many schema objects this state directory holds that this build does not define —
	// a database written by a different or newer Portico. It is one sqlite_schema scan against
	// a set built once, so the cost does not grow with what it finds, and it happens once.
	// Objects a subsystem installs for itself on first use count as this build's own.
	// A directory this process just created cannot hold a foreign database, so its
	// warning is downgraded (the count is still published for diagnostics).
	if unknown, err := persistence.UnknownSchemaObjects(ctx, db); err == nil {
		dbwork.PublishForeignSchemaObjects(len(unknown))
		if warnForeignSchema(unknown) {
			log.Printf("This state directory holds %d schema objects this build does not define; they are left untouched. First: %s", len(unknown), strings.Join(unknown[:min(3, len(unknown))], ", "))
		}
	}
	// Owner-paused retention must never pause WAL pressure monitoring. Start it
	// asynchronously on this same lifecycle so housekeeping reaches its own
	// ticker instead of running retention's never-ending timer inline.
	supervise.Supervise(ctx, "cmd.record-retention", func(ctx context.Context) { retentionRun(ctx, db) })
	interval, integrity := integritySchedule()
	lastIntegrity := time.Time{}
	lastReceiptSweep := time.Now()
	lastUnknownSweep := time.Now()
	lastPlannerRefresh := time.Now()
	receipts := idempotency.Store{SweepBegin: func(ctx context.Context) (idempotency.Transaction, error) {
		return dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	}}
	if integrity && interval == 0 {
		// "startup" means once, after the handler is live and things are quiet.
		if dbwork.Yield(ctx) {
			reportIntegrity(ctx, db)
		}
		integrity = false
	}
	checkpoints, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		log.Printf("Checkpoint pressure monitoring could not resolve the database file; regular checks remain active: %v", err)
	}
	ticker := time.NewTicker(housekeepingPollInterval)
	defer ticker.Stop()
	ancillary := ancillaryMaintenanceCadence{last: time.Now()}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !dbwork.Yield(ctx) {
			return
		}
		if result, ran := checkpoints.Tick(ctx); ran {
			if result.Mode == "truncate" && checkpointSettled(result) {
				log.Printf("Write-ahead log truncated from %d frames", result.BeforeLogFrames)
			}
		}
		housekeepingTickHook()
		// Only WAL size probes become more frequent. Preserve the previous
		// cadence for backlog draining, receipt pruning and structural checks.
		if !ancillary.due(time.Now()) {
			continue
		}
		if time.Since(lastPlannerRefresh) >= plannerRefreshInterval {
			// A startup-only refresh freezes one-row bootstrap estimates while
			// sessions and catalogues grow. Reuse the yielding, short-writer
			// refresh; it counts only stale tables and samples index keys.
			lastPlannerRefresh = time.Now()
			if err := plannerRefresh(ctx, db); err != nil {
				if ctx.Err() == nil {
					log.Printf("Planner statistics refresh will retry: %v", err)
				}
			} else {
				log.Printf("Database planner statistics refresh completed")
			}
		}
		if err := persistence.ReclaimSpace(ctx, db); err != nil {
			log.Printf("warn: Database space reclamation: %v", err)
		}
		if time.Since(lastReceiptSweep) >= 15*time.Minute {
			// Check the indexed expiry on a read connection first. Backlog is
			// drained in short gated transactions rather than one long writer.
			backlog, err := sweepDueReceipts(ctx, db, receipts)
			if err != nil {
				log.Printf("warn: Idempotency receipt pruning failed: %v", err)
				lastReceiptSweep = time.Now()
			} else if !backlog {
				lastReceiptSweep = time.Now()
			}
		}
		if time.Since(lastUnknownSweep) >= 15*time.Minute {
			// Guessed names are stored only as keyed digests. Retain their
			// decaying attempt debt for one day, then prune a bounded batch at
			// maintenance priority even while foreground work continues.
			result, err := dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, `DELETE FROM identity_unknown_attempts WHERE rowid IN (SELECT rowid FROM identity_unknown_attempts WHERE last_failure<? ORDER BY last_failure LIMIT 100)`, time.Now().Add(-24*time.Hour).Unix())
			if err != nil {
				log.Printf("warn: Unknown sign-in attempt pruning failed: %v", err)
				lastUnknownSweep = time.Now()
			} else if count, _ := result.RowsAffected(); count < 100 {
				// The per-account ledger decays the same way: a day after its
				// last failure (and past any delay it set) a row says nothing an
				// absent row would not, so it goes in the same bounded batches.
				now := time.Now()
				result, err = dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, `DELETE FROM identity_credential_attempts WHERE rowid IN (SELECT rowid FROM identity_credential_attempts WHERE last_failure<? AND locked_until<? ORDER BY last_failure LIMIT 100)`, now.Add(-24*time.Hour).Unix(), now.Unix())
				if err != nil {
					log.Printf("warn: Sign-in attempt pruning failed: %v", err)
					lastUnknownSweep = now
				} else if count, _ := result.RowsAffected(); count < 100 {
					lastUnknownSweep = now
				}
			}
		}
		if !integrity || time.Since(lastIntegrity) < interval {
			continue
		}
		// The structural check runs only when nothing is waiting on the database,
		// because it is the size of the database.
		if dbwork.WriteGate().ActiveOrWaiting() || dbwork.ForegroundWorkActive() {
			continue
		}
		lastIntegrity = time.Now()
		reportIntegrity(ctx, db)
	}
}

type ancillaryMaintenanceCadence struct{ last time.Time }

func (c *ancillaryMaintenanceCadence) due(now time.Time) bool {
	if now.Sub(c.last) < housekeepingAncillaryInterval {
		return false
	}
	c.last = now
	return true
}

// A fully copied PASSIVE log can still need a hard reset. Reader drain timeout
// and retry backoff preserve SQLite's successful copy counts, so those counts
// alone cannot stop housekeeping retries when no further commits arrive.
// Only completed outcomes settle; future pressure outcomes also remain retryable.
func checkpointSettled(result dbwork.CheckpointResult) bool {
	return dbwork.CheckpointSettled(result)
}

// warnForeignSchema reports whether the foreign-schema warning is logged.
// A directory this process just created cannot hold a foreign database, so its
// warning is downgraded (the count is still published for diagnostics).
func warnForeignSchema(unknown []string) bool {
	return len(unknown) > 0 && !stateDirFreshThisProcess
}

// sweepDueReceipts never takes the writer gate for an empty expiry index.
// Under a backlog it re-enters the gate for each small batch, giving queued
// foreground requests a chance between batches.
func sweepDueReceipts(ctx context.Context, db *sql.DB, receipts idempotency.Store) (bool, error) {
	var due bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_idempotency WHERE expires_at<=? LIMIT 1)`, time.Now().Unix()).Scan(&due); err != nil {
		return false, err
	}
	if !due {
		return false, nil
	}
	for batch := 0; batch < 10; batch++ {
		removed, err := receipts.Sweep(ctx, 100)
		if err != nil {
			return false, err
		}
		if removed < 100 {
			return false, nil
		}
		runtime.Gosched()
	}
	return true, nil
}

// reportIntegrity runs one structural check, publishes it for the diagnostic and
// says anything an owner needs to act on out loud. A check whose result only
// reaches a diagnostic nobody opened is a check that found nothing.
func reportIntegrity(ctx context.Context, db *sql.DB) {
	report := dbwork.Integrity(ctx, db)
	dbwork.PublishIntegrity(report)
	if report.Error != "" {
		log.Printf("Database structural check could not finish: %s", report.Error)
		return
	}
	if report.QuickCheck != "ok" && report.QuickCheck != "" {
		log.Printf("Database structural check reported: %s", report.QuickCheck)
	}
	if report.ForeignKeyViolations > 0 {
		// Named by child table, because "2,419 orphaned rows" is not something
		// anybody can act on and "1,310 of them in screen_metadata_fields" is.
		worst := ""
		for _, table := range dbwork.SortedTables(report.ByChildTable) {
			worst += fmt.Sprintf(" %s=%d", table, report.ByChildTable[table])
		}
		log.Printf("Database holds %d orphaned rows across %d tables:%s", report.ForeignKeyViolations, len(report.ByChildTable), worst)
	}
}
