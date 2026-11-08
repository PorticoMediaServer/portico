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

// Protocol-only source facts and synthetic complete profiles. This is not
// production ScanDriver issuance, native-byte qualification or a latency run.
func inventoryFacadeFixture(t *testing.T, clock ...*time.Time) (*Writer, map[string]ScanReceiptRequest, *executor, *governor.Authority) {
	t.Helper()
	e, requests, _ := inventoryLookupFixture(t)
	if len(clock) > 1 || (len(clock) == 1 && clock[0] == nil) {
		t.Fatal("invalid fixture clock")
	}
	if len(clock) == 1 {
		*clock[0] = e.now()
		e.now = func() time.Time { return *clock[0] }
	}
	if _, err := e.db.ExecContext(t.Context(), "INSERT INTO catalog_libraries(id,library_id,root,retired) VALUES(8,'retired-library','/retired',1)"); err != nil {
		t.Fatal(err)
	}
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	profile := func(defs []stmt.Definition) effectivePlan {
		p := effectivePlan{recipe: turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond},
			setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}},
			headerSync: Work{WALBytes: 32, Hold: 5 * time.Millisecond}}
		for _, def := range defs {
			p.steps = append(p.steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
		}
		p.need[governor.MemoryBytes], p.need[governor.CPUMilli] = 128<<20, 100
		p.need[governor.WALBytes], p.need[governor.IOBytes] = int64(p.recipe.limit.WALBytes), 64<<20
		p.io = []governor.DeviceIO{{DeviceID: "protocol-fixture", Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
		return p
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	p, scan := profile(defs), profile(inventoryHistoryDefinitions())
	scan.inventoryMemory = inventoryMemoryBudget{64 << 20, 64 << 20}
	// Runtime binding qualifies the COMPLETE compiled set through actual
	// Capture. Source facts/resource profiles remain protocol fixtures only.
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, append(defs, inventoryHistoryDefinitions()...), "inventory_dispatch_test.go", "inventory_resolution_test.go", "scan_receipts_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.registration, scan.registration = golden.Receipt(), golden.Receipt()
	gate, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = p.need
	}
	// Capture the clock function: later cancellation instrumentation may read
	// Authority.Usage, which must never run recursively under GV's mutex.
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: gate})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, gate, 1, p, scan)
	if err != nil {
		t.Fatal(err)
	}
	return &Writer{effective: r}, requests, e, a
}

func inventoryTestDisclosure(t *testing.T, w *Writer, q ScanReceiptRequest) *ScanDisclosure {
	t.Helper()
	return &ScanDisclosure{issuer: w, scope: &scanScope{runID: q.RunID, libraryKey: q.LibraryKey, libraryID: q.LibraryID,
		incarnation: q.Receipt.Identity.Incarnation, lifetime: t.Context()}}
}

func TestInventoryFacadeHistoryAckAndRetiredFloor(t *testing.T) {
	w, requests, e, a := inventoryFacadeFixture(t)
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	for range 2 {
		out, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.Outcome.Reference != q.Receipt ||
			out.RunID != q.RunID || out.SourceRevision != "opaque:007" || out.AcceptedRunRevision != 2 || len(out.Members) != 1 || out.Members[0].AcceptedAssetRevision != 1 {
			t.Fatal("saved result was not returned exactly", out, err)
		}
		v := out.Outcome.Evidence
		if v == nil || v.Purpose != InventoryAccept || v.Class != governor.ScanMinimum || v.Durability != Normal || v.Commit != CommitSucceeded ||
			v.Policy == nil || v.PhysicalReturnedAt == nil || v.WALBefore == nil || v.WALAfter == nil {
			t.Fatal("missing actual Inventory frame evidence", v)
		}
		// Returned slice mutation must not change the native historical result.
		out.Members[0].ObjectID = "changed"
	}
	for _, mutate := range []func(*ScanReceiptRequest){
		func(q *ScanReceiptRequest) { q.Receipt.RequestDigest[0]++ },
		func(q *ScanReceiptRequest) { q.Receipt.Identity.OperationID[0]++ },
	} {
		wrong := q
		mutate(&wrong)
		if out, err := w.ResolveScan(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) || out.RunID != "" || len(out.Members) != 0 {
			t.Fatal("conflict disclosed result", out, err)
		}
		if ack, err := w.AckScan(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) || ack.AcknowledgedThrough != 0 {
			t.Fatal("conflict changed floor", ack, err)
		}
	}
	ack, err := w.AckScan(t.Context(), cap, q)
	if err != nil || ack.AcknowledgedThrough != 1 || ack.Evidence == nil || ack.Evidence.Commit != CommitSucceeded {
		t.Fatal(ack, err)
	}
	// ACK alone retained all physical children; delete them here only as a
	// historical-floor fixture, never as a production reclaim command.
	var count int
	if err := e.db.QueryRowContext(t.Context(), "SELECT count(*) FROM scan_command_results WHERE run_id=?", q.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatal("ACK refunded/deleted child", count, err)
	}
	for _, sql := range []string{"DELETE FROM scan_command_results WHERE run_id=?", "DELETE FROM scan_command_receipts WHERE run_id=?"} {
		if _, err := e.db.ExecContext(t.Context(), sql, q.RunID); err != nil {
			t.Fatal(err)
		}
	}
	out, err := w.ResolveScan(t.Context(), cap, q)
	if err != nil || out.Outcome.State != ResultRetired || out.RunID != "" || len(out.Members) != 0 || out.Digest != (ResultDigest{}) {
		t.Fatal("retired result invented Applied", out, err)
	}
	ack, err = w.AckScan(t.Context(), cap, q)
	if err != nil || ack.AcknowledgedThrough != 1 || a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("retired ACK or physical retirement failed", ack, err)
	}
}

