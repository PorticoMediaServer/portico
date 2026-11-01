package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A focused SQL-bound probe, not a filesystem scan: reuse the cached raw
// inventory fixture and add N resolved episodic-source records. The real
// all-kind scan controls separately verify actual scan behavior and RSS.
func TestScanFinishGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_INVENTORY_SCALE_ITEMS"))
	if n == 0 {
		t.Skip("hosted scan-finish probe")
	}
	seed := filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("inventory-%d.sqlite", n))
	path := filepath.Join(t.TempDir(), "finish.sqlite")
	copyInventoryScaleFile(t, path, seed)
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := &loopFixture{t: t, db: db, s: New(db, catalog.New(db), assets.Probe{})}
	if err = db.QueryRow(`SELECT s.library_id,s.id,l.root FROM library_sources s JOIN libraries l ON l.id=s.library_id LIMIT 1`).Scan(&f.library, &f.source, &f.root); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO episodic_sources(library_id,asset_id,source_name) SELECT ?,token,path FROM catalog_assets`, f.library)
	job := f.queue()
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
	f.exec(`UPDATE inventory_runs SET phase='analysis_enqueue' WHERE job_id=?`, job.ID)
	source := f.librarySource()
	matched := f.count(`SELECT count(*) FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE s.id=? AND o.root_incarnation=s.incarnation`, f.source)
	if matched != n {
		t.Fatalf("source proof matched %d/%d raw fixture objects", matched, n)
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	dbwork.ResetPlans()
	start := time.Now()
	var failure any
	func() { defer func() { failure = recover() }(); err = f.s.enqueueAnalysisPage(ctx, job.ID, source) }()
	plans := dbwork.Plans()
	result := map[string]any{"items": n, "fixture": "raw inventory plus N resolved episodic-source facts; no unresolved issues", "elapsedMs": float64(time.Since(start).Microseconds()) / 1000, "plans": plans, "panic": fmt.Sprint(failure), "error": fmt.Sprint(err), "statements": dbwork.StatementProfile()}
	for _, q := range trace() {
		if strings.HasPrefix(q, "SELECT EXISTS(SELECT 1 FROM library_sources s CROSS JOIN episodic_sources e") {
			result["attentionPlan"] = explainIssued(t, db, q, f.source)
		}
		if strings.HasPrefix(q, "UPDATE jobs SET status=CASE") && strings.Contains(q, "FROM episodic_sources e JOIN inventory_objects") {
			result["attentionPlan"] = explainIssued(t, db, q, job.ID, f.source, job.ID)
		}
	}
	if (failure != nil || err != nil || plans.Overruns != 0) && os.Getenv("PORTICO_EXPECT_FINISH_FAILURE") == "" {
		t.Errorf("scan finish: panic=%v error=%v overruns=%d", failure, err, plans.Overruns)
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	t.Log(string(b))
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "finish.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
