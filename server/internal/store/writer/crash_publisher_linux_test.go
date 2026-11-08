package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// Publisher-job fragment stage and head flip process-crash cells. Test-only:
// no production hook, startup adapter, permit, profile or new constructor is
// added, and no real TMDB/provider call is made. The fragment/head commands are
// the existing deterministic fixture requests; each killed child ships its exact
// original typed receipt request/identity and the pre-command raw checkpoint.
const publisherCrashDomain = "publisher_crash"

// Before the fixture's 2036 lease horizon and fixed so an in-process control run
// and every killed child observe the same acceptance time. Protocol input, not a
// latency or native-cost claim.
var publisherCrashClock = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func init() { registerCrashDomain(publisherCrashDomain, publisherCrashFixtureChild) }

// Payload is test-only data. It carries the original typed request/identity, the
// fixed clock and the raw pre-command checkpoint; it is never a permit, source
// proof, reconstructed result or retry authority.
type publisherCrashPayload struct {
	Purpose CommandID
	Request PublisherReceiptRequest
	ClockMS int64
	Pre     map[string][32]byte
}

// The existing publisher fixture seeded through its real command body, with the
// clock fixed BEFORE binding so the governor, acceptance time and every native
// observation use the same canonical instant.
func publisherCrashSeeded(t *testing.T, purpose CommandID) (*executor, *searchFragmentStageCommand, *searchHeadPublishCommand) {
	t.Helper()
	var e *executor
	var fragment *searchFragmentStageCommand
	var head *searchHeadPublishCommand
	switch purpose {
	case SearchFragmentStage:
		e, fragment = searchFragmentNativeSeed(t)
	case SearchHeadPublish:
		e, head = searchHeadNativeSeed(t)
	default:
		t.Fatal("unclosed publisher crash purpose", purpose)
	}
	e.now = func() time.Time { return publisherCrashClock }
	publisherCrashCanonicalGeneration(t, e)
	return e, fragment, head
}

