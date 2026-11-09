package writer

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoRootReclaimDefinitionsAll() []stmt.Definition {
	return append(append(uoRootDefinitions(), uoRootAckDefinitions()...), uoRootReclaimDefinitions()...)
}

// uoRootReclaimFixture seeds one native root/owner on the canonical schema. The
// identity below is the exact root scope the private current proof must bind;
// no test role, cached stamp or descriptor fills it.
func uoRootReclaimFixture(t *testing.T) (w *executor, path, root string, identity uoRootIdentity, incarnation [16]byte) {
	t.Helper()
	w, path, _ = testExecutor(t)
	identity = uoRootIdentity{account: "account", memberIncarnation: "membership", profile: "profile", profileLifetime: strings.Repeat("p", 32), installation: strings.Repeat("i", 32), family: "family"}
	incarnation = [16]byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	root = "abcdefghijklmnopqrstuv"
	uoRootReclaimRoot(t, w, root, identity, incarnation, 1)
	return w, path, root, identity, incarnation
}

func uoRootReclaimRoot(t *testing.T, w *executor, root string, identity uoRootIdentity, incarnation [16]byte, registration int64) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,'active')`,
		root, identity.installation, identity.account, identity.profile, identity.memberIncarnation, identity.profileLifetime, identity.family, registration)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, root, incarnation[:])
}

func uoRootReclaimTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, RootReceiptReclaim, uoRootReclaimDefinitionsAll())
}

func uoRootReclaimProof(t *testing.T, turn *commandTurn, identity uoRootIdentity) *uoRootCurrentProof {
	t.Helper()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok {
		t.Fatal("turn has no deadline")
	}
	return &uoRootCurrentProof{turn: turn, purpose: RootReceiptReclaim, lifetime: t.Context(), deadline: deadline, identity: identity}
}

func uoRootReclaimRef(root string, sequence int64, operation, request byte) uoRootReference {
	var inc [16]byte
	inc[0] = 1
	var op [16]byte
	op[0] = operation
	return uoRootReference{root: root, kind: RootReceiptReclaim,
		identity: CommandIdentity{Incarnation: inc, Sequence: sequence, OperationID: op},
		request:  RequestDigest{request}}
}

// uoRootReclaimChild inserts one ACKed-eligible child original directly on the
// native graph. It returns that row's exact variable bytes (independent of the
// reclaim arithmetic): root 22 + incarnation 16 + operation 16 + request 32 +
// result digest 32 + payload.
func uoRootReclaimChild(t *testing.T, w *executor, root string, incarnation [16]byte, sequence int64, payload int) int64 {
	t.Helper()
	op := bytes.Repeat([]byte{byte(sequence)}, 16)
	request := bytes.Repeat([]byte{byte(sequence)}, 32)
	digest := bytes.Repeat([]byte{7}, 32)
	body := bytes.Repeat([]byte{'x'}, payload)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,1,0,?,?,100)`,
		root, incarnation[:], sequence, op, int64(SetPersonalFlags), request, digest, body)
	return int64(22 + 16 + 16 + 32 + 32 + len(body))
}

// uoRootReclaimOwner publishes the exact owner debt/floor after the fixture
// writes above. It never anticipates the reclaim's own arithmetic.
func uoRootReclaimOwner(t *testing.T, w *executor, root string, last, acknowledged, rows, bytes, due int64) {
	t.Helper()
	var dueArg any
	if due >= 0 {
		dueArg = due
	}
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET last_command_sequence=?,acknowledged_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,receipt_reclaim_due_ms=?,revision=revision+1 WHERE root_id=?`,
		last, acknowledged, rows, bytes, dueArg, root)
}

func uoRootReclaimCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func uoRootReclaimScalar(t *testing.T, w *executor, q string, args ...any) (n int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// uoRootReclaimSlotResult reads the fixed cleanup slot with plain SQL. It is an
// independent oracle, never the reclaim's own encode/decode.
func uoRootReclaimSlotResult(t *testing.T, w *executor, root string) (sequence, payload, control, receipts, rows, bytes int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT coalesce(reclaim_command_sequence,0),coalesce(octet_length(reclaim_result_payload),0),control_variable_bytes,receipt_rows,receipt_variable_bytes,(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?) FROM user_mutation_command_owners WHERE root_id=?`,
		root, root).Scan(&sequence, &payload, &control, &rows, &bytes, &receipts); err != nil {
		t.Fatal(err)
	}
	return sequence, payload, control, receipts, rows, bytes
}

