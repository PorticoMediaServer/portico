package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestWorkerDoesNotReprobeEmptyVisibilitySourceGeneration(t *testing.T) {
	db := cc1OpenDB(t, "empty-source")
	cc1Drain(t, db)
	w := NewWorker(db)
	probes := 0
	for turn := 0; turn < 8; turn++ {
		ctx, read := dbwork.TraceQueryPlans(context.Background())
		n, err := w.Step(ctx, 4)
		if err != nil || n != 0 {
			t.Fatalf("settled worker turn %d: units=%d err=%v", turn, n, err)
		}
		for _, query := range read() {
			if strings.Contains(query, "compact_visibility_source_jobs_turn") {
				probes++
			}
		}
	}
	if probes != visibilitySourceKinds {
		t.Fatalf("unchanged empty source had %d job probes, want one %d-kind census", probes, visibilitySourceKinds)
	}
}

func TestWorkerPublishesEmptyVisibilityOnlyAfterOwnerCommit(t *testing.T) {
	db := cc1OpenDB(t, "empty-source-owner")
	cc1Drain(t, db)
	w := NewWorker(db)
	if err := w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		owner, err := dbwork.Begin(context.Background(), db, dbwork.ClassInteractive)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := w.Step(owner.Context(), 4); err != nil || n != 0 {
			owner.Rollback()
			t.Fatalf("nested empty step: %d %v", n, err)
		}
		if w.sourceEmpty.empty {
			owner.Rollback()
			t.Fatal("uncommitted observation escaped its owner")
		}
		if commit {
			err = owner.Commit()
		} else {
			err = owner.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		if w.sourceEmpty.empty != commit {
			t.Fatalf("commit=%v memo=%+v", commit, w.sourceEmpty)
		}
	}
}

func TestWorkerEmptyVisibilitySeesAcceptedClockChange(t *testing.T) {
	db := cc1OpenDB(t, "empty-source-clock")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film", Title: "Film"}, nil)
	cc1Drain(t, db)
	w := NewWorker(db)
	if n, err := w.Step(context.Background(), 4); err != nil || n != 0 {
		t.Fatalf("empty step: %d %v", n, err)
	}
	if !w.sourceEmpty.empty {
		t.Fatal("empty source was not retained")
	}
	before := w.sourceEmpty.sequence
	// The accepted browse-row trigger advances the source clock and enqueues.
	cc1Exec(t, db, `UPDATE catalog_browse_rows SET year=year+1 WHERE entity_id=?`, item)
	if next := visibilityFanoutClock(t, db); next <= before {
		t.Fatal("fixture did not advance its source clock")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); got == 0 {
		t.Fatal("fixture did not enqueue")
	}
	for turn := 0; turn < 20; turn++ {
		n, err := w.Step(context.Background(), 4)
		if err != nil {
			t.Fatal(err)
		}
		if left := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); left == 0 {
			return
		}
		if n == 0 {
			t.Fatal("old empty memo hid new source jobs")
		}
	}
	t.Fatal("new source jobs did not drain")
}

