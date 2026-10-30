package metadata

// The fixture songs are compact catalogue tracks and their MusicBrainz jobs
// are minted by the catalogue writer before synthetic leases are applied.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The MusicBrainz claim once scanned mb_jobs under the write gate; with ~1M
// pending jobs every write waited behind it. The claim must stay O(batch):
// selection runs outside the gate as raw 128-row windows of mb_jobs_due, the gate
// sees one job by key, and the live-lease probe reads the bounded operations
// table through its status index. This test builds a large synthetic pending
// set and asserts all three: every claim query plan uses an index with no full
// scan, the claim still finds the due job, and a concurrent write completes
// within a small bound while the claim runs.
func TestMBClaimStaysBoundedOverLargePendingSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, names := mbLaneDB(t)
	defer db.Close()
	// The query plans are the guard at any size; the release and deep tiers also
	// time the claim and a concurrent write over 200,000 pending jobs.
	synthJobs := 5000
	if mbClaimScaleTier() {
		synthJobs = 200000
	}
	seedMBClaimJobs(t, db, synthJobs)
	// After the seed: inserting songs reset the fixture album's job to pending
	// through the song-created trigger, so exclude it again to exercise the
	// song path deterministically.
	mbClaimExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	var jobs int
	if e := db.QueryRow(`SELECT count(*) FROM mb_jobs`).Scan(&jobs); e != nil {
		t.Fatal(e)
	}
	if jobs != synthJobs+2 {
		t.Fatalf("synthetic pending set has %d jobs, not %d", jobs, synthJobs+2)
	}
	stamp := "2026-09-24T12:00:00Z"
	// The live-lease probe is the production statement: a lease range over the
	// partial operations index. It is a bounded index seek, and it is checked
	// before any due-job work.
	mbClaimPlan(t, db, "live-lease probe", `SELECT count(*) FROM mb_publication_operations WHERE status IN('claimed','staged') AND lease_until>?`, "mb_operations_status", stamp)
	// The claim picks from the same raw 128-row MATERIALIZED due window the
	// production picker uses, then evaluates mbClaimable per due row. The
	// window must stay an mb_jobs_due seek; the only permitted scan is the
	// recursive ancestor step, which is separately bounded to five points.
	assertMBProductionWindow(t)
	mbClaimPlan(t, db, "raw due window", mbClaimRawDueWindow, "mb_jobs_due", stamp, stamp, "", "", "", 0)
	mbClaimablePlan(t, db, "due window", mbClaimDueWindow, "mb_jobs_due", stamp, stamp, "", "", "", 0, stamp, stamp, stamp)
	mbClaimablePlan(t, db, "gated re-check", `SELECT kind,entity_id,selected_id,manual,review_search,generation,attempts FROM mb_jobs WHERE kind=? AND entity_id=? AND `+mbClaimable, "sqlite_autoindex_mb_jobs_1", "song", names["song"].ID, stamp, stamp, stamp)

	s := New(db, "")
	claimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	j, e := s.claimMB(claimCtx)
	elapsed := time.Since(start)
	if e != nil || j == nil {
		t.Fatal("claim over the synthetic set found nothing", j, e)
	}
	if j.kind != "song" || j.id != names["song"].Public || j.lease == "" || j.until == "" {
		t.Fatalf("claim returned the wrong job: %+v", j)
	}
	t.Logf("claim over %d pending jobs took %s", jobs, elapsed)

	// The claim leased the song; release it so the concurrency pass claims again.
	mbClaimExec(t, db, `UPDATE mb_jobs SET lease='',lease_until='' WHERE kind='song' AND entity_id=?;DELETE FROM mb_publication_operations`, names["song"].ID)
	done := make(chan error, 1)
	go func() {
		_, e := s.claimMB(claimCtx)
		done <- e
	}()
	writeStart := time.Now()
	_, e = dbwork.ExecWrite(claimCtx, db, dbwork.ClassBackgroundMedia, `UPDATE catalog_entities SET title='Concurrent write',sort_key=portico_sort_title('Concurrent write',sort_title,metadata_language),sort_head=substr(portico_sort_title('Concurrent write',sort_title,metadata_language),1,1) WHERE id=?`, names["song"].ID)
	writeElapsed := time.Since(writeStart)
	if e != nil {
		t.Fatal("concurrent write failed while the claim ran", e)
	}
	if e = <-done; e != nil {
		t.Fatal("concurrent claim failed", e)
	}
	t.Logf("concurrent write during the claim took %s", writeElapsed)
	if writeElapsed > 10*time.Second {
		t.Fatalf("concurrent write blocked %s behind the claim", writeElapsed)
	}
}