// TestUORootReclaimTwoCycles proves the complete bounded cycle twice on one
// root: ACKed child refund, fixed-slot save, ACK floor movement, then a second
// cycle that replaces the tombstone without creating any child receipt.
func TestUORootReclaimTwoCycles(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	for cycle := int64(0); cycle < 2; cycle++ {
		source := 1 + 2*cycle
		childBytes := uoRootReclaimChild(t, w, root, incarnation, source, 64)
		uoRootReclaimOwner(t, w, root, source, source, 1, childBytes, 100)
		cleanup := uoRootReclaimRef(root, source+1, byte(source+10), byte(source+20))
		turn := uoRootReclaimTurn(t, w)
		proof := uoRootReclaimProof(t, turn, identity)
		lookup, err := lookupUORootReclaim(t.Context(), turn, proof, cleanup)
		if err != nil || lookup.decision != receiptFresh {
			t.Fatal("fresh cycle", lookup.decision, err)
		}
		out, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300)
		if err != nil || out.removedCount != 1 || out.removedSequence != source || out.removedBytes != childBytes || out.remainingRows != 0 || out.remainingBytes != 0 || !out.noNext || out.floor != source {
			t.Fatal("bounded child refund", out, err)
		}
		uoRootReclaimCommit(t, turn)
		sequence, payload, control, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root)
		if sequence != source+1 || payload < 1 || payload > uoRootReclaimPayloadCap || control != uoRootSlotEnvelope+payload || receipts != 0 || rows != 0 || bytes != 0 {
			t.Fatal("fixed slot after reclaim", sequence, payload, control, receipts, rows, bytes)
		}
		// The reclaim's own original is the only stored result and it is in the
		// owner slot, never a child receipt.
		if got := uoRootReclaimScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE command_kind=?`, int64(RootReceiptReclaim)); got != 0 {
			t.Fatal("recursive cleanup receipt", got)
		}
		turn = uoRootReclaimTurn(t, w)
		proof = uoRootReclaimProof(t, turn, identity)
		lookup, err = lookupUORootReclaim(t.Context(), turn, proof, cleanup)
		if err != nil || lookup.decision != receiptReplay {
			t.Fatal("reclaim replay", lookup.decision, err)
		}
		floor, err := acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 400)
		if err != nil || floor != source+1 {
			t.Fatal("ACK floor", floor, err)
		}
		uoRootReclaimCommit(t, turn)
		if got := uoRootReclaimScalar(t, w, `SELECT acknowledged_command_sequence FROM user_mutation_command_owners WHERE root_id=?`, root); got != source+1 {
			t.Fatal("ACK floor not durable", got)
		}
		// ACK moves only the floor: the tombstone slot and its control bytes stay.
		sequence, payload, _, _, _, _ = uoRootReclaimSlotResult(t, w, root)
		if sequence != source+1 || payload < 1 {
			t.Fatal("ACK refunded or erased the tombstone", sequence, payload)
		}
	}
}

// TestUORootReclaimFixedSlotOriginalReplay proves the retained original is
// returned verbatim with zero effects, and that a newer slot is never decoded
// as an older command.
func TestUORootReclaimFixedSlotOriginalReplay(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 32)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	cleanup := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	lookup, err := lookupUORootReclaim(t.Context(), turn, proof, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	want, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300)
	if err != nil {
		t.Fatal(err)
	}
	uoRootReclaimCommit(t, turn)

	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	replay, err := lookupUORootReclaim(t.Context(), turn, proof, cleanup)
	if err != nil || replay.decision != receiptReplay {
		t.Fatal(replay.decision, err)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := restoreUORootReclaim(t.Context(), turn, proof, replay)
	if err != nil || got != want {
		t.Fatal(got, want, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("original replay changed facts", before, after, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A different request identity can never read the same original.
	wrong := cleanup
	wrong.request[0]++
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	if _, err := lookupUORootReclaim(t.Context(), turn, proof, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("different request not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// Once ACKed, the original is retired; the newer tombstone cannot answer it.
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	lookup, err = lookupUORootReclaim(t.Context(), turn, proof, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 400); err != nil {
		t.Fatal(err)
	}
	uoRootReclaimCommit(t, turn)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	retired, err := lookupUORootReclaim(t.Context(), turn, proof, cleanup)
	if err != nil || retired.decision != receiptRetired {
		t.Fatal(retired.decision, err)
	}
	if _, err := restoreUORootReclaim(t.Context(), turn, proof, retired); !errors.Is(err, ErrInvalid) {
		t.Fatal("retired original restored", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// TestUORootReclaimUnackedSuccessorRefused proves no cleanup runs while a
// successor is unacknowledged: ACK, not reclaim, owns the floor.
func TestUORootReclaimUnackedSuccessorRefused(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	first := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	second := uoRootReclaimChild(t, w, root, incarnation, 2, 16)
	uoRootReclaimOwner(t, w, root, 2, 1, 2, first+second, 100)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	if _, err := lookupUORootReclaim(t.Context(), turn, proof, uoRootReclaimRef(root, 3, 9, 19)); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("cleanup passed an unACKed successor", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root); got != 2 {
		t.Fatal("refused cleanup deleted a child", got)
	}
}

// TestUORootReclaimBoundedQuantaLiveRowsSurvive proves each quantum removes
// exactly one eligible child over several quanta, the refund equals that row's
// actual bytes and rows sorting after the victim survive untouched.
func TestUORootReclaimBoundedQuantaLiveRowsSurvive(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	bytesBySequence := map[int64]int64{}
	var total int64
	for sequence := int64(1); sequence <= 3; sequence++ {
		bytesBySequence[sequence] = uoRootReclaimChild(t, w, root, incarnation, sequence, 24)
		total += bytesBySequence[sequence]
	}
	uoRootReclaimOwner(t, w, root, 3, 3, 3, total, 100)
	remaining := int64(3)
	remainingBytes := total
	for quantum := int64(0); quantum < 3; quantum++ {
		victim := quantum + 1
		ref := uoRootReclaimRef(root, 4+quantum, byte(10+quantum), byte(20+quantum))
		turn := uoRootReclaimTurn(t, w)
		proof := uoRootReclaimProof(t, turn, identity)
		lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
		if err != nil || lookup.decision != receiptFresh {
			t.Fatal("quantum lookup", lookup.decision, err)
		}
		out, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300)
		if err != nil || out.removedCount != 1 || out.removedSequence != victim || out.removedBytes != bytesBySequence[victim] {
			t.Fatal("one eligible child per quantum", quantum, out, err)
		}
		remaining--
		remainingBytes -= bytesBySequence[victim]
		if out.remainingRows != remaining || out.remainingBytes != remainingBytes || out.noNext != (remaining == 0) {
			t.Fatal("bounded debt", quantum, out, remaining, remainingBytes)
		}
		uoRootReclaimCommit(t, turn)
		// Every row beyond the victim is still present.
		if got := uoRootReclaimScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=? AND command_sequence>?`, root, victim); got != remaining {
			t.Fatal("surviving rows after quantum", quantum, got, remaining)
		}
		if got := uoRootReclaimScalar(t, w, `SELECT receipt_rows FROM user_mutation_command_owners WHERE root_id=?`, root); got != remaining {
			t.Fatal("owner rows after quantum", quantum, got)
		}
		turn = uoRootReclaimTurn(t, w)
		proof = uoRootReclaimProof(t, turn, identity)
		lookup, err = lookupUORootReclaim(t.Context(), turn, proof, ref)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 400); err != nil {
			t.Fatal(err)
		}
		uoRootReclaimCommit(t, turn)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root); got != 0 {
		t.Fatal("eligible rows remained", got)
	}
}

