package writer

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicStreamControlRequest(original basicOriginalScope, s basicStreamFactStage, purpose BasicPurpose) basicControlRequest {
	return basicControlRequest{original: original, purpose: purpose, operation: basicControlStreamFact, source: s, family: 1}
}

func TestBasicControlCommandsAreClosedAndDistinct(t *testing.T) {
	_, s, original, identity := basicControlFixture(t)
	stage, err := newBasicStageCommand(identity, basicStreamControlRequest(original, s, BasicPurposeStage))
	if err != nil {
		t.Fatal(err)
	}
	seal, err := newBasicSealCommand(identity, basicStreamControlRequest(original, s, BasicPurposeSeal))
	if err != nil {
		t.Fatal(err)
	}
	if stage.digest == seal.digest {
		t.Fatal("Stage and Seal share a request digest")
	}
	qs, err := stage.receiptRequest()
	if err != nil || qs.Purpose != BasicPurposeStage || qs.Scan.Receipt.RequestDigest != stage.digest || qs.Scan.RunID != original.scope.runID {
		t.Fatal("stage receipt request", qs, err)
	}
	ql, err := seal.receiptRequest()
	if err != nil || ql.Purpose != BasicPurposeSeal || ql.Scan.Receipt.RequestDigest != seal.digest {
		t.Fatal("seal receipt request", ql, err)
	}
	// A purpose cannot be borrowed by the other constructor.
	if _, err := newBasicStageCommand(identity, basicStreamControlRequest(original, s, BasicPurposeSeal)); !errors.Is(err, ErrInvalid) {
		t.Fatal("Stage constructor accepted a Seal request", err)
	}
	if _, err := newBasicSealCommand(identity, basicStreamControlRequest(original, s, BasicPurposeAccept)); !errors.Is(err, ErrInvalid) {
		t.Fatal("Seal constructor accepted an Accept request", err)
	}
	for name, change := range map[string]func(*basicControlRequest){
		"stream_family":   func(r *basicControlRequest) { r.family = 2 },
		"stream_rows":     func(r *basicControlRequest) { r.rows = []basicStageMediaRow{} },
		"stream_range":    func(r *basicControlRequest) { r.start, r.end = 0, 1 },
		"unknown_op":      func(r *basicControlRequest) { r.operation = 255 },
		"foreign_run":     func(r *basicControlRequest) { r.source.runID = "other-run" },
		"foreign_library": func(r *basicControlRequest) { r.source.libraryID++ },
		"no_change":       func(r *basicControlRequest) { r.source.changeID = 0 },
		"no_revision":     func(r *basicControlRequest) { r.source.objectRevision = "" },
		"media_family":    func(r *basicControlRequest) { r.operation, r.family = basicControlMediaRows, 10 },
	} {
		r := basicStreamControlRequest(original, s, BasicPurposeStage)
		change(&r)
		if _, err := newBasicStageCommand(identity, r); err == nil {
			t.Fatal("invalid Stage request accepted", name)
		}
	}
	// Exactly one fresh command per invocation, matching its purpose.
	if _, err := (&basicInvocation{request: qs, stage: stage, seal: seal}).validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("two fresh commands validated", err)
	}
	if _, err := (&basicInvocation{request: ql, stage: stage}).validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("Stage command validated under a Seal receipt", err)
	}
	if kind, err := (&basicInvocation{request: qs, stage: stage}).validate(); err != nil || kind != BasicStage {
		t.Fatal("Stage invocation", kind, err)
	}
	if kind, err := (&basicInvocation{request: ql, seal: seal}).validate(); err != nil || kind != BasicSeal {
		t.Fatal("Seal invocation", kind, err)
	}
}