// The canonical schema gives search_generations a randomblob incarnation. Pin it
// in fixture setup for the in-process control and every killed child so the raw
// checkpoint can be compared byte-exactly across processes. The publisher
// commands never read this column; its generation receipt rows move with it in
// one deferred-FK transaction, so the foreign-key census stays exact.
func publisherCrashCanonicalGeneration(t *testing.T, e *executor) {
	t.Helper()
	var current []byte
	if err := e.db.QueryRowContext(t.Context(), `SELECT command_incarnation FROM search_generations WHERE id=7`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	pinned := make([]byte, 16)
	for i := range pinned {
		pinned[i] = byte(0xd0 + i)
	}
	if string(current) == string(pinned) {
		return
	}
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), "PRAGMA defer_foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(),
		`UPDATE search_generation_receipts SET command_incarnation=? WHERE search_generation_id=7 AND command_incarnation=?`, pinned, current); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE search_generations SET command_incarnation=? WHERE id=7`, pinned); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func publisherCrashBound(t *testing.T, purpose CommandID) *publisherGovernedFixture {
	t.Helper()
	e, fragment, head := publisherCrashSeeded(t, purpose)
	return bindPublisherFixture(t, e, purpose, fragment, head, nil, false)
}

// The exact typed fragment request the existing seed constructs. Rebuilding it
// values-only proves the parent retries the SAME original request/identity: the
// reconstructed reference must equal the witnessed original digest.
func publisherCrashFragmentCommand(t *testing.T, reference ReceiptReference) *searchFragmentStageCommand {
	t.Helper()
	s, rows := searchFragmentStageSnapshotFixture(t)
	command, err := newSearchFragmentStageCommand(CommandIdentity{s.job.incarnation, 8, [16]byte{71}},
		searchFragmentStageRequest{"fragment_stage", s, rows[0].row})
	if err != nil {
		t.Fatal(err)
	}
	if got := (ReceiptReference{command.identity, command.digest}); got != reference {
		t.Fatal("reconstructed fragment request is not the witnessed original", got, reference)
	}
	return command
}

func publisherCrashPurpose(operation string) CommandID {
	switch operation {
	case "fragment":
		return SearchFragmentStage
	case "flip":
		return SearchHeadPublish
	default:
		return 0
	}
}

func publisherCrashRun(t *testing.T, f *publisherGovernedFixture, purpose CommandID, fragment *searchFragmentStageCommand, head *searchHeadPublishCommand) (searchPublisherCommandResult, AckResult, error) {
	t.Helper()
	if purpose != SearchFragmentStage && purpose != SearchHeadPublish {
		t.Fatal("unclosed publisher crash purpose", purpose)
	}
	return f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, purpose, fragment, head, false)
}

// Child: build the REAL command fixture, arm the crash driver, and execute the
// real command body. Each distinct executed statement ID is a single boundary;
// fixed BEGIN/COMMIT/return and uncertain-COMMIT boundaries are explicit.
func publisherCrashFixtureChild(t *testing.T, witness *crashWitness) {
	t.Helper()
	purpose := publisherCrashPurpose(witness.Case.Operation)
	if purpose == 0 {
		t.Fatal("unsupported publisher crash operation", witness.Case)
	}
	f := publisherCrashBound(t, purpose)
	e, path := f.e, publisherFixturePath(t, f.e)
	pre, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	witness.Path, witness.Reference = path, f.q.Receipt
	payload, err := json.Marshal(publisherCrashPayload{Purpose: purpose, Request: f.q,
		ClockMS: publisherCrashClock.UnixMilli(), Pre: pre})
	if err != nil {
		t.Fatal(err)
	}
	witness.Payload = payload
	ids := map[string]string{}
	for _, entry := range f.w.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, e, path, witness, publisherCrashClock, func(query string) {
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
	result, _, runErr := publisherCrashRun(t, f, purpose, f.fragment, f.head)
	armed = false
	if witness.Case.CommitFault != "" {
		// A lost COMMIT reply must return a zero value, quarantine the runtime
		// and never report success. Only then is the uncertain boundary stable.
		if !errors.Is(runErr, ErrResolveRequired) || !errors.Is(runErr, errCrashCommitReply) ||
			result != (searchPublisherCommandResult{}) || !e.ambiguous || !f.w.effective.fenced {
			t.Fatal("publisher uncertain COMMIT reported success or escaped quarantine", result, runErr)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
		t.Fatal("publisher uncertain boundary was never reached", witness.Case)
	}
	if runErr != nil {
		t.Fatal("actual publisher command refused", runErr)
	}
	if result.Outcome.State != Applied || result.Outcome.Replayed || result.Outcome.Reference != f.q.Receipt {
		t.Fatal("actual publisher command did not apply the original", result.Outcome)
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("publisher boundary was never reached", witness.Case)
}

// Oracle: one in-process real execution. It learns the executed native IDs and
// freezes the complete saved result, raw receipt row and post-COMMIT checkpoint
// BEFORE any child spawns. It never supplies the child's pre-command state.
type publisherCrashOracle struct {
	Operation string
	Purpose   CommandID
	Request   PublisherReceiptRequest
	Result    searchPublisherCommandResult
	Post      map[string][32]byte
	Receipt   map[string]effectiveRawCell
}

func publisherCrashOracleFor(t *testing.T, operation string, purpose CommandID) (publisherCrashOracle, []string) {
	t.Helper()
	f := publisherCrashBound(t, purpose)
	e, path := f.e, publisherFixturePath(t, f.e)
	ids := map[string]string{}
	for _, entry := range f.w.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	witness := crashWitness{Case: crashCase{publisherCrashDomain, operation, "trace", ""}}
	installCrashDriverAt(t, e, path, &witness, publisherCrashClock, func(query string) {
		if !armed {
			return
		}
		if id, ok := ids[strings.TrimSpace(query)]; ok {
			seen[id] = true
		}
	})
	armed = true
	result, _, err := publisherCrashRun(t, f, purpose, f.fragment, f.head)
	armed = false
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.State != Applied || result.Outcome.Replayed || result.Outcome.Reference != f.q.Receipt {
		t.Fatal("trace control did not apply the original", result.Outcome)
	}
	post, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found, err := readPublisherCrashReceipt(t.Context(), e.db, f.q)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("trace control lost its committed receipt")
	}
	result.Outcome.Evidence = nil
	phases := make([]string, 0, len(seen))
	for id := range seen {
		phases = append(phases, "native_"+id)
	}
	return publisherCrashOracle{Operation: operation, Purpose: purpose, Request: f.q,
		Result: result, Post: post, Receipt: receipt}, phases
}

func readPublisherCrashReceipt(ctx context.Context, reader effectiveJournalReader, q PublisherReceiptRequest) (map[string]effectiveRawCell, bool, error) {
	if q.JobID <= 0 || q.Receipt.Identity.Sequence <= 0 {
		return nil, false, ErrInvalid
	}
	rows, err := reader.QueryContext(ctx,
		`SELECT * FROM search_publish_receipts WHERE publish_job_id=? AND command_incarnation=? AND command_sequence=?`,
		q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	if !rows.Next() {
		return nil, false, errors.Join(rows.Err(), rows.Close())
	}
	values, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range dest {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, false, err
	}
	row, err := effectiveRawRow(columns, values)
	if err != nil {
		return nil, false, err
	}
	if rows.Next() || rows.Err() != nil {
		return nil, false, errors.New("duplicate original publisher receipt")
	}
	return row, true, nil
}

func publisherCrashReceiptCount(t *testing.T, ctx context.Context, reader inventoryCrashReader, q PublisherReceiptRequest) int64 {
	t.Helper()
	var n int64
	if err := reader.QueryRowContext(ctx,
		`SELECT count(*) FROM search_publish_receipts WHERE publish_job_id=? AND command_incarnation=? AND command_sequence=?`,
		q.JobID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Reopen only the killed canonical file under the existing ownership opening.
// No seeding, reset, replay of setup SQL or second fixture; the integrity and
// foreign-key census is on the reopened file itself.
func publisherCrashReopen(t *testing.T, path string) *scanOpening {
	t.Helper()
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner)
	conn, err := opening.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("integrity_check", integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign_key_check", rows.Err())
	}
	return opening
}

// ACK is the shared scalar kernel: the domain may project its already-looked-up
// saved fact, but no outcome/evidence belongs in the command result and no fresh
// effect may be written.
func publisherCrashCheckAck(t *testing.T, operation string, got searchPublisherCommandResult, ack AckResult, want searchPublisherCommandResult, q PublisherReceiptRequest, err error) {
	t.Helper()
	if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil ||
		got.Outcome != (ReceiptOutcome{}) || got.Fragment != want.Fragment || got.Head != want.Head {
		t.Fatal(operation, got, ack, err)
	}
}

// Restart: rebuild the writer/dispatcher on the SAME existing file and verify
// the original identity. A committed receipt must be byte-exact, serve its saved
// result on replay, ACK exactly once and leave a second replay write-free. A
// missing receipt must leave the pre-command checkpoint untouched and resolve
// Pending; the fragment before-COMMIT cell additionally retries once.
func verifyPublisherCrash(t *testing.T, oracle publisherCrashOracle, witness crashWitness, retry bool) {
	t.Helper()
	if witness.Case.Domain != publisherCrashDomain || witness.Case.Operation != oracle.Operation ||
		witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("publisher witness lost its original operation", witness.Case)
	}
	var payload publisherCrashPayload
	if err := json.Unmarshal(witness.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Purpose != oracle.Purpose || payload.Request != oracle.Request || witness.Reference != oracle.Request.Receipt ||
		payload.ClockMS != publisherCrashClock.UnixMilli() {
		t.Fatalf("witness original request/identity changed: %+v", payload)
	}
	opening := publisherCrashReopen(t, witness.Path)
	e := opening.executor
	e.now = func() time.Time { return time.UnixMilli(payload.ClockMS) }
	f := bindPublisherFixture(t, e, oracle.Purpose, nil, nil, nil, false)
	cap := publisherFixtureCapability(t, f.w, payload.Request)
	committed := publisherCrashCommitted(witness.Case)
	reopened, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found, err := readPublisherCrashReceipt(t.Context(), e.db, payload.Request)
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		if found {
			t.Fatal("pre-COMMIT publisher kill left an original receipt")
		}
		if err := compareN1DriverCheckpoint(payload.Pre, reopened); err != nil {
			t.Fatal("pre-COMMIT publisher kill changed native state", err)
		}
		if retry {
			publisherCrashRetry(t, f, cap, payload, oracle)
			return
		}
		pending, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, false)
		if err != nil || pending.Outcome.State != Pending || pending.Outcome.Replayed ||
			pending.Fragment != (searchFragmentStageResult{}) || pending.Head != (searchHeadPublishResult{}) {
			t.Fatal("missing publisher receipt did not resolve Pending", pending, err)
		}
		settled, err := n1DriverCheckpoint(t.Context(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareN1DriverCheckpoint(payload.Pre, settled); err != nil {
			t.Fatal("missing-receipt resolve wrote state", err)
		}
		if n := publisherCrashReceiptCount(t, t.Context(), e.db, payload.Request); n != 0 {
			t.Fatal("missing-receipt resolve minted a receipt", n)
		}
		return
	}
	if !found {
		t.Fatal("post-COMMIT publisher crash lost its original receipt")
	}
	if !reflect.DeepEqual(receipt, oracle.Receipt) {
		t.Fatalf("reopened raw receipt differs:\n got %#v\nwant %#v", receipt, oracle.Receipt)
	}
	if err := compareN1DriverCheckpoint(oracle.Post, reopened); err != nil {
		t.Fatal("post-COMMIT reopened checkpoint differs from the control", err)
	}
	want := oracle.Result
	want.Outcome.Replayed = true
	first, ack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, false)
	if err != nil || first.Outcome.State != Applied || !first.Outcome.Replayed || first.Outcome.Evidence == nil || ack != (AckResult{}) {
		t.Fatal("restart replay did not serve the original", first, ack, err)
	}
	first.Outcome.Evidence = nil
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("restart replay changed the saved result:\n got %#v\nwant %#v", first, want)
	}
	second, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, false)
	if err != nil || second.Outcome.State != Applied || !second.Outcome.Replayed || second.Outcome.Evidence == nil {
		t.Fatal("second restart replay did not serve the original", second, err)
	}
	second.Outcome.Evidence = nil
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("second restart replay changed the saved result:\n got %#v\nwant %#v", second, want)
	}
	afterReplay, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(reopened, afterReplay); err != nil {
		t.Fatal("second replay wrote state", err)
	}
	acked, ackBack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, true)
	publisherCrashCheckAck(t, "restart ACK", acked, ackBack, oracle.Result, payload.Request, err)
	settled, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	acked, again, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, true)
	publisherCrashCheckAck(t, "repeated restart ACK", acked, again, searchPublisherCommandResult{}, payload.Request, err)
	if again.AcknowledgedThrough != ackBack.AcknowledgedThrough {
		t.Fatal("repeated restart ACK changed the floor", again, ackBack)
	}
	afterAck, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, afterAck); err != nil {
		t.Fatal("repeated ACK wrote state", err)
	}
	if n := publisherCrashReceiptCount(t, t.Context(), e.db, payload.Request); n != 1 {
		t.Fatal("restart lost or duplicated the original receipt", n)
	}
	retired, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, oracle.Purpose, nil, nil, false)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Outcome.Replayed ||
		retired.Fragment != (searchFragmentStageResult{}) || retired.Head != (searchHeadPublishResult{}) {
		t.Fatal("ACKed original still served its saved result", retired, err)
	}
	final, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(afterAck, final); err != nil {
		t.Fatal("retired resolve wrote state", err)
	}
}

// publisherCrashCommitted is the deterministic physical truth of one boundary.
// Only a completed/returned COMMIT is durable; every in-transaction statement,
// BEGIN and the pre-COMMIT fault roll back with the killed process.
func publisherCrashCommitted(cell crashCase) bool {
	switch cell.Boundary {
	case "after_commit", "physical_return", "api_return":
		return true
	case "uncertain_return":
		return cell.CommitFault == "after"
	default:
		return false
	}
}

// Fresh retry after the fragment cell was killed before COMMIT. The same
// original typed request executes exactly once on the reopened file and reaches
// the byte-identical control state; a duplicated retry (command or resolve)
// serves the saved result with zero new effects.
func publisherCrashRetry(t *testing.T, f *publisherGovernedFixture, cap *PublisherDisclosure, payload publisherCrashPayload, oracle publisherCrashOracle) {
	t.Helper()
	if payload.Purpose != SearchFragmentStage || payload.Request != oracle.Request {
		t.Fatal("fresh retry cell is not the fragment original", payload)
	}
	command := publisherCrashFragmentCommand(t, payload.Request.Receipt)
	if n := publisherCrashReceiptCount(t, t.Context(), f.e.db, payload.Request); n != 0 {
		t.Fatal("fresh retry started with a receipt", n)
	}
	applied, ack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, payload.Purpose, command, nil, false)
	if err != nil || applied.Outcome.State != Applied || applied.Outcome.Replayed || applied.Outcome.Evidence == nil || ack != (AckResult{}) {
		t.Fatal("fresh retry did not execute exactly once", applied, ack, err)
	}
	applied.Outcome.Evidence = nil
	if !reflect.DeepEqual(applied, oracle.Result) {
		t.Fatalf("fresh retry changed the control result:\n got %#v\nwant %#v", applied, oracle.Result)
	}
	receipt, found, err := readPublisherCrashReceipt(t.Context(), f.e.db, payload.Request)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !reflect.DeepEqual(receipt, oracle.Receipt) {
		t.Fatalf("fresh retry raw receipt differs:\n got %#v\nwant %#v", receipt, oracle.Receipt)
	}
	if n := publisherCrashReceiptCount(t, t.Context(), f.e.db, payload.Request); n != 1 {
		t.Fatal("fresh retry produced duplicate or missing receipts", n)
	}
	retried, err := n1DriverCheckpoint(t.Context(), f.e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(oracle.Post, retried); err != nil {
		t.Fatal("fresh retry did not reach the exact control state", err)
	}
	want := oracle.Result
	want.Outcome.Replayed = true
	// A duplicated retry resends the SAME command: it must replay, not reexecute.
	dup, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, payload.Purpose, command, nil, false)
	if err != nil || dup.Outcome.State != Applied || !dup.Outcome.Replayed || dup.Outcome.Evidence == nil {
		t.Fatal("duplicated retry did not replay the saved result", dup, err)
	}
	dup.Outcome.Evidence = nil
	if !reflect.DeepEqual(dup, want) {
		t.Fatalf("duplicated retry changed the saved result:\n got %#v\nwant %#v", dup, want)
	}
	resolved, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, payload.Purpose, nil, nil, false)
	if err != nil || resolved.Outcome.State != Applied || !resolved.Outcome.Replayed {
		t.Fatal("duplicated resolve did not replay the saved result", resolved, err)
	}
	afterDup, err := n1DriverCheckpoint(t.Context(), f.e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(retried, afterDup); err != nil {
		t.Fatal("duplicated retry wrote state", err)
	}
	acked, ackBack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, payload.Purpose, nil, nil, true)
	publisherCrashCheckAck(t, "retry ACK", acked, ackBack, oracle.Result, payload.Request, err)
	settled, err := n1DriverCheckpoint(t.Context(), f.e.db)
	if err != nil {
		t.Fatal(err)
	}
	acked, again, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, payload.Request, payload.Purpose, nil, nil, true)
	publisherCrashCheckAck(t, "repeated retry ACK", acked, again, searchPublisherCommandResult{}, payload.Request, err)
	if again.AcknowledgedThrough != ackBack.AcknowledgedThrough {
		t.Fatal("repeated retry ACK changed the floor", again, ackBack)
	}
	afterAck, err := n1DriverCheckpoint(t.Context(), f.e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, afterAck); err != nil {
		t.Fatal("repeated retry ACK wrote state", err)
	}
	if n := publisherCrashReceiptCount(t, t.Context(), f.e.db, payload.Request); n != 1 {
		t.Fatal("retry lost or duplicated the original receipt", n)
	}
}

// One boundary per traced distinct statement plus the explicit BEGIN/COMMIT,
// return and uncertain-COMMIT boundaries. The fragment before-COMMIT cell is the
// single receipt-miss/fresh-retry cell; no exhaustive statement matrix.
func TestCrashReplayPublisherFragmentAndFlip(t *testing.T) {
	if raceDetector {
		// The sequential child matrix exceeds one shard's race budget. The same
		// cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	for _, spec := range []struct {
		operation string
		purpose   CommandID
		retryAt   string
	}{
		{"fragment", SearchFragmentStage, "before_commit"},
		{"flip", SearchHeadPublish, ""},
	} {
		oracle, phases := publisherCrashOracleFor(t, spec.operation, spec.purpose)
		phases = append([]string{"before_begin", "after_begin"}, phases...)
		phases = append(phases, "before_commit", "after_commit", "physical_return", "api_return")
		for _, phase := range phases {
			retry := spec.retryAt == phase
			name := phase
			if retry {
				name += "/fresh_retry"
			}
			t.Run(spec.operation+"/"+name, func(t *testing.T) {
				witness := runCrashChild(t, crashCase{publisherCrashDomain, spec.operation, phase, ""})
				verifyPublisherCrash(t, oracle, witness, retry)
			})
		}
		for _, fault := range []string{"before", "after"} {
			t.Run(fmt.Sprint(spec.operation, "/uncertain_return/", fault), func(t *testing.T) {
				witness := runCrashChild(t, crashCase{publisherCrashDomain, spec.operation, "uncertain_return", fault})
				verifyPublisherCrash(t, oracle, witness, false)
			})
		}
	}
}
