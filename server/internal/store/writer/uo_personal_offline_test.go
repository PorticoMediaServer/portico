package writer

import "testing"

func TestUOPersonalOfflineDeviceConflictAndResolve(t *testing.T) {
	w, target := uoPersonalFixture(t)
	q := uoPersonalOfflineInput{target: target, field: uoPersonalWatchlist, operation: "op1", device: "device-one", mutation: "m1", authoredAt: target.at, deviceSequence: 1, baseValue: "0", value: "1"}
	turn := uoPersonalTurn(t, w)
	e, err := applyUOPersonalOffline(t.Context(), turn, q)
	if err != nil || !e.changed || e.revision != 1 || e.deviceSequence != 1 || e.conflictID != "" {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE personal_items SET rating=7,revision=2 WHERE item_id=2111`)
	q.operation = "op2"
	q.mutation = "m2"
	q.deviceSequence = 2
	q.field = uoPersonalRating
	q.baseValue = "null"
	q.value = "3"
	turn = uoPersonalTurn(t, w)
	e, err = applyUOPersonalOffline(t.Context(), turn, q)
	if err != nil || e.changed || e.revision != 2 || e.conflictID == "" {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	var rating, seq, eventCount int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT rating,(SELECT sequence FROM personal_device_sequences WHERE profile_id='profile-one' AND device_id='device-one'),(SELECT count(*) FROM personal_explicit_events) FROM personal_items WHERE item_id=2111`).Scan(&rating, &seq, &eventCount); err != nil || rating != 7 || seq != 2 || eventCount != 2 {
		t.Fatal("conflict overwrote current state", rating, seq, eventCount, err)
	}
	target.expectedRevision = 2
	turn = uoPersonalTurn(t, w)
	r, err := applyUOPersonalResolve(t.Context(), turn, uoPersonalResolveInput{target: target, conflictID: e.conflictID, field: uoPersonalRating, baseRevision: 0, value: "3"})
	if err != nil || r.revision != 3 || !r.changed {
		t.Fatal(r, err)
	}
	uoFlagsCommit(t, turn)
	turn = uoPersonalTurn(t, w)
	dup, err := applyUOPersonalOffline(t.Context(), turn, q)
	if err == nil || dup != (uoPersonalOfflineEffect{}) {
		t.Fatal("device replay entered body", dup, err)
	}
	uoPersonalRollback(t, turn)
	q.operation = "op4"
	q.mutation = "m4"
	q.deviceSequence = 4
	turn = uoPersonalTurn(t, w)
	gap, err := applyUOPersonalOffline(t.Context(), turn, q)
	if err == nil || gap != (uoPersonalOfflineEffect{}) {
		t.Fatal("device gap accepted", gap, err)
	}
	uoPersonalRollback(t, turn)
	var resolved int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT resolved FROM personal_conflicts WHERE id=?`, e.conflictID).Scan(&resolved); err != nil || resolved != 1 {
		t.Fatal(resolved, err)
	}
}
func TestUOPersonalOfflineRollbackAfterUniqueMutationFailure(t *testing.T) {
	w, target := uoPersonalFixture(t)
	q := uoPersonalOfflineInput{target: target, field: uoPersonalFavorite, operation: "op1", device: "d", mutation: "same", authoredAt: target.at, deviceSequence: 1, baseValue: "0", value: "1"}
	turn := uoPersonalTurn(t, w)
	_, err := applyUOPersonalOffline(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	q.operation = "op2"
	q.deviceSequence = 2
	q.baseRevision = 1
	q.baseValue = "1"
	q.value = "0"
	turn = uoPersonalTurn(t, w)
	bad, err := applyUOPersonalOffline(t.Context(), turn, q)
	if err == nil || bad != (uoPersonalOfflineEffect{}) {
		t.Fatal(bad, err)
	}
	uoPersonalRollback(t, turn)
	var favorite, revision, seq int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT favorite,revision,(SELECT sequence FROM personal_device_sequences WHERE device_id='d') FROM personal_items WHERE item_id=2111`).Scan(&favorite, &revision, &seq); err != nil || favorite != 1 || revision != 1 || seq != 1 {
		t.Fatal("unique failure leaked field/sequence", favorite, revision, seq, err)
	}
}
