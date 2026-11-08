package writer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// Linux process-crash controls for the independent search fragment reclaimer.
// The tested surface is the REAL reclaimer dispatcher entry over the existing
// composed publisher fixture: one retained entity fragment, its own receipt/ACK
// owner and native retirement. No production hook, prefix, permit, profile or
// startup adapter is added, and no expected state is copied from a writer result.
const reclaimerCrashDomain = "reclaimer_crash"

// reclaimerCrashClock is one deterministic test instant before the fixture's
// frozen job lease expiry. It is the SINGLE clock observed by the fixture's
// governor, WAL gate and command turn, so a child and its parent oracle agree
// on every persisted millisecond value. It is not a production clock or lease.
var reclaimerCrashClock = time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)

func init() { registerCrashDomain(reclaimerCrashDomain, crashReclaimerFixture) }

// reclaimerCrashPayload is test-only data carried across the SIGKILL. It keeps
// the exact original typed receipt request/identity/IDs, the raw file checkpoint
// observed before the tested command, and the native receipt census at that
// point. It is not a grant, lease, decoded result or reconstructed receipt.
type reclaimerCrashPayload struct {
	Operation     string
	ReclaimerID   int64
	PublicationID int64
	LibraryID     int64
	EntityID      int64
	Receipt       ReceiptReference
	ReceiptRows   int64
	Initial       map[string][32]byte
	ClockMS       int64
}

type reclaimerReceiptRow struct {
	Incarnation      []byte
	Sequence         int64
	OperationID      []byte
	RequestDigest    []byte
	Kind             string
	AcceptedRevision int64
	AppliedMS        int64
	PublicationID    int64
	FragmentID       int64
	AfterFragmentID  int64
	Complete         int64
}

func reclaimerReceipt(t *testing.T, reader inventoryCrashReader, q searchReclaimerReceiptRequest) (row reclaimerReceiptRow, found bool) {
	t.Helper()
	err := reader.QueryRowContext(t.Context(),
		`SELECT command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,publication_id,fragment_id,after_fragment_id,complete
FROM search_reclaim_receipts WHERE reclaim_job_id=? AND command_incarnation=? AND command_sequence=?`,
		q.ReclaimerID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence).Scan(
		&row.Incarnation, &row.Sequence, &row.OperationID, &row.RequestDigest, &row.Kind,
		&row.AcceptedRevision, &row.AppliedMS, &row.PublicationID, &row.FragmentID, &row.AfterFragmentID, &row.Complete)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return row, true
}

