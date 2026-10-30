package dbwork

import (
	"context"
	"testing"
)

// An autocommit ExecWrite that changed rows is a publication, like a committed
// transaction; one that changed nothing is not. Response validators and caches
// keyed on Publications depend on it (PERF-12).
func TestExecWriteThatChangesRowsIsAPublication(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	before := Publications()
	if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO rows(id,value) VALUES(1,'a')`); err != nil {
		t.Fatal(err)
	}
	if Publications() <= before {
		t.Fatal("a changing ExecWrite did not count as a publication")
	}
	unchanged := Publications()
	if _, err := ExecWrite(ctx, db, ClassInteractive, `UPDATE rows SET value='b' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if Publications() != unchanged {
		t.Fatal("an ExecWrite that changed nothing counted as a publication")
	}
}