func TestInventoryFacadeFreshMissAndCurrentDisclosure(t *testing.T) {
	w, requests, e, _ := inventoryFacadeFixture(t)
	for _, name := range []string{"new", "stale", "remaining"} {
		q := requests[name]
		cap := inventoryTestDisclosure(t, w, q)
		out, err := w.ResolveScan(t.Context(), cap, q)
		want := map[string]ResultState{"new": Pending, "stale": Applied, "remaining": ResultRetired}[name]
		if err != nil || out.Outcome.State != want {
			t.Fatal(name, out, err)
		}
	}
	q := requests["new"]
	cap := inventoryTestDisclosure(t, w, q)
	if ack, err := w.AckScan(t.Context(), cap, q); !errors.Is(err, ErrResolveRequired) || ack.AcknowledgedThrough != 0 {
		t.Fatal("fresh miss ACKed", ack, err)
	}
	in, identity := inventoryRequestFixture()
	in.scope = inventoryScope{q.RunID, q.LibraryKey, q.LibraryID}
	in.run.lastSequence, in.run.acknowledgedSequence = 0, 0
	identity = q.Receipt.Identity
	command, err := newInventoryAcceptCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.AcceptInventory(t.Context(), cap, command)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("incomplete fresh body became Applied", out, err)
	}
	for _, invalid := range []InventoryAcceptCommand{nil, (*inventoryAcceptCommand)(nil), contractInventory{}} {
		if _, err := w.AcceptInventory(t.Context(), cap, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatal("non-concrete command entered dispatcher", err)
		}
	}
	expired, cancelLifetime := context.WithCancel(t.Context())
	cancelLifetime()
	revoked := inventoryTestDisclosure(t, w, q)
	revoked.scope.revoked.Store(true)
	dead := inventoryTestDisclosure(t, w, q)
	dead.scope.lifetime = expired
	uninitialized := inventoryTestDisclosure(t, w, q)
	uninitialized.scope.lifetime = nil
	for _, wrong := range []*ScanDisclosure{nil, {}, {issuer: &Writer{}, scope: cap.scope}, revoked, dead, uninitialized} {
		if out, err := w.ResolveScan(t.Context(), wrong, q); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, InventoryResult{}) {
			t.Fatal("invalid capability reached native frame", out, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := w.ResolveScan(cancelled, revoked, q); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("caller cancellation masked principal revocation", err)
	}
	wrong := q
	wrong.LibraryID++
	if _, err := w.ResolveScan(t.Context(), cap, wrong); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("wrong scope reached history", err)
	}
	if _, err := e.db.ExecContext(t.Context(), "DELETE FROM scan_command_receipts WHERE run_id=?", requests["hit"].RunID); err == nil {
		t.Fatal("fixture unexpectedly bypassed result FK")
	}
}

