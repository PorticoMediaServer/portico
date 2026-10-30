package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Moving the state folder rebases Portico's own absolute paths on first
// start: a copied folder starts, the DVR library root and asset paths point
// at the new folder, and the recorded folder is stored.
func TestRebaseStatePathsOnMovedState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	oldState := t.TempDir()
	db, err := Open(filepath.Join(oldState, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('dvr','Recorded TV','recordings',?)`, filepath.Join(oldState, "recordings")); err != nil {
		t.Fatal(err)
	}
	if err = RebaseStatePaths(ctx, db, oldState); err != nil {
		t.Fatal(err)
	}
	var recorded string
	if err = db.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='state_directory'`).Scan(&recorded); err != nil || recorded != oldState {
		t.Fatalf("recorded folder: %q %v", recorded, err)
	}
	// A second start in place rewrites nothing.
	if _, err = db.Exec(`UPDATE libraries SET root=? WHERE id='dvr'`, filepath.Join(oldState, "recordings")); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Stop, copy the whole folder, point at the copy, start.
	newState := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(oldState, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(newState, "server.sqlite"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	moved, err := Open(filepath.Join(newState, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer moved.Close()
	if err = RebaseStatePaths(ctx, moved, newState); err != nil {
		t.Fatal(err)
	}
	var root string
	if err = moved.QueryRowContext(ctx, `SELECT root FROM libraries WHERE id='dvr'`).Scan(&root); err != nil || root != filepath.Join(newState, "recordings") {
		t.Fatalf("rebased root: %q %v", root, err)
	}
	if err = moved.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='state_directory'`).Scan(&recorded); err != nil || recorded != newState {
		t.Fatalf("recorded folder: %q %v", recorded, err)
	}
	// Values outside the old folder are never touched.
	if _, err = moved.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('ext','External','movie','/media/films')`); err != nil {
		t.Fatal(err)
	}
	if err = RebaseStatePaths(ctx, moved, newState); err != nil {
		t.Fatal(err)
	}
	if err = moved.QueryRowContext(ctx, `SELECT root FROM libraries WHERE id='ext'`).Scan(&root); err != nil || root != "/media/films" {
		t.Fatalf("external root rewritten: %q %v", root, err)
	}
}

// The rebase is a durable job: a start interrupted part-way resumes the same
// move from its saved position, so a path is never rewritten twice, even when
// the new folder sits inside the old one; and the folder it was moving to is
// recorded only once every column is done.
func TestRebaseResumesAnInterruptedMoveExactlyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldState := "/srv/portico"
	newState := "/srv/portico/moved" // inside the old folder: a double rewrite would show
	if _, err = db.Exec(`INSERT INTO configuration(key,value) VALUES('state_directory',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, oldState); err != nil {
		t.Fatal(err)
	}
	const rows = 700 // several windows and change batches
	for i := 0; i < rows; i++ {
		root := fmt.Sprintf("%s/recordings/%d", oldState, i)
		if i%3 == 0 {
			root = fmt.Sprintf("/media/%d", i) // never touched
		}
		if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'recordings',?)`, fmt.Sprint(i), fmt.Sprint(i), root); err != nil {
			t.Fatal(err)
		}
	}
	// Interrupt: the job exists and is half-way through the first column.
	if _, err = db.Exec(`INSERT INTO state_rebase_jobs(singleton,old_prefix,new_prefix,registry_version,phase,after_rowid,started_ms) VALUES(1,?,?,?,0,0,0)`, oldState, newState, rebaseRegistryVersion); err != nil {
		t.Fatal(err)
	}
	var half int64
	if err = db.QueryRow(`SELECT rowid FROM libraries ORDER BY rowid LIMIT 1 OFFSET 350`).Scan(&half); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE libraries SET root=?||substr(root,length(?)+1) WHERE rowid<=? AND (root=? OR root LIKE ?)`, newState, oldState, half, oldState, oldState+"/%"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE state_rebase_jobs SET after_rowid=?`, half); err != nil {
		t.Fatal(err)
	}
	// The next start finishes it — and then moves on to where the folder is now.
	current := t.TempDir()
	if err = RebaseStatePaths(ctx, db, current); err != nil {
		t.Fatal(err)
	}
	result, err := db.Query(`SELECT id,root FROM libraries`)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	for result.Next() {
		var id, root string
		if err = result.Scan(&id, &root); err != nil {
			t.Fatal(err)
		}
		var n int
		fmt.Sscan(id, &n)
		want := fmt.Sprintf("%s/recordings/%d", current, n)
		if n%3 == 0 {
			want = fmt.Sprintf("/media/%d", n)
		}
		if root != want {
			t.Fatalf("library %d: %q, want %q", n, root, want)
		}
	}
	var jobs int
	var recorded string
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM state_rebase_jobs),(SELECT value FROM configuration WHERE key='state_directory')`).Scan(&jobs, &recorded); err != nil || jobs != 0 || recorded != current {
		t.Fatalf("job left %d, recorded %q: %v", jobs, recorded, err)
	}
}

// A rebase chunk is a maintenance write: the plan counts every row its
// triggers change, not just the rows the statement names. Rewriting one
// libraries root fires the library-scope publication triggers, which touch the
// facet source state, catalog_upstream_sources and catalog_dirty for both the
// old and the new library. This runs the resume scenario and measures the
// actually issued rows, pages and VM operations of every chunk against the
// maintenance plan it runs under, so the quantum cannot grow past the plan
// unnoticed again.
func TestRebaseChunkFitsTheMaintenancePlan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldState := "/srv/portico"
	newState := "/srv/portico/moved"
	if _, err = db.Exec(`INSERT INTO configuration(key,value) VALUES('state_directory',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, oldState); err != nil {
		t.Fatal(err)
	}
	const rows = 700
	for i := 0; i < rows; i++ {
		root := fmt.Sprintf("%s/recordings/%d", oldState, i)
		if i%3 == 0 {
			root = fmt.Sprintf("/media/%d", i)
		}
		if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'recordings',?)`, fmt.Sprint(i), fmt.Sprint(i), root); err != nil {
			t.Fatal(err)
		}
	}
	// Interrupted half-way through the first column, as a crash leaves it.
	if _, err = db.Exec(`INSERT INTO state_rebase_jobs(singleton,old_prefix,new_prefix,registry_version,phase,after_rowid,started_ms) VALUES(1,?,?,?,0,0,0)`, oldState, newState, rebaseRegistryVersion); err != nil {
		t.Fatal(err)
	}
	var half int64
	if err = db.QueryRow(`SELECT rowid FROM libraries ORDER BY rowid LIMIT 1 OFFSET 350`).Scan(&half); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE libraries SET root=?||substr(root,length(?)+1) WHERE rowid<=? AND (root=? OR root LIKE ?)`, newState, oldState, half, oldState, oldState+"/%"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE state_rebase_jobs SET after_rowid=?`, half); err != nil {
		t.Fatal(err)
	}
	current := t.TempDir()
	dbwork.ResetPlans()
	if err = RebaseStatePaths(ctx, db, current); err != nil {
		t.Fatal(err)
	}
	stats := dbwork.Plans()
	plan := dbwork.DefaultPlan(dbwork.ClassMaintenance)
	t.Logf("rebase resumed: %d writes, largest %d rows, %d pages, %d VM operations (maintenance plan %d, %d, %d)",
		stats.Measured, stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps, plan.Rows, plan.Pages, plan.VMOps)
	if stats.Overruns != 0 {
		t.Fatalf("%d rebase writes overran their plan: %+v", stats.Overruns, stats.Recent)
	}
	if stats.Largest.Rows > plan.Rows {
		t.Fatalf("one rebase chunk changed %d rows, maintenance plan %d: lower rebaseChanges", stats.Largest.Rows, plan.Rows)
	}
	if stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
		t.Fatalf("one rebase chunk spent %d pages, %d VM operations; plan %d, %d", stats.Largest.Pages, stats.Largest.VMOps, plan.Pages, plan.VMOps)
	}
	// The resumed chunk continues from the saved position: every library is
	// rewritten exactly once, into the folder the job was moving to.
	var wrong int
	if err = db.QueryRow(`SELECT count(*) FROM libraries
	 WHERE (CAST(id AS INTEGER)%3=0 AND root<>'/media/'||id)
	    OR (CAST(id AS INTEGER)%3<>0 AND root<>?||'/recordings/'||id)`, current).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Fatalf("%d libraries were not rebased exactly once", wrong)
	}
	var jobs int
	var recorded string
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM state_rebase_jobs),(SELECT value FROM configuration WHERE key='state_directory')`).Scan(&jobs, &recorded); err != nil || jobs != 0 || recorded != current {
		t.Fatalf("job left %d, recorded %q: %v", jobs, recorded, err)
	}
}

// A job left by a build that walked a different registry is refused, not
// guessed at.
func TestRebaseRefusesAJobFromAnotherRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO state_rebase_jobs(singleton,old_prefix,new_prefix,registry_version,phase,after_rowid,started_ms) VALUES(1,'/a','/b',?,0,0,0)`, rebaseRegistryVersion+1); err != nil {
		t.Fatal(err)
	}
	if err = RebaseStatePaths(context.Background(), db, t.TempDir()); !errors.Is(err, ErrRebaseRegistry) {
		t.Fatalf("foreign job: %v", err)
	}
}
