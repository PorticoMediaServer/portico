package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Basic Accept process-kill cells over the real accepting body. Each executed
// statement is stopped once, plus transaction/return boundaries and the two
// ambiguous COMMIT outcomes. Expected state is independently rebuilt on a
// fresh fixture with the same frozen clock; the survivor is compared raw.

type basicCrashFacts struct {
	Path    string
	Receipt BasicReceiptRequest
	Config  sqliteconn.Config
}

type basicCrashResult struct {
	Purpose                     BasicPurpose
	RunID                       string
	AcceptedRunRevision         int64
	AppliedMS                   int64
	SourceID, SourceIncarnation string
	SourceRevision              string
	ChangeID, ExpectedEpoch     int64
	ResultCount                 int64
	Members                     []InventoryMember
	Digest                      ResultDigest
}

type basicCrashExpectation struct {
	Result basicCrashResult
	Phases []string
}

func init() { registerCrashDomain("basic_accept", crashBasicAcceptFixture) }

// Before the fixture lease horizon (4000000000000 ms); deterministic for both
// the survivor and the independently rebuilt expectation.
var basicCrashClock = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

func basicCrashDatabasePath(t *testing.T, e *executor) string {
	t.Helper()
	var seq int
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil || path == "" {
		t.Fatal("basic crash database path", path, err)
	}
	return path
}