func reclaimerReceiptCount(t *testing.T, reader inventoryCrashReader, job int64) int64 {
	t.Helper()
	var count int64
	if err := reader.QueryRowContext(t.Context(), `SELECT count(*) FROM search_reclaim_receipts WHERE reclaim_job_id=?`, job).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func reclaimerFloor(t *testing.T, reader inventoryCrashReader, job int64) (last, acknowledged int64) {
	t.Helper()
	if err := reader.QueryRowContext(t.Context(),
		`SELECT last_command_sequence,acknowledged_command_sequence FROM search_reclaim_jobs WHERE id=?`, job).Scan(&last, &acknowledged); err != nil {
		t.Fatal(err)
	}
	return last, acknowledged
}

// reclaimerCrashFixtureOn prepares the SAME composed publisher fixture, then
// rebinds one W runtime whose governor, WAL gate and dispatcher all observe the
// one frozen test clock. The initial binding only creates the real native input;
// it executes no command and writes no receipt.
func reclaimerCrashFixtureOn(t *testing.T) *publisherGovernedFixture {
	t.Helper()
	seeded := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
	if seeded.reclaim == nil {
		t.Fatal("reclaimer seed lost its closed command")
	}
	seeded.e.now = func() time.Time { return reclaimerCrashClock }
	return bindPublisherFixture(t, seeded.e, SearchFragmentReclaim, nil, nil, seeded.reclaim, false)
}

// crashReclaimerFixture commits the fixture's real native input, arms the crash
// observer over the actual reclaimer registry, and then executes the real
// command body. The first distinct execution of every manifested statement is
// one reported boundary; BEGIN/COMMIT and the dispatcher's physical/API return
// add the explicit crash points.
func crashReclaimerFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	if witness.Case.Operation != "resolve" && witness.Case.Operation != "ack" && witness.Case.Operation != "retry" {
		t.Fatal("unsupported reclaimer crash operation", witness.Case)
	}
	f := reclaimerCrashFixtureOn(t)
	e, path := f.e, publisherFixturePath(t, f.e)
	clock := reclaimerCrashClock
	q := f.rq
	if q.ReclaimerID <= 0 || q.PublicationID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 || q.Receipt.Identity.Sequence != 1 {
		t.Fatal("reclaimer fixture did not expose its original typed receipt", q)
	}
	if witness.Case.Operation == "ack" {
		// Commit the one real original beyond every tested ACK boundary. The
		// receipt is the durable same-owner handle the client ACKs.
		out, ack, err := runReclaimer(t, f, f.reclaim, false)
		if err != nil || ack != (AckResult{}) || out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != q.Receipt {
			t.Fatal("reclaimer ACK setup", out, ack, err)
		}
	}
	payload := reclaimerCrashPayload{
		Operation: witness.Case.Operation, ReclaimerID: q.ReclaimerID, PublicationID: q.PublicationID,
		LibraryID: q.LibraryID, EntityID: q.EntityID, Receipt: q.Receipt, ClockMS: clock.UnixMilli(),
	}
	payload.ReceiptRows = reclaimerReceiptCount(t, e.db, q.ReclaimerID)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	payload.Initial = initial
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	witness.Path, witness.Reference, witness.Payload = path, q.Receipt, encoded
	ids := map[string]string{}
	for _, entry := range f.w.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, e, path, witness, clock, func(query string) {
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
	switch witness.Case.Operation {
	case "resolve", "retry":
		out, ack, err := runReclaimer(t, f, f.reclaim, false)
		armed = false
		if witness.Case.CommitFault != "" {
			if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) ||
				out != (searchPublisherCommandResult{}) || ack != (AckResult{}) || !e.ambiguous || !f.w.effective.fenced {
				t.Fatal("reclaimer uncertain COMMIT reported success or escaped quarantine", out, ack, err)
			}
			witness.Unknown = true
			crashPoint(t, witness, "uncertain_return")
			t.Fatal("reclaimer uncertain boundary was never reached", witness.Case)
		}
		if err != nil {
			t.Fatal("actual reclaimer resolve refused", err)
		}
		if out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != q.Receipt ||
			out.Reclaim.JobID != q.ReclaimerID || out.Reclaim.PublicationID != q.PublicationID {
			t.Fatal("reclaimer resolve did not apply the original", out)
		}
	case "ack":
		_, ack, err := runReclaimer(t, f, nil, true)
		armed = false
		if witness.Case.CommitFault != "" {
			if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) ||
				ack != (AckResult{}) || !e.ambiguous || !f.w.effective.fenced {
				t.Fatal("reclaimer ACK uncertain COMMIT reported success or escaped quarantine", ack, err)
			}
			witness.Unknown = true
			crashPoint(t, witness, "uncertain_return")
			t.Fatal("reclaimer ACK uncertain boundary was never reached", witness.Case)
		}
		if err != nil {
			t.Fatal("actual reclaimer ACK refused", err)
		}
		if ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil {
			t.Fatal("reclaimer ACK floor/evidence", ack)
		}
	}
	if witness.Case.Boundary == "trace" {
		return
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("requested reclaimer boundary was never reached", witness.Case)
}

// reclaimerCrashReference is the parent's own real execution of the SAME
// deterministic original on an independently constructed fixture. Its raw
// checkpoints and receipt row are the restart oracle; it is never seeded from a
// child observation and never stands in for a writer result.
type reclaimerCrashReference struct {
	rq                searchReclaimerReceiptRequest
	command           *searchFragmentReclaimCommand
	initial, resolved map[string][32]byte
	acked             map[string][32]byte
	saved             searchFragmentReclaimResult
	receipt           reclaimerReceiptRow
}

