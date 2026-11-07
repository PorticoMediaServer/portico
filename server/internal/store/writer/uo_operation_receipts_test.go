package writer

import (
	"strings"
	"testing"
)

func uoOperationTestReceipt() uoOperationReceipt {
	return uoOperationReceipt{scope: uoOperationScope{authority: "local", account: "account", profile: "profile", membership: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32)}, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}, kind: BeginPersonalJob, nowMS: 100, expiresMS: 1000}
}
func TestUOOperationCodecOriginalBindingAndCorruption(t *testing.T) {
	q := uoOperationTestReceipt()
	want := uoOperationResult{status: 2, revision: 7, cursor: 8, done: 6, failed: 2, appliedMS: 100, target: [16]byte{4}}
	encoded, digest, err := encodeUOOperationResult(q, want)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(encoded, digest, err)
	}
	got, err := decodeUOOperationResult(q, encoded)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	wrong := q
	wrong.identity.Sequence++
	if _, err := decodeUOOperationResult(wrong, encoded); err == nil {
		t.Fatal("foreign sequence decoded")
	}
	corrupt := []byte(encoded)
	corrupt[30] = '!'
	if _, err := decodeUOOperationResult(q, string(corrupt)); err == nil {
		t.Fatal("corruption decoded")
	}
	other := q
	other.scope.lifetime = strings.Repeat("c", 32)
	oldScope, oldKey, _ := q.keys()
	newScope, newKey, _ := other.keys()
	if oldScope == newScope || oldKey != newKey {
		t.Fatal("scope birth did not isolate saved original")
	}
}
func TestUOOperationNativeReplayHasNoNewEffects(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := uoOperationTestReceipt()
	result := uoOperationResult{status: 2, revision: 7, cursor: 2, appliedMS: 100, target: [16]byte{5}}
	turn := wc008NativeTurn(t, w, BeginPersonalJob, uoOperationDefinitions())
	if _, err := persistUOOperationResult(t.Context(), turn, q, result); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	q.nowMS = 200
	turn = wc008NativeTurn(t, w, BeginPersonalJob, uoOperationDefinitions())
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := restoreUOOperationResult(t.Context(), turn, q)
	if err != nil || got != result {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay mutated rows", before, after, err)
	}
	uoFlagsCommit(t, turn)
	q.nowMS = 1001
	turn = wc008NativeTurn(t, w, BeginPersonalJob, uoOperationDefinitions())
	if got, err := restoreUOOperationResult(t.Context(), turn, q); err == nil || got != (uoOperationResult{}) {
		t.Fatal("expired original disclosed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
