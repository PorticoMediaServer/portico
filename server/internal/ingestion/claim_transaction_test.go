package ingestion

// PERF-SRV-026: the per-quantum claim (queued->running plus last_turn) is one
// gated transaction. These tests drive the real processQuantum entry point
// rather than a helper, because the finding was a redundant admission at that
// call site: a helper test would keep passing if the old call sites came back.
//
// The claim seam is measured exactly by making the quantum stop immediately
// after it: a paused or cancelled run fails the status read that follows the
// claim, so the only gated transactions in that quantum are the claim's.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const (
	claimStatusStatement = `UPDATE jobs SET status='running' WHERE id=? AND status='queued'`
	claimTurnStatement   = `UPDATE inventory_runs SET last_turn=? WHERE job_id=?`
	claimReadStatement   = `SELECT r.source_id,j.status,r.phase FROM inventory_runs r JOIN jobs j ON j.id=r.job_id WHERE r.job_id=?`
	healthStatement      = `UPDATE library_sources SET health='inventory_running' WHERE id=? AND health!='inventory_running'`
	phaseStatement       = `UPDATE inventory_runs SET pause_reason='',phase='basic_running' WHERE job_id=?`
	fenceStatement       = `SELECT r.source_id FROM inventory_runs r JOIN jobs j ON j.id=r.job_id JOIN library_sources s`
)

type claimFixture struct {
	t   *testing.T
	db  *sql.DB
	s   *Service
	job Job
	lib string
}

