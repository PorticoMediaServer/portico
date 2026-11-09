package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
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

// TestUORootReclaimReleaseDenialReplacesBody proves a release-time identity
// denial replaces the completed body and rolls the whole prefix back. A
// draining state alone is not a denial on the 654 maintenance path; these
// changed/missing roots still are.
func TestUORootReclaimReleaseDenialReplacesBody(t *testing.T) {
	changes := map[string]func(t *testing.T, turn *commandTurn, root string){
		"account": func(t *testing.T, turn *commandTurn, root string) {
			t.Helper()
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET account_id='other-account' WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
		},
		"lifetime": func(t *testing.T, turn *commandTurn, root string) {
			t.Helper()
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET profile_incarnation='dddddddddddddddddddddddddddddddd' WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
		},
		"membership": func(t *testing.T, turn *commandTurn, root string) {
			t.Helper()
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET account_incarnation='other-membership' WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
		},
		"installation": func(t *testing.T, turn *commandTurn, root string) {
			t.Helper()
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET installation_incarnation='eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee' WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
		},
		"missing": func(t *testing.T, turn *commandTurn, root string) {
			t.Helper()
			// The cleanup already removed the only child, so the owner and root
			// rows can be removed to model a root that is no longer there.
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM user_mutation_command_owners WHERE root_id=?`, root); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM user_mutation_roots WHERE id=?`, root); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
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
			// The root identity disappears after the effect but before release.
			// The same helper the dispatcher calls must deny and the caller must
			// withdraw.
			change(t, turn, root)
			if err := uoRootReclaimRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrUORootGone) {
				t.Fatal("release denial missed", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if sequence, _, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || receipts != 1 || rows != 1 {
				t.Fatal("release denial left a prefix", sequence, receipts, rows)
			}
		})
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

// TestUORootReclaimStaleIncarnationNeedsResolve proves a stale-incarnation retry
// goes through W's one shared classifier, not a second fixed-slot classifier: it
// answers ResolveRequired joined with OwnerUnavailable, never a raw sql.ErrNoRows,
// and publishes/writes nothing.
func TestUORootReclaimStaleIncarnationNeedsResolve(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	stale := uoRootReclaimRef(root, 2, 9, 19)
	stale.identity.Incarnation = [16]byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: stale, nowMS: 300})
	if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, ErrOwnerUnavailable) || errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale incarnation not W's owner-unavailable class", err)
	}
	if out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("stale incarnation published output", out, floor)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("stale incarnation changed facts", before, after)
	}
	if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || payload != 0 || receipts != 1 || rows != 1 {
		t.Fatal("stale incarnation touched the slot or child", sequence, payload, receipts, rows)
	}
}