// seedMBClaimJobs adds n pending song jobs that sort before the fixture's own
// song in claim order but can never be claimed: their leases are held to 2099,
// so the claim's index walk must step over every one of them through
// mb_jobs_due without touching the write gate. Inserts go through the real
// catalog tables so the triggers mint the jobs exactly as a scan would.
func seedMBClaimJobs(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	c := catalogtest.New(t, db)
	var album int64
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE kind=? AND title='Local Album'`, int(compactcatalog.Album)).Scan(&album); err != nil {
		t.Fatal(err)
	}
	library := c.Handle("lib")
	start := time.Now()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("/music/aa%06d.flac", i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: library, Kind: compactcatalog.Track, Parent: album,
				Key: compactcatalog.ItemKey("/music", path, 0), Title: fmt.Sprintf("Synth %06d", i),
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"album_id": album, "disc_number": 1, "track_number": i + 1}); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE mb_jobs SET lease='synthetic',lease_until='2099-01-01T00:00:00Z' WHERE kind='song' AND entity_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	t.Logf("seeded %d synthetic pending jobs in %s", n, time.Since(start))
}

// This local copy keeps the bounded-claim test independent of the unassigned
// performance_tier_test.go helper.
func mbClaimScaleTier() bool {
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
		return true
	}
	return false
}

// mbClaimDueWindow is the production picker's raw window: the due rows are
// taken first by the mb_jobs_due order and LIMIT 128, then mbClaimable is
// evaluated per due row. It is the statement the claim actually runs, not a
// whole-table LIMIT 1 probe.
const mbClaimRawDueWindow = `SELECT * FROM mb_jobs WHERE status='pending' AND next_attempt<=? AND lease_until<=?
 AND (next_attempt,lease_until,kind,entity_id)>(?,?,?,?)
 ORDER BY next_attempt,lease_until,kind,entity_id LIMIT 128`

const mbClaimDueWindow = `WITH due AS MATERIALIZED (
 ` + mbClaimRawDueWindow + `)
 SELECT mb_jobs.kind,pid(e.public_id),mb_jobs.next_attempt,mb_jobs.lease_until,mb_jobs.entity_id,(` + mbClaimable + `) FROM due AS mb_jobs JOIN catalog_entities e ON e.id=mb_jobs.entity_id ORDER BY mb_jobs.next_attempt,mb_jobs.lease_until,mb_jobs.kind,mb_jobs.entity_id`

// mbClaimPlan asserts the query is served by the named index: every plan node
// is an index search, with no table scan and no sort spill. A scan of mb_jobs
// here would be O(jobs) per claim.
func mbClaimPlan(t *testing.T, db *sql.DB, name, query, index string, args ...any) {
	t.Helper()
	rows, e := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	seen := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if e = rows.Scan(&a, &b, &c, &detail); e != nil {
			t.Fatal(e)
		}
		t.Logf("PLAN %s | %s", name, detail)
		if strings.Contains(detail, "SCAN") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatalf("claim query is not index-bounded: %s | %s", name, detail)
		}
		if strings.Contains(detail, "SEARCH") && strings.Contains(detail, index) {
			seen = true
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatalf("claim query does not use %s: %s", index, name)
	}
}

// Pin the complete production SQL before permitting its bounded CTE scans.
// A changed picker requires reviewing the bounds again; matching a fragment
// cannot approve another query that happens to contain a LIMIT.
func assertMBProductionWindow(t *testing.T) {
	t.Helper()
	source, err := os.ReadFile("mb_mutation_claim.go")
	if err != nil {
		t.Fatal(err)
	}
	const start = "rows, err := read.QueryContext(ctx, `"
	_, rest, ok := strings.Cut(string(source), start)
	if !ok {
		t.Fatal("production due query not found")
	}
	query, _, ok := strings.Cut(rest, "`, stamp, stamp, cursor.Next")
	if !ok {
		t.Fatal("production due query boundary changed")
	}
	query = strings.ReplaceAll(query, "`+mbClaimable+`", mbClaimable)
	if strings.Join(strings.Fields(query), " ") != strings.Join(strings.Fields(mbClaimDueWindow), " ") {
		t.Fatal("production due query changed: review its indexed 128-row window")
	}
}