func TestInventoryFacadeRetiredLibraryReplaysBeforeFreshRefusal(t *testing.T) {
	w, requests, e, _ := inventoryFacadeFixture(t)
	q := requests["hit"]
	in, _ := inventoryRequestFixture()
	in.scope = inventoryScope{q.RunID, q.LibraryKey, q.LibraryID}
	in.run.lastSequence, in.run.acknowledgedSequence = 0, 0
	command, err := newInventoryAcceptCommand(q.Receipt.Identity, in)
	if err != nil {
		t.Fatal(err)
	}
	// Bind the fixture's previously independent native receipt to this complete
	// ORIGINAL request. Its generated IDs stay only in the result projection.
	q.Receipt.RequestDigest = command.digest
	v := inventoryAcceptResult{RunID: q.RunID, AcceptedRunRevision: 2, AppliedMS: 100,
		SourceID: "scan-source-hit", SourceIncarnation: "source-incarnation", SourceRevision: "opaque:007", NextCursor: "native-next",
		ResultCount: 1, Members: []inventoryResultMember{{0, 0, 0, "object-hit", 7, 31, 41, 1}}}
	digest, err := inventoryAcceptResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), "UPDATE scan_command_receipts SET request_digest=?,result_digest=? WHERE run_id=?", command.digest[:], digest[:], q.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), "UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE job_id=?", q.RunID); err != nil {
		t.Fatal(err)
	}
	// Retirement is NOT a historical disclosure predicate. It is mandatory
	// fresh eligibility, which remains closed with the incomplete SC body.
	if _, err := e.db.ExecContext(t.Context(), "UPDATE catalog_libraries SET retired=1 WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	out, err := w.AcceptInventory(t.Context(), inventoryTestDisclosure(t, w, q), command)
	if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed || out.Digest != digest || out.AcceptedRunRevision != 2 || out.SourceRevision != v.SourceRevision ||
		command.digest != q.Receipt.RequestDigest || len(out.Members) != 1 || out.Members[0].AcceptedAssetRevision != 1 {
		t.Fatal("fresh source/lease/overflow checks hid original history", out, err)
	}
	fresh := requests["new"]
	in.scope = inventoryScope{fresh.RunID, fresh.LibraryKey, fresh.LibraryID}
	newCommand, err := newInventoryAcceptCommand(fresh.Receipt.Identity, in)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := w.AcceptInventory(t.Context(), inventoryTestDisclosure(t, w, fresh), newCommand)
	if !errors.Is(err, ErrConflict) || refused.Outcome.State == Applied || refused.RunID != "" || len(refused.Members) != 0 {
		t.Fatal("retired library accepted fresh work", refused, err)
	}
	var last, receipts int64
	if err := e.db.QueryRowContext(t.Context(), "SELECT last_command_sequence FROM inventory_runs WHERE job_id=?", fresh.RunID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(t.Context(), "SELECT count(*) FROM scan_command_receipts WHERE run_id=?", fresh.RunID).Scan(&receipts); err != nil || last != 0 || receipts != 0 {
		t.Fatal("retired-library refusal committed a prefix", last, receipts, err)
	}
}

type inventoryReceiptCloseConn struct {
	writerDriverConn
	hook func()
}

func (c *inventoryReceiptCloseConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.Contains(q, "FROM scan_command_receipts") {
		return &receiptCloseRows{Rows: rows, hook: c.hook}, nil
	}
	return rows, err
}

func TestInventoryFacadeCancelRevokeBothOrders(t *testing.T) {
	for _, ack := range []bool{false, true} {
		for _, cancelFirst := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "resolve", true: "ack"}[ack], map[bool]string{false: "revoke_first", true: "cancel_first"}[cancelFirst]}, "/"), func(t *testing.T) {
				w, requests, e, a := inventoryFacadeFixture(t)
				q := requests["hit"]
				cap := inventoryTestDisclosure(t, w, q)
				if err := e.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(w.effective.path, e.config)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				e.openPool(connector, func(conn driver.Conn) driver.Conn {
					return &inventoryReceiptCloseConn{writerDriverConn: conn.(writerDriverConn), hook: func() {
						if cancelFirst {
							cancel()
						}
						cap.scope.revoked.Store(true)
						cancel()
					}}
				})
				primeEffectiveFixturePool(t, e)
				var out InventoryResult
				var result AckResult
				if ack {
					result, err = w.AckScan(ctx, cap, q)
				} else {
					out, err = w.ResolveScan(ctx, cap, q)
				}
				if !errors.Is(err, ErrDisclosureDenied) || out.RunID != "" || out.Outcome.State != 0 || out.Outcome.Reference != (ReceiptReference{}) || result.AcknowledgedThrough != 0 {
					t.Fatal("revoked result or ACK leaked", out, result, err)
				}
				if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
					t.Fatal("cancel/revoke stranded custody")
				}
			})
		}
	}
}

