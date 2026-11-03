package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestRequestMemoReusesAnswersOnlyInReadSnapshotsOfOneRequest(t *testing.T) {
	db := testDB(t)
	request := WithRequestMemo(context.Background())
	proofs := 0
	prove := func() (string, error) { proofs++; return "answer", nil }
	read := func(ctx context.Context) string {
		t.Helper()
		var got string
		if err := WithReadSnapshot(ctx, db, func(snapshot context.Context) error {
			var err error
			got, err = RememberForRequest(snapshot, Snapshot(snapshot), "who", nil, prove)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	read(request)
	read(request)
	if proofs != 1 {
		t.Fatalf("two snapshots of one request proved %d times, want 1", proofs)
	}
	read(WithRequestMemo(context.Background()))
	if proofs != 2 {
		t.Fatal("another request reused this request's answer")
	}
	read(context.Background())
	if proofs != 3 {
		t.Fatal("work outside a request reused an answer")
	}

	// Inside a gated write the answer is always proved in that transaction.
	if err := WithWriteTx(request, db, ClassInteractive, func(tx *sql.Tx) error {
		_, err := RememberForRequest(request, tx, "who", nil, prove)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if proofs != 4 {
		t.Fatal("a write transaction reused a remembered answer")
	}
	// And the write emptied the memo: the next read proves again.
	read(request)
	if proofs != 5 {
		t.Fatal("a request that wrote kept its remembered authority")
	}
}

func TestRequestMemoKeepsOnlySuccessesForItsLifetime(t *testing.T) {
	db := testDB(t)
	request := WithRequestMemo(context.Background())
	tx, end, err := BeginRead(request, db)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	refused := errors.New("unauthorized")
	calls := 0
	for range 2 {
		if _, err := RememberForRequest(request, tx, "denied", nil, func() (int, error) { calls++; return 0, refused }); !errors.Is(err, refused) {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("a refusal was remembered")
	}
	memo := requestMemoFrom(request)
	memo.entries["old"] = requestMemoEntry{value: 1, at: time.Now().Add(-RequestMemoLifetime)}
	if got, _ := RememberForRequest(request, tx, "old", nil, func() (int, error) { return 2, nil }); got != 2 {
		t.Fatal("an answer older than its lifetime was reused")
	}
	labels, _ := RememberForRequest(request, tx, "labels", func(v []string) []string { return append([]string(nil), v...) }, func() ([]string, error) { return []string{"a"}, nil })
	labels[0] = "changed"
	again, _ := RememberForRequest(request, tx, "labels", func(v []string) []string { return append([]string(nil), v...) }, func() ([]string, error) { return nil, errors.New("not called") })
	if again[0] != "a" {
		t.Fatal("one caller changed another caller's remembered answer")
	}
}

// A transaction dbwork did not open as a read snapshot may have written, so it
// never reuses an answer, and a snapshot's answers stop when it ends.
func TestRequestMemoIgnoresOtherTransactions(t *testing.T) {
	db := testDB(t)
	request := WithRequestMemo(context.Background())
	raw, err := db.BeginTx(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Rollback()
	calls := 0
	for range 2 {
		RememberForRequest(request, raw, "k", nil, func() (int, error) { calls++; return 1, nil })
	}
	if calls != 2 {
		t.Fatal("a transaction dbwork did not open as a read snapshot reused an answer")
	}
	tx, end, err := BeginRead(request, db)
	if err != nil {
		t.Fatal(err)
	}
	end()
	if readSnapshotTx(tx) {
		t.Fatal("an ended snapshot is still registered")
	}
}