func newReclaimerCrashReference(t *testing.T) *reclaimerCrashReference {
	t.Helper()
	f := reclaimerCrashFixtureOn(t)
	e := f.e
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	out, ack, err := runReclaimer(t, f, f.reclaim, false)
	if err != nil || ack != (AckResult{}) || out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != f.rq.Receipt {
		t.Fatal("reference resolve", out, ack, err)
	}
	if out.Reclaim.JobID != f.rq.ReclaimerID || out.Reclaim.PublicationID != f.rq.PublicationID || out.Reclaim.FragmentID <= 0 {
		t.Fatal("reference resolve lost the original normalized values", out.Reclaim)
	}
	resolved, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := reclaimerReceipt(t, e.db, f.rq)
	if !found || reclaimerReceiptCount(t, e.db, f.rq.ReclaimerID) != 1 {
		t.Fatal("reference did not retain exactly one raw receipt")
	}
	replay, replayAck, err := runReclaimer(t, f, nil, false)
	if err != nil || replayAck != (AckResult{}) || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Reclaim != out.Reclaim {
		t.Fatal("reference replay", replay, replayAck, err)
	}
	_, first, err := runReclaimer(t, f, nil, true)
	if err != nil || first.AcknowledgedThrough != f.rq.Receipt.Identity.Sequence || first.Evidence == nil {
		t.Fatal("reference ACK", first, err)
	}
	acked, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	return &reclaimerCrashReference{rq: f.rq, command: f.reclaim, initial: initial, resolved: resolved, acked: acked, saved: out.Reclaim, receipt: receipt}
}

