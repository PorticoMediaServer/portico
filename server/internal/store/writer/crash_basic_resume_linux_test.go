package writer

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func init() { registerCrashDomain("basic_resume", crashBasicResumeChild) }

type basicResumeCrashSource struct {
	Config    sqliteconn.Config
	Receipt   BasicReceiptRequest
	Initial   map[string][32]byte
	HeaderRun effectiveRawJournal
	Trace     []string
}

func basicResumeCrashSeed(t *testing.T, operation string) (basicWalk, *basicStageCommand) {
	t.Helper()
	var walk basicWalk
	switch operation {
	case "copy":
		walk = basicResumeWalk(t)
	case "finish":
		walk = basicWalkThroughPin(t)
		walk.revision = basicWalkCopyAll(t, walk)
	default:
		t.Fatal("unknown resume crash operation", operation)
	}
	walk.e.now = func() time.Time { return basicCrashClock }
	if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_lease_owner=5,job_lease_generation=7,job_lease_expires_ms=1,job_attempts=3,job_next_attempt_ms=NULL,fault='retry' WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	if operation == "finish" {
		if _, err := walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_next_attempt_ms=1 WHERE id=51`); err != nil {
			t.Fatal(err)
		}
	}
	cmd, err := newBasicStageCommand(walk.chain.identity, basicControlRequest{original: walk.chain.original, purpose: BasicPurposeStage, operation: basicControlResume, source: walk.s})
	if err != nil {
		t.Fatal(err)
	}
	return walk, cmd
}

// Each observed execution is numbered, including repeated SQL IDs. The fixture
// uses CR's protected turn/physical driver hooks and the real Stage body; the
// separate portable tests exercise its facade's history-first classification.
func executeBasicResumeCrash(t *testing.T, witness *crashWitness) (source basicResumeCrashSource) {
	t.Helper()
	walk, cmd := basicResumeCrashSeed(t, witness.Case.Operation)
	e := walk.e
	path := basicCrashDatabasePath(t, e)
	source.Config = e.config
	source.Config.Synchronous = sqliteconn.Normal
	e.config = source.Config
	source.Receipt, _ = cmd.receiptRequest()
	var err error
	source.Initial, err = n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.HeaderRun, err = readEffectiveJournal(t.Context(), e.db, []string{"catalog_info_changes", "inventory_runs", "scan_command_receipts"})
	if err != nil {
		t.Fatal(err)
	}
	defs := basicControlCommandDefinitions(BasicStage)
	registry := nativeInventoryFixtureRegistry(t, e, defs, "basic_resume.go", "basic_resume_statements.go", "basic_resume_test.go")
	ids := map[string]string{}
	for _, entry := range registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	witness.Path, witness.Reference = path, source.Receipt.Scan.Receipt
	publish := func() {
		witness.Payload, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
	}
	publish()
	armed := false
	counts := map[string]int{}
	installCrashDriverAt(t, e, path, witness, basicCrashClock, func(query string) {
		if !armed {
			return
		}
		if id, ok := ids[strings.TrimSpace(query)]; ok {
			counts[id]++
			phase := fmt.Sprintf("native_%s_%d", id, counts[id])
			source.Trace = append(source.Trace, phase)
			publish()
			crashPoint(t, witness, phase)
		}
	})
	armed = true
	turn := basicHandlerTurn(t, e, BasicStage, defs, registry)
	if _, err = applyBasicStage(t.Context(), turn, cmd); err != nil {
		t.Fatal("resume apply", err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if err = checkBasicTerminal(t.Context(), turn, BasicPurposeStage, cmd.request.original.run.leaseExpiresMS); err != nil {
		t.Fatal(err)
	}
	_, err = turn.transaction.commit()
	armed = false
	if witness.Case.CommitFault != "" {
		if err == nil || !e.ambiguous {
			t.Fatal("lost COMMIT reply escaped fence", err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
		t.Fatal("uncertain cut was not reached")
	}
	if err != nil {
		t.Fatal(err)
	}
	if witness.Case.Boundary != "trace" {
		crashPoint(t, witness, "api_return")
		t.Fatal("cut was not reached")
	}
	return source
}

func crashBasicResumeChild(t *testing.T, w *crashWitness) { executeBasicResumeCrash(t, w) }

func verifyBasicResumeCrash(t *testing.T, witness crashWitness, cmd *basicStageCommand) {
	t.Helper()
	var source basicResumeCrashSource
	if err := json.Unmarshal(witness.Payload, &source); err != nil {
		t.Fatal(err)
	}
	q, _ := cmd.receiptRequest()
	if q != source.Receipt || witness.Reference != q.Scan.Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("lost original or ambiguity")
	}
	opening := basicCrashReopen(t, witness.Path)
	e := opening.executor
	// Rebind the original's connection policy, not the survivor's default plan.
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(witness.Path, source.Config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	e.config = source.Config
	e.openPool(connector, nil)
	e.now = func() time.Time { return basicCrashClock }
	state, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	committed := basicCrashCommitted(witness.Case)
	if !committed {
		if err = compareN1DriverCheckpoint(source.Initial, state); err != nil {
			t.Fatal("partial reset/copy/receipt survived", err)
		}
	} else {
		for table, before := range source.Initial {
			switch table {
			case "catalog_info_changes", "inventory_runs", "scan_command_receipts":
				continue
			case "asset_streams":
				if witness.Case.Operation == "copy" {
					continue
				}
			}
			if state[table] != before {
				t.Fatal("unrelated table changed", table)
			}
		}
	}
	defs := basicControlCommandDefinitions(BasicStage)
	registry := nativeInventoryFixtureRegistry(t, e, defs, "basic_resume.go", "basic_resume_statements.go", "basic_resume_test.go")
	if !committed {
		turn := basicHandlerTurn(t, e, BasicStage, defs, registry)
		lookup, err := lookupBasic(t.Context(), turn, q)
		if err != nil || lookup.decision != receiptFresh {
			t.Fatal("pre-COMMIT original was not Pending", lookup, err)
		}
		if _, err = applyBasicStage(t.Context(), turn, cmd); err != nil {
			t.Fatal("retry same original", err)
		}
		basicHandlerCommit(t, turn, BasicPurposeStage, cmd.request.original.run.leaseExpiresMS)
	}
	// Independent expected header/run deltas, over the actual pre-command rows.
	// Every saved cursor key, both fences, input revision, shape and scalar field
	// outside these deltas must remain byte-for-byte identical.
	header := source.HeaderRun["catalog_info_changes"].Rows[0]
	revision := header["job_revision"].Int
	copyPhase, attempts := int64(1), int64(4)
	if witness.Case.Operation == "finish" {
		copyPhase, attempts = 0, 0
	}
	for key, value := range map[string]any{"job_revision": revision + 1, "copy_phase": copyPhase, "job_lease_owner": int64(0), "job_lease_generation": int64(0), "job_lease_expires_ms": int64(0), "job_attempts": attempts, "job_next_attempt_ms": nil, "job_due_ms": nil, "fault": ""} {
		progressSet(header, key, value)
	}
	run := source.HeaderRun["inventory_runs"].Rows[0]
	progressSet(run, "job_revision", cmd.request.original.run.revision+1)
	progressSet(run, "last_command_sequence", cmd.identity.Sequence)
	actual, err := readEffectiveJournal(t.Context(), e.db, []string{"catalog_info_changes", "inventory_runs", "scan_command_receipts"})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"catalog_info_changes", "inventory_runs"} {
		if !reflect.DeepEqual(actual[table], source.HeaderRun[table]) {
			t.Fatal("native resume delta", table)
		}
	}
	// All preceding receipts are retained verbatim; exactly one original is new.
	receipts := actual["scan_command_receipts"]
	var saved []map[string]effectiveRawCell
	for _, row := range receipts.Rows {
		if row["command_sequence"].Int != cmd.identity.Sequence {
			saved = append(saved, row)
		}
	}
	if len(receipts.Rows) != len(source.HeaderRun["scan_command_receipts"].Rows)+1 || !reflect.DeepEqual(saved, source.HeaderRun["scan_command_receipts"].Rows) {
		t.Fatal("receipt prefix changed")
	}
	if witness.Case.Operation == "copy" {
		var stream string
		if err = e.db.QueryRowContext(t.Context(), `SELECT json_array(stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,has_asset_stream_breadth) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&stream); err != nil || stream != `[0,"video","h264","","Main",0,"",1,0,"","","",0]` {
			t.Fatal("saved cursor skipped/changed stream", stream, err)
		}
		var count int
		if err = e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM asset_streams WHERE asset_id='basic-asset'`).Scan(&count); err != nil || count != 1 {
			t.Fatal("stream cardinality", count, err)
		}
	}
	beforeReplay, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, e, BasicStage, defs, registry)
	lookup, err := lookupBasic(t.Context(), turn, q)
	want := BasicResult{Purpose: BasicPurposeStage, RunID: q.Scan.RunID, AcceptedRunRevision: cmd.request.original.run.revision + 1, AppliedMS: basicCrashClock.UnixMilli(), SourceID: cmd.request.source.sourceID, SourceIncarnation: cmd.request.source.incarnation, SourceRevision: cmd.request.source.objectRevision, ChangeID: 51, ExpectedEpoch: 17, Continuation: BasicContinuation{NextFamily: 2, NextCopyPhase: 1, AfterEntity: 31}, ResultCount: 0, Members: []InventoryMember{}}
	if witness.Case.Operation == "finish" {
		want.Continuation = BasicContinuation{AfterEntity: 31}
	}
	want.Digest, err = basicResultDigest(cmd.identity, cmd.digest, want)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(lookup.result, want) {
		t.Fatal("original resume projection", lookup, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	afterReplay, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil || !reflect.DeepEqual(beforeReplay, afterReplay) {
		t.Fatal("replay repeated reset/copy", err)
	}
	turn = basicHandlerTurn(t, e, BasicStage, defs, registry)
	lookup, err = lookupBasic(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if ack, err := acknowledgeBasic(t.Context(), turn, q, lookup); err != nil || ack != cmd.identity.Sequence {
		t.Fatal("ACK", ack, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeStage, cmd.request.original.run.leaseExpiresMS)
	turn = basicHandlerTurn(t, e, BasicStage, defs, registry)
	if lookup, err = lookupBasic(t.Context(), turn, q); err != nil || lookup.decision != receiptRetired {
		t.Fatal("ACKed resume resurrected", lookup, err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = opening.close(); err != nil && !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
}

func crashReplayBasicResumeEveryStep(t *testing.T, operation string) {
	trace := crashWitness{Case: crashCase{Domain: "basic_resume", Operation: operation, Boundary: "trace"}}
	source := executeBasicResumeCrash(t, &trace)
	required := []string{string(basicResumeHeader), string(basicResumeLengths), string(basicResumeTexts), string(basicResumeAsset), string(basicResumeLink), string(basicResumePhase)}
	if operation == "copy" {
		required = append(required, string(basicResumeReset), string(basicCopyCursorCAS))
	} else {
		required = append(required, string(basicResumeResetDue), string(basicResumeFinish))
	}
	for _, id := range required {
		found := false
		for _, step := range source.Trace {
			if strings.HasPrefix(step, "native_"+id+"_") {
				found = true
			}
		}
		if !found {
			t.Fatal("missing native crash cut", id)
		}
	}
	t.Logf("%s: %d native executions plus 8 transaction/return/uncertain cuts", operation, len(source.Trace))
	_, cmd := basicResumeCrashSeed(t, operation)
	phases := append([]string{"before_begin", "after_begin"}, source.Trace...)
	phases = append(phases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			verifyBasicResumeCrash(t, runCrashChild(t, crashCase{"basic_resume", operation, phase, ""}), cmd)
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("uncertain_"+fault, func(t *testing.T) {
			verifyBasicResumeCrash(t, runCrashChild(t, crashCase{"basic_resume", operation, "uncertain_return", fault}), cmd)
		})
	}
}
func TestCrashReplayBasicResumeCopyEveryStep(t *testing.T) {
	crashReplayBasicResumeEveryStep(t, "copy")
}
func TestCrashReplayBasicResumeFinishEveryStep(t *testing.T) {
	crashReplayBasicResumeEveryStep(t, "finish")
}
