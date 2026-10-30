package httpapi

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// readinessWriteProbeDeadline bounds the fresh writer probe on both public
// readiness routes. A supervisor asks these questions often, so a blocked
// writer must be reported promptly rather than waiting behind a gate holder.
const readinessWriteProbeDeadline = 250 * time.Millisecond

// readinessResult is the complete public answer. It carries no database,
// watchdog or gate internals.
type readinessResult struct {
	Ready  bool
	Status string
}

// readinessState is the single decision shared by /health/ready and
// /v1/readiness. A configured watchdog refusal wins before any probe. The
// probe itself takes the same normal-priority writer gate and real BEGIN as a
// request-path mutation, then rolls back without writing anything.
func readinessState(ctx context.Context, db *sql.DB, health dbwork.HealthState,
	probe func(context.Context) error) readinessResult {
	if db == nil {
		return readinessResult{Ready: false, Status: "starting"}
	}
	switch health {
	case dbwork.CorruptState:
		return readinessResult{Ready: false, Status: "corrupt"}
	case dbwork.DegradedState:
		return readinessResult{Ready: false, Status: "degraded"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, readinessWriteProbeDeadline)
	defer cancel()
	if err := probe(probeCtx); err != nil {
		return readinessResult{Ready: false, Status: "degraded"}
	}
	return readinessResult{Ready: true, Status: "ready"}
}

func (d Dependencies) readiness(ctx context.Context) readinessResult {
	health := dbwork.HealthyState
	if d.Watchdog != nil {
		health = d.Watchdog.Report().State
	}
	return readinessState(ctx, d.DB, health, func(probeCtx context.Context) error {
		// Keep the read-pool availability check as well: a free writer does
		// not make the API usable when every foreground read slot is pinned.
		if err := d.DB.PingContext(probeCtx); err != nil {
			return err
		}
		return dbwork.CheckWriteReady(probeCtx, d.DB)
	})
}