func TestInventoryFacadeMissingProofAndSharedAvailability(t *testing.T) {
	w, requests, e, a := inventoryFacadeFixture(t)
	q := requests["hit"]
	cap := inventoryTestDisclosure(t, w, q)
	w.effective.mu.Lock()
	_, err := w.ResolveScan(t.Context(), cap, q)
	other := EnrichmentReceiptRequest{ProviderOperationID: "other-driver", LibraryID: 7, EntityID: 31, Receipt: q.Receipt}
	otherCap := &EnrichmentDisclosure{issuer: w, scope: &enrichmentScope{ownerID: other.ProviderOperationID, libraryID: 7, entityID: 31, lifetime: t.Context()}}
	_, otherErr := w.ResolveEffective(t.Context(), otherCap, other)
	w.effective.mu.Unlock()
	if !errors.Is(err, ErrBusy) || !errors.Is(otherErr, ErrBusy) || a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("domains did not share the same writer custody", err, otherErr)
	}
	for _, sql := range []string{"DELETE FROM scan_command_results WHERE run_id=?", "DELETE FROM scan_command_receipts WHERE run_id=?"} {
		if _, err := e.db.ExecContext(t.Context(), sql, q.RunID); err != nil {
			t.Fatal(err)
		}
	}
	out, err := w.ResolveScan(t.Context(), cap, q)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("above-floor missing proof became fresh", out, err)
	}
	q = requests["new"]
	cap = inventoryTestDisclosure(t, w, q)
	if _, err := e.db.ExecContext(t.Context(), "DELETE FROM inventory_runs WHERE job_id=?", q.RunID); err != nil {
		t.Fatal(err)
	}
	out, err = w.ResolveScan(t.Context(), cap, q)
	if !errors.Is(err, ErrOwnerUnavailable) || !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("missing owner was registered or guessed retired", out, err)
	}
}

func TestInventoryPlanClosedOwnedAndWhole(t *testing.T) {
	w, _, _, _ := inventoryFacadeFixture(t)
	r := w.effective
	for name, mutate := range map[string]func(*effectivePlan){
		"missing_receipt":    func(p *effectivePlan) { p.registration = nil },
		"missing":            func(p *effectivePlan) { p.steps = p.steps[:len(p.steps)-1] },
		"duplicate":          func(p *effectivePlan) { p.steps[0].id = p.steps[1].id },
		"foreign":            func(p *effectivePlan) { p.steps[0].id = enrichmentEffectiveOwner },
		"unreserved_step":    func(p *effectivePlan) { p.steps[0].work.Hold = p.recipe.limit.Hold },
		"wrong_wal":          func(p *effectivePlan) { p.need[governor.WALBytes]-- },
		"no_header_forecast": func(p *effectivePlan) { p.headerSync = Work{} },
	} {
		t.Run(name, func(t *testing.T) {
			p := *r.inventoryPlan
			p.steps = append([]statementStep(nil), p.steps...)
			mutate(&p)
			if candidate, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, r.plan, p); err == nil || candidate != nil {
				t.Fatal("incomplete Inventory recipe entered runtime", candidate, err)
			}
		})
	}
	primary := r.plan
	primary.registration = nil
	if _, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, primary, *r.inventoryPlan); !errors.Is(err, stmt.ErrUnqualified) {
		t.Fatal("missing primary Capture proof accepted", err)
	}
	// A real receipt for the Effective-only set must not qualify Inventory or
	// the combined runtime, even though the native connection is the same.
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	conn, err := r.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foreign := captureEffectiveFixture(t, conn, r.executor.config, defs).Receipt()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for _, foreignProfile := range []string{"primary", "inventory"} {
		primary, inventory := r.plan, *r.inventoryPlan
		if foreignProfile == "primary" {
			primary.registration = foreign
		} else {
			inventory.registration = foreign
		}
		if _, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, primary, inventory); !errors.Is(err, stmt.ErrUnqualified) {
			t.Fatal("foreign or incomplete full-set Capture receipt accepted", foreignProfile, err)
		}
	}
	p := *r.inventoryPlan
	p.steps = append([]statementStep(nil), p.steps...)
	p.io = append([]governor.DeviceIO(nil), p.io...)
	candidate, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, r.plan, p)
	if err != nil {
		t.Fatal(err)
	}
	p.steps[0].work.Rows++
	p.io[0].DeviceID = "mutated"
	if reflect.DeepEqual(p.steps, candidate.inventoryPlan.steps) || candidate.inventoryPlan.io[0].DeviceID == "mutated" {
		t.Fatal("composition retained mutable profile backing")
	}
	if _, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, r.plan, p, p); !errors.Is(err, ErrInvalid) {
		t.Fatal("multiple Inventory profiles installed", err)
	}
	for _, forbidden := range []stmt.ID{scanReceiptInsert, scanResultInsert, scanReceiptReclaimHeader} {
		if _, err := r.registry.Lookup(forbidden); err == nil {
			t.Fatal("history profile admitted fresh or reclaim SQL", forbidden)
		}
	}
}

