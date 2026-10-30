package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

func TestEntityRemovalRetiresOnlyItsAvailabilityHolderAtomically(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := cc1OpenDB(t, "availability-delete.sqlite")
	library := cc1Library(t, db, "films", "Films", "movie", "/films")
	first := cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "first", Title: "First"}, nil)
	other := cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "other", Title: "Other"}, nil)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO catalog_availability_work(entity_id,revision,source_epoch) VALUES(?,1,1),(?,2,3)`, first, other)
		return e
	})
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = DeleteEntityTx(context.Background(), tx, first); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_availability_work WHERE entity_id=?`, first); n != 1 {
		t.Fatal("rollback lost holder", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_entities WHERE id=?`, first); n != 1 {
		t.Fatal("rollback lost entity", n)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return DeleteEntityTx(ctx, tx, first) })
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_availability_work WHERE entity_id=?`, first); n != 0 {
		t.Fatal("removed entity left holder", n)
	}
	if n := cc1Scalar(t, db, `SELECT revision FROM catalog_availability_work WHERE entity_id=?`, other); n != 2 {
		t.Fatal("other holder changed", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_entities WHERE id=?`, first); n != 0 {
		t.Fatal("entity survived removal", n)
	}
}
