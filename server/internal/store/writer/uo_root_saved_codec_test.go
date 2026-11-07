package writer

import (
	"crypto/sha256"
	"reflect"
	"testing"
)

func uoSavedCodecResult(root string) uoRootSavedResult {
	return uoRootSavedResult{root: root, resource: "resourceabcdefghijklmn", kind: EditSavedResourceMembers, revision: 2, generation: 3, changed: true, entriesPresent: true,
		entries: []uoRootSavedEntry{{1, "public-item-one", "entryabcdefghijklmnopq"}, {4, "missing-item", ""}, {2, "old-entry", "oldentryabcdefghijklmn"}}, undoToken: "undoabcdefghijklmnopqr", undoUntilMS: 30100, appliedMS: 100}
}
func TestUORootSavedCanonicalBindingsAndMalformed(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoSavedCodecResult("abcdefghijklmnopqrstuv")
	p, d, err := encodeUORootCanonical(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	if sole, err := canonicalResultDigest(i, q, v); err != nil || sole != d {
		t.Fatal("bypassed sole W codec", sole, d, err)
	}
	got, err := decodeUORootSaved(i, q, v.root, v.kind, p, d)
	if err != nil || !reflect.DeepEqual(got, v) {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*CommandIdentity, *RequestDigest, *string, *CommandID){
		func(i *CommandIdentity, _ *RequestDigest, _ *string, _ *CommandID) { i.Incarnation[0]++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string, _ *CommandID) { i.Sequence++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string, _ *CommandID) { i.OperationID[0]++ },
		func(_ *CommandIdentity, q *RequestDigest, _ *string, _ *CommandID) { q[0]++ },
		func(_ *CommandIdentity, _ *RequestDigest, r *string, _ *CommandID) { *r = "zyxwvutsrqponmlkjihgfe" },
		func(_ *CommandIdentity, _ *RequestDigest, _ *string, k *CommandID) { *k = PatchSavedResource },
	} {
		ii, qq, root, kind := i, q, v.root, v.kind
		mutate(&ii, &qq, &root, &kind)
		if _, err := decodeUORootSaved(ii, qq, root, kind, p, d); err == nil {
			t.Fatal("foreign original decoded")
		}
	}
	// A valid checksum does not make an incomplete/trailing payload canonical.
	for _, bad := range [][]byte{p[:len(p)-1], append(append([]byte(nil), p...), 0)} {
		if _, err := decodeUORootSaved(i, q, v.root, v.kind, bad, ResultDigest(sha256.Sum256(bad))); err == nil {
			t.Fatal("malformed payload decoded")
		}
	}
	v.entries = make([]uoRootSavedEntry, 200)
	for n := range v.entries {
		v.entries[n] = uoRootSavedEntry{4, "missing-item", ""}
	}
	if _, _, err := encodeUORootCanonical(i, q, v); err != nil {
		t.Fatal("legal public batch refused", err)
	}
	v.entries = append(v.entries, v.entries[0])
	if _, _, err := encodeUORootCanonical(i, q, v); err == nil {
		t.Fatal("over-limit outcome batch accepted")
	}
	v = uoSavedCodecResult(v.root)
	v.entries[0].status = 9
	if _, _, err := encodeUORootCanonical(i, q, v); err == nil {
		t.Fatal("unknown outcome accepted")
	}
	for _, mutate := range []func(*uoRootSavedResult){
		func(v *uoRootSavedResult) { v.entriesPresent = false; v.entries = nil },
		func(v *uoRootSavedResult) { v.changed = false },
		func(v *uoRootSavedResult) { v.generation = 0 },
		func(v *uoRootSavedResult) { v.undoUntilMS = v.appliedMS },
	} {
		v = uoSavedCodecResult(v.root)
		mutate(&v)
		if _, _, err := encodeUORootCanonical(i, q, v); err == nil {
			t.Fatal("incomplete result accepted", v)
		}
	}
	for _, kind := range []CommandID{CreateSavedResource, PatchSavedResource, DeleteSavedResource, ReorderSavedResource, SetSavedResourceSharing} {
		v = uoRootSavedResult{root: v.root, resource: "resourceabcdefghijklmn", kind: kind, revision: 1, generation: 1, changed: true, deleted: kind == DeleteSavedResource, appliedMS: 100}
		p, d, err = encodeUORootCanonical(i, q, v)
		if err != nil {
			t.Fatal(kind, err)
		}
		if got, err := decodeUORootSaved(i, q, v.root, kind, p, d); err != nil || !reflect.DeepEqual(got, v) {
			t.Fatal(kind, got, err)
		}
	}
}

func TestUORootSavedNativeReplayHeaderCountAndACK(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: EditSavedResourceMembers, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	v := uoSavedCodecResult(root)
	turn := wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootSaved(t.Context(), turn, lookup, v); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	turn = wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err = lookupUORootCommand(t.Context(), turn, q)
	if err != nil || lookup.resultCount != 3 || lookup.decision != receiptReplay {
		t.Fatal(lookup, err)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := restoreUORootSaved(t.Context(), turn, lookup)
	if err != nil || !reflect.DeepEqual(got, v) {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay wrote source", before, after, err)
	}
	if floor, err := acknowledgeUORootSaved(t.Context(), turn, lookup, 200); err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	uoFlagsCommit(t, turn)
	var rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil || rows != 1 || bytes <= 118 {
		t.Fatal("ACK refunded original", rows, bytes, err)
	}
}

func TestUORootSavedNativeCorruptHeaderCountRefuses(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: EditSavedResourceMembers, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn := wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootSaved(t.Context(), turn, lookup, uoSavedCodecResult(root)); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE user_mutation_command_receipts SET result_count=2 WHERE root_id=?`, root)
	turn = wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err = lookupUORootCommand(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoreUORootSaved(t.Context(), turn, lookup); err == nil {
		t.Fatal("incomplete header accepted")
	}
	if _, err := acknowledgeUORootSaved(t.Context(), turn, lookup, 200); err == nil {
		t.Fatal("corrupt original ACKed")
	}
}

func TestUORootSavedCannotBorrowNormalPurpose(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: SetSavedResourceSharing, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn := wc008NativeTurn(t, w, SetPersonalFlags, uoRootAllDefinitions())
	if _, err := lookupUORootCommand(t.Context(), turn, q); err == nil {
		t.Fatal("FULL original borrowed a NORMAL purpose")
	}
}
