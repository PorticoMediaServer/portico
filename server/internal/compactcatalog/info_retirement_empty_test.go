package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestInfoRetirementSkipsEmptyFamiliesWithinTheRawQuantum(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"available": false, "duration": 91.0})
	})
	var change int64
	reached := false
	for turn := 0; turn < 64; turn++ {
		var phase int
		if err := f.c.DB.QueryRow(`SELECT id,phase FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=0`, f.asset, f.token).Scan(&change, &phase); err != nil {
			t.Fatal(err)
		}
		if phase == 5 {
			reached = true
			break
		}
		f.step(t)
	}
	if !reached {
		t.Fatal("real publication never reached child retirement")
	}
	var before int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_scalar_old WHERE change_id=?`, change).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before < 1 || before > 8 {
		t.Fatalf("unexpected real scalar input size %d", before)
	}
	dbwork.ResetPlans()
	f.step(t)
	stats := dbwork.Plans()
	if stats.Measured != 1 || stats.Overruns != 0 {
		t.Fatalf("retirement exceeded its existing writer plan: %+v", stats)
	}
	var phase, family, remaining int
	if err := f.c.DB.QueryRow(`SELECT phase,copy_family,(SELECT count(*) FROM catalog_info_scalar_old WHERE change_id=c.id) FROM catalog_info_changes c WHERE id=?`, change).Scan(&phase, &family, &remaining); err != nil {
		t.Fatal(err)
	}
	// All 18 canonical child families are empty for a scalar-only change. They
	// must not cost 18 separate writer turns; the populated family remains the
	// durable frontier, and the header is still custody after this child turn.
	if phase != 5 || family != 18 || remaining != 0 {
		t.Fatalf("empty prefix did not reach exactly the scalar family: phase=%d family=%d rows=%d", phase, family, remaining)
	}
	t.Logf("retirement: raw=%d rows=%d pages=%d vmops=%d", before, stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
	// The next turn proves all remaining child prefixes empty. It must retire
	// the header then, rather than persist another empty protocol cursor.
	dbwork.ResetPlans()
	f.step(t)
	var headers int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE id=?`, change).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if headers != 0 {
		t.Fatal("proved-empty child prefixes left an extra parent-only turn")
	}
	if stats := dbwork.Plans(); stats.Measured != 1 || stats.Overruns != 0 {
		t.Fatalf("empty-tail parent retirement exceeded its existing writer plan: %+v", stats)
	}
	for turn := 0; turn < 16; turn++ {
		if f.step(t) {
			return
		}
	}
	t.Fatal("scalar-only child-first retirement did not finish")
}