// newClaimFixture builds one library and one queued scan. Analysis is limited
// to the operations listed, which keeps a quantum to local naming work.
func newClaimFixture(t *testing.T, operations string, files ...string) *claimFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalog.New(db)
	lib, err := c.Create("Library", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='basic',operations_json=? WHERE library_id=?`, operations, lib.ID); err != nil {
		t.Fatal(err)
	}
	f := &claimFixture{t: t, db: db, s: New(db, c, assets.Probe{}), lib: lib.ID}
	for _, file := range files {
		path := filepath.Join(root, file)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.job, err = f.s.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *claimFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// state is the owner-visible snapshot the claim and its neighbours own.
func (f *claimFixture) state() (status, phase, lastTurn, health string, processed, analyzed int) {
	f.t.Helper()
	err := f.db.QueryRow(`SELECT j.status,r.phase,r.last_turn,s.health,j.processed,r.analyzed FROM jobs j JOIN inventory_runs r ON r.job_id=j.id JOIN library_sources s ON s.id=r.source_id WHERE j.id=?`, f.job.ID).Scan(&status, &phase, &lastTurn, &health, &processed, &analyzed)
	if err != nil {
		f.t.Fatal(err)
	}
	return status, phase, lastTurn, health, processed, analyzed
}

// normalized drops the commit bookkeeping statement (rowChanges reads SQLite's
// change counter inside the transaction) so a trace shows application work.
func normalized(statements []string) []string {
	out := make([]string, 0, len(statements))
	for _, statement := range statements {
		line := strings.Join(strings.Fields(statement), " ")
		if line == `SELECT total_changes()` {
			continue
		}
		out = append(out, line)
	}
	return out
}

// writerAdmissions counts acquisitions of the single write gate. It is the
// resource the finding is about: two statements admitted separately hold the
// writer twice even when one of them changes nothing.
func writerAdmissions() uint64 { return dbwork.WriteGate().Stats().Acquired }

func indexOf(statements []string, want string) int {
	for i, statement := range statements {
		if statement == want {
			return i
		}
	}
	return -1
}

func indexOfPrefix(statements []string, prefix string) int {
	for i, statement := range statements {
		if strings.HasPrefix(statement, prefix) {
			return i
		}
	}
	return -1
}

// A refused quantum takes exactly one gated transaction for the claim, issues
// the same two statements in the same order, and still reads back the status a
// pause committed. Two writer admissions here is the pre-fix shape.
func TestScanClaimIsOneGatedTransactionAtTheSeam(t *testing.T) {
	f := newClaimFixture(t, `[]`)
	if err := f.s.Control(context.Background(), f.job.ID, "pause", nil); err != nil {
		t.Fatal(err)
	}
	_, pausedPhase, _, pausedHealth, _, _ := f.state()

	started := time.Now()
	admissions := writerAdmissions()
	ctx, cost := dbwork.Measure(context.Background())
	ctx, statementList := dbwork.TraceStatements(ctx)
	if f.s.processQuantum(ctx, f.job.ID, f.lib) {
		t.Fatal("a paused run was reported as progress")
	}
	if got := writerAdmissions() - admissions; got != 1 {
		t.Fatalf("the claim took %d writer admissions, want exactly 1", got)
	}
	if got := cost().Transactions; got != 1 {
		t.Fatalf("the claim ran in %d database transactions, want exactly 1", got)
	}
	trace := normalized(statementList())
	want := []string{claimStatusStatement, claimTurnStatement, claimReadStatement}
	if len(trace) != len(want) {
		t.Fatalf("refused quantum statements = %q, want %q", trace, want)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("statement %d = %q, want %q", i, trace[i], want[i])
		}
	}

	status, phase, lastTurn, health, processed, analyzed := f.state()
	if status != "paused" || phase != pausedPhase || health != pausedHealth || processed != 0 || analyzed != 0 {
		t.Fatalf("refused claim changed state: status=%s phase=%s health=%s processed=%d analyzed=%d", status, phase, health, processed, analyzed)
	}
	turn, err := time.Parse(time.RFC3339Nano, lastTurn)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Before(started) {
		t.Fatalf("last_turn %s predates the quantum that wrote it", lastTurn)
	}
}

// The turn is sampled inside the writer after the status statement, not when
// the quantum was entered and then queued behind the gate: a timestamp taken
// before admission would predate the release below.
func TestScanClaimSamplesTheTurnAfterWriterAdmission(t *testing.T) {
	f := newClaimFixture(t, `[]`)
	if err := f.s.Control(context.Background(), f.job.ID, "pause", nil); err != nil {
		t.Fatal(err)
	}
	release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-released:
		default:
			release()
		}
	})
	done := make(chan bool, 1)
	go func() { done <- f.s.processQuantum(context.Background(), f.job.ID, f.lib) }()
	// Wait until the claim is actually queued behind the held gate, so anything
	// sampled before admission is unambiguously older than the release.
	deadline := time.Now().Add(10 * time.Second)
	for dbwork.WriteGate().Stats().Waiting["background-media"] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the quantum never reached the claim")
		}
		time.Sleep(time.Millisecond)
	}
	queued := time.Now()
	time.Sleep(2 * time.Millisecond)
	admitted := time.Now()
	release()
	close(released)
	select {
	case progress := <-done:
		if progress {
			t.Fatal("a paused run was reported as progress")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("quantum did not finish after the writer was released")
	}
	_, _, lastTurn, _, _, _ := f.state()
	turn, err := time.Parse(time.RFC3339Nano, lastTurn)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Before(queued) || turn.Before(admitted) {
		t.Fatalf("last_turn %s was sampled at or before admission (%s), want after the claim was admitted at %s", lastTurn, queued.Format(time.RFC3339Nano), admitted.Format(time.RFC3339Nano))
	}
}

// If the second statement of the claim fails, neither it nor the status change
// commits. The pre-fix shape committed the status first, so the job was left
// running with no work in flight.
func TestScanClaimSecondStatementFailureRollsBothBack(t *testing.T) {
	f := newClaimFixture(t, `[]`)
	const untouchedTurn = "2000-01-01T00:00:00Z"
	f.exec(`UPDATE inventory_runs SET last_turn=? WHERE job_id=?`, untouchedTurn, f.job.ID)
	_, queuedPhase, _, queuedHealth, _, _ := f.state()

	f.exec(`CREATE TRIGGER claim_turn_abort BEFORE UPDATE OF last_turn ON inventory_runs BEGIN SELECT RAISE(ABORT,'injected claim failure'); END`)
	published, admissions := dbwork.Publications(), writerAdmissions()
	ctx, cost := dbwork.Measure(context.Background())
	ctx, statementList := dbwork.TraceStatements(ctx)
	if f.s.processQuantum(ctx, f.job.ID, f.lib) {
		t.Fatal("a failed claim was reported as progress")
	}
	trace := normalized(statementList())
	if len(trace) != 2 || trace[0] != claimStatusStatement || trace[1] != claimTurnStatement {
		t.Fatalf("failed claim statements = %q, want the claim pair only", trace)
	}
	status, phase, lastTurn, health, processed, analyzed := f.state()
	if status != "queued" {
		t.Fatalf("failed claim left the job %s instead of truthfully queued", status)
	}
	if lastTurn != untouchedTurn {
		t.Fatalf("failed claim moved last_turn to %s", lastTurn)
	}
	if phase != queuedPhase || health != queuedHealth || processed != 0 || analyzed != 0 {
		t.Fatalf("failed claim changed state: phase=%s health=%s processed=%d analyzed=%d", phase, health, processed, analyzed)
	}
	if got := dbwork.Publications(); got != published {
		t.Fatal("a rolled-back claim published a transaction")
	}
	if got := writerAdmissions() - admissions; got != 1 {
		t.Fatalf("the failed claim took %d writer admissions, want exactly 1", got)
	}
	if got := cost().Transactions; got != 1 {
		t.Fatalf("the failed claim ran in %d database transactions, want exactly 1", got)
	}

	// With the injection removed the same queued run is picked up and works.
	f.exec(`DROP TRIGGER claim_turn_abort`)
	if !f.s.processQuantum(context.Background(), f.job.ID, f.lib) {
		t.Fatal("the queued run was not picked up after the failed claim")
	}
	status, _, lastTurn, health, _, _ = f.state()
	if status == "queued" || status == "failed" || lastTurn == untouchedTurn || health != "inventory_running" {
		t.Fatalf("resumed claim did not run: status=%s health=%s last_turn=%s", status, health, lastTurn)
	}
}

// A cancel that committed first is not revived by the conditional status
// update, and no health transition follows it.
func TestScanClaimDoesNotReviveACancelledRun(t *testing.T) {
	f := newClaimFixture(t, `[]`)
	if err := f.s.Cancel(f.job.ID); err != nil {
		t.Fatal(err)
	}
	_, cancelledPhase, _, cancelledHealth, _, _ := f.state()

	started, admissions := time.Now(), writerAdmissions()
	ctx, cost := dbwork.Measure(context.Background())
	ctx, mutationList := dbwork.TraceStatements(ctx)
	if f.s.processQuantum(ctx, f.job.ID, f.lib) {
		t.Fatal("a cancelled run was reported as progress")
	}
	if got := writerAdmissions() - admissions; got != 1 {
		t.Fatalf("the claim took %d writer admissions, want exactly 1", got)
	}
	if got := cost().Transactions; got != 1 {
		t.Fatalf("the claim ran in %d database transactions, want exactly 1", got)
	}
	trace := normalized(mutationList())
	if len(trace) != 3 || trace[0] != claimStatusStatement || trace[1] != claimTurnStatement || trace[2] != claimReadStatement {
		t.Fatalf("cancelled quantum statements = %q", trace)
	}
	status, phase, lastTurn, health, _, _ := f.state()
	if status != "cancelled" || phase != cancelledPhase || health != cancelledHealth || health == "inventory_running" {
		t.Fatalf("cancelled claim changed state: status=%s phase=%s health=%s", status, phase, health)
	}
	turn, err := time.Parse(time.RFC3339Nano, lastTurn)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Before(started) {
		t.Fatalf("last_turn %s predates the quantum that wrote it", lastTurn)
	}
}

// The grouping is only the claim pair. A successful quantum still writes the
// health fence after the claim, and analysis still publishes basic_running in
// its own transaction before its work, with the ordinary finish unchanged.
func TestSuccessfulQuantumKeepsItsHealthAndPhaseFences(t *testing.T) {
	f := newClaimFixture(t, `["local_metadata"]`, "Film (2000) {tmdb-1}/Film.mkv")

	var sawHealth, sawPhase bool
	for n := 0; n < 60; n++ {
		state, err := f.s.Get(f.job.ID)
		if err != nil {
			t.Fatal(err)
		}
		status, phase := state.Status, state.Phase
		if status == "complete" || status == "complete_with_warnings" {
			break
		}
		if status == "failed" {
			t.Fatalf("scan failed: %+v", state)
		}
		ctx, cost := dbwork.Measure(context.Background())
		ctx, statementList := dbwork.TraceStatements(ctx)
		f.s.processQuantum(ctx, f.job.ID, f.lib)
		trace := normalized(statementList())
		if len(trace) == 0 {
			t.Fatalf("quantum %d issued no statements", n)
		}
		if trace[0] != claimStatusStatement && trace[0] != claimTurnStatement {
			// The claim's status statement is a no-op after the first quantum, but
			// it is still issued first: nothing else may precede the claim pair.
			t.Fatalf("quantum began with %q", trace[0])
		}
		if got := cost().Transactions; got < 1 {
			t.Fatalf("quantum took %d transactions", got)
		}
		claimRead := indexOf(trace, claimReadStatement)
		if claimRead < 0 {
			t.Fatalf("quantum %d issued no post-claim status read: %q", n, trace)
		}
		switch phase {
		case "inventory":
			if health := indexOf(trace, healthStatement); health >= 0 {
				sawHealth = true
				fence := indexOfPrefix(trace, fenceStatement)
				if fence < claimRead || fence > health {
					t.Fatalf("InventoryFenceTx is no longer re-proved between the claim read and the health transition: %q", trace)
				}
			}
		case "basic_running", "basic_pending":
			if staged := indexOf(trace, phaseStatement); staged >= 0 {
				sawPhase = true
				if staged < claimRead {
					t.Fatalf("basic_running ran before the claim read: %q", trace)
				}
			}
		}
	}
	status, phase, _, health, processed, analyzed := f.state()
	if status != "complete" && status != "complete_with_warnings" {
		t.Fatalf("scan did not finish: status=%s phase=%s processed=%d analyzed=%d", status, phase, processed, analyzed)
	}
	if !sawHealth {
		t.Fatal("a successful inventory quantum no longer writes the inventory_running health fence")
	}
	if !sawPhase {
		t.Fatal("analysis no longer publishes basic_running before its work")
	}
	if health == "inventory_running" {
		t.Fatalf("finished scan left source health %s", health)
	}
}
