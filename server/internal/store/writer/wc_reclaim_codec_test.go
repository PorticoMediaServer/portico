package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
)

func wcReclaimCodecFixture() (CommandIdentity, RequestDigest, ReclaimValue) {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 5, OperationID: [16]byte{2}}, RequestDigest{3},
		ReclaimValue{CommandKind: wcReclaimKind, ResultVersion: 1, CapturedAcknowledgedSequence: 4,
			RemovedSequence: wcPresent(1), RemovedCount: 1, RemovedVariableBytes: 137, ResultAfterSequence: 1, AppliedMS: 123, ResultCount: 1}
}
func wcReclaimCodecHash(t *testing.T, id CommandIdentity, request RequestDigest, v ReclaimValue) string {
	t.Helper()
	if err := preflightReclaimResult(id, request, v); err != nil {
		t.Fatal(err)
	}
	e := requestEncoder{h: sha256.New()}
	if err := writeReclaimValue(&e, id, request, v); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(e.h.Sum(nil))
}
func TestWCReclaimCodecGoldenAndBindings(t *testing.T) {
	id, request, v := wcReclaimCodecFixture()
	// Independent v1 wire-vector, including domain and NULL/present witness.
	const removed = "ab6b4e6be37657fa57824e4bcdfc454696dec77391a6987728c4620c134bd46f"
	if got := wcReclaimCodecHash(t, id, request, v); got != removed {
		t.Fatal("removed wire", got)
	}
	zero := v
	zero.RemovedSequence, zero.RemovedCount, zero.RemovedVariableBytes = sql.NullInt64{}, 0, 0
	zero.ResultAfterSequence, zero.ResultNoNext = 0, true
	const empty = "8ed1b2fe4681916e1231f4bef59b38814c13c797f66ba43174f2027b4477122a"
	if got := wcReclaimCodecHash(t, id, request, zero); got != empty {
		t.Fatal("zero-removal wire", got)
	}
	for _, change := range []string{"operation", "incarnation", "request", "native_bytes", "applied_clock", "no_next"} {
		i, r, value := id, request, v
		switch change {
		case "operation":
			i.OperationID[0]++
		case "incarnation":
			i.Incarnation[0]++
		case "request":
			r[0]++
		case "native_bytes":
			value.RemovedVariableBytes++
		case "applied_clock":
			value.AppliedMS++
		case "no_next":
			value.ResultNoNext = true
		}
		if got := wcReclaimCodecHash(t, i, r, value); got == removed {
			t.Fatal("unbound", change)
		}
	}
}
func TestWCReclaimCodecRejectsMalformedResults(t *testing.T) {
	id, request, base := wcReclaimCodecFixture()
	for _, name := range []string{"kind", "version", "scalar_count", "count_two", "null_removed", "zero_without_no_next", "zero_with_debit", "outside_ack", "cursor", "negative_clock", "zero_debit"} {
		t.Run(name, func(t *testing.T) {
			v := base
			switch name {
			case "kind":
				v.CommandKind = "capture_report"
			case "version":
				v.ResultVersion++
			case "scalar_count":
				v.ResultCount = 0
			case "count_two":
				v.RemovedCount = 2
			case "null_removed":
				v.RemovedSequence.Valid = false
			case "zero_without_no_next":
				v.RemovedSequence, v.RemovedCount, v.RemovedVariableBytes, v.ResultAfterSequence = sql.NullInt64{}, 0, 0, 0
			case "zero_with_debit":
				v.RemovedSequence, v.RemovedCount, v.ResultAfterSequence, v.ResultNoNext = sql.NullInt64{}, 0, 0, true
			case "outside_ack":
				v.RemovedSequence, v.ResultAfterSequence = wcPresent(5), 5
			case "cursor":
				v.ResultAfterSequence++
			case "negative_clock":
				v.AppliedMS = -1
			case "zero_debit":
				v.RemovedVariableBytes = 0
			}
			if err := preflightReclaimResult(id, request, v); err == nil {
				t.Fatal("malformed result accepted")
			}
		})
	}
	for _, bad := range []CommandIdentity{{Incarnation: id.Incarnation, Sequence: 5}, {Sequence: 5, OperationID: id.OperationID}, {Incarnation: id.Incarnation, Sequence: 6, OperationID: id.OperationID}} {
		if preflightReclaimResult(bad, request, base) == nil {
			t.Fatal("unbound or nonadjacent identity")
		}
	}
}

func TestWCReclaimRequestFreezesNativeOldContext(t *testing.T) {
	raw, owner := wcReclaimRawFixture()
	owner.acknowledgedSequence = owner.lastSequence
	slot, version, err := decodeWCReclaimSlotShape(raw, owner)
	if err != nil {
		t.Fatal(err)
	}
	q := wcReclaimRequest{Scope: wcProgressScope{playbackID: owner.playbackID, incarnation: owner.incarnation, profileKey: "viewer", subjectID: 11, accountID: "account", profileID: "profile", memberBirth: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", profileBirth: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", libraryID: 7}, Snapshot: wcReclaimSnapshot{owner: owner, slot: slot, resultVersion: version, controlBytes: 208}}
	id := CommandIdentity{Incarnation: owner.incarnation, Sequence: 6, OperationID: [16]byte{7}}
	if err := preflightWCReclaimRequest(id, q); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unacked_old", "no_due", "wrong_scope", "invalid_debt", "unversioned_slot", "missing_control"} {
		bad := q
		switch name {
		case "unacked_old":
			bad.Snapshot.owner.acknowledgedSequence--
		case "no_due":
			bad.Snapshot.owner.due = false
		case "wrong_scope":
			bad.Scope.incarnation[0]++
		case "invalid_debt":
			bad.Snapshot.owner.variableBytes = 0
		case "unversioned_slot":
			bad.Snapshot.resultVersion = 0
		case "missing_control":
			bad.Snapshot.controlBytes = 0
		}
		if preflightWCReclaimRequest(id, bad) == nil {
			t.Fatal("invalid native request accepted", name)
		}
	}
	// No guessed purpose/default, including in request preparation.
	if _, err := wcReclaimRequestDigest(ProgressAccept, id, q); err == nil {
		t.Fatal("request aliased source purpose")
	}
}
