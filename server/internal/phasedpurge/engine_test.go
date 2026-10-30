package phasedpurge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "purge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{`CREATE TABLE leaf(id INTEGER PRIMARY KEY)`, `CREATE TABLE job(id INTEGER PRIMARY KEY,cursor TEXT NOT NULL)`, `INSERT INTO job VALUES(1,'')`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func owner() Owner {
	return Owner{ReadBound: 4, Save: func(ctx context.Context, tx *sql.Tx, c Cursor) error {
		raw, err := EncodeCursor(c)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE job SET cursor=? WHERE id=1`, raw)
		return err
	}, Finalize: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM job WHERE id=1`)
		return err
	}}
}
func unit(n int) Unit {
	return Unit{Name: "leaf", Phase: 0, ReadBound: n + 1, Run: func(ctx context.Context, tx *sql.Tx, _ []Key) (Outcome, error) {
		r, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM leaf WHERE id IN(SELECT id FROM leaf ORDER BY id LIMIT %d)`, n))
		if err != nil {
			return Outcome{}, err
		}
		changed, err := r.RowsAffected()
		return Outcome{Changed: changed > 0, Drained: changed == 0}, err
	}, Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leaf LIMIT 1)`).Scan(&exists)
		return !exists, err
	}}
}
func rows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func seed(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := db.Exec(`INSERT INTO leaf VALUES(?)`, i); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEffectBudgetIncludesDurableCursor(t *testing.T) {
	for _, n := range []int{23, 24} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := fixture(t)
			seed(t, db, n)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			result, err := Turn(context.Background(), tx, []Unit{unit(n)}, Cursor{}, owner())
			if n == 24 {
				if !errors.Is(err, ErrEffects) || result.Done || result.Worked {
					t.Fatalf("false acceptance: %+v %v", result, err)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if rows(t, db, "leaf") != 24 {
					t.Fatal("oversized turn partially committed")
				}
				var raw string
				if err = db.QueryRow(`SELECT cursor FROM job`).Scan(&raw); err != nil || raw != "" {
					t.Fatalf("cursor advanced: %q %v", raw, err)
				}
			} else {
				if err != nil || result.Effects != 24 {
					t.Fatalf("bounded turn: %+v %v", result, err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if rows(t, db, "leaf") != 0 {
					t.Fatal("bounded turn did not commit")
				}
			}
		})
	}
}
func TestTriggerFanoutFalsifierRollsBackWholeTurn(t *testing.T) {
	db := fixture(t)
	seed(t, db, 1)
	if _, err := db.Exec(`CREATE TABLE fanout(id INTEGER);CREATE TRIGGER wide_delete AFTER DELETE ON leaf BEGIN INSERT INTO fanout SELECT value FROM json_each('[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24]');END`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	result, err := Turn(context.Background(), tx, []Unit{unit(1)}, Cursor{}, owner())
	if !errors.Is(err, ErrEffects) || result.Done || result.Worked {
		t.Fatalf("wide trigger accepted %+v %v", result, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if rows(t, db, "leaf") != 1 || rows(t, db, "fanout") != 0 {
		t.Fatal("trigger or root effect escaped rollback")
	}
}
func TestTypedCursorPreservesIntegerAndBlobAcrossRestart(t *testing.T) {
	keys, err := Keys(int64(9223372036854775807), int64(-9223372036854775807-1), []byte{0, 255, 128}, "x")
	if err != nil {
		t.Fatal(err)
	}
	plan := []Unit{{KeyKinds: []Kind{Integer, Integer, Blob, Text}}}
	raw, err := EncodeCursor(Cursor{Key: keys})
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeCursor(raw, plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SQLKeys(c.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []any{int64(9223372036854775807), int64(-9223372036854775807 - 1), []byte{0, 255, 128}, "x"}) {
		t.Fatalf("key changed: %#v", got)
	}
	for _, bad := range []string{`{"u":-1}`, `{"u":2}`, `{"u":0,"v":1}`, `{"u":0,"unknown":1}`, `{"k":[{"t":"integer","v":"9223372036854775808"}]}`, `{"k":[{"t":"integer","v":"01"}]}`, `null`, `null null`, `{"u":1,"k":[{"t":"text","v":"x"}]}`} {
		if _, err = DecodeCursor(bad, plan); !errors.Is(err, ErrCursor) {
			t.Fatalf("bad cursor accepted: %s %v", bad, err)
		}
	}
}
func TestDeclaredRawReadBudgetStopsBeforeAnotherUnit(t *testing.T) {
	db := fixture(t)
	calls := 0
	u := Unit{Name: "empty", ReadBound: 45, Run: func(context.Context, *sql.Tx, []Key) (Outcome, error) { calls++; return Outcome{Drained: true}, nil }, Empty: func(context.Context, *sql.Tx) (bool, error) { return true, nil }}
	plan := []Unit{u, u, u, u}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := Turn(context.Background(), tx, plan, Cursor{}, owner())
	if err != nil || calls != 2 || result.Cursor.Unit != 2 || result.Reads != 94 || result.Reads > ReadLimit {
		t.Fatalf("raw bound: calls%d %+v %v", calls, result, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestCursorCommitRestartAndResidueProofBeforeFinalize(t *testing.T) {
	db := fixture(t)
	seed(t, db, 2)
	plan := []Unit{unit(1)}
	for i := 0; i < 2; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var raw string
		if err = tx.QueryRow(`SELECT cursor FROM job WHERE id=1`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		c, err := DecodeCursor(raw, plan)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Turn(context.Background(), tx, plan, c, owner())
		if err != nil || res.Done {
			t.Fatalf("early finish %+v %v", res, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// Source admission must be fenced by the owner; this deliberate residue is a
	// falsifier proving verification returns to cleanup instead of reporting done.
	if _, err := db.Exec(`INSERT INTO leaf VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Turn(context.Background(), tx, plan, Cursor{Unit: 1}, owner())
	if err != nil || res.Done || res.Cursor.Unit != 0 {
		t.Fatalf("residue skipped %+v %v", res, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		tx, err = db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var raw string
		if err = tx.QueryRow(`SELECT cursor FROM job WHERE id=1`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		c, e := DecodeCursor(raw, plan)
		if e != nil {
			t.Fatal(e)
		}
		res, err = Turn(context.Background(), tx, plan, c, owner())
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if res.Done {
			if rows(t, db, "job") != 0 || rows(t, db, "leaf") != 0 {
				t.Fatal("false finalization")
			}
			return
		}
	}
	t.Fatal("bounded phases did not converge")
}
func TestCustodyAndMeterErrorsNeverReturnCompletion(t *testing.T) {
	db := fixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	o := owner()
	sentinel := errors.New("custody changed")
	o.Before = func(context.Context, *sql.Tx) error { return sentinel }
	res, err := Turn(context.Background(), tx, []Unit{unit(1)}, Cursor{Unit: 1}, o)
	if !errors.Is(err, sentinel) || res.Done || res.Worked {
		t.Fatalf("custody falsely finished %+v %v", res, err)
	}
	tx.Rollback()
	res, err = Turn(context.Background(), tx, []Unit{unit(1)}, Cursor{}, owner())
	if err == nil || res.Done || res.Worked {
		t.Fatalf("meter failure hidden %+v %v", res, err)
	}
	if rows(t, db, "job") != 1 {
		t.Fatal("failed finalization removed job")
	}
}

func TestCursorRejectsUnprovedCompletionAndInvalidText(t *testing.T) {
	plan := []Unit{unit(1)}
	if _, err := DecodeCursor(string([]byte{0xff}), plan); !errors.Is(err, ErrCursor) {
		t.Fatalf("invalid UTF8 cursor accepted: %v", err)
	}
	if _, err := DecodeCursor(`{"u":1,"v":1}`, plan); !errors.Is(err, ErrCursor) {
		t.Fatalf("completion cursor accepted: %v", err)
	}
	if _, err := Keys(string([]byte{0xff})); !errors.Is(err, ErrCursor) {
		t.Fatalf("invalid UTF8 accepted: %v", err)
	}
	if _, err := EncodeCursor(Cursor{Key: []Key{{Text, string([]byte{0xff})}}}); !errors.Is(err, ErrCursor) {
		t.Fatalf("invalid encoded text accepted: %v", err)
	}
}
func TestPhaseRewindAndBeforeEffectsAreMetered(t *testing.T) {
	db := fixture(t)
	plan := []Unit{unit(1), unit(1)}
	plan[1].Run = func(context.Context, *sql.Tx, []Key) (Outcome, error) { return Outcome{Rewind: true}, nil }
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Turn(context.Background(), tx, plan, Cursor{Unit: 1}, owner())
	if err != nil || res.Cursor.Unit != 0 || res.Done {
		t.Fatalf("phase not rewound: %+v %v", res, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	plan[0].Run = plan[1].Run
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err = Turn(context.Background(), tx, plan, Cursor{}, owner())
	if !errors.Is(err, ErrNoProgress) || res.Worked || res.Done {
		t.Fatalf("blocked phase accepted: %+v %v", res, err)
	}
	tx.Rollback()
	seed(t, db, 24)
	o := owner()
	o.Before = func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM leaf`)
		return err
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err = Turn(context.Background(), tx, []Unit{unit(1)}, Cursor{}, o)
	if !errors.Is(err, ErrEffects) || res.Worked || res.Done {
		t.Fatalf("Before escaped meter: %+v %v", res, err)
	}
	tx.Rollback()
	if rows(t, db, "leaf") != 24 {
		t.Fatal("Before effects escaped rollback")
	}
}