// The due alias materializes at most 128 indexed rows. The ancestor coroutine
// seeds one row and follows at most four parent primary keys. All base tables
// must still seek; no sort spill or other scan is permitted.
func mbClaimablePlan(t *testing.T, db *sql.DB, name, query, index string, args ...any) {
	t.Helper()
	const ancestors = `NOT EXISTS(WITH RECURSIVE ancestors(id,depth) AS (SELECT mb_jobs.entity_id,0 UNION ALL SELECT e.parent_id,a.depth+1 FROM ancestors a JOIN catalog_entities e ON e.id=a.id WHERE e.parent_id IS NOT NULL AND a.depth<4)`
	if !strings.Contains(query, ancestors) {
		t.Fatal("ancestor query is not the exact five-point CTE")
	}
	due := query == mbClaimDueWindow
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	seen, entityIndex, targetIndex := false, false, false
	dueScans, ancestorScans, constants := 0, 0, 0
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		t.Logf("PLAN %s | %s", name, detail)
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatalf("claim statement spills a sort: %s | %s", name, detail)
		}
		if strings.Contains(detail, "SCAN") {
			switch detail {
			case "SCAN mb_jobs":
				if !due {
					t.Fatalf("base mb_jobs scan outside pinned materialization: %s", name)
				}
				dueScans++
			case "SCAN a":
				ancestorScans++
			case "SCAN CONSTANT ROW":
				constants++
			default:
				t.Fatalf("claim statement has an unbounded scan: %s | %s", name, detail)
			}
		}
		if strings.HasPrefix(detail, "SEARCH ") && strings.Contains(detail, index) {
			seen = true
		}
		if detail == "SEARCH m USING INDEX catalog_entity_mutations_entity_fk (entity_id=?)" {
			entityIndex = true
		}
		if detail == "SEARCH m USING INDEX catalog_entity_mutations_target_fk (target_id=?)" {
			targetIndex = true
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seen || !entityIndex || !targetIndex {
		t.Fatalf("missing required claim/mutation index seeks: %s", name)
	}
	if ancestorScans != 2 || constants != 1 || dueScans != map[bool]int{true: 1, false: 0}[due] {
		t.Fatalf("unexpected materialization nodes: due=%d ancestors=%d seed=%d", dueScans, ancestorScans, constants)
	}
	pointParent := false
	for i, node := range plan {
		if node == "RECURSIVE STEP" && i+2 < len(plan) && plan[i+1] == "SCAN a" && plan[i+2] == "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
			pointParent = true
		}
	}
	if !pointParent {
		t.Fatal("ancestor recursion does not use a parent primary-key seek")
	}
}

func mbClaimExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, e := db.Exec(query, args...); e != nil {
		t.Fatal(query, e)
	}
}

// While MusicBrainz asks for a pause, the claim must not walk the pending set
// at all: every job fails the cooldown condition, so the walk read the whole
// backlog (every song) once per step for nothing, throughout every rate-limit
// pause.
func TestMBClaimSkipsTheBacklogDuringACooldown(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	seedMBClaimJobs(t, db, 500)
	mbClaimExec(t, db, `INSERT INTO metadata_provider_cooldowns(provider,next_attempt) VALUES('musicbrainz','2999-01-01T00:00:00Z') ON CONFLICT(provider) DO UPDATE SET next_attempt=excluded.next_attempt`)
	s := New(db, "")
	ctx, statements := dbwork.TraceStatements(context.Background())
	j, e := s.claimMB(ctx)
	if e != nil || j != nil {
		t.Fatal("a job was claimed during the cooldown", j, e)
	}
	for _, statement := range statements() {
		if strings.Contains(statement, "FROM mb_jobs") {
			t.Fatalf("the claim searched the backlog during a cooldown: %s", statement)
		}
	}
}
