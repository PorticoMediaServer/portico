package writer

import (
	"crypto/sha256"
	"database/sql"
	"testing"
)

func TestUORootFlagsClosedCanonicalResult(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootFlagsResult{root: "abcdefghijklmnopqrstuv", public: [16]byte{4}, appliedMS: 100,
		effect: uoPersonalFlagsEffect{state: uoPersonalFlagsState{revision: 1, watchlisted: true, favorite: true, rating: sql.NullInt64{Valid: true}}, changed: true, viewerRevision: 2, selectionRevision: 3}}
	p, d, err := encodeUORootCanonical(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	if sole, err := canonicalResultDigest(i, q, v); err != nil || sole != d {
		t.Fatal("result bypassed W codec", sole, d, err)
	}
	if got, err := decodeUORootFlags(i, q, v.root, p, d); err != nil || got != v {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*CommandIdentity, *RequestDigest, *string){
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Incarnation[0]++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.OperationID[0]++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Sequence++ },
		func(_ *CommandIdentity, q *RequestDigest, _ *string) { q[0]++ },
		func(_ *CommandIdentity, _ *RequestDigest, root *string) { *root = "zyxwvutsrqponmlkjihgfe" },
	} {
		ii, qq, root := i, q, v.root
		mutate(&ii, &qq, &root)
		if _, err := decodeUORootFlags(ii, qq, root, p, d); err == nil {
			t.Fatal("foreign original decoded")
		}
	}
	for _, offset := range []int{24, 25, 26, 27, 36} {
		bad := append([]byte(nil), p...)
		bad[len(bad)-61+offset] = 2
		if _, err := decodeUORootFlags(i, q, v.root, bad, ResultDigest(sha256.Sum256(bad))); err == nil {
			t.Fatal("noncanonical boolean decoded", offset)
		}
	}
	if _, _, err := encodeUORootCanonical(i, q, struct{ Payload []byte }{p}); err == nil {
		t.Fatal("unregistered result accepted")
	}
	v.effect.changed = false
	if _, _, err := encodeUORootCanonical(i, q, v); err == nil {
		t.Fatal("no-op accepted mutation revisions")
	}
	v.effect.viewerRevision, v.effect.selectionRevision = 0, 0
	if _, _, err := encodeUORootCanonical(i, q, v); err != nil {
		t.Fatal("complete no-op rejected", err)
	}
}

func TestUORootFlagsNativeReplayAndACK(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: SetPersonalFlags, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	want := uoPersonalFlagsEffect{state: uoPersonalFlagsState{revision: 1, watchlisted: true, rating: sql.NullInt64{Valid: true}}, changed: true, viewerRevision: 2, selectionRevision: 3}
	turn := wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	if _, err := persistUORootFlags(t.Context(), turn, lookup, [16]byte{4}, want, 100); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	turn = wc008NativeTurn(t, w, q.kind, uoRootAllDefinitions())
	lookup, err = lookupUORootCommand(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptReplay {
		t.Fatal(lookup.decision, err)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := restoreUORootFlags(t.Context(), turn, lookup)
	if err != nil || got.effect != want || got.public != [16]byte{4} || got.appliedMS != 100 {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay wrote state", before, after, err)
	}
	if _, err := restoreUORootOperation(t.Context(), turn, lookup); err == nil {
		t.Fatal("flags decoded as incomplete operation")
	}
	if floor, err := acknowledgeUORootFlags(t.Context(), turn, lookup, 200); err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	uoFlagsCommit(t, turn)
	var floor, rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence,receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&floor, &rows, &bytes); err != nil || floor != 1 || rows != 1 || bytes <= 118 {
		t.Fatal("ACK refunded retained original", floor, rows, bytes, err)
	}
}
