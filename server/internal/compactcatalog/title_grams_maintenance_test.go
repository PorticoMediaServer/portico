package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func titleGramDB(t *testing.T) *sql.DB {
	t.Helper()
	db, e := persistence.OpenFresh(filepath.Join(t.TempDir(), "title-grams.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type gramCost struct {
	elapsed         time.Duration
	pages, rows, vm int64
}

func measureGramCall(t *testing.T, fn func() error) gramCost {
	t.Helper()
	dbwork.ResetPlans()
	start := time.Now()
	if e := fn(); e != nil {
		t.Fatal(e)
	}
	elapsed := time.Since(start)
	cost := dbwork.Plans().Largest
	return gramCost{elapsed: elapsed, pages: cost.Pages, rows: cost.Rows, vm: cost.VMOps}
}
func logGramCosts(t *testing.T, label string, costs []gramCost) {
	t.Helper()
	times := make([]time.Duration, len(costs))
	pages := make([]int64, len(costs))
	rows := int64(0)
	vm := int64(0)
	for i, c := range costs {
		times[i] = c.elapsed
		pages[i] = c.pages
		rows = max(rows, c.rows)
		vm = max(vm, c.vm)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	t.Logf("%s n=%d median-time=%s max-time=%s median-cache-page-writes=%d max-cache-page-writes=%d max-rows=%d max-VM=%d (observations, not frame bounds)", label, len(costs), times[len(times)/2], times[len(times)-1], pages[len(pages)/2], pages[len(pages)-1], rows, vm)
}

func TestTitleGramsMaintenancePreservesExactSearchAndMeasuresChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := titleGramDB(t)
	ctx := context.Background()
	const live = 1024
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for id := 1; id <= live; id++ {
		if _, e = tx.Exec(`INSERT INTO catalog_title_grams(rowid,title) VALUES(?,?)`, id, fmt.Sprintf("common shared title identifier%06d suffix%04d", id, id%97)); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var deletes, merges []gramCost
	deletePlan := dbwork.WritePlan{Name: "fixture-native-title-delete", Why: "measure the approved native FTS contentless-delete exception under churn; not a relational/frame cap"}
	for round := 0; round < 24; round++ {
		for offset := 1; offset <= 16; offset++ {
			old := round*16 + offset
			deletes = append(deletes, measureGramCall(t, func() error {
				_, e := dbwork.ExecWrite(dbwork.WithPlan(ctx, deletePlan), db, dbwork.ClassMaintenance, `DELETE FROM catalog_title_grams WHERE rowid=?`, old)
				return e
			}))
			if _, e = db.Exec(`INSERT INTO catalog_title_grams(rowid,title) VALUES(?,?)`, live+old, fmt.Sprintf("common shared replacement identifier%06d suffix%04d", old, old%97)); e != nil {
				t.Fatal(e)
			}
		}
		merges = append(merges, measureGramCall(t, func() error { return maintainTitleGrams(ctx, db) }))
	}
	// Contentless-delete and merge must preserve the exact surviving set, not
	// just its count, including all new postings from interleaved insert commits.
	rows, e := db.Query(`SELECT rowid FROM catalog_title_grams WHERE catalog_title_grams MATCH 'com' ORDER BY rowid`)
	if e != nil {
		t.Fatal(e)
	}
	want := 24*16 + 1
	count := 0
	for rows.Next() {
		var id int
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		if id != want {
			rows.Close()
			t.Fatalf("search got%d want%d", id, want)
		}
		want++
		count++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Fatal(e)
	}
	if count != live || want != live+24*16+1 {
		t.Fatal("merge lost or revived rows", count, want)
	}
	logGramCosts(t, "delete with interleaved merge4", deletes)
	logGramCosts(t, "native merge4", merges)
	var worked bool
	for _, c := range merges {
		worked = worked || c.pages > 0
	}
	if !worked {
		t.Fatal("churn fixture never exercised physical merge work")
	}
}

func TestTitleGramsMaintenanceQueuesBelowUserWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := titleGramDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	holder, e := dbwork.Begin(ctx, db, dbwork.ClassBackgroundMedia)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback()
	done := make(chan string, 2)
	errs := make(chan error, 2)
	go func() { e := maintainTitleGrams(ctx, db); errs <- e; done <- "merge" }()
	wait := func(class dbwork.Class) {
		t.Helper()
		for dbwork.WriteGate().Waiting()[class.String()] < 1 {
			select {
			case <-ctx.Done():
				t.Fatal("writer did not queue", class)
			case <-time.After(time.Millisecond):
			}
		}
	}
	wait(dbwork.ClassMaintenance)
	go func() {
		_, e := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO configuration(key,value) VALUES('fts-priority','user')`)
		errs <- e
		done <- "user"
	}()
	wait(dbwork.ClassInteractive)
	holder.Rollback()
	if got := <-done; got != "user" {
		t.Fatal("maintenance overtook queued user", got)
	}
	if got := <-done; got != "merge" {
		t.Fatal(got)
	}
	for range 2 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
}