func TestBasicStageMediaRowVariantRoundTrips(t *testing.T) {
	for _, row := range []basicMediaRow{
		basicChapterFact{revision: 3, status: "observed", fingerprint: "f"},
		basicSubtitleSidecar{id: "s", path: "/media/a.srt", digest: "d"},
	} {
		v, err := basicStageMediaRowOf(row)
		if err != nil {
			t.Fatal(err)
		}
		back, err := v.row()
		if err != nil || !reflect.DeepEqual(back, row) {
			t.Fatal("media row variant changed", back, row, err)
		}
	}
	if _, err := (basicStageMediaRow{}).row(); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty variant admitted", err)
	}
	two := basicStageMediaRow{chapterFact: &basicChapterFact{}, chapter: &basicChapter{}}
	if _, err := two.row(); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambiguous variant admitted", err)
	}
}

func TestBasicControlProfilesOwnEveryPossibleUse(t *testing.T) {
	for _, purpose := range []CommandID{BasicStage, BasicSeal} {
		defs := basicControlCommandDefinitions(purpose)
		if len(defs) == 0 {
			t.Fatal("empty profile", purpose)
		}
		seen := map[stmt.ID]bool{}
		for _, d := range defs {
			if seen[d.ID] || basicControlMaxUses(purpose, d.ID) <= 0 || commandStatementMaxUses(purpose, d.ID) != basicControlMaxUses(purpose, d.ID) {
				t.Fatal("profile use", purpose, d.ID)
			}
			seen[d.ID] = true
		}
		for _, id := range []stmt.ID{basicControlReceipt, basicControlRunCAS, basicControlPostState, basicReceiptHeader, scanReceiptOwner} {
			if !seen[id] {
				t.Fatal("profile misses shared history/control point", purpose, id)
			}
		}
		stageOnly, sealOnly := seen[basicStageStreamInsert] && seen[basicMediaChapterInsert], seen[basicSealStreamFact] && seen[basicMediaChapterRows]
		if (purpose == BasicStage) != stageOnly || (purpose == BasicSeal) != sealOnly {
			t.Fatal("profile carries the other purpose's body", purpose)
		}
	}
	if basicControlCommandDefinitions(BasicAccept) != nil || basicControlMaxUses(BasicAccept, basicStageStreamInsert) != 0 {
		t.Fatal("control profile leaked to Accept")
	}
}

func basicControlProfilePlan(purpose CommandID) effectivePlan {
	p := effectiveFixturePlan()
	p.steps = nil
	p.basicMemory = commandMemoryBudget{4 << 20, 4 << 20}
	p.need[governor.MemoryBytes] = 8 << 20
	p.recipe.limit.Rows, p.recipe.limit.VMOps, p.recipe.limit.Pages = 1<<20, 1<<32, 1<<20
	for _, d := range basicControlCommandDefinitions(purpose) {
		work := Work{Rows: 4, VMOps: 160000, Pages: 16, Hold: 25 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.WALBytes = 65536
		}
		p.steps = append(p.steps, statementStep{d.ID, work})
	}
	return p
}