// TestUORootReclaimBoundedDueNoNext proves the due candidate probe is one keyed
// index row with no scan or temporary sort, and that clearing the last due root
// yields an explicit no-next rather than an idle receipt.
func TestUORootReclaimBoundedDueNoNext(t *testing.T) {
	w, _, root, _, incarnation := uoRootReclaimFixture(t)
	other := "zyxwvutsrqponmlkjihgfe"
	uoRootReclaimRoot(t, w, other, uoRootIdentity{account: "account2", memberIncarnation: "membership2", profile: "profile2", profileLifetime: strings.Repeat("q", 32), installation: strings.Repeat("j", 32), family: "family2"}, incarnation, 2)
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_reclaim_due_ms=100 WHERE root_id=?`, root)
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_reclaim_due_ms=200 WHERE root_id=?`, other)
	turn := uoRootReclaimTurn(t, w)
	got, dueMS, found, err := uoRootReclaimDueRoot(t.Context(), turn, 300)
	if err != nil || !found || got != root || dueMS != 100 {
		t.Fatal("first due candidate", got, dueMS, found, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_reclaim_due_ms=NULL WHERE root_id=?`, root)
	turn = uoRootReclaimTurn(t, w)
	got, dueMS, found, err = uoRootReclaimDueRoot(t.Context(), turn, 300)
	if err != nil || !found || got != other || dueMS != 200 {
		t.Fatal("second due candidate", got, dueMS, found, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_reclaim_due_ms=NULL WHERE root_id=?`, other)
	turn = uoRootReclaimTurn(t, w)
	if _, _, found, err = uoRootReclaimDueRoot(t.Context(), turn, 300); err != nil || found {
		t.Fatal("no-next not explicit", found, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A due time in the future is not a candidate yet.
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET receipt_reclaim_due_ms=500 WHERE root_id=?`, root)
	turn = uoRootReclaimTurn(t, w)
	if _, _, found, err = uoRootReclaimDueRoot(t.Context(), turn, 300); err != nil || found {
		t.Fatal("future due selected", found, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// The probe is an index seek, not a table scan or temporary sort.
	defs := uoRootReclaimDefinitions()
	uoRootReclaimDueStatement := defs[len(defs)-1]
	if uoRootReclaimDueStatement.ID != uoRootReclaimDue || len(uoRootReclaimDueStatement.Plan.Indexes) != 1 || uoRootReclaimDueStatement.Plan.Indexes[0] != "user_mutation_command_reclaim_due" {
		t.Fatal("due probe lost its partial index", uoRootReclaimDueStatement.Plan)
	}
}

// TestUORootReclaimRetainedTombstone proves the fixed slot and its quota stay
// physically retained after ACK, and a repeat ACK is a no-op floor.
func TestUORootReclaimRetainedTombstone(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 8)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300); err != nil {
		t.Fatal(err)
	}
	uoRootReclaimCommit(t, turn)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	lookup, err = lookupUORootReclaim(t.Context(), turn, proof, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 400); err != nil {
		t.Fatal(err)
	}
	uoRootReclaimCommit(t, turn)
	sequence, payload, control, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root)
	if sequence != 2 || payload < 1 || control != uoRootSlotEnvelope+payload || receipts != 0 || rows != 0 || bytes != 0 {
		t.Fatal("tombstone not retained", sequence, payload, control, receipts, rows, bytes)
	}
	var before int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	lookup, err = lookupUORootReclaim(t.Context(), turn, proof, ref)
	if err != nil || lookup.decision != receiptRetired {
		t.Fatal(lookup.decision, err)
	}
	if floor, err := acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 500); err != nil || floor != 2 {
		t.Fatal("repeat ACK floor", floor, err)
	}
	uoRootReclaimCommit(t, turn)
	var after int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("repeat ACK moved facts", before, after, err)
	}
}

// TestUORootReclaimCurrentProofSameTurn proves the private proof binds this
// exact frame, purpose and root identity. A caller string, prior frame, other
// purpose or foreign root can never reach retained custody.
func TestUORootReclaimCurrentProofSameTurn(t *testing.T) {
	w, _, root, identity, _ := uoRootReclaimFixture(t)
	ref := uoRootReclaimRef(root, 1, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	if _, err := lookupUORootReclaim(t.Context(), turn, nil, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("nil proof reached custody", err)
	}
	proof := uoRootReclaimProof(t, turn, identity)
	wrongPurpose := *proof
	wrongPurpose.purpose = SetPersonalFlags
	if _, err := lookupUORootReclaim(t.Context(), turn, &wrongPurpose, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("other purpose proof accepted", err)
	}
	foreign := *proof
	foreign.identity.family = "other-family"
	if _, err := lookupUORootReclaim(t.Context(), turn, &foreign, ref); !errors.Is(err, ErrUORootGone) {
		t.Fatal("foreign root identity passed", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A proof from a rolled-back frame cannot be reused on the next frame.
	prior := uoRootReclaimTurn(t, w)
	priorProof := uoRootReclaimProof(t, prior, identity)
	if _, err := prior.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	current := uoRootReclaimTurn(t, w)
	if _, err := lookupUORootReclaim(t.Context(), current, priorProof, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("prior frame proof accepted", err)
	}
	if _, err := current.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A finished frame cannot redeem its own proof either.
	finished := uoRootReclaimTurn(t, w)
	finishedProof := uoRootReclaimProof(t, finished, identity)
	uoRootReclaimCommit(t, finished)
	if _, err := lookupUORootReclaim(t.Context(), finished, finishedProof, ref); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("finished frame proof accepted", err)
	}
}

// TestUORootReclaimReleaseDenialReplacesBody proves a release-time authority
// denial replaces the completed body and rolls the whole prefix back.
func TestUORootReclaimReleaseDenialReplacesBody(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300); err != nil {
		t.Fatal(err)
	}
	// The root starts draining after the effect but before release. The same
	// helper the dispatcher calls must deny and the caller must withdraw.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	if err := uoRootReclaimRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrUORootGone) {
		t.Fatal("release denial missed", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if sequence, _, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || receipts != 1 || rows != 1 {
		t.Fatal("release denial left a prefix", sequence, receipts, rows)
	}
}

// TestUORootReclaimCancellationLeavesNoResult proves a canceled caller or frame
// never saves a cleanup original.
func TestUORootReclaimCancellationLeavesNoResult(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lookupUORootReclaim(ctx, turn, proof, ref); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lookup not canceled", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := reclaimUORootReceipt(ctx, turn, proof, lookup, 300); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled reclaim not canceled", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if sequence, _, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || receipts != 1 || rows != 1 {
		t.Fatal("canceled reclaim retained a prefix", sequence, receipts, rows)
	}
}

// TestUORootReclaimDispatchPostCommitCallerCancel proves a caller cancel that
// arrives only after the physical COMMIT returns zero output, while the
// committed original is resolved by a fresh retry without re-applying effects.
func TestUORootReclaimDispatchPostCommitCallerCancel(t *testing.T) {
	w, path, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	installFaultPool(w, &faultConnector{Connector: connector, afterNativeCommit: cancel})
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(caller, turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if !errors.Is(err, context.Canceled) || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("post-commit cancel published a result", out, floor, err)
	}
	// The COMMIT is durable: one child removed, the fixed slot retained.
	sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root)
	if sequence != 2 || payload < 1 || receipts != 0 || rows != 0 {
		t.Fatal("committed original missing", sequence, payload, receipts, rows)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	retryTurn := uoRootReclaimTurn(t, w)
	retryProof := uoRootReclaimProof(t, retryTurn, identity)
	retry, retryFloor, err := runUORootReclaimDispatch(t.Context(), retryTurn, retryProof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if err != nil || retryFloor != 0 || retry.removedCount != 1 || retry.removedSequence != 1 || retry.removedBytes != childBytes || retry.remainingRows != 0 {
		t.Fatal("retry did not resolve the original", retry, retryFloor, err)
	}
	after := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	if before != after {
		t.Fatal("retry re-applied the cleanup", before, after)
	}
}

// TestUORootReclaimDispatchUnknownCommitRequiresResolve proves an unknown
// physical COMMIT is never Applied and returns zero output.
func TestUORootReclaimDispatchUnknownCommitRequiresResolve(t *testing.T) {
	w, path, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: true})
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if !errors.Is(err, ErrResolveRequired) || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("unknown commit invented a result", out, floor, err)
	}
}

// TestUORootReclaimFinishGuards is the local structural control: every effect
// entry releases through the shared first-action finish, and the dispatcher
// releases the body BEFORE the physical terminal.
func TestUORootReclaimFinishGuards(t *testing.T) {
	for file, names := range map[string][]string{
		"uo_root_reclaim.go": {"lookupUORootReclaim", "loadUORootSlot", "restoreUORootReclaim", "probeUORootChild", "reclaimUORootReceipt", "acknowledgeUORootReclaim", "uoRootReclaimDueRoot", "runUORootReclaimDispatch"},
		"uo_root_ack.go":     {"acknowledgeUORootOperation", "acknowledgeUORootVerified"},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			for _, name := range names {
				if fn.Name.Name != name {
					continue
				}
				if fn.Body == nil || len(fn.Body.List) == 0 {
					t.Fatal(name, "has no finish")
				}
				d, ok := fn.Body.List[0].(*ast.DeferStmt)
				if !ok || !guardedFinishDefer(fn, d) {
					t.Fatal(name, "does not use shared first-action finish")
				}
				found[name] = true
			}
		}
		for _, name := range names {
			if !found[name] {
				t.Fatal("missing native effect", name)
			}
		}
	}
	// A replay is not a new body: it is the same complete original.
	if _, _, err := runUORootReclaimDispatch(t.Context(), nil, nil, uoRootReclaimInvocation{}); !errors.Is(err, ErrUORootCurrentFenceUnavailable) && !errors.Is(err, ErrInvalid) {
		t.Fatal("nil frame dispatch", err)
	}
}
