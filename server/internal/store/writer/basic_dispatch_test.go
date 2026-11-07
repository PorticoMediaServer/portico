package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Structural forecasts only, never production figures or qualification. Cover
// simultaneous original/preparation, native copies, complete result TEXT and
// codec scratch at the byte caps, rather than just this small StageFact input.
// The declared reserve is not allocated by the fixture.
const basicDispatchCopyBytes = uint64(64<<10) + 32*uint64(maxCanonicalRequestBytes+maxCanonicalResultBytes)

func basicDispatchPlan(purpose CommandID) effectivePlan {
	p := effectiveFixturePlan()
	p.steps = nil
	p.basicMemory = commandMemoryBudget{basicDispatchCopyBytes, 4 << 20}
	p.need[governor.MemoryBytes] = int64(p.basicMemory.preparationBytes + p.basicMemory.reservedBytes)
	p.recipe.limit.Rows, p.recipe.limit.VMOps, p.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	for _, def := range basicCommandDefinitions(purpose) {
		rows := uint64(16)

		// Every one-fact effect/history use is reserved by the real
		// freezeCommandPlan before native BEGIN; no future operation is enabled.
		work := Work{Rows: rows, VMOps: 10000 * rows, Pages: 16, Hold: 25 * time.Microsecond}
		if def.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		p.steps = append(p.steps, statementStep{def.ID, work})
	}
	return p
}

