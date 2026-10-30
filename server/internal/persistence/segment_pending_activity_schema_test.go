package persistence_test

import (
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The projection is only exact if every supported segment_files mutation keeps
// it exact, so this compares it with the aggregate it replaced after every step
// of a deterministic sequence. A fixed seed keeps a failure reproducible.
const segmentPendingSequenceSteps = 3000

type segmentPendingRow struct {
	library string
	count   int64
	max     int64
	min     int64
}

func openSegmentPendingBaseline(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func readSegmentPendingRows(t *testing.T, db *sql.DB, query string) []segmentPendingRow {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []segmentPendingRow{}
	for rows.Next() {
		var row segmentPendingRow
		if err = rows.Scan(&row.library, &row.count, &row.max, &row.min); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSegmentPendingProjection(t *testing.T, db *sql.DB, step int, statement string) {
	t.Helper()
	got := readSegmentPendingRows(t, db,
		`SELECT library_id,pending_count,max_updated_ms,min_updated_ms FROM segment_pending_activity ORDER BY library_id`)
	want := readSegmentPendingRows(t, db,
		`SELECT library_id,count(*),max(updated_ms),min(updated_ms) FROM segment_files WHERE state='pending' GROUP BY library_id ORDER BY library_id`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("step %d (%s) left the projection at %+v, pending rows are %+v", step, statement, got, want)
	}
}

func TestSegmentPendingActivityTracksPendingRowsExactly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := openSegmentPendingBaseline(t)
	assertSegmentPendingProjection(t, db, 0, "empty baseline")
	rng := rand.New(rand.NewSource(20261028))
	libraries := []string{"", "lib-a", "lib-b", "lib-c", "orphan-9f3"}
	states := []string{"pending", "pending", "pending", "complete", "failed", "unsupported"}
	modes := []string{"standard", "thorough"}
	objects := []string{}
	next := 0
	randomObject := func() string {
		if len(objects) == 0 {
			return "obj-missing"
		}
		return objects[rng.Intn(len(objects))]
	}
	randomLibrary := func() string { return libraries[rng.Intn(len(libraries))] }
	randomState := func() string { return states[rng.Intn(len(states))] }
	// Repeated and negative timestamps keep ties, a moved MAX and a moved MIN
	// common rather than rare.
	timestamp := func() int64 { return int64(rng.Intn(5)-1) * 250 }
	upsert := `INSERT INTO segment_files(object_id,source_revision,library_id,mode,planning_policy,state,attempt,next_ms,error_code,result_id,match_digest,updated_ms)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(object_id) DO UPDATE SET source_revision=excluded.source_revision,library_id=excluded.library_id,mode=excluded.mode,
 planning_policy=excluded.planning_policy,state=excluded.state,attempt=0,next_ms=0,error_code='',result_id='',match_digest='',updated_ms=excluded.updated_ms`
	var statement string
	var args []any
	step := 0
	for step = 1; step <= segmentPendingSequenceSteps; step++ {
		args = nil
		switch rng.Intn(15) {
		case 0, 1, 2:
			id := fmt.Sprintf("obj-%04d", next)
			next++
			objects = append(objects, id)
			statement = upsert
			args = []any{id, "rev-1", randomLibrary(), modes[rng.Intn(len(modes))], "", randomState(), rng.Intn(3), 0, "", "", "", timestamp()}
		case 3, 4:
			id := randomObject()
			if rng.Intn(4) == 0 {
				id = fmt.Sprintf("obj-%04d", next)
				next++
				objects = append(objects, id)
			}
			statement = upsert
			args = []any{id, "rev-1", randomLibrary(), modes[rng.Intn(len(modes))], "", randomState(), 0, 0, "", "", "", timestamp()}
		case 5:
			statement = `DELETE FROM segment_files WHERE object_id=?`
			args = []any{randomObject()}
		case 6:
			statement = `UPDATE segment_files SET state=? WHERE object_id=?`
			args = []any{randomState(), randomObject()}
		case 7:
			statement = `UPDATE segment_files SET library_id=? WHERE object_id=?`
			args = []any{randomLibrary(), randomObject()}
		case 8:
			statement = `UPDATE segment_files SET updated_ms=? WHERE object_id=?`
			args = []any{timestamp(), randomObject()}
		case 9:
			statement = `UPDATE segment_files SET state=?,updated_ms=? WHERE object_id=?`
			args = []any{randomState(), timestamp(), randomObject()}
		case 10:
			statement = `UPDATE segment_files SET library_id=?,state=?,updated_ms=? WHERE object_id=?`
			args = []any{randomLibrary(), randomState(), timestamp(), randomObject()}
		case 11:
			// Columns outside the projection's key must never cause a write.
			statement = `UPDATE segment_files SET attempt=attempt+1,next_ms=?,error_code=?,result_id=?,match_digest=?,show_id=?,mode=?,planning_policy=?,source_revision=? WHERE object_id=?`
			args = []any{rng.Intn(60000), "e", "r", "d", rng.Intn(1000), modes[rng.Intn(len(modes))], "policy", "rev-2", randomObject()}
		case 12:
			// A bulk requeue enters or re-times every row of one library.
			statement = `UPDATE segment_files SET state='pending',attempt=0,next_ms=0,error_code='',match_digest='',updated_ms=? WHERE library_id=?`
			args = []any{timestamp(), randomLibrary()}
		case 13:
			statement = `DELETE FROM segment_files WHERE library_id=? AND state='pending'`
			args = []any{randomLibrary()}
		case 14:
			// A no-op of every projected column.
			id := randomObject()
			statement = `UPDATE segment_files SET library_id=library_id,state=state,updated_ms=updated_ms WHERE object_id=?`
			args = []any{id}
		}
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatalf("step %d (%s) failed: %v", step, statement, err)
		}
		assertSegmentPendingProjection(t, db, step, statement)
	}
	if len(objects) < 20 {
		t.Fatalf("the sequence only created %d rows", len(objects))
	}
	var pending int64
	if err := db.QueryRow(`SELECT count(*) FROM segment_files WHERE state='pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending == 0 {
		t.Fatal("the sequence left no pending rows, so the comparison proved nothing")
	}
}

func TestSegmentPendingActivityTickReadNeverTouchesSegmentFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := openSegmentPendingBaseline(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 'segments:'||library_id,pending_count||':'||max_updated_ms FROM segment_pending_activity`)
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
	if strings.Contains(joined, "segment_files") {
		t.Fatalf("the 1s tick still reads segment_files:\n%s", joined)
	}
	if !strings.Contains(joined, "segment_pending_activity") {
		t.Fatalf("the 1s tick does not read the projection:\n%s", joined)
	}
}
