package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

// A production turn takes one pruning family per writer lease, and repeated
// turns reach the daily closure while keeping referenced rows and the retained
// generation.
func TestRetentionTurnPrunesOneFamilyThenFinishes(t *testing.T) {
	db, show := failingFixture(t, "retention-turn.sqlite")
	var generation int
	if err := db.QueryRow(`SELECT generation FROM catalog_generations`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_terms(vocab,key,label) VALUES(1,'unused','Unused'),(1,'kept','Kept')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_entity_terms(entity_id,term_id) SELECT ?,id FROM catalog_terms WHERE vocab=1 AND key='kept'`, show); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_people(token,identity_key,name,sort_name) VALUES('unused-person','unused-person','Unused','Unused'),('kept-person','kept-person','Kept','Kept')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO show_people_credits(show_id,identity_key,provider,credit_id,name,role,department,ordinal) VALUES(?,'kept-person','test','credit','Kept','Actor','Cast',0)`, show); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_browse_rows_previous`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_rows_previous(generation,present,entity_id,title) VALUES(?,1,?,'Old'),(?,1,?,'Retained')`, generation-1, show, generation, show); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_generations SET retaining=1`)
		return err
	})

	turn := retentionTurn{}
	phase := -1
	for i, d := range retentionDictionaries {
		if d.table == "catalog_terms" {
			phase = i
			break
		}
	}
	if phase < 0 {
		t.Fatal("catalog_terms is not a retention family")
	}
	turn.phase = phase

	var worked, closed bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		worked, closed, err = turn.step(ctx, tx, retentionRawTurn)
		return err
	})
	if !worked || closed {
		t.Fatalf("first turn worked=%v closed=%v", worked, closed)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_people WHERE identity_key='unused-person'`); n != 1 {
		t.Fatalf("one turn touched a second dictionary family: %d rows left", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_retention_cursors WHERE name='catalog_terms'`); n != 1 {
		t.Fatal("the catalog_terms cursor did not advance")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_retention_cursors WHERE name='catalog_people'`); n != 0 {
		t.Fatal("one turn advanced another family's cursor")
	}

	for i := 0; i < 500 && !closed; i++ {
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			_, closed, err = turn.step(ctx, tx, retentionRawTurn)
			return err
		})
	}
	if !closed {
		t.Fatal("the daily retention walk never closed")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_terms WHERE key='unused'`); n != 0 {
		t.Fatal("unreferenced vocabulary survived closure")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_terms WHERE key='kept'`); n != 1 {
		t.Fatal("referenced vocabulary was removed")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_people WHERE identity_key='unused-person'`); n != 0 {
		t.Fatal("unreferenced person survived closure")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_people WHERE identity_key='kept-person'`); n != 1 {
		t.Fatal("referenced person was removed")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows_previous WHERE generation<?`, generation); n != 0 {
		t.Fatal("an unretained prior generation survived closure")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows_previous WHERE generation=? AND entity_id=?`, generation, show); n != 1 {
		t.Fatal("the retained generation was removed")
	}
	if n := cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`); n != 1 {
		t.Fatal("closure changed the generation retention state")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_retention_cursors`); n != 0 {
		t.Fatalf("closure left %d owned cursor rows", n)
	}
}
