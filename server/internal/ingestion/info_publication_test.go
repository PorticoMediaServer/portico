package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"testing"
)

func settleInventoryAnalysis(t *testing.T, s *catalog.Service, db *sql.DB, ctx context.Context, job, object, revision string, policy int64, f assets.Facts) error {
	t.Helper()
	return settleInventoryInfo(t, db, ctx, func() (compactcatalog.InfoAcceptReceipt, error) {
		return s.AcceptInventoryAnalysis(ctx, job, object, revision, policy, f)
	})
}
func settleInventoryLocalRefresh(t *testing.T, s *catalog.Service, db *sql.DB, ctx context.Context, job, object, revision string, policy int64, f assets.Facts) error {
	t.Helper()
	return settleInventoryInfo(t, db, ctx, func() (compactcatalog.InfoAcceptReceipt, error) {
		return s.AcceptInventoryLocalRefresh(ctx, job, object, revision, policy, f)
	})
}
func settleInventoryInfo(t *testing.T, db *sql.DB, ctx context.Context, accept func() (compactcatalog.InfoAcceptReceipt, error)) error {
	t.Helper()
	for turn := 0; turn < 8; turn++ {
		receipt, err := accept()
		if err != nil {
			return err
		}
		if receipt.State == compactcatalog.InfoInputApplied {
			return nil
		}
		if err = compactcatalog.Drain(ctx, db); err != nil {
			return err
		}
	}
	return errors.New("informational source did not apply")
}

// The application runs the catalogue worker alongside scans. Fixtures must
// provide that same bounded progress while an accepted-pending frame waits;
// scan completion is not a substitute for source41 activation.
func advanceScanInfo(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := compactcatalog.NewWorker(db).Step(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
}

// Preserve process()'s 32-scan-quantum ceiling while interleaving the separately
// running catalogue worker after each quantum, rather than replaying expensive
// sidecar/probe IO32 times before allowing a source frame to advance once.
func scanInfoTurn(t *testing.T, s *Service, db *sql.DB, job, library string) {
	t.Helper()
	for turn := 0; turn < 32; turn++ {
		s.processQuantum(context.Background(), job, library)
		advanceScanInfo(t, db)
		state, err := s.Get(job)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status != "running" && state.Status != "queued" {
			return
		}
	}
}