func TestVisibilityEmptyMemoMatchesRowsAndPassCursors(t *testing.T) {
	build := func(cached bool) []string {
		db := cc1OpenDB(t, fmt.Sprint("empty-parity-", cached))
		lib := cc1Library(t, db, "tv", "TV", "tv", "/tv")
		item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "item", Title: "Item"}, nil)
		container := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "container", Title: "Container"}, nil)
		missing := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "missing", Title: "Missing"}, nil)
		for i := 0; i < 10; i++ {
			class := cc1Class(t, db, lib, fmt.Sprint("class-", i))
			if i == 7 {
				cc1Exec(t, db, `UPDATE compact_visibility_classes SET retired=1 WHERE id=?`, class)
			}
		}
		visibilityFanoutBrowseRow(t, db, item, lib, 1)
		visibilityFanoutBrowseRow(t, db, container, lib, 2)
		visibilityFanoutMember(t, db, container, item, 0)
		visibilityFanoutMember(t, db, container, item, 1)
		visibilityFanoutMember(t, db, missing, item, 0)
		visibilityFanoutReset(t, db, 10)
		// Kind1 is empty at the first probe. Kind2 creates kind1 children at
		// that SAME clock; whole-queue emptiness must not be inferred early.
		visibilityFanoutInsertJob(t, db, 2, 0, item, "", 10, 10)
		var memo visibilitySourceEmptyMemo
		var trace []string
		idle := 0
		for turn := 0; turn < 100; turn++ {
			if turn == 2 {
				cc1Exec(t, db, `UPDATE catalog_browse_rows SET year=year+1 WHERE entity_id=?`, item)
			}
			n := 0
			err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				ctx := context.Background()
				next := memo
				if cached {
					ctx = context.WithValue(ctx, visibilitySourceEmptyKey{}, &next)
					if err := dbwork.AfterCommit(tx, func() { memo = next }); err != nil {
						return err
					}
				}
				var err error
				n, err = StepVisibilitySourceFanout(ctx, tx, 2)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			trace = append(trace, fmt.Sprint(n, ";", groupRows(t, db, `SELECT * FROM compact_visibility_source_jobs ORDER BY kind,library_id,subject_id,subject_text`), ";", groupRows(t, db, `SELECT * FROM compact_visibility_dirty ORDER BY class_id,entity_id`), ";", groupRows(t, db, `SELECT * FROM compact_visibility_source_state ORDER BY id`)))
			if n == 0 {
				if cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`) != 0 {
					t.Fatal("empty result hid jobs")
				}
				idle++
				if idle == 3 {
					return trace
				}
			} else {
				idle = 0
			}
		}
		t.Fatal("source did not drain")
		return nil
	}
	a, b := build(false), build(true)
	if !reflect.DeepEqual(a, b) {
		for i := 0; i < min(len(a), len(b)); i++ {
			if a[i] != b[i] {
				t.Fatalf("rows/cursors changed after call %d\nplain %s\ncached %s", i, a[i], b[i])
			}
		}
		t.Fatalf("turn count changed: %d %d", len(a), len(b))
	}
}

func TestWorkerEmptyVisibilityRollbackRestoresLastJob(t *testing.T) {
	db := cc1OpenDB(t, "empty-source-rollback")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "item", Title: "Item"}, nil)
	cc1Drain(t, db)
	w := NewWorker(db)
	if err := w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	visibilityFanoutReset(t, db, 20)
	visibilityFanoutInsertJob(t, db, 1, lib, item, "", 20, 20)
	owner, err := dbwork.Begin(context.Background(), db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback()
	if n, err := w.Step(owner.Context(), 4); err != nil || n != 1 {
		t.Fatalf("last-job step: %d %v", n, err)
	}
	if n, err := w.Step(owner.Context(), 4); err != nil || n != 0 {
		t.Fatalf("tentatively empty step: %d %v", n, err)
	}
	if err := owner.Rollback(); err != nil {
		t.Fatal(err)
	}
	if w.sourceEmpty.empty {
		t.Fatal("rolled-back deletion published an empty memo")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); got != 1 {
		t.Fatalf("rollback did not restore its job: %d", got)
	}
	if n, err := w.Step(context.Background(), 4); err != nil || n != 1 {
		t.Fatalf("restored job was hidden: %d %v", n, err)
	}
}

func TestNewWorkerRechecksEmptyVisibility(t *testing.T) {
	db := cc1OpenDB(t, "empty-source-new-worker")
	cc1Drain(t, db)
	w := NewWorker(db)
	if n, err := w.Step(context.Background(), 4); err != nil || n != 0 {
		t.Fatalf("empty step: %d %v", n, err)
	}
	if !w.sourceEmpty.empty {
		t.Fatal("fixture did not retain emptiness")
	}
	fresh := NewWorker(db)
	ctx, read := dbwork.TraceQueryPlans(context.Background())
	if n, err := fresh.Step(ctx, 4); err != nil || n != 0 {
		t.Fatalf("new worker's empty step: %d %v", n, err)
	}
	probes := 0
	for _, q := range read() {
		if strings.Contains(q, "compact_visibility_source_jobs_turn") {
			probes++
		}
	}
	if probes != visibilitySourceKinds {
		t.Fatalf("new worker inherited an old cache: %d probes", probes)
	}
}
