package main

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func initializeConsole(db *sql.DB, scan *ingestion.Service, player *playback.Service, hls *playback.HLS, cat *catalog.Service, lyricFetch *lyrics.Bulk, state string, existing ...*operations.Store) (*operations.Store, *operations.Scheduler, error) {
	store := operations.New(db)
	if len(existing) > 0 && existing[0] != nil {
		store = existing[0]
	}
	scheduler := operations.NewScheduler(store)
	err := scheduler.Register(operations.Adapter{Kind: "library-scan", Lane: "background-media", Resource: operations.LaneWriteHeavy,
		ValidateTx: func(ctx context.Context, tx *sql.Tx, resource string) error {
			var n int
			return tx.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id=?`, resource).Scan(&n)
		},
		StartTx: func(ctx context.Context, tx *sql.Tx, operation, resource string) (string, error) {
			return scan.QueueOperationTx(ctx, tx, operation, resource)
		},
		Observe: func(ctx context.Context, id string) (operations.JobObservation, error) {
			v, err := scan.ObserveOperation(ctx, id)
			code := ""
			if v.State == "failed" {
				code = "scan-failed"
			}
			return operations.JobObservation{State: v.State, Phase: v.Phase, Processed: &v.Processed, ErrorCode: code}, err
		},
		Cancel:    scan.CancelOperation,
		ControlTx: scan.ControlOperationTx,
		Interrupt: scan.InterruptOperation,
	})
	if err != nil {
		return nil, nil, err
	}
	noResource := func(ctx context.Context, tx *sql.Tx, resource string) error {
		if resource != "" {
			return operations.ErrInvalid
		}
		return nil
	}
	for _, hook := range []struct {
		kind string
		run  func(context.Context, string) error
	}{
		{"retention-cleanup", func(ctx context.Context, id string) error { return store.Prune(ctx) }},
		// The notification tick raises the conditions that are continuously true
		// rather than momentary: free space on the state volume and a certificate
		// running out. Measuring a volume is a platform call, so the path is passed
		// in and a platform that cannot answer simply reports nothing.
		{"notification-maintenance", func(ctx context.Context, id string) error {
			free, total, ok := telemetry.VolumeUsage(state)
			if !ok {
				free, total = 0, 0
			}
			return store.NotificationMaintenance(ctx, free, total)
		}},
		{"expired-playback-cleanup", func(ctx context.Context, id string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			return player.Cleanup()
		}},
		// Missing titles past the owner's rule are removed (it also runs on its
		// own after library changes; this lets an owner schedule or start it).
		{"missing-titles-cleanup", func(ctx context.Context, id string) error {
			_, e := cat.EmptyMissingDue(ctx)
			return e
		}},
		{"personal-receipt-cleanup", func(ctx context.Context, id string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			return cat.CleanupPersonalReceipts()
		}},
	} {
		if err = scheduler.Register(operations.Adapter{Kind: hook.kind, Lane: "maintenance", Resource: operations.LaneMaintenance, ValidateTx: noResource, Maintenance: hook.run}); err != nil {
			return nil, nil, err
		}
	}
	if lyricFetch != nil {
		// Library lyric acquisition is a background-media producer like the
		// scanner: the console admits it, the lyrics domain runs and records it.
		err = scheduler.Register(operations.Adapter{Kind: httpapi.LyricsFetchJobKind, Lane: "background-media", Resource: operations.LaneMetadata, ResourceRequired: true,
			ValidateTx: func(ctx context.Context, tx *sql.Tx, resource string) error {
				var n int
				return tx.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id=?`, resource).Scan(&n)
			},
			StartTx: func(ctx context.Context, tx *sql.Tx, operation, resource string) (string, error) {
				return lyricFetch.QueueTx(ctx, tx, operation, resource)
			},
			Observe: func(ctx context.Context, id string) (operations.JobObservation, error) {
				v, err := lyricFetch.Observe(ctx, id)
				processed := v.Processed
				return operations.JobObservation{State: v.State, Phase: v.Phase, Processed: &processed, ErrorCode: v.ErrorCode}, err
			},
			Cancel:   lyricFetch.Cancel,
			CancelTx: lyricFetch.CancelTx,
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if hls != nil {
		if err = scheduler.Register(operations.Adapter{Kind: "generated-playback-cleanup", Lane: "maintenance", Resource: operations.LaneMaintenance, ValidateTx: noResource, Maintenance: func(ctx context.Context, _ string) error { return hls.CleanupGenerated(ctx) }}); err != nil {
			return nil, nil, err
		}
	}
	return store, scheduler, nil
}