// basicCrashCommand rebuilds the fixture's deterministic original command
// without seeding SQL. Identity, source facts and payload are the fixture's.
func basicCrashCommand(t *testing.T) *basicAcceptCommand {
	t.Helper()
	s := basicStreamFactStage{changeID: 51, inputRevision: 7, libraryID: 7, assetID: 41, expectedEpoch: 17, assetToken: "basic-asset",
		runID: "basic-run", sourceID: "basic-source", incarnation: "basic-incarnation", objectID: "basic-object", objectRevision: "object:019",
		sourceGeneration: 23, policyRevision: 29, witnessFingerprint: [32]byte(bytes.Repeat([]byte{0x31}, 32)), factRevision: 31,
		size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "probe-fingerprint"}
	identity := CommandIdentity{Incarnation: [16]byte(bytes.Repeat([]byte{1}, 16)), Sequence: 19, OperationID: [16]byte(bytes.Repeat([]byte{2}, 16))}
	original := basicOriginalScope{scope: inventoryScope{s.runID, "basic-library", s.libraryID},
		source: inventorySource{id: s.sourceID, incarnation: s.incarnation, rootIdentity: "root:17", resolvedRoot: []byte("/media"), generation: s.sourceGeneration},
		run:    inventoryRun{revision: 41, policyRevision: s.policyRevision, leaseOwner: 13, leaseGeneration: 17, leaseExpiresMS: 4000000000000, lastSequence: 18, acknowledgedSequence: 18, phase: "inventory", scanMode: "first-import"}}
	analysis, err := prepareBasicObjectAnalysis(s, "basic-library", "actual-probe-algorithm", `["probe","local_metadata","subtitles"]`, `["probe","local_metadata","subtitles"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	nfo, err := prepareBasicNFOSet(s, "basic-library", nil, nil, nil, basicNFOStatus{status: "observed"}, true)
	if err != nil {
		t.Fatal(err)
	}
	command, err := newBasicAcceptCommand(identity, basicAcceptRequest{original: original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf,
		activation: basicActivationEffects{source: s, changeRevision: 13, familyMask: 511, shapeID: 61, invalidationRevision: 1, fileSizeRevision: 1},
		entityID:   31, duration: 100, height: 1080, analysis: *analysis, nfo: nfo, oldScalars: []basicFirstOldScalar{
			{field: 4, kind: 3, text: sql.NullString{String: "old", Valid: true}},
			{field: 5, kind: 3, text: sql.NullString{String: "vp9", Valid: true}},
			{field: 6, kind: 3, text: sql.NullString{String: "aac", Valid: true}},
			{field: 7, kind: 1, integer: sql.NullInt64{Int64: 1920, Valid: true}},
			{field: 8, kind: 1, integer: sql.NullInt64{Int64: 720, Valid: true}},
			{field: 9, kind: 2, real: sql.NullFloat64{Float64: 90, Valid: true}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func basicCrashMirror(value BasicResult) basicCrashResult {
	out := basicCrashResult{Purpose: value.Purpose, RunID: value.RunID, AcceptedRunRevision: value.AcceptedRunRevision, AppliedMS: value.AppliedMS,
		SourceID: value.SourceID, SourceIncarnation: value.SourceIncarnation, SourceRevision: value.SourceRevision, ChangeID: value.ChangeID,
		ExpectedEpoch: value.ExpectedEpoch, ResultCount: value.ResultCount, Digest: value.Digest}
	out.Members = append([]InventoryMember(nil), value.Members...)
	return out
}

func basicCrashSeedCommand(t *testing.T) (*executor, *basicAcceptCommand) {
	t.Helper()
	w, command := basicFirstAcceptCommandFixture(t)
	w.now = func() time.Time { return basicCrashClock }
	return w, command
}

func basicCrashExpectationFor(t *testing.T) basicCrashExpectation {
	t.Helper()
	w, command := basicCrashSeedCommand(t)
	defs := basicFirstAcceptTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	trace := crashWitness{Case: crashCase{Domain: "basic_accept", Boundary: "trace"}}
	ids := map[string]string{}
	for _, entry := range registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	var phases []string
	seen := map[string]bool{}
	installCrashDriverAt(t, w, basicCrashDatabasePath(t, w), &trace, basicCrashClock, func(query string) {
		if id, ok := ids[strings.TrimSpace(query)]; ok && !seen[id] {
			seen[id] = true
			phases = append(phases, "native_"+id)
		}
	})
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	value, err := applyBasicAccept(t.Context(), turn, command)
	if err != nil {
		t.Fatal(err)
	}
	basicHandlerCommit(t, turn, BasicPurposeAccept, command.request.original.run.leaseExpiresMS)
	return basicCrashExpectation{Result: basicCrashMirror(value), Phases: phases}
}

func crashBasicAcceptFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	w, command := basicCrashSeedCommand(t)
	path := basicCrashDatabasePath(t, w)
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	facts := basicCrashFacts{Path: path, Receipt: q, Config: w.config}
	payload, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	witness.Path, witness.Payload = path, payload
	defs := basicFirstAcceptTestDefinitions()
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	ids := map[string]string{}
	for _, entry := range registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, w, path, witness, basicCrashClock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		crashPoint(t, witness, "native_"+id)
	})
	armed = true
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	if _, err = applyBasicAccept(t.Context(), turn, command); err != nil {
		t.Fatal("basic accept apply", err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if err = checkBasicTerminal(t.Context(), turn, BasicPurposeAccept, command.request.original.run.leaseExpiresMS); err != nil {
		t.Fatal(err)
	}
	_, commitErr := turn.transaction.commit()
	armed = false
	if witness.Case.CommitFault != "" {
		if commitErr == nil || !w.ambiguous {
			t.Fatal("basic uncertain COMMIT reported success", commitErr)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
		t.Fatal("basic uncertain boundary was never reached", witness.Case)
	}
	if commitErr != nil {
		t.Fatal("basic commit", commitErr)
	}
	if witness.Case.Boundary == "trace" {
		return
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("basic boundary was never reached", witness.Case)
}

func basicCrashReopen(t *testing.T, path string) *scanOpening {
	t.Helper()
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("reopen stat: %v %v", info, err)
	}
	owner := scanOpeningOwner(t, path)
	opening, err := openScanStore(t.Context(), owner, path, 1024)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = opening.close() })
	var integrity string
	if err := opening.executor.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("basic reopen integrity", integrity, err)
	}
	foreign, err := opening.executor.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Next() {
		t.Fatal("basic reopen foreign key violation")
	}
	if err := errors.Join(foreign.Err(), foreign.Close()); err != nil {
		t.Fatal(err)
	}
	return opening
}

func basicCrashCommitted(c crashCase) bool {
	if c.CommitFault != "" {
		return c.CommitFault == "after"
	}
	return c.Boundary == "after_commit" || c.Boundary == "physical_return" || c.Boundary == "api_return"
}

func basicCrashParse(t *testing.T, witness crashWitness) (basicCrashFacts, *basicAcceptCommand) {
	t.Helper()
	var facts basicCrashFacts
	if len(witness.Payload) == 0 {
		t.Fatal("basic crash witness lost its payload")
	}
	if err := json.Unmarshal(witness.Payload, &facts); err != nil {
		t.Fatal(err)
	}
	command := basicCrashCommand(t)
	q, err := command.receiptRequest()
	if err != nil || q != facts.Receipt {
		t.Fatal("basic original receipt changed", q, facts.Receipt, err)
	}
	return facts, command
}

func basicCrashState(t *testing.T, ctx context.Context, reader inventoryCrashReader) (receipts int64, epoch int64, pending int64, shape int64, linkPending sql.NullInt64) {
	t.Helper()
	if err := reader.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT count(*) FROM scan_command_receipts WHERE run_id='basic-run' AND command_kind='basic_accept'),0),
 (SELECT info_epoch FROM catalog_assets WHERE id=41),
 (SELECT info_publication_pending FROM catalog_assets WHERE id=41),
 COALESCE((SELECT info_shape_id FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),-1),
 (SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&receipts, &epoch, &pending, &shape, &linkPending); err != nil {
		t.Fatal(err)
	}
	return receipts, epoch, pending, shape, linkPending
}

func verifyBasicCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	facts, command := basicCrashParse(t, witness)
	expect := basicCrashExpectationFor(t)
	opening := basicCrashReopen(t, facts.Path)
	e := opening.executor
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(facts.Path, facts.Config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	e.config = facts.Config
	e.openPool(connector, nil)
	if err := e.db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	retryConn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	retryGolden := captureEffectiveFixture(t, retryConn, facts.Config, basicFirstAcceptTestDefinitions(), "basic_accept_handler_test.go", "basic_accept_command.go", "basic_accept_handler.go", "basic_accept_control.go")
	if err := retryConn.Close(); err != nil {
		t.Fatal(err)
	}
	retryRegistry, err := stmt.New(basicFirstAcceptTestDefinitions(), retryGolden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return basicCrashClock }
	q := facts.Receipt
	committed := basicCrashCommitted(witness.Case)
	receipts, epoch, pending, shape, linkPending := basicCrashState(t, t.Context(), e.db)
	if committed {
		if receipts != 1 || epoch != 18 || pending != 0 || shape != 61 || linkPending.Valid {
			t.Fatalf("committed survivor state receipts=%d epoch=%d pending=%d shape=%d linkPending=%v", receipts, epoch, pending, shape, linkPending)
		}
	} else {
		if receipts != 0 || epoch != 17 || pending != 1 || shape != -1 || !linkPending.Valid || linkPending.Int64 != 51 {
			t.Fatalf("pre-COMMIT survivor wrote effects receipts=%d epoch=%d pending=%d shape=%d linkPending=%v", receipts, epoch, pending, shape, linkPending)
		}
		defs := basicFirstAcceptTestDefinitions()
		turn := basicHandlerTurn(t, e, BasicAccept, defs, retryRegistry)
		if _, err := applyBasicAccept(t.Context(), turn, command); err != nil {
			t.Fatalf("basic retry: %v", err)
		}
		basicHandlerCommit(t, turn, BasicPurposeAccept, command.request.original.run.leaseExpiresMS)
		receipts, epoch, pending, shape, linkPending = basicCrashState(t, t.Context(), e.db)
		if receipts != 1 || epoch != 18 || pending != 0 || shape != 61 || linkPending.Valid {
			t.Fatalf("retry state receipts=%d epoch=%d pending=%d shape=%d linkPending=%v", receipts, epoch, pending, shape, linkPending)
		}
	}
	defs := basicFirstAcceptTestDefinitions()
	registry := retryRegistry
	turn := basicHandlerTurn(t, e, BasicAccept, defs, registry)
	lookup, err := lookupBasic(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(basicCrashMirror(lookup.result), expect.Result) {
		t.Fatalf("basic original did not resolve: decision=%v err=%v got=%+v want=%+v", lookup.decision, err, basicCrashMirror(lookup.result), expect.Result)
	}
	ack, err := acknowledgeBasic(t.Context(), turn, q, lookup)
	if err != nil || ack != command.identity.Sequence {
		t.Fatal("basic ACK", ack, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	settled := basicCrashStateSnapshot(t, e.db)
	turn = basicHandlerTurn(t, e, BasicAccept, defs, registry)
	retired, err := lookupBasic(t.Context(), turn, q)
	if err != nil || retired.decision != receiptRetired {
		t.Fatal("basic ACKed original resurrected", retired.decision, err)
	}
	if _, err := acknowledgeBasic(t.Context(), turn, q, retired); err != nil {
		t.Fatal("basic repeated ACK", err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if after := basicCrashStateSnapshot(t, e.db); after != settled {
		t.Fatalf("basic second replay wrote state: %+v vs %+v", after, settled)
	}
	if err := opening.close(); err != nil && !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
}

type basicCrashSnapshot struct {
	receipts, epoch, pending, shape, linkPending int64
	linkValid                                    bool
	runRevision, ack                             int64
}

func basicCrashStateSnapshot(t *testing.T, db *sql.DB) basicCrashSnapshot {
	t.Helper()
	var s basicCrashSnapshot
	var link sql.NullInt64
	if err := db.QueryRowContext(t.Context(), `SELECT
 COALESCE((SELECT count(*) FROM scan_command_receipts WHERE run_id='basic-run' AND command_kind='basic_accept'),0),
 (SELECT info_epoch FROM catalog_assets WHERE id=41),
 (SELECT info_publication_pending FROM catalog_assets WHERE id=41),
 COALESCE((SELECT info_shape_id FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),-1),
 (SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41),
 (SELECT job_revision FROM inventory_runs WHERE job_id='basic-run'),
 (SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id='basic-run')`).Scan(&s.receipts, &s.epoch, &s.pending, &s.shape, &link, &s.runRevision, &s.ack); err != nil {
		t.Fatal(err)
	}
	s.linkPending, s.linkValid = link.Int64, link.Valid
	return s
}

func TestCrashReplayBasicAccept(t *testing.T) {
	if raceDetector {
		// Each cell rebuilds the accepting fixture in its own child, so the
		// sequential matrix needs more than one shard's race budget. The same
		// cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	expect := basicCrashExpectationFor(t)
	boundaries := append([]string{"before_begin", "after_begin"}, expect.Phases...)
	boundaries = append(boundaries, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range boundaries {
		t.Run("accept/"+phase, func(t *testing.T) {
			verifyBasicCrash(t, runCrashChild(t, crashCase{"basic_accept", "accept", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("accept/uncertain_return/"+fault, func(t *testing.T) {
			verifyBasicCrash(t, runCrashChild(t, crashCase{"basic_accept", "accept", "uncertain_return", fault}))
		})
	}
}