// reopenReclaimer opens the killed child's file through the canonical existing
// opening (ownership, native identity, schema assertion), repeats the independent
// integrity checks, and rebuilds the SAME writer/dispatcher on that file. It
// runs no setup SQL and replays no seed.
func reopenReclaimer(t *testing.T, path string, command *searchFragmentReclaimCommand) *publisherGovernedFixture {
	t.Helper()
	owner := scanOpeningOwner(t, path)
	opening := openScanFixture(t, path, owner)
	e := opening.executor
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = conn.Close()
		t.Fatal("integrity_check", integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if rows.Next() {
		_ = rows.Close()
		_ = conn.Close()
		t.Fatal("foreign_key_check reported a violated reference")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := errors.Join(rows.Close(), conn.Close()); err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return reclaimerCrashClock }
	return bindPublisherFixture(t, e, SearchFragmentReclaim, nil, nil, command, false)
}

func reclaimerCheckpoint(t *testing.T, fixture *publisherGovernedFixture) map[string][32]byte {
	t.Helper()
	checkpoint, err := n1DriverCheckpoint(t.Context(), fixture.e.db)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

// reclaimerCommitted is the physical commit point each boundary claims. A
// lost-COMMIT-reply fault is committed exactly when the reply was lost after
// the native COMMIT.
func reclaimerCommitted(c crashCase) bool {
	switch c.Boundary {
	case "after_commit", "physical_return", "api_return":
		return true
	case "uncertain_return":
		return c.CommitFault == "after"
	}
	return false
}

func verifyReclaimerResolvesSaved(t *testing.T, f *publisherGovernedFixture, ref *reclaimerCrashReference) {
	t.Helper()
	out, ack, err := runReclaimer(t, f, nil, false)
	if err != nil || ack != (AckResult{}) || out.Outcome.State != Applied || !out.Outcome.Replayed ||
		out.Outcome.Reference != ref.rq.Receipt || out.Reclaim != ref.saved {
		t.Fatal("restart did not resolve the original to its exact saved result", out, ack, err)
	}
}

func verifyReclaimerResolvesRetired(t *testing.T, f *publisherGovernedFixture, ref *reclaimerCrashReference) {
	t.Helper()
	out, ack, err := runReclaimer(t, f, nil, false)
	if err != nil || ack != (AckResult{}) || out.Outcome.State != ResultRetired ||
		out.Reclaim != (searchFragmentReclaimResult{}) || out.Outcome.Replayed {
		t.Fatal("ACKed original still served its retired fragment payload", out, ack, err)
	}
}

// verifyReclaimerReplayWritesNothing proves the duplicate retry/resolve is a
// pure saved-result replay by comparing every user table before and after.
func verifyReclaimerReplayWritesNothing(t *testing.T, f *publisherGovernedFixture, ref *reclaimerCrashReference) {
	t.Helper()
	settled := reclaimerCheckpoint(t, f)
	verifyReclaimerResolvesSaved(t, f, ref)
	after := reclaimerCheckpoint(t, f)
	if err := compareN1DriverCheckpoint(settled, after); err != nil {
		t.Fatal("duplicate retry wrote state", err)
	}
}

func verifyReclaimerAckIdempotent(t *testing.T, f *publisherGovernedFixture, ref *reclaimerCrashReference) {
	t.Helper()
	_, first, err := runReclaimer(t, f, nil, true)
	if err != nil || first.AcknowledgedThrough != ref.rq.Receipt.Identity.Sequence || first.Evidence == nil {
		t.Fatal("restart reclaimer ACK", first, err)
	}
	settled := reclaimerCheckpoint(t, f)
	_, second, err := runReclaimer(t, f, nil, true)
	if err != nil || second.AcknowledgedThrough != first.AcknowledgedThrough || second.Evidence == nil {
		t.Fatal("repeated restart reclaimer ACK", second, err)
	}
	after := reclaimerCheckpoint(t, f)
	if err := compareN1DriverCheckpoint(settled, after); err != nil {
		t.Fatal("repeated reclaimer ACK wrote state", err)
	}
}

// reclaimerChangedTables names the raw tables the reference's own real command
// changed between two observed states. It is derived from actual execution.
func reclaimerChangedTables(before, after map[string][32]byte) map[string]bool {
	changed := map[string]bool{}
	for name, hash := range after {
		if before[name] != hash {
			changed[name] = true
		}
	}
	return changed
}

// reclaimerMatches proves the reopened file equals the reference's real
// post-state on every changed table and is byte-identical to the child's own
// pre-command state on every other table. Random schema-default incarnations in
// unrelated seed tables are not effects of this command.
func reclaimerMatches(label string, changed map[string]bool, reference, baseline, got map[string][32]byte) error {
	if len(got) != len(reference) {
		return fmt.Errorf("%s: table inventory changed: got %d, want %d", label, len(got), len(reference))
	}
	for name, want := range reference {
		actual, ok := got[name]
		if !ok {
			return fmt.Errorf("%s: lost table %s", label, name)
		}
		if changed[name] {
			if actual != want {
				return fmt.Errorf("%s: changed table %s differs from the reference", label, name)
			}
			continue
		}
		if before, ok := baseline[name]; !ok || actual != before {
			return fmt.Errorf("%s: unrelated table %s changed", label, name)
		}
	}
	return nil
}

func verifyReclaimerCrash(t *testing.T, ref *reclaimerCrashReference, witness crashWitness) {
	t.Helper()
	if witness.Case.Domain != reclaimerCrashDomain || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("reclaimer witness lost its case/uncertainty", witness.Case)
	}
	var payload reclaimerCrashPayload
	if err := json.Unmarshal(witness.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Operation != witness.Case.Operation || payload.Receipt != witness.Reference || witness.Reference != ref.rq.Receipt ||
		payload.ReclaimerID != ref.rq.ReclaimerID || payload.PublicationID != ref.rq.PublicationID ||
		payload.LibraryID != ref.rq.LibraryID || payload.EntityID != ref.rq.EntityID || payload.ClockMS != reclaimerCrashClock.UnixMilli() {
		t.Fatal("reclaimer witness did not carry the exact original typed receipt", payload)
	}
	wantReceipts := int64(0)
	if payload.Operation == "ack" {
		// The ACK child committed the one real original before arming.
		wantReceipts = 1
	}
	if payload.ReceiptRows != wantReceipts {
		t.Fatal("child original receipt census", payload.ReceiptRows, wantReceipts, payload.Operation)
	}

	f := reopenReclaimer(t, witness.Path, ref.command)
	expectPre, expectPost := ref.initial, ref.resolved
	preReceipts, postReceipts := int64(0), int64(1)
	if payload.Operation == "ack" {
		expectPre, expectPost = ref.resolved, ref.acked
		preReceipts, postReceipts = 1, 1
	}
	got := reclaimerCheckpoint(t, f)
	receipts := reclaimerReceiptCount(t, f.e.db, ref.rq.ReclaimerID)
	// The child's OWN pre-command checkpoint is the baseline. Only tables the
	// reference's real command changes are compared across processes; unrelated
	// seed tables may carry schema randomblob defaults and are never effects.
	preMatch := reflect.DeepEqual(got, payload.Initial) && receipts == preReceipts
	changed := reclaimerChangedTables(expectPre, expectPost)
	postErr := reclaimerMatches("restart", changed, expectPost, payload.Initial, got)
	postMatch := postErr == nil && receipts == postReceipts
	committed := false
	switch {
	case preMatch:
	case postMatch:
		committed = true
	default:
		t.Fatalf("restart state is neither the original nor the committed raw state: pre=%v post=%v receipts=%d: %v", preMatch, postMatch, receipts, postErr)
	}
	if committed != reclaimerCommitted(witness.Case) {
		t.Fatal("crash state contradicts the physical commit point", witness.Case, committed)
	}

	switch {
	case payload.Operation == "ack" && committed:
		// Green-end ACK: the same original is retired and the floor replays free.
		if last, acknowledged := reclaimerFloor(t, f.e.db, ref.rq.ReclaimerID); last != 1 || acknowledged != 1 {
			t.Fatal("committed ACK lost its owner floor", last, acknowledged)
		}
		verifyReclaimerResolvesRetired(t, f, ref)
		verifyReclaimerAckIdempotent(t, f, ref)
	case payload.Operation == "ack":
		// Pre-COMMIT ACK: the receipt still replays and one ACK advances the floor.
		if last, acknowledged := reclaimerFloor(t, f.e.db, ref.rq.ReclaimerID); last != 1 || acknowledged != 0 {
			t.Fatal("pre-COMMIT ACK changed the owner floor", last, acknowledged)
		}
		verifyReclaimerResolvesSaved(t, f, ref)
		verifyReclaimerAckIdempotent(t, f, ref)
	case committed:
		verifyReclaimerResolvesSaved(t, f, ref)
		verifyReclaimerReplayWritesNothing(t, f, ref)
		verifyReclaimerAckIdempotent(t, f, ref)
		verifyReclaimerResolvesRetired(t, f, ref)
	default:
		// Pre-COMMIT resolve/retry. Nothing was written, so the documented
		// recovery is ONE client retry of the same original typed command.
		if receipts != 0 {
			t.Fatal("pre-COMMIT crash left a receipt", receipts)
		}
		out, ack, err := runReclaimer(t, f, ref.command, false)
		if err != nil || ack != (AckResult{}) || out.Outcome.State != Applied || out.Outcome.Replayed ||
			out.Outcome.Reference != ref.rq.Receipt || out.Reclaim != ref.saved {
			t.Fatal("retry did not execute the original exactly once", out, ack, err)
		}
		if receipts := reclaimerReceiptCount(t, f.e.db, ref.rq.ReclaimerID); receipts != 1 {
			t.Fatal("retry did not leave exactly one receipt", receipts)
		}
		if err := reclaimerMatches("retry", reclaimerChangedTables(ref.initial, ref.resolved), ref.resolved, payload.Initial, reclaimerCheckpoint(t, f)); err != nil {
			t.Fatal("retry did not reach the reference committed state", err)
		}
		verifyReclaimerReplayWritesNothing(t, f, ref)
		verifyReclaimerAckIdempotent(t, f, ref)
		verifyReclaimerResolvesRetired(t, f, ref)
	}

	// Raw original receipt identity/result and the final floor are byte-exact
	// against the reference's own real execution.
	row, found := reclaimerReceipt(t, f.e.db, ref.rq)
	if !found || !reflect.DeepEqual(row, ref.receipt) || reclaimerReceiptCount(t, f.e.db, ref.rq.ReclaimerID) != 1 {
		t.Fatal("restart raw reclaimer receipt changed or duplicated", row, ref.receipt, found)
	}
	if last, acknowledged := reclaimerFloor(t, f.e.db, ref.rq.ReclaimerID); last != 1 || acknowledged != 1 {
		t.Fatal("final acknowledged reclaimer floor", last, acknowledged)
	}
	if err := reclaimerMatches("settled", reclaimerChangedTables(ref.initial, ref.acked), ref.acked, payload.Initial, reclaimerCheckpoint(t, f)); err != nil {
		t.Fatal("settled ACKed state differs from the reference", err)
	}
	if f.e.db.Stats().InUse != 0 {
		t.Fatal("restart physical custody leaked")
	}
}

// runReclaimerCrashTrace discovers one realistic boundary per executed
// statement in the SAME fixture, for each tested operation. It is an in-process
// trace cell, not a child crash matrix.
func runReclaimerCrashTrace(t *testing.T) (resolve, ack []string) {
	t.Helper()
	f := reclaimerCrashFixtureOn(t)
	e, path := f.e, publisherFixturePath(t, f.e)
	ids := map[string]string{}
	for _, entry := range f.w.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	witness := crashWitness{Case: crashCase{Domain: reclaimerCrashDomain, Operation: "resolve", Boundary: "trace"}}
	trace := func(run func() error) []string {
		seen := map[string]bool{}
		armed := false
		installCrashDriverAt(t, e, path, &witness, reclaimerCrashClock, func(query string) {
			if !armed {
				return
			}
			if id, ok := ids[strings.TrimSpace(query)]; ok && !seen[id] {
				seen[id] = true
			}
		})
		armed = true
		if err := run(); err != nil {
			t.Fatal(err)
		}
		armed = false
		phases := make([]string, 0, len(seen))
		for id := range seen {
			phases = append(phases, "native_"+id)
		}
		sort.Strings(phases)
		return phases
	}
	resolve = trace(func() error { _, _, err := runReclaimer(t, f, f.reclaim, false); return err })
	ack = trace(func() error { _, _, err := runReclaimer(t, f, nil, true); return err })
	if len(resolve) == 0 || len(ack) == 0 {
		t.Fatal("trace found no executed reclaimer statements")
	}
	return resolve, ack
}

func TestCrashReplayIndependentReclaimer(t *testing.T) {
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	ref := newReclaimerCrashReference(t)
	resolvePhases, ackPhases := runReclaimerCrashTrace(t)
	resolvePhases = append([]string{"before_begin", "after_begin"}, resolvePhases...)
	resolvePhases = append(resolvePhases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range resolvePhases {
		t.Run("resolve/"+phase, func(t *testing.T) {
			verifyReclaimerCrash(t, ref, runCrashChild(t, crashCase{reclaimerCrashDomain, "resolve", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("resolve/uncertain_return/"+fault, func(t *testing.T) {
			verifyReclaimerCrash(t, ref, runCrashChild(t, crashCase{reclaimerCrashDomain, "resolve", "uncertain_return", fault}))
		})
	}

	ackPhases = append([]string{"before_begin", "after_begin"}, ackPhases...)
	ackPhases = append(ackPhases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range ackPhases {
		t.Run("ack/"+phase, func(t *testing.T) {
			verifyReclaimerCrash(t, ref, runCrashChild(t, crashCase{reclaimerCrashDomain, "ack", phase, ""}))
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("ack/uncertain_return/"+fault, func(t *testing.T) {
			verifyReclaimerCrash(t, ref, runCrashChild(t, crashCase{reclaimerCrashDomain, "ack", "uncertain_return", fault}))
		})
	}

	// Receipt-miss/fresh-retry: killed before COMMIT so no receipt exists.
	t.Run("retry/before_commit", func(t *testing.T) {
		verifyReclaimerCrash(t, ref, runCrashChild(t, crashCase{reclaimerCrashDomain, "retry", "before_commit", ""}))
	})
}
