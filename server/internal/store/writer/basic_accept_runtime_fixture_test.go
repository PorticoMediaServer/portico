package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
	"time"
)

// Trusted component composition, not product enable or a measured host fit.
// Capture covers the complete existing registry and the closed Accept profile.
func basicAcceptDispatchFixture(t *testing.T) (*Writer, *executor, *governor.Authority, basicInvocation, effectivePlan) {
	t.Helper()
	e, c := basicFirstAcceptCommandFixture(t)
	q, err := c.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	primary := effectiveFixturePlan()
	ap := effectiveFixturePlan()
	ap.steps = nil
	ap.basicAcceptBounds = basicAcceptBounds{nfo: true}
	ap.basicMemory = commandMemoryBudget{4 << 20, 4 << 20}
	ap.need[governor.MemoryBytes] = 8 << 20
	ap.recipe.limit.Rows, ap.recipe.limit.VMOps, ap.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	acceptDefs := basicFinalDefinitions(ap.basicAcceptBounds)
	defs = append(defs, acceptDefs...)
	for _, d := range acceptDefs {
		work := Work{Rows: basicFinalRows(ap.basicAcceptBounds, d.ID), VMOps: 160000, Pages: 16, Hold: 25 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		ap.steps = append(ap.steps, statementStep{d.ID, work})
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "basic_accept_runtime_fixture_test.go", "basic_control_test.go", "basic_stage_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	primary.registration, ap.registration = golden.Receipt(), golden.Receipt()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = primary.need
		partitions[i][governor.MemoryBytes] = max(primary.need[governor.MemoryBytes], ap.need[governor.MemoryBytes])
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newEffectiveRuntime(e, path, g, a, wal, 1, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeBasicAcceptProfile(t.Context(), runtime, ap); err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: runtime}, e, a, basicInvocation{request: q, accept: c}, ap
}
func basicDispatchReturned(t *testing.T, e *executor, a *governor.Authority) {
	t.Helper()
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("Basic retained grant or physical connection", a.Usage(), e.db.Stats())
	}
}