func TestInventoryFacadeAckPhysicalOutcomesAndLateRevocation(t *testing.T) {
	for _, mode := range []string{"overrun", "ambiguous", "revoke_after_commit"} {
		t.Run(mode, func(t *testing.T) {
			var clock time.Time
			w, requests, e, a := inventoryFacadeFixture(t, &clock)
			q := requests["hit"]
			cap := inventoryTestDisclosure(t, w, q)
			if err := e.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(w.effective.path, e.config)
			if err != nil {
				t.Fatal(err)
			}
			start := clock
			fault := &faultConnector{Connector: connector}
			switch mode {
			case "overrun":
				fault.afterNativeCommit = func() { clock = start.Add(30 * time.Millisecond) }
			case "ambiguous":
				fault.replyError, fault.afterCommit = true, true
			case "revoke_after_commit":
				fault.afterNativeCommit = func() { cap.scope.revoked.Store(true) }
			}
			installFaultPool(e, fault)
			primeEffectiveFixturePool(t, e)
			ack, err := w.AckScan(t.Context(), cap, q)
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("ACK lost grant custody", ack, err)
			}
			if mode == "overrun" {
				if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil ||
					!ack.Evidence.HoldTargetExceeded || ack.Evidence.HoldTarget <= 0 || ack.Evidence.HoldTarget > MaxCommandHold ||
					ack.Evidence.HoldSafetyCeiling != MaxCommandSafetyHold {
					t.Fatal("over-target ACK failed or lost evidence", ack, err)
				}
			} else if !reflect.DeepEqual(ack, AckResult{}) {
				t.Fatal("ACK lost physical outcome", ack, err)
			}
			if mode == "overrun" {
				db, _, openErr := sqliteconn.Open(t.Context(), w.effective.path, e.config)
				if openErr != nil {
					t.Fatal(openErr)
				}
				var floor int64
				readErr := db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?", q.RunID).Scan(&floor)
				closeErr := db.Close()
				if readErr != nil || closeErr != nil || floor != 1 {
					t.Fatal("committed floor lost", floor, readErr, closeErr)
				}
			}
			switch mode {
			case "overrun":
				if err != nil || e.fenced || w.effective.fenced || e.holdTargetOverruns.Load() != 1 {
					t.Fatal("over-target ACK fenced or lost counter", ack, err)
				}
				retired, resolveErr := w.ResolveScan(t.Context(), cap, q)
				if resolveErr != nil || retired.Outcome.State != ResultRetired || retired.RunID != "" || len(retired.Members) != 0 || retired.Digest != (ResultDigest{}) {
					t.Fatal("next original resolution did not remain retired", retired, resolveErr)
				}
			case "ambiguous":
				if !errors.Is(err, ErrResolveRequired) || !w.effective.fenced || !fault.nativeClosed.Load() {
					t.Fatal("ambiguous ACK escaped quarantine", ack, err)
				}
			case "revoke_after_commit":
				if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(ack, AckResult{}) {
					t.Fatal("late revocation disclosed saved floor", ack, err)
				}
				var floor int64
				if err := e.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id=?", q.RunID).Scan(&floor); err != nil || floor != 1 {
					t.Fatal("redaction fabricated a rollback", floor, err)
				}
			}
		})
	}
}