func basicDispatchFixture(t *testing.T) (*Writer, *executor, *governor.Authority, basicInvocation, [2]effectivePlan) {
	t.Helper()
	e, source, original, identity := basicControlFixture(t)
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	stage, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage,
		operation: basicStageFactInput, fact: source, changeRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	q, err := stage.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	primary := effectiveFixturePlan()
	plans := [2]effectivePlan{basicDispatchPlan(BasicStage), basicDispatchPlan(BasicSeal)}
	primary.need = plans[0].need
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	for _, purpose := range []CommandID{BasicStage, BasicSeal} {
		for _, def := range basicCommandDefinitions(purpose) {
			found := false
			for _, old := range defs {
				if old.ID == def.ID {
					if !reflect.DeepEqual(old, def) {
						t.Fatal("inconsistent shared definition", def.ID)
					}
					found = true
					break
				}
			}
			if !found {
				defs = append(defs, def)
			}
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// One actual FULL-startup Capture of the entire registry. Reuse its
	// receipt on every NORMAL turn; never recapture after the first command.
	// SC's exact four-octet domain registration is a prerequisite. Keep this
	// fail closed when that rule is absent: no SQL rewrite, waiver or stub.
	golden := captureEffectiveFixture(t, conn, e.config, defs,
		"basic_dispatch_test.go", "basic_control_test.go", "basic_stage_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	primary.registration = golden.Receipt()
	for i := range plans {
		plans[i].registration = golden.Receipt()
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20,
		SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = primary.need
	}
	g, authority, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, authority, wal, 1, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeBasicProfiles(t.Context(), r, plans[0], plans[1]); err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: r}, e, authority, basicInvocation{request: q, stage: stage}, plans
}

func basicDispatchReturned(t *testing.T, e *executor, a *governor.Authority) {
	t.Helper()
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("Basic retained grant or physical connection", a.Usage(), e.db.Stats())
	}
}

func basicDispatchEvidence(t *testing.T, v *ExecutionEvidence) {
	t.Helper()
	if v == nil || v.Purpose != BasicStage || v.Class != governor.ScanMinimum || v.Durability != Normal ||
		v.Commit != CommitSucceeded || v.CommitAttemptedAt == nil || v.CommitReturnedAt == nil ||
		v.Policy == nil || v.Policy.Synchronous != int(sqliteconn.Normal) || v.PhysicalReturnedAt == nil || v.WALBefore == nil || v.WALAfter == nil {
		t.Fatal("missing actual same-purpose NORMAL physical frame evidence", v)
	}
}

func basicDispatchUnchanged(t *testing.T, e *executor) {
	t.Helper()
	var exact bool
	if err := e.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=41 AND last_command_sequence=18 AND acknowledged_command_sequence=18 AND discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')
 AND (SELECT used_bytes=1000 FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT rows_staged=0 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT count(*)=0 FROM catalog_info_new_stream_facts)
 AND (SELECT count(*)=0 FROM scan_command_receipts)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)
 AND (SELECT count(*)=0 FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT phase=0 AND job_revision=1 FROM catalog_info_changes WHERE id=51)`).Scan(&exact); err != nil || !exact {
		t.Fatal("refusal committed a Basic prefix", exact, err)
	}
}

func TestBasicDispatchFreshHistoryAckAndRetiredFloor(t *testing.T) {
	w, e, a, in, _ := basicDispatchFixture(t)
	q := in.request
	cap := inventoryTestDisclosure(t, w, q.Scan)
	fresh, ack, err := w.effective.runBasic(t.Context(), w, cap, in, false)
	if err != nil || !reflect.DeepEqual(ack, AckResult{}) || fresh.Outcome.State != Applied || fresh.Outcome.Replayed ||
		fresh.Outcome.Reference != q.Scan.Receipt || fresh.Purpose != BasicPurposeStage || fresh.RunID != q.Scan.RunID ||
		fresh.AcceptedRunRevision != 42 || fresh.SourceID != "basic-source" || fresh.SourceIncarnation != "basic-incarnation" ||
		fresh.SourceRevision != "object:019" || fresh.ChangeID != 51 || fresh.ExpectedEpoch != 17 ||
		fresh.Continuation != (BasicContinuation{}) || fresh.ResultCount != 0 || fresh.Members == nil || len(fresh.Members) != 0 || fresh.Digest == (ResultDigest{}) {
		t.Fatal("fresh StageFact was not physically Applied", fresh, ack, err)
	}
	basicDispatchEvidence(t, fresh.Outcome.Evidence)
	basicDispatchReturned(t, e, a)
	var exact bool
	if err := e.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=42 AND last_command_sequence=19 AND acknowledged_command_sequence=18 AND discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')
 AND (SELECT rows_staged=1 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=51)
 AND (SELECT accepted_revision=42 AND applied_ms=? AND request_digest=? AND result_digest=? AND command_kind='basic_stage' FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=19)
 AND (SELECT count(*)=0 FROM scan_command_results)
 AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)
 AND (SELECT count(*)=0 FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT phase=0 AND job_revision=1 FROM catalog_info_changes WHERE id=51)`,
		fresh.AppliedMS, q.Scan.Receipt.RequestDigest[:], fresh.Digest[:], 1000+192+len(in.stage.request.fact.assetToken)+len(in.stage.request.fact.fingerprint)).Scan(&exact); err != nil || !exact {
		t.Fatal("fresh receipt/effect/progress mismatch", exact, err)
	}
	for _, sql := range []string{
		`UPDATE catalog_libraries SET retired=1 WHERE id=7`,
		`UPDATE library_sources SET generation=24,root='/changed',root_identity='root:changed' WHERE id='basic-source'`,
		`UPDATE inventory_objects SET revision='object:020' WHERE id='basic-object'`,
		`UPDATE inventory_runs SET job_revision=99,job_lease_owner=99,job_lease_generation=99,job_lease_expires_ms=0 WHERE job_id='basic-run'`,
	} {
		if _, err := e.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	// Both the original fresh entry and original Resolve must classify saved
	// history before examining today's source/object/lease/run revision.
	for _, original := range []basicInvocation{in, {request: q}} {
		out, ack, err := w.effective.runBasic(t.Context(), w, cap, original, false)
		if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.Outcome.Reference != q.Scan.Receipt || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("current drift hid original history", out, ack, err)
		}
		basicDispatchEvidence(t, out.Outcome.Evidence)
		expected := fresh
		out.Outcome, expected.Outcome = ReceiptOutcome{}, ReceiptOutcome{}
		if !reflect.DeepEqual(out, expected) {
			t.Fatal("historical result was reconstructed from current state", out, fresh)
		}
		basicDispatchReturned(t, e, a)
	}
	out, ack, err := w.effective.runBasic(t.Context(), w, cap, basicInvocation{request: q}, true)
	if err != nil || !reflect.DeepEqual(out, BasicResult{}) || ack.AcknowledgedThrough != 19 {
		t.Fatal(out, ack, err)
	}
	basicDispatchEvidence(t, ack.Evidence)
	if err := e.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=99 AND last_command_sequence=19 AND acknowledged_command_sequence=19 AND job_lease_owner=99 AND job_lease_generation=99 AND job_lease_expires_ms=0 AND discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT count(*)=1 FROM scan_command_receipts WHERE run_id='basic-run' AND request_digest=? AND result_digest=? AND accepted_revision=42 AND applied_ms=?)
 AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=51)
 AND (SELECT rows_staged=1 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)
 AND (SELECT count(*)=0 FROM asset_stream_facts WHERE asset_id='basic-asset')
 AND (SELECT phase=0 AND job_revision=1 FROM catalog_info_changes WHERE id=51)
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')`, q.Scan.Receipt.RequestDigest[:], fresh.Digest[:], fresh.AppliedMS,
		1000+192+len(in.stage.request.fact.assetToken)+len(in.stage.request.fact.fingerprint)).Scan(&exact); err != nil || !exact {
		t.Fatal("ACK changed more than the floor or deleted the header", exact, err)
	}
	for _, original := range []basicInvocation{{request: q}, in} {
		out, _, err = w.effective.runBasic(t.Context(), w, cap, original, false)
		if err != nil || out.Outcome.State != ResultRetired || out.Outcome.Replayed || out.Outcome.Reference != q.Scan.Receipt {
			t.Fatal("floor resurrected Applied", out, err)
		}
		basicDispatchEvidence(t, out.Outcome.Evidence)
		out.Outcome = ReceiptOutcome{}
		if !reflect.DeepEqual(out, BasicResult{}) {
			t.Fatal("retired response retained a historical payload", out)
		}
	}
	basicDispatchReturned(t, e, a)
}