// TestUORootReclaimDrainingTransitionFinishes proves the SOL-USERS ruling 1
// transition rule: a root that becomes draining after prepare but before final
// release still finishes the cleanup when identity/lifetime/incarnation are
// unchanged. Draining does not cancel an already-authorized 654 maintenance
// quantum.
func TestUORootReclaimDrainingTransitionFinishes(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	// The reclaim's own save starts draining the root mid-effect. The final
	// fresh release admits it because the exact root identity is unchanged.
	wc008NativeExec(t, w, `CREATE TRIGGER uo_root_reclaim_late_drain AFTER UPDATE ON user_mutation_command_owners WHEN NEW.reclaim_command_sequence IS NOT NULL BEGIN UPDATE user_mutation_roots SET state='draining' WHERE id=NEW.root_id; END`)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if err != nil || floor != 0 || out.removedCount != 1 || out.removedSequence != 1 || out.removedBytes != childBytes || out.remainingRows != 0 || !out.noNext {
		t.Fatal("active->draining transition did not finish", out, floor, err)
	}
	if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 2 || payload < 1 || receipts != 0 || rows != 0 {
		t.Fatal("transition cleanup was not durable", sequence, payload, receipts, rows)
	}
	var state string
	if err := w.db.QueryRowContext(t.Context(), `SELECT state FROM user_mutation_roots WHERE id=?`, root).Scan(&state); err != nil || state != "draining" {
		t.Fatal("root state", state, err)
	}
	// ACK was not advanced by the cleanup itself.
	if got := uoRootReclaimScalar(t, w, `SELECT acknowledged_command_sequence FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("transition cleanup advanced the floor", got)
	}
}

// TestUORootReclaimDispatchIdentityChangeReplacesBody proves a release-time
// root identity change (not a drain) still replaces the body: the dispatcher
// returns zero output and the whole cleanup prefix is rolled back.
func TestUORootReclaimDispatchIdentityChangeReplacesBody(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	wc008NativeExec(t, w, `CREATE TRIGGER uo_root_reclaim_late_foreign AFTER UPDATE ON user_mutation_command_owners WHEN NEW.reclaim_command_sequence IS NOT NULL BEGIN UPDATE user_mutation_roots SET account_id='other-account' WHERE id=NEW.root_id; END`)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if !errors.Is(err, ErrUORootGone) || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("identity change published a body", out, floor, err)
	}
	// The identity change itself is a rolled-back fact, as is every cleanup byte.
	if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || payload != 0 || receipts != 1 || rows != 1 {
		t.Fatal("identity change left a prefix", sequence, payload, receipts, rows)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT acknowledged_command_sequence FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("identity change moved the floor", got)
	}
}

// TestUORootReclaimDispatchAckMovesFloor proves the dispatcher's inv.ack arm
// saves the floor only, never refunds, and clears the due time when no receipt
// debt remains. A retained due row after the final ACK would make the bounded
// sweeper pick the same idle root forever.
func TestUORootReclaimDispatchAckMovesFloor(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if err != nil || floor != 0 || out.removedCount != 1 || out.removedSequence != 1 || out.removedBytes != childBytes {
		t.Fatal("fresh dispatch", out, floor, err)
	}
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	out, floor, err = runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 400, ack: true})
	if err != nil || floor != 2 || out != (uoRootReclaimResult{}) {
		t.Fatal("ACK dispatch", out, floor, err)
	}
	// ACK moved only the floor; the tombstone slot, receipt debt and quota stay.
	sequence, payload, control, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root)
	if sequence != 2 || payload < 1 || control != uoRootSlotEnvelope+payload || receipts != 0 || rows != 0 || bytes != 0 {
		t.Fatal("ACK refunded or erased the tombstone", sequence, payload, control, receipts, rows, bytes)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT receipt_reclaim_due_ms IS NULL FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("idle root kept a due row after the final ACK", got)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	if out, floor, err = runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 500, ack: true}); err != nil || floor != 2 || out != (uoRootReclaimResult{}) {
		t.Fatal("repeat ACK dispatch", out, floor, err)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("repeat ACK moved facts", before, after)
	}
}

// TestUORootReclaimDrainingReplayAndAck proves a draining root still returns an
// authorized original and lets ACK move the floor. Under SOL-USERS ruling 1 the
// closed 654 path also classifies a Fresh retry as Fresh there; it is the due
// debt, not the drain, that still refuses when no eligible child remains.
func TestUORootReclaimDrainingReplayAndAck(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	want, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if err != nil || floor != 0 || want.removedCount != 1 {
		t.Fatal("fresh dispatch", want, floor, err)
	}
	wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root)

	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	got, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 350})
	if err != nil || floor != 0 || got != want {
		t.Fatal("draining replay", got, want, err)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("draining replay added effects", before, after)
	}

	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	if out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 400, ack: true}); err != nil || floor != 2 || out != (uoRootReclaimResult{}) {
		t.Fatal("draining ACK", out, floor, err)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT receipt_reclaim_due_ms IS NULL FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("draining ACK kept a due row", got)
	}

	// The drain itself no longer refuses a Fresh retry: classification admits
	// it, and the absent due debt is the named refusal.
	next := uoRootReclaimRef(root, 3, 10, 20)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	lookup, err := lookupUORootReclaim(t.Context(), turn, proof, next)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal("draining root did not classify the retry", lookup.decision, err)
	}
	if _, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 500); !errors.Is(err, ErrConflict) {
		t.Fatal("draining root without due debt did not refuse", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// uoRootReclaimDrain moves the fixture root to the draining state. ACK still
// advances a draining root's floor; the 654 reclaim maintenance path is the one
// purpose allowed to run Fresh there.
func uoRootReclaimDrain(t *testing.T, w *executor, root string) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root)
}

// TestUORootReclaimDrainingTwoCycles is the Tier A ruling control: ACK plus two
// complete reclaim cycles on a DRAINING root, with a byte-exact fixed-slot
// replay of each cycle's original (replay reads only the slot and leaves
// total_changes unchanged).
func TestUORootReclaimDrainingTwoCycles(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	uoRootReclaimDrain(t, w, root)
	for cycle := int64(0); cycle < 2; cycle++ {
		source := 1 + 2*cycle
		childBytes := uoRootReclaimChild(t, w, root, incarnation, source, 64)
		uoRootReclaimOwner(t, w, root, source, source, 1, childBytes, 100)
		ref := uoRootReclaimRef(root, source+1, byte(source+10), byte(source+20))
		turn := uoRootReclaimTurn(t, w)
		proof := uoRootReclaimProof(t, turn, identity)
		lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
		if err != nil || lookup.decision != receiptFresh {
			t.Fatal("draining fresh cycle", cycle, lookup.decision, err)
		}
		want, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300)
		if err != nil || want.removedCount != 1 || want.removedSequence != source || want.removedBytes != childBytes || want.remainingRows != 0 || want.remainingBytes != 0 || !want.noNext {
			t.Fatal("draining child refund", cycle, want, err)
		}
		uoRootReclaimCommit(t, turn)

		// Byte-exact original replay on the draining root: only the fixed slot
		// is read and no fact changes.
		turn = uoRootReclaimTurn(t, w)
		proof = uoRootReclaimProof(t, turn, identity)
		replay, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
		if err != nil || replay.decision != receiptReplay {
			t.Fatal("draining replay lookup", cycle, replay.decision, err)
		}
		var before, after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		got, err := restoreUORootReclaim(t.Context(), turn, proof, replay)
		if err != nil || got != want {
			t.Fatal("draining original replay", cycle, got, want, err)
		}
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
			t.Fatal("draining replay changed facts", cycle, before, after, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}

		// ACK moves only the floor; the tombstone slot stays retained.
		turn = uoRootReclaimTurn(t, w)
		proof = uoRootReclaimProof(t, turn, identity)
		lookup, err = lookupUORootReclaim(t.Context(), turn, proof, ref)
		if err != nil {
			t.Fatal(err)
		}
		floor, err := acknowledgeUORootReclaim(t.Context(), turn, proof, lookup, 400)
		if err != nil || floor != source+1 {
			t.Fatal("draining ACK floor", cycle, floor, err)
		}
		uoRootReclaimCommit(t, turn)
		if sequence, payload, _, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root); sequence != source+1 || payload < 1 || receipts != 0 || rows != 0 || bytes != 0 {
			t.Fatal("draining fixed slot after ACK", cycle, sequence, payload, receipts, rows, bytes)
		}
	}
}

// TestUORootReclaimDrainingUnackedSurvivor proves the exception changes only
// the root state gate: an unACKed successor still refuses cleanup on a draining
// root and survives.
func TestUORootReclaimDrainingUnackedSurvivor(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	uoRootReclaimDrain(t, w, root)
	first := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	second := uoRootReclaimChild(t, w, root, incarnation, 2, 16)
	uoRootReclaimOwner(t, w, root, 2, 1, 2, first+second, 100)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	if _, err := lookupUORootReclaim(t.Context(), turn, proof, uoRootReclaimRef(root, 3, 9, 19)); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("draining cleanup passed an unACKed successor", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, root); got != 2 {
		t.Fatal("draining refusal deleted a child", got)
	}
}

// TestUORootReclaimDrainingNoIdleDebt proves no table growth or idle debt is
// left after the last eligible child on a draining root: the due time is NULL
// and the next reclaim refuses with the named refusal.
func TestUORootReclaimDrainingNoIdleDebt(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	uoRootReclaimDrain(t, w, root)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	ref := uoRootReclaimRef(root, 2, 9, 19)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 300})
	if err != nil || floor != 0 || out.removedCount != 1 || out.remainingRows != 0 {
		t.Fatal("draining cleanup", out, floor, err)
	}
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	if out, floor, err = runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: ref, nowMS: 400, ack: true}); err != nil || floor != 2 || out != (uoRootReclaimResult{}) {
		t.Fatal("draining ACK", out, floor, err)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT receipt_reclaim_due_ms IS NULL FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("draining idle root kept a due row", got)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn = uoRootReclaimTurn(t, w)
	proof = uoRootReclaimProof(t, turn, identity)
	if out, floor, err = runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: uoRootReclaimRef(root, 3, 10, 20), nowMS: 500}); !errors.Is(err, ErrConflict) || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("draining reclaim without debt did not refuse", out, floor, err)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("refused draining reclaim changed facts", before, after)
	}
	if sequence, payload, control, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root); sequence != 2 || payload < 1 || control != uoRootSlotEnvelope+payload || receipts != 0 || rows != 0 || bytes != 0 {
		t.Fatal("draining no-debt state", sequence, payload, control, receipts, rows, bytes)
	}
}

// TestUORootReclaimDrainingCurrentDenied proves the maintenance exception does
// not weaken the current proof: a proof from a finished frame is refused before
// custody with zero output and no deletion on a draining root.
func TestUORootReclaimDrainingCurrentDenied(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	uoRootReclaimDrain(t, w, root)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	prior := uoRootReclaimTurn(t, w)
	priorProof := uoRootReclaimProof(t, prior, identity)
	if _, err := prior.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	current := uoRootReclaimTurn(t, w)
	if _, err := lookupUORootReclaim(t.Context(), current, priorProof, uoRootReclaimRef(root, 2, 9, 19)); !errors.Is(err, ErrUORootCurrentFenceUnavailable) {
		t.Fatal("stale proof reached draining custody", err)
	}
	var after int64
	if err := current.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("stale proof changed facts", before, after)
	}
	if _, err := current.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || payload != 0 || receipts != 1 || rows != 1 {
		t.Fatal("stale proof touched the draining root", sequence, payload, receipts, rows)
	}
}

// TestUORootReclaimPrefixRollbackAfterChildDelete proves a fault after the
// child delete but before the slot save rolls the whole turn back: the child
// survives, the slot stays empty and no refund/cursor is applied.
func TestUORootReclaimPrefixRollbackAfterChildDelete(t *testing.T) {
	w, _, root, identity, incarnation := uoRootReclaimFixture(t)
	uoRootReclaimDrain(t, w, root)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	// The child DELETE succeeds; the owner slot UPDATE is the next effect and
	// fails, so the prefix must not survive.
	wc008NativeExec(t, w, `CREATE TRIGGER uo_root_reclaim_fail_slot AFTER UPDATE ON user_mutation_command_owners WHEN NEW.reclaim_command_sequence IS NOT NULL BEGIN SELECT RAISE(ABORT,'injected slot failure'); END`)
	turn := uoRootReclaimTurn(t, w)
	proof := uoRootReclaimProof(t, turn, identity)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, proof, uoRootReclaimInvocation{reference: uoRootReclaimRef(root, 2, 9, 19), nowMS: 300})
	if err == nil || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("injected slot fault published a body", out, floor, err)
	}
	sequence, payload, _, receipts, rows, bytes := uoRootReclaimSlotResult(t, w, root)
	if sequence != 0 || payload != 0 || receipts != 1 || rows != 1 || bytes != childBytes {
		t.Fatal("prefix survived the slot fault", sequence, payload, receipts, rows, bytes)
	}
	var child int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM user_mutation_command_receipts WHERE root_id=? AND command_sequence=1`, root).Scan(&child); err != nil || child != childBytes {
		t.Fatal("deleted child was not restored", child, err)
	}
	if got := uoRootReclaimScalar(t, w, `SELECT acknowledged_command_sequence FROM user_mutation_command_owners WHERE root_id=?`, root); got != 1 {
		t.Fatal("refund path moved the floor", got)
	}
}

