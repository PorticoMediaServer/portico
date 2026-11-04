package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func visibilitySourceBatchFixture(t *testing.T, classes, jobs int) (*sql.DB, *Worker) {
	t.Helper()
	db := cc1OpenDB(t, "visibility-batch")
	lib := cc1Library(t, db, "batch", "Batch", "movie", "/batch")
	w := NewWorker(db)
	if err := w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < classes; i++ {
		id := cc1Class(t, db, lib, fmt.Sprintf("class-%02d", i))
		if i == classes-1 {
			cc1Exec(t, db, `UPDATE compact_visibility_classes SET retired=1 WHERE id=?`, id)
		}
	}
	ids := make([]int64, jobs)
	for i := range ids {
		ids[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: fmt.Sprintf("film:%d", i), Title: "Film", Year: 2000}, nil)
	}
	visibilityFanoutReset(t, db, 100)
	for i, id := range ids {
		visibilityFanoutInsertJob(t, db, visibilitySourceKindRow, lib, id, "", int64(i+1), int64(i+1))
	}
	return db, w
}

func visibilitySourceBatchRows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	out := map[string][][]any{}
	for _, table := range []string{"compact_visibility_source_jobs", "compact_visibility_dirty", "compact_visibility_source_state"} {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1,2,3`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			out[table] = append(out[table], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return out
}

func TestVisibilitySourceBatchMatchesIndividualRowsAndCursors(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		classes, jobs, transactions int
	}{{"empty-acknowledgements", 0, 10, 8}, {"raw-pages-and-retired-rejections", 3, 3, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			individual, _ := visibilitySourceBatchFixture(t, tc.classes, tc.jobs)
			batched, _ := visibilitySourceBatchFixture(t, tc.classes, tc.jobs)
			ctx, before := dbwork.Measure(context.Background())
			used := 0
			for used < upstreamQuantum {
				if err := dbwork.WithWriteTx(ctx, individual, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
					n, err := StepVisibilitySourceFanout(ctx, tx, upstreamQuantum-used)
					used += n
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, after := dbwork.Measure(context.Background())
			dbwork.ResetPlans()
			if err := dbwork.WithWriteTx(ctx, batched, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				// Test the raw allowance independently of wall-clock scheduling.
				// The production hold boundary is exercised below.
				n, err := drainVisibilitySources(ctx, tx, upstreamQuantum, func() (bool, error) { return true, nil })
				if n != upstreamQuantum {
					t.Fatalf("shared batch used %d units, want %d", n, upstreamQuantum)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if got, want := visibilitySourceBatchRows(t, batched), visibilitySourceBatchRows(t, individual); !reflect.DeepEqual(got, want) {
				t.Fatalf("batched source rows/cursors differ:\ngot %#v\nwant %#v", got, want)
			}
			if before().Transactions != int64(tc.transactions) || after().Transactions != 1 || dbwork.Plans().Overruns != 0 {
				t.Fatalf("source batch escaped its writer/row plan: before=%+v after=%+v plans=%+v", before(), after(), dbwork.Plans())
			}
			t.Logf("source allowance=%d transactions=%d→%d statements=%d→%d", used, before().Transactions, after().Transactions, before().Statements, after().Statements)
		})
	}
}

func TestVisibilitySourceBatchStopsAtHoldTargetAndReleasesWriter(t *testing.T) {
	db, w := visibilitySourceBatchFixture(t, 0, 10)
	// Tighten the existing target so one indivisible source call uses it all.
	w.PaceToHold(time.Nanosecond)
	n, err := w.Step(context.Background(), BatchMax)
	if err != nil || n != 1 {
		t.Fatalf("expired hold target admitted another source job: n=%d err=%v", n, err)
	}
	if pending := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); pending != 9 {
		t.Fatalf("batch consumed %d jobs after its hold target", 10-pending)
	}
	if dbwork.WriteGate().Held() {
		t.Fatal("source batch retained the writer across its pacing boundary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	foreground, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal("foreground writer could not run between source batches", err)
	}
	foreground.Rollback()
}
