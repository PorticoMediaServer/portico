package tableretention

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const cursorPolicyTable = "prepared_media_jobs"

func openCursorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedCursorRows inserts protected rows first and eligible rows last, then puts
// both cohorts on the age ledger in that same order so the cursor walks the
// protected cohort before it ever reaches work it may delete.
func seedCursorRows(t *testing.T, db *sql.DB, protected, eligible int) {
	t.Helper()
	for n := 0; n < protected+eligible; n++ {
		state := "running"
		if n >= protected {
			state = "failed"
		}
		if _, err := db.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES(?,1,'a','l','p','t','{}','r','{}',?,'phase',0,0)`, fmt.Sprintf("job%d", n), state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE maintenance_retention_rows SET created_ms=CAST(record_key AS INTEGER) WHERE table_name=?`, cursorPolicyTable); err != nil {
		t.Fatal(err)
	}
}

func countCursorRows(t *testing.T, db *sql.DB, state string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM prepared_media_jobs WHERE state=?`, state).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A sweep walks one bounded page of the age ledger at a time, so a long run of
// aged rows that stay protected by their predicate is not rescanned from the
// start on every call.
func TestSweepExaminesOnePagePerPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	resetCursors()
	t.Cleanup(resetCursors)
	db := openCursorTestDB(t)
	const protected, eligible, page = 70, 10, 32
	seedCursorRows(t, db, protected, eligible)

	more, err := Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("full candidate page not reported as backlog")
	}
	cursor := loadCursor(db, cursorPolicyTable)
	if !cursor.set || cursor.created != page {
		t.Fatalf("cursor after first page: %+v", cursor)
	}
	if got := countCursorRows(t, db, "running"); got != protected {
		t.Fatalf("protected rows removed: %d left", got)
	}
	if got := countCursorRows(t, db, "failed"); got != eligible {
		t.Fatalf("eligible rows removed from protected cohort: %d left", got)
	}

	more, err = Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("second full candidate page not reported as backlog")
	}
	cursor = loadCursor(db, cursorPolicyTable)
	if !cursor.set || cursor.created != 2*page {
		t.Fatalf("cursor after second page: %+v", cursor)
	}
	if got := countCursorRows(t, db, "running"); got != protected {
		t.Fatalf("protected rows removed: %d left", got)
	}
	if got := countCursorRows(t, db, "failed"); got != eligible {
		t.Fatalf("eligible rows removed before their page: %d left", got)
	}

	more, err = Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if more {
		t.Fatal("end of the ledger still reported as backlog")
	}
	if got := countCursorRows(t, db, "failed"); got != 0 {
		t.Fatalf("eligible rows retained: %d left", got)
	}
	if got := countCursorRows(t, db, "running"); got != protected {
		t.Fatalf("protected rows removed: %d left", got)
	}
	if cursor = loadCursor(db, cursorPolicyTable); cursor.set {
		t.Fatalf("cursor not reset at the end of the ledger: %+v", cursor)
	}
}

// Parent policies take the same bounded page: a run of retired-but-blocked
// parents is walked one page at a time, and the cursor only passes a parent
// once that parent itself is gone.
func TestPruneParentExaminesOnePagePerCall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	resetCursors()
	t.Cleanup(resetCursors)
	db := openCursorTestDB(t)
	const protected, page = 40, 32
	now := time.Now().UnixMilli()
	for n := 0; n < protected; n++ {
		if _, err := db.Exec(`INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,created_at_ms,updated_at_ms) VALUES(?,'Old','local','a','p','host','host-only','ended',0,?)`, fmt.Sprintf("blocked%d", n), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,created_at_ms,updated_at_ms) VALUES('retired','Old','local','a','p','host','host-only','ended',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE maintenance_retention_rows SET created_ms=CAST(record_key AS INTEGER) WHERE table_name='social_groups'`); err != nil {
		t.Fatal(err)
	}
	countGroups := func() int {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM social_groups`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	more, err := Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("full parent page not reported as backlog")
	}
	if cursor := loadCursor(db, "social_groups"); !cursor.set || cursor.created != page {
		t.Fatalf("parent cursor after first page: %+v", cursor)
	}
	if got := countGroups(); got != protected+1 {
		t.Fatalf("parent deleted before its page: %d left", got)
	}

	more, err = Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("deleted parent not reported as backlog")
	}
	if cursor := loadCursor(db, "social_groups"); !cursor.set || cursor.key != "41" {
		t.Fatalf("parent cursor after its page: %+v", cursor)
	}
	if got := countGroups(); got != protected {
		t.Fatalf("retired parent retained: %d left", got)
	}

	more, err = Sweep(context.Background(), db, time.Now(), page)
	if err != nil {
		t.Fatal(err)
	}
	if more {
		t.Fatal("end of the parent ledger still reported as backlog")
	}
	if cursor := loadCursor(db, "social_groups"); cursor.set {
		t.Fatalf("parent cursor not reset at the end of the ledger: %+v", cursor)
	}
	if got := countGroups(); got != protected {
		t.Fatalf("blocked parents removed: %d left", got)
	}
}

// The candidate page must walk the age index in its own order; a temporary
// sort would examine every aged row again on each call.
func TestCandidatePagePlanUsesTheAgeIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := openCursorTestDB(t)
	query := `SELECT record_key,created_ms FROM maintenance_retention_rows INDEXED BY maintenance_retention_age
 WHERE table_name=? AND created_ms<? AND created_ms>=? AND (created_ms>? OR (created_ms=? AND record_key>?))
 ORDER BY created_ms,record_key LIMIT ?`
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, cursorPolicyTable, time.Now().UnixMilli(), 0, 0, 0, "", 32)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "maintenance_retention_age") {
		t.Fatalf("candidate page does not use the age index:\n%s", joined)
	}
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("candidate page sorts its result:\n%s", joined)
	}
	if !strings.Contains(joined, "created_ms>?") {
		t.Fatalf("candidate page does not start at the cursor:\n%s", joined)
	}
}