func TestBasicDispatchCurrentDisclosureConflictsAndClosedProfiles(t *testing.T) {
	w, e, a, in, plans := basicDispatchFixture(t)
	r, q := w.effective, in.request
	cap := inventoryTestDisclosure(t, w, q.Scan)
	registry, installed := r.registry, r.basicPlans
	r.basicPlans = [2]*effectivePlan{}
	for _, bad := range []struct {
		name string
		edit func(*[2]effectivePlan)
	}{
		{"missing_stage_capture", func(p *[2]effectivePlan) { p[0].registration = nil }},
		{"missing_seal_capture", func(p *[2]effectivePlan) { p[1].registration = nil }},
		{"missing_basic_memory", func(p *[2]effectivePlan) { p[0].basicMemory = commandMemoryBudget{} }},
	} {
		p := plans
		bad.edit(&p)
		err := composeBasicProfiles(t.Context(), r, p[0], p[1])
		if err == nil || r.registry != registry || r.basicPlans != ([2]*effectivePlan{}) {
			t.Fatal(bad.name, "unproved profile/registry installed", err)
		}
		if bad.name == "missing_basic_memory" {
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
				t.Fatal("missing memory profile lost typed refusal", err)
			}
		} else if !errors.Is(err, stmt.ErrUnqualified) {
			t.Fatal("missing Capture lost qualification refusal", err)
		}
		basicDispatchUnchanged(t, e)
	}
	if out, ack, err := r.runBasic(t.Context(), w, cap, in, false); !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("absent profiles admitted fresh mutation", out, ack, err)
	}
	r.basicPlans = installed
	// Retirement is a fresh-only eligibility predicate. Prove the miss before
	// the later duplicate-fact state or stale source/run can mask that gate.
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=1 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	if out, ack, err := r.runBasic(t.Context(), w, cap, in, false); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("retired fresh library admitted effects", out, ack, err)
	}
	basicDispatchUnchanged(t, e)
	if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET retired=0 WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	revoked := inventoryTestDisclosure(t, w, q.Scan)
	revoked.scope.revoked.Store(true)
	foreign := inventoryTestDisclosure(t, w, q.Scan)
	foreign.issuer = &Writer{}
	mismatch := inventoryTestDisclosure(t, w, q.Scan)
	mismatch.scope.libraryID++
	for _, denied := range []*ScanDisclosure{nil, foreign, mismatch, revoked} {
		out, ack, err := r.runBasic(t.Context(), w, denied, in, false)
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("current disclosure admitted fresh effects", out, ack, err)
		}
		basicDispatchUnchanged(t, e)
	}
	accept := basicInvocation{request: q}
	accept.request.Purpose = BasicPurposeAccept
	if out, ack, err := r.runBasic(t.Context(), w, cap, accept, false); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("Accept arm opened", out, ack, err)
	}
	if _, _, err := r.runBasic(t.Context(), w, cap, in, false); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BasicReceiptRequest){
		func(q *BasicReceiptRequest) { q.Scan.Receipt.RequestDigest[0]++ },
		func(q *BasicReceiptRequest) { q.Scan.Receipt.Identity.OperationID[0]++ },
	} {
		wrong := q
		mutate(&wrong)
		for _, acking := range []bool{false, true} {
			out, ack, err := r.runBasic(t.Context(), w, cap, basicInvocation{request: wrong}, acking)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
				t.Fatal("bad original disclosed history or advanced ACK", out, ack, err)
			}
		}
	}
	for _, acking := range []bool{false, true} {
		out, ack, err := r.runBasic(t.Context(), w, revoked, basicInvocation{request: q}, acking)
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("revocation disclosed saved result/floor", out, ack, err)
		}
	}
	var exact bool
	if err := e.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=42 AND last_command_sequence=19 AND acknowledged_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT count(*)=1 FROM scan_command_receipts WHERE run_id='basic-run')
 AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=51)`).Scan(&exact); err != nil || !exact {
		t.Fatal("conflict/revocation mutated saved state", exact, err)
	}
	basicDispatchReturned(t, e, a)
}

type basicDispatchCancelConn struct {
	writerDriverConn
	afterRunCAS func()
}

func (c *basicDispatchCancelConn) QueryContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, sql, args)
	if err == nil && strings.HasPrefix(sql, "UPDATE inventory_runs SET job_revision=") {
		return &receiptCloseRows{Rows: rows, hook: c.afterRunCAS}, nil
	}
	return rows, err
}

func TestBasicDispatchLateCancellationRollsBackNativeStageAndRunCAS(t *testing.T) {
	w, e, a, in, _ := basicDispatchFixture(t)
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(w.effective.path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reached := false
	e.openPool(connector, func(c driver.Conn) driver.Conn {
		return &basicDispatchCancelConn{writerDriverConn: c.(writerDriverConn), afterRunCAS: func() {
			reached = true
			cancel() // actual native StageFact and UPDATE RETURNING already ran
		}}
	})
	primeEffectiveFixturePool(t, e)
	out, ack, err := w.effective.runBasic(ctx, w, inventoryTestDisclosure(t, w, in.request.Scan), in, false)
	if !reached || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("late cancellation published or missed native CAS", reached, out, ack, err)
	}
	basicDispatchUnchanged(t, e)
	basicDispatchReturned(t, e, a)
}

// The opaque native scope returns request key bytes. Even a small independent
// memory partition must refuse BEFORE that SELECT can allocate an echo.
type basicScopeReadConn struct {
	writerDriverConn
	seen *bool
}

func (c *basicScopeReadConn) QueryContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(sql, "SELECT id,library_id FROM jobs ") {
		*c.seen = true
	}
	return c.writerDriverConn.QueryContext(ctx, sql, args)
}
func TestBasicDispatchMemoryPrecedesScopeEcho(t *testing.T) {
	w, e, a, in, plans := basicDispatchFixture(t)
	r := w.effective
	r.basicPlans = [2]*effectivePlan{}
	plans[0].basicMemory = commandMemoryBudget{preparationBytes: 4096, reservedBytes: uint64(plans[0].need[governor.MemoryBytes]) - 4096}
	if err := composeBasicProfiles(t.Context(), r, plans[0], plans[1]); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(r.path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	read := false
	e.openPool(connector, func(c driver.Conn) driver.Conn {
		return &basicScopeReadConn{writerDriverConn: c.(writerDriverConn), seen: &read}
	})
	primeEffectiveFixturePool(t, e)
	out, ack, err := r.runBasic(t.Context(), w, inventoryTestDisclosure(t, w, in.request.Scan), in, false)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || read || !reflect.DeepEqual(out, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("scope copied bytes before whole envelope", read, out, ack, err)
	}
	basicDispatchUnchanged(t, e)
	basicDispatchReturned(t, e, a)
}

func TestBasicDispatchMalformedWorkLifetimeFailsClosed(t *testing.T) {
	in := basicInvocation{work: &scanDriverWork{}}
	var r *effectiveRuntime
	value, ack, err := r.runBasic(t.Context(), nil, nil, in, false)
	if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(value, BasicResult{}) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal(value, ack, err)
	}
}