// TestUORootReclaimDrainingDomainFreshStillRefused is the negative control for
// the SOL-USERS exception: UO-01's generic domain command still treats a
// draining root as replay-only for Fresh.
func TestUORootReclaimDrainingDomainFreshStillRefused(t *testing.T) {
	w, proof, root, q := uoRootPersisted(t)
	uoRootReclaimDrain(t, w, root)
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, root)
	next := q
	next.identity.Sequence = 2
	turn, binding := uoRootSecurityTurn(t, w, proof)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, next); !errors.Is(err, ErrUORootGone) {
		t.Fatal("draining domain root admitted Fresh", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// TestUORootReclaimDueProbeIsNotAuthority proves the due-root probe is a
// candidate observation only. A due root whose current proof is absent gets no
// sweeper path, and the reclaim entries never call the unproved probe.
func TestUORootReclaimDueProbeIsNotAuthority(t *testing.T) {
	w, _, root, _, incarnation := uoRootReclaimFixture(t)
	childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
	uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, 100)
	turn := uoRootReclaimTurn(t, w)
	got, dueMS, found, err := uoRootReclaimDueRoot(t.Context(), turn, 300)
	if err != nil || !found || got != root || dueMS != 100 {
		t.Fatal("due candidate not observable", got, dueMS, found, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn = uoRootReclaimTurn(t, w)
	out, floor, err := runUORootReclaimDispatch(t.Context(), turn, nil, uoRootReclaimInvocation{reference: uoRootReclaimRef(root, 2, 9, 19), nowMS: 300})
	if !errors.Is(err, ErrUORootCurrentFenceUnavailable) || out != (uoRootReclaimResult{}) || floor != 0 {
		t.Fatal("due probe supplied sweeper authority", out, floor, err)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("unauthenticated due root changed facts", before, after)
	}
	if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || payload != 0 || receipts != 1 || rows != 1 {
		t.Fatal("unauthenticated due root was cleaned", sequence, payload, receipts, rows)
	}

	// Structural: the dispatch/reclaim entry points do not consume the probe as
	// authority or as a way to select a root.
	f, err := parser.ParseFile(token.NewFileSet(), "uo_root_reclaim.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]bool{"lookupUORootReclaim": true, "reclaimUORootReceipt": true, "runUORootReclaimDispatch": true}
	seen := map[string]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !checked[fn.Name.Name] {
			continue
		}
		seen[fn.Name.Name] = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "uoRootReclaimDueRoot" {
				t.Fatalf("%s consumes the unproved due probe", fn.Name.Name)
			}
			return true
		})
	}
	for name := range checked {
		if !seen[name] {
			t.Fatal("missing reclaim entry", name)
		}
	}
}

// TestUORootReclaimDueRefusal proves a cleanup needs a real, already-due owner
// debt: a future due and a NULL due both refuse without deleting anything.
func TestUORootReclaimDueRefusal(t *testing.T) {
	for name, due := range map[string]int64{"future": 500, "absent": -1} {
		t.Run(name, func(t *testing.T) {
			w, _, root, identity, incarnation := uoRootReclaimFixture(t)
			childBytes := uoRootReclaimChild(t, w, root, incarnation, 1, 16)
			uoRootReclaimOwner(t, w, root, 1, 1, 1, childBytes, due)
			ref := uoRootReclaimRef(root, 2, 9, 19)
			turn := uoRootReclaimTurn(t, w)
			proof := uoRootReclaimProof(t, turn, identity)
			lookup, err := lookupUORootReclaim(t.Context(), turn, proof, ref)
			if err != nil || lookup.decision != receiptFresh {
				t.Fatal("fresh lookup", lookup.decision, err)
			}
			if _, err := reclaimUORootReceipt(t.Context(), turn, proof, lookup, 300); !errors.Is(err, ErrConflict) {
				t.Fatal("undue cleanup ran", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if sequence, payload, _, receipts, rows, _ := uoRootReclaimSlotResult(t, w, root); sequence != 0 || payload != 0 || receipts != 1 || rows != 1 {
				t.Fatal("refused cleanup left a prefix", sequence, payload, receipts, rows)
			}
		})
	}
}

// uoRootAckSecurityTurn mirrors uoRootSecurityTurn with the ACK statements
// registered. acknowledgeUORootOperation is purpose-generic; UO-03 will be its
// first real NORMAL caller, so this behavioural control drives it through UO-01's
// FULL consumer while no us_current_fence mint exists.
func uoRootAckSecurityTurn(t *testing.T, w *executor, proof *usOwnerAuthorityProof) (*commandTurn, *uoRootBinding) {
	t.Helper()
	recipe := testRecipe()
	// The ACK control re-validates the original before moving the floor, so this
	// frame needs room for the US-01 current checks plus the receipt reads.
	recipe.limit = Work{20000, 2_000_000, 20000, 8 << 20, 24 * time.Millisecond}
	tx, err := w.begin(t.Context(), USMemberLibraries, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	defs := append(append(usAuthorityDefinitions(), uoRootDefinitions()...), uoRootAckDefinitions()...)
	for i := range defs {
		if len(defs[i].Cohorts) == 0 {
			var args []any
			for _, p := range defs[i].Params {
				switch p.Type {
				case stmt.Text:
					args = append(args, "unit")
				case stmt.Blob:
					args = append(args, []byte{1})
				default:
					args = append(args, int64(1))
				}
			}
			defs[i].Cohorts = []stmt.Cohort{{Name: "unqualified-native-control", Bindings: args}}
		}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	budget := usOwnerAuthorityBudget{memory: 4 << 20, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	stamp, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), budget)
	if err != nil || stamp == nil {
		t.Fatal("current US owner stamp", err)
	}
	binding, err := bindUORoot(turn, stamp, nil)
	if err != nil {
		t.Fatal(err)
	}
	return turn, &binding
}

// TestUORootAcknowledgeOperationMovesFloorOnly proves the shared non-reclaim ACK
// body validates the retained original, moves only the historical floor/due/
// revision, never refunds, and treats a repeat ACK as a retired no-op.
func TestUORootAcknowledgeOperationMovesFloorOnly(t *testing.T) {
	w, proof, root, q := uoRootPersisted(t)
	turn, binding := uoRootAckSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || lookup.decision != receiptReplay {
		t.Fatal("replay lookup", lookup.decision, err)
	}
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok {
		t.Fatal("turn has no deadline")
	}
	ackProof := &uoRootCurrentProof{turn: turn, purpose: USMemberLibraries, lifetime: t.Context(), deadline: deadline, identity: binding.identity}
	if _, err := acknowledgeUORootOperation(t.Context(), turn, ackProof, lookup, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero clock moved the floor", err)
	}
	floor, err := acknowledgeUORootOperation(t.Context(), turn, ackProof, lookup, 400)
	if err != nil || floor != 1 {
		t.Fatal("ACK floor", floor, err)
	}
	var receipts, rows, bytes, due int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),receipt_rows,receipt_variable_bytes,coalesce(receipt_reclaim_due_ms,-1) FROM user_mutation_command_owners WHERE root_id=?`, root, root).Scan(&receipts, &rows, &bytes, &due); err != nil {
		t.Fatal(err)
	}
	// ACK verifies the original but refunds nothing and leaves a due candidate.
	if receipts != 1 || rows != 1 || bytes <= 0 || due != 400 {
		t.Fatal("ACK refunded or lost the original", receipts, rows, bytes, due)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	before := uoRootReclaimScalar(t, w, `SELECT total_changes()`)
	turn, binding = uoRootAckSecurityTurn(t, w, proof)
	retired, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || retired.decision != receiptRetired {
		t.Fatal("retired lookup", retired.decision, err)
	}
	deadline, ok = turn.transaction.ctx.Deadline()
	if !ok {
		t.Fatal("retry turn has no deadline")
	}
	ackProof = &uoRootCurrentProof{turn: turn, purpose: USMemberLibraries, lifetime: t.Context(), deadline: deadline, identity: binding.identity}
	if floor, err := acknowledgeUORootOperation(t.Context(), turn, ackProof, retired, 500); err != nil || floor != 1 {
		t.Fatal("repeat ACK floor", floor, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if after := uoRootReclaimScalar(t, w, `SELECT total_changes()`); before != after {
		t.Fatal("repeat ACK moved facts", before, after)
	}
}