func basicControlDispatchFixture(t *testing.T) (*Writer, *executor, *governor.Authority, basicStreamFactStage, basicOriginalScope, CommandIdentity) {
	t.Helper()
	e, s, original, identity := basicControlFixture(t)
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	primary := effectiveFixturePlan()
	stage, seal := basicControlProfilePlan(BasicStage), basicControlProfilePlan(BasicSeal)
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		seen[d.ID] = true
	}
	for _, purpose := range []CommandID{BasicStage, BasicSeal} {
		for _, d := range basicControlCommandDefinitions(purpose) {
			if !seen[d.ID] {
				seen[d.ID] = true
				defs = append(defs, d)
			}
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "basic_stage_command_test.go", "basic_control_test.go", "basic_stage_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	primary.registration, stage.registration, seal.registration = golden.Receipt(), golden.Receipt(), golden.Receipt()
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = primary.need
		partitions[i][governor.MemoryBytes] = max(primary.need[governor.MemoryBytes], stage.need[governor.MemoryBytes])
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newEffectiveRuntime(e, path, g, a, wal, 1, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeBasicControlProfiles(t.Context(), runtime, stage, seal); err != nil {
		t.Fatal(err)
	}
	if err := composeBasicControlProfiles(t.Context(), runtime, stage, seal); !errors.Is(err, ErrBusy) {
		t.Fatal("control profiles installed twice", err)
	}
	return &Writer{effective: runtime}, e, a, s, original, identity
}

// Native Stage then Seal of the family-1 stream fact through the SAME
// dispatcher: each saves its own header-only control receipt, replays and ACKs
// floor-only, and neither activates the asset or changes the change phase.
func TestBasicStageAndSealDispatchSaveControlReceipts(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	stage, err := newBasicStageCommand(identity, basicStreamControlRequest(original, s, BasicPurposeStage))
	if err != nil {
		t.Fatal(err)
	}
	q, err := stage.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	disclosure := inventoryTestDisclosure(t, w, q.Scan)
	got, ack, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q, stage: stage}, false)
	if err != nil || ack != (AckResult{}) || got.Outcome.State != Applied || got.Outcome.Replayed || got.Purpose != BasicPurposeStage ||
		got.ResultCount != 0 || got.Members == nil || len(got.Members) != 0 || got.AcceptedRunRevision != original.run.revision+1 || got.ChangeID != s.changeID {
		t.Fatal("Stage dispatch", got, ack, err)
	}
	basicDispatchReturned(t, e, authority)
	replay, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q}, false)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != got.Digest || replay.Purpose != BasicPurposeStage {
		t.Fatal("Stage replay", replay, err)
	}
	_, ack, err = w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q}, true)
	if err != nil || ack.AcknowledgedThrough != identity.Sequence {
		t.Fatal("Stage ACK", ack, err)
	}
	// Seal is the next command on the SAME run, after Stage's floor.
	next := identity
	next.Sequence++
	next.OperationID[0] ^= 0x40
	after := original
	after.run.revision, after.run.lastSequence, after.run.acknowledgedSequence = got.AcceptedRunRevision, identity.Sequence, identity.Sequence
	seal, err := newBasicSealCommand(next, basicStreamControlRequest(after, s, BasicPurposeSeal))
	if err != nil {
		t.Fatal(err)
	}
	ql, err := seal.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	sealed, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: ql, seal: seal}, false)
	if err != nil || sealed.Outcome.State != Applied || sealed.Purpose != BasicPurposeSeal || sealed.Continuation != (BasicContinuation{}) || len(sealed.Members) != 0 {
		t.Fatal("Seal dispatch", sealed, err)
	}
	basicDispatchReturned(t, e, authority)
	var staged, isSealed, epoch int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT rows_staged,sealed FROM catalog_info_input_families WHERE change_id=51 AND family=1`).Scan(&staged, &isSealed); err != nil || staged != 1 || isSealed != 1 {
		t.Fatal("family-1 native state", staged, isSealed, err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil || epoch != s.expectedEpoch {
		t.Fatal("Stage/Seal moved the asset epoch", epoch, err)
	}
	var kinds []string
	rows, err := e.db.QueryContext(t.Context(), `SELECT command_kind FROM scan_command_receipts WHERE run_id='basic-run' ORDER BY command_sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	if !reflect.DeepEqual(kinds, []string{"basic_stage", "basic_seal"}) {
		t.Fatal("saved control receipt kinds", kinds)
	}
}

// A Stage command never runs against a runtime without its installed profile:
// the Accept-only runtime refuses it before any turn opens.
func TestBasicStageRefusesWithoutInstalledProfile(t *testing.T) {
	w, e, authority, _, _ := basicAcceptDispatchFixture(t)
	if w.effective.basicPlan(BasicStage) != nil || w.effective.basicPlan(BasicSeal) != nil || w.effective.basicPlan(BasicAccept) == nil {
		t.Fatal("unexpected installed Basic profiles")
	}
	_, s, original, identity := basicControlFixture(t)
	stage, err := newBasicStageCommand(identity, basicStreamControlRequest(original, s, BasicPurposeStage))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := stage.receiptRequest()
	_, _, err = w.effective.runBasic(t.Context(), w, inventoryTestDisclosure(t, w, q.Scan), basicInvocation{request: q, stage: stage}, false)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatal("Stage ran without its profile", err)
	}
	basicDispatchReturned(t, e, authority)
}

