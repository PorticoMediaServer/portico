package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestCommitProgressBelongsToTheAcceptingContext(t *testing.T) {
	db := testDB(t)
	tracked, count := TrackCommitProgress(context.Background())
	other, otherCount := TrackCommitProgress(context.Background())
	write := func(ctx context.Context, sql string) {
		t.Helper()
		if _, err := ExecWrite(ctx, db, ClassBackgroundMedia, sql); err != nil {
			t.Fatal(err)
		}
	}
	write(other, `INSERT INTO rows(value) VALUES('other')`)
	if count() != 0 || otherCount() != 1 {
		t.Fatalf("unrelated autocommit leaked: own=%d other=%d", count(), otherCount())
	}
	write(tracked, `UPDATE rows SET value='never' WHERE 0`)
	if count() != 0 {
		t.Fatal("zero-row autocommit reported progress")
	}
	write(tracked, `INSERT INTO rows(value) VALUES('own')`)
	if count() != 1 {
		t.Fatalf("own autocommit progress %d", count())
	}
	fail := errors.New("rollback")
	if err := WithWriteTx(tracked, db, ClassBackgroundMedia, func(tx *sql.Tx) error {
		if _, e := tx.Exec(`INSERT INTO rows(value) VALUES('rolled back')`); e != nil {
			return e
		}
		return fail
	}); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("rollback reported progress")
	}
	if err := WithWriteTx(other, db, ClassBackgroundMedia, func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO rows(value) VALUES('other tx')`); return e }); err != nil {
		t.Fatal(err)
	}
	if count() != 1 || otherCount() != 2 {
		t.Fatalf("unrelated transaction leaked: own=%d other=%d", count(), otherCount())
	}
	w, err := Begin(tracked, db, ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	inner, err := Begin(w.Context(), db, ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Rollback()
	if _, err = inner.Tx().Exec(`INSERT INTO rows(value) VALUES('nested')`); err != nil {
		t.Fatal(err)
	}
	if err = inner.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = ExecWrite(inner.Context(), db, ClassBackgroundMedia, `INSERT INTO rows(value) VALUES('joined autocommit')`); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("nested acceptance published before outer commit")
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatalf("outer commit not counted exactly once: %d", count())
	}
	r, err := BeginSnapshot(tracked, db)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Commit(); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("read snapshot reported progress")
	}
	if _, err = ExecWrite(tracked, db, ClassBackgroundMedia, `INSERT INTO nonexistent VALUES(1)`); err == nil {
		t.Fatal("failed write accepted")
	}
	if count() != 2 {
		t.Fatal("failed write reported progress")
	}
}

func TestCommitProgressNestedTrackerStartsAnIndependentScope(t *testing.T) {
	db := testDB(t)
	outer, outerCount := TrackCommitProgress(context.Background())
	inner, innerCount := TrackCommitProgress(outer)
	if _, err := ExecWrite(inner, db, ClassBackgroundMedia, `INSERT INTO rows(value) VALUES('inner scope')`); err != nil {
		t.Fatal(err)
	}
	if innerCount() != 1 || outerCount() != 0 {
		t.Fatalf("nested tracker counts outer=%d inner=%d", outerCount(), innerCount())
	}
	if _, err := ExecWrite(outer, db, ClassBackgroundMedia, `INSERT INTO rows(value) VALUES('outer scope')`); err != nil {
		t.Fatal(err)
	}
	if innerCount() != 1 || outerCount() != 1 {
		t.Fatalf("outer tracker counts outer=%d inner=%d", outerCount(), innerCount())
	}
}
