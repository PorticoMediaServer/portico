package administration

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// RunLogoImports drains source-publication jobs. Saving a source publishes its
// logo URLs and enqueues the job in the same database transaction. The worker
// never fetches on a request path or polls when there is no work.
func (s *Service) RunLogoImports(ctx context.Context) {
	if s == nil || s.db == nil || s.LogoFetch == nil {
		return
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "live_logo_jobs")
	defer unregister()
	for ctx.Err() == nil {
		if !dbwork.Yield(ctx) {
			return
		}
		var source, generation, last string
		err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
			`SELECT source_id,generation_id,last_channel FROM live_logo_jobs INDEXED BY live_logo_jobs_due WHERE state='pending' AND next_attempt_ms<=? ORDER BY next_attempt_ms,source_id LIMIT 1`, s.milliseconds()).Scan(&source, &generation, &last)
		if err == sql.ErrNoRows {
			var next sql.NullInt64
			if err = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
				`SELECT min(next_attempt_ms) FROM live_logo_jobs WHERE state='pending'`).Scan(&next); err != nil {
				log.Printf("channel logo schedule: %v", err)
				if !wake.Wait(ctx) {
					return
				}
				continue
			}
			if !next.Valid {
				if !wake.Wait(ctx) {
					return
				}
				continue
			}
			wait := time.Until(time.UnixMilli(next.Int64))
			if wait > 0 {
				waitCtx, cancel := context.WithTimeout(ctx, wait)
				_ = wake.Wait(waitCtx)
				cancel()
			}
			continue
		}
		if err != nil {
			log.Printf("channel logo queue: %v", err)
			if !wake.Wait(ctx) {
				return
			}
			continue
		}
		batchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		result, nextChannel, done, importErr := s.importLogosBatch(batchCtx, nil, source, "logo-"+generation, last, 8)
		batchErr := batchCtx.Err()
		cancel()
		state, nextAttempt := "pending", int64(0)
		if done {
			state = "complete"
		}
		if importErr != nil {
			if ctx.Err() == nil && (batchErr == context.DeadlineExceeded || errors.Is(importErr, context.DeadlineExceeded)) {
				nextAttempt = s.milliseconds() + 60_000
			} else {
				state = "failed"
			}
			if ctx.Err() == nil {
				log.Printf("channel logo import: %v", importErr)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia,
			`UPDATE live_logo_jobs SET state=?,imported=imported+?,skipped=skipped+?,last_channel=?,next_attempt_ms=? WHERE source_id=? AND generation_id=? AND state='pending'`,
			state, result.Imported, result.Skipped, nextChannel, nextAttempt, source, generation); err != nil {
			log.Printf("channel logo receipt: %v", err)
			if !wake.Wait(ctx) {
				return
			}
		}
	}
}