// Seal before Stage, and a stale original run revision, are refused by the
// native family/run CAS through the SAME dispatcher with no receipt or effect.
func TestBasicControlDispatchRefusesSealBeforeStageAndStaleRun(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	counts := func() (receipts, revision, staged int64) {
		t.Helper()
		if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM scan_command_receipts),(SELECT job_revision FROM inventory_runs WHERE job_id='basic-run'),(SELECT rows_staged FROM catalog_info_input_families WHERE change_id=51 AND family=1)`).Scan(&receipts, &revision, &staged); err != nil {
			t.Fatal(err)
		}
		return
	}
	r0, v0, f0 := counts()
	seal, err := newBasicSealCommand(identity, basicStreamControlRequest(original, s, BasicPurposeSeal))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := seal.receiptRequest()
	disclosure := inventoryTestDisclosure(t, w, q.Scan)
	if got, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q, seal: seal}, false); err == nil || got.Outcome.State == Applied {
		t.Fatal("Seal ran before Stage", got, err)
	}
	basicDispatchReturned(t, e, authority)
	if r, v, f := counts(); r != r0 || v != v0 || f != f0 {
		t.Fatal("refused Seal left effects", r, v, f)
	}
	stale := original
	stale.run.revision--
	stage, err := newBasicStageCommand(identity, basicStreamControlRequest(stale, s, BasicPurposeStage))
	if err != nil {
		t.Fatal(err)
	}
	q, _ = stage.receiptRequest()
	if got, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q, stage: stage}, false); err == nil || got.Outcome.State == Applied {
		t.Fatal("stale run revision staged", got, err)
	}
	basicDispatchReturned(t, e, authority)
	if r, v, f := counts(); r != r0 || v != v0 || f != f0 {
		t.Fatal("stale Stage left effects", r, v, f)
	}
}

// A media family (4, one chapter fact) stages and seals through the SAME
// dispatcher with header-only receipts, after the Stage receipt's floor.
func TestBasicMediaStageThenSealDispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	for _, q := range []string{`UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`,
		`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,4,0,1)`} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	row, err := basicStageMediaRowOf(basicChapterFact{37, "known", "chapters:37"})
	if err != nil {
		t.Fatal(err)
	}
	media := func(purpose BasicPurpose, scope basicOriginalScope) basicControlRequest {
		r := basicControlRequest{original: scope, purpose: purpose, operation: basicControlMediaRows, source: s, family: 4, rows: []basicStageMediaRow{row}}
		if purpose == BasicPurposeStage {
			r.end = 1
		}
		return r
	}
	stage, err := newBasicStageCommand(identity, media(BasicPurposeStage, original))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := stage.receiptRequest()
	disclosure := inventoryTestDisclosure(t, w, q.Scan)
	got, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q, stage: stage}, false)
	if err != nil || got.Outcome.State != Applied || got.Purpose != BasicPurposeStage || len(got.Members) != 0 {
		t.Fatal("media Stage", got, err)
	}
	if _, ack, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: q}, true); err != nil || ack.AcknowledgedThrough != identity.Sequence {
		t.Fatal("media Stage ACK", ack, err)
	}
	next := identity
	next.Sequence++
	next.OperationID[0] ^= 0x20
	after := original
	after.run.revision, after.run.lastSequence, after.run.acknowledgedSequence = got.AcceptedRunRevision, identity.Sequence, identity.Sequence
	seal, err := newBasicSealCommand(next, media(BasicPurposeSeal, after))
	if err != nil {
		t.Fatal(err)
	}
	ql, _ := seal.receiptRequest()
	sealed, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: ql, seal: seal}, false)
	if err != nil || sealed.Outcome.State != Applied || sealed.Purpose != BasicPurposeSeal || sealed.Continuation != (BasicContinuation{}) {
		t.Fatal("media Seal", sealed, err)
	}
	basicDispatchReturned(t, e, authority)
	var staged, isSealed int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT rows_staged,sealed FROM catalog_info_input_families WHERE change_id=51 AND family=4`).Scan(&staged, &isSealed); err != nil || staged != 1 || isSealed != 1 {
		t.Fatal("family-4 native state", staged, isSealed, err)
	}
}
