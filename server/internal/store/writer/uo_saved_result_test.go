package writer

import (
	"reflect"
	"testing"
)

func TestUOSavedOriginalNativeSourceAndReplay(t *testing.T) {
	w, root := uoRootFixture(t)
	name := "Original collection"
	q := uoSavedInput{action: uoSavedCreate, id: "resourceabcdefghijklmn", kind: "collection", name: &name, owner: "owner", authority: "local", account: "account", profile: "profile", created: "2026-11-07T00:00:00Z", security: &uoSavedTestSecurity{}, nowMS: 100}
	ref := uoRootReference{root: root, kind: CreateSavedResource, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	defs := append(uoRootAllDefinitions(), uoSavedDefinitions()...)
	turn := wc008NativeTurn(t, w, ref.kind, defs)
	lookup, err := lookupUORootCommand(t.Context(), turn, ref)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := applyUOSaved(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	original, err := uoSavedOriginalResult(root, ref.kind, q, effect, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootSaved(t.Context(), turn, lookup, original); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	// Original disclosure must not query or reconstruct a successor's state.
	wc008NativeExec(t, w, `UPDATE saved_resources SET name='Successor',revision=2 WHERE id=?`, q.id)
	turn = wc008NativeTurn(t, w, ref.kind, defs)
	lookup, err = lookupUORootCommand(t.Context(), turn, ref)
	if err != nil || lookup.decision != receiptReplay {
		t.Fatal(lookup, err)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := restoreUORootSaved(t.Context(), turn, lookup)
	if err != nil || !reflect.DeepEqual(got, original) || got.revision != 1 || got.selectionRevision != effect.selectionRevision {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay wrote source", before, after, err)
	}
	if _, err := acknowledgeUORootSaved(t.Context(), turn, lookup, 200); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
}

func TestUOSavedOriginalPreservesAcceptedUnchangedAndDebt(t *testing.T) {
	q := uoSavedInput{action: uoSavedEntries, id: "resourceabcdefghijklmn", revision: 5}
	effect := uoSavedEffect{id: q.id, revision: 6, generation: 9, selectionRevision: 10, reclaimedRows: 1, reclaimedBytes: 42,
		outcomes: []uoSavedOutcome{{input: "public-item", status: "unchanged"}, {input: "missing", status: "not_found"}}}
	v, err := uoSavedOriginalResult("abcdefghijklmnopqrstuv", EditSavedResourceMembers, q, effect, 100)
	if err != nil || !v.changed || v.revision != 6 || v.reclaimedBytes != 42 || v.selectionRevision != 10 || len(v.entries) != 2 || v.entries[0].status != 3 || v.entries[1].status != 4 {
		t.Fatal(v, err)
	}
	if _, err := uoSavedOriginalResult(v.root, PatchSavedResource, q, effect, 100); err == nil {
		t.Fatal("foreign purpose accepted")
	}
	effect.outcomes[0].status = "unknown"
	if _, err := uoSavedOriginalResult(v.root, EditSavedResourceMembers, q, effect, 100); err == nil {
		t.Fatal("unknown outcome accepted")
	}
}

func TestUOSavedClosedPointAndInputBounds(t *testing.T) {
	w, _ := uoFlagsFixture(t)
	turn := uoSavedTestTurn(t, w)
	if _, err := readUOSavedEntry(t.Context(), turn, &uoSavedTestSecurity{}, "head", "resource"); err == nil {
		t.Fatal("uncompiled byte point accepted")
	}
	if _, err := readUOSavedEntry(t.Context(), turn, nil, "entry", "resource", "entry"); err == nil {
		t.Fatal("missing byte admission accepted")
	}
}

func TestUOSavedPermutationRefusesTiedRanks(t *testing.T) {
	w, _ := uoFlagsFixture(t)
	s := &uoSavedTestSecurity{}
	uoSavedTestCreate(t, w, s, "saved-one")
	wc008NativeExec(t, w, `INSERT INTO saved_resource_entries(id,resource_id,order_key) VALUES('entry-one','saved-one','U'),('entry-two','saved-one','U')`)
	turn := uoSavedTestTurn(t, w)
	if _, err := applyUOSaved(t.Context(), turn, uoSavedInput{action: uoSavedReorder, id: "saved-one", revision: 1, order: []string{"entry-two", "entry-one"}, security: s}); err == nil {
		t.Fatal("ambiguous permutation claimed acceptance")
	}
}
