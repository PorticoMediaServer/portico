package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestScanRetentionWritesStayInsideTheirDeclaredPlan(t *testing.T) {
	f := newLoopFixture(t, "movie")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	f.exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('retention-plan',?,'complete',?)`, f.library, old)
	f.exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,finished_at) SELECT 'retention-plan',id,generation,incarnation,root_identity,1,'complete',? FROM library_sources WHERE id=?`, old, f.source)
	f.exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<513) INSERT INTO inventory_directories(job_id,relative_path,state) SELECT 'retention-plan','old-'||x,'done' FROM n`)
	f.exec(`WITH RECURSIVE n(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<513) INSERT INTO inventory_changes(source_id,job_id,object_id,kind,created_at) SELECT ?,'retention-plan','old-'||x,'observed',? FROM n`, f.source, time.Now().Add(-40*24*time.Hour).UTC().Format(time.RFC3339Nano))
	dbwork.ResetPlans()
	for turn := 0; turn < 40; turn++ {
		more, err := f.s.PruneSource(context.Background(), f.source)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	stats := dbwork.Plans()
	t.Logf("retention maximum rows/pages/VM: %+v overrun count %d", stats.Largest, stats.Overruns)
	if stats.Overruns != 0 {
		t.Fatalf("retention exceeded its unchanged plan: %+v", stats.Recent)
	}
	if stats.Largest.Rows > 65 {
		t.Fatal("retention grew beyond one raw window and its cursor", stats.Largest)
	}
	if f.count(`SELECT count(*) FROM inventory_directories WHERE job_id='retention-plan'`) != 0 || f.count(`SELECT count(*) FROM inventory_changes WHERE job_id='retention-plan'`) != 0 {
		t.Fatal("bounded retention did not drain old scratch and changes")
	}
}
