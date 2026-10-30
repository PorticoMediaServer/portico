package dbwork

import (
	"context"
	"errors"
	"testing"
)

func TestReadSnapshotDoesNotPublishHistoricalConnectionChanges(t *testing.T) {
	db := testDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`INSERT INTO rows(value) VALUES('previous writer')`); err != nil {
		t.Fatal(err)
	}
	published, changed := Publications(), ChangingCommits()
	for range 3 {
		r, err := BeginSnapshot(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err = r.Tx().QueryRow(`SELECT count(*) FROM rows`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err = r.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if Publications() != published || ChangingCommits() != changed {
		t.Fatal("read snapshots published a write")
	}
}

func TestSnapshotRowMutationRollsBack(t *testing.T) {
	db := testDB(t)
	r, err := BeginSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Rollback()
	if _, err = r.Tx().Exec(`INSERT INTO rows(value) VALUES('not allowed')`); err != nil {
		t.Fatal(err)
	}
	if err = r.Commit(); !errors.Is(err, ErrSnapshotWrite) {
		t.Fatalf("commit: %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM rows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mutation persisted: %d, %v", count, err)
	}
}

func TestBorrowedSnapshotPublishesOnlyOuterWrite(t *testing.T) {
	db := testDB(t)
	w, err := Begin(context.Background(), db, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if _, err = w.Tx().Exec(`INSERT INTO rows(value) VALUES('outer')`); err != nil {
		t.Fatal(err)
	}
	published, changed := Publications(), ChangingCommits()
	r, err := BeginSnapshot(w.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Commit(); err != nil {
		t.Fatal(err)
	}
	if Publications() != published || ChangingCommits() != changed {
		t.Fatal("borrowed snapshot published before outer commit")
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
	if Publications() != published+1 || ChangingCommits() != changed+1 {
		t.Fatal("outer write did not publish once")
	}
}
