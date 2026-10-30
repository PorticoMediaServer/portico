package catalog

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// PERF-S22: admission bounds a profile's jobs in flight, never its jobs over
// time. Ten thousand finished jobs this month don't refuse the next one (the
// old 30-day quota did, and 100,000 server-wide locked everyone out); 32
// active jobs do, and the slot frees the moment one finishes.
func TestBulkAdmissionCountsJobsInFlightNotHistory(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 3)
	now := time.Now().UnixMilli()
	insert := func(from, to int, state string) {
		t.Helper()
		for i := from; i < to; i++ {
			if _, e := s.db.Exec(`INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms) VALUES(?,?,?,'h','{}','{}','{}',?,?,?)`, fmt.Sprintf("seed-%05d", i), v.Profile, fmt.Sprintf("seed-op-%05d", i), state, now, now); e != nil {
				t.Fatal(e)
			}
		}
	}
	insert(0, 10000, "complete")
	yes := true
	job := func(op string) (BulkJob, error) {
		return s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: op, Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	}
	if _, e := job("after-history"); e != nil {
		t.Fatalf("10,000 finished jobs refused a new one: %v", e)
	}
	insert(10000, 10000+MaxActiveBulkJobs-1, "queued")
	if _, e := job("over-the-limit"); !errors.Is(e, ErrPersonalCapacity) {
		t.Fatalf("job %d in flight admitted: %v", MaxActiveBulkJobs+1, e)
	}
	// Another profile is not affected by this one's jobs in flight.
	other := p
	other.ProfileID = "other"
	ov := v
	ov.Profile = identity.PersonalKey(other.Viewer)
	if _, e := s.CreateBulkJob(context.Background(), other, ov, JobRequest{OperationID: "other-profile", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access); e != nil {
		t.Fatalf("another profile was refused: %v", e)
	}
	if _, e := s.db.Exec(`UPDATE personal_jobs SET state='complete' WHERE id='seed-10000'`); e != nil {
		t.Fatal(e)
	}
	if _, e := job("slot-freed"); e != nil {
		t.Fatalf("a finished job did not free its slot: %v", e)
	}
	// The count is a seek over the profile's active jobs (0221), not a walk of its history.
	plan := bulkQueryPlan(t, s, `SELECT count(*) FROM personal_jobs WHERE profile_id=? AND state IN('building','queued','running')`, v.Profile)
	if !strings.Contains(plan, "personal_jobs_active") {
		t.Fatalf("admission count does not use the active-jobs index:\n%s", plan)
	}
}

// A finished job keeps its progress and failures page but not its captured
// membership: the rows that ran it go before the operation ends, in short
// batches, so marking a library-sized selection leaves no library-sized table.
func TestFinishedBulkJobDropsItsMembership(t *testing.T) {
	testtier.Media(t, "a 2,600-item bulk job run to completion")
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2600)
	yes := true
	j, e := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "purge", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes}}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	step := s.BulkAdapter(access).Step
	phases := map[string]bool{}
	for i := 0; ; i++ {
		obs, e := step(context.Background(), j.JobID)
		if e != nil {
			t.Fatal(e)
		}
		phases[obs.Phase] = true
		if obs.State != "running" {
			if obs.State != "succeeded" || obs.Phase != "complete" {
				t.Fatalf("final observation %+v", obs)
			}
			break
		}
		if i > 10000 {
			t.Fatal("the job never finished")
		}
	}
	var items, done int
	if e = s.db.QueryRow(`SELECT count(*) FROM personal_job_items WHERE job_id=?`, j.JobID).Scan(&items); e != nil || items != 0 {
		t.Fatalf("captured membership left after the job: %d (%v)", items, e)
	}
	if e = s.db.QueryRow(`SELECT done FROM personal_jobs WHERE id=?`, j.JobID).Scan(&done); e != nil || done != 2600 {
		t.Fatalf("progress lost: %d (%v)", done, e)
	}
	read, e := s.BulkJob(v.Profile, j.JobID)
	if e != nil || read.State != "complete" || read.Done != 2600 {
		t.Fatalf("finished job no longer readable: %+v %v", read, e)
	}
	// The operationId still replays the same job.
	replay, e := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "purge", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes}}, scheduler, access)
	if e != nil || replay.JobID != j.JobID {
		t.Fatalf("replay after purge: %+v %v", replay, e)
	}
}

// Finished jobs are forgotten after the 30-day receipt window, a batch per
// write; recent finished jobs and old unfinished ones stay.
func TestCleanupBulkJobsForgetsOldFinishedJobs(t *testing.T) {
	s, _, _, v, _, _, _ := bulkFixture(t, 1)
	old := time.Now().Add(-bulkRetention - time.Hour).UnixMilli()
	recent := time.Now().UnixMilli()
	for _, j := range []struct {
		id, state string
		at        int64
		items     int
	}{{"old-done", "partial", old, 4500}, {"old-failed", "failed", old, 0}, {"recent-done", "complete", recent, 3}, {"old-running", "running", old, 3}} {
		if _, e := s.db.Exec(`INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms) VALUES(?,?,?,'h','{}','{}','{}',?,?,?)`, j.id, v.Profile, "op-"+j.id, j.state, j.at, j.at); e != nil {
			t.Fatal(e)
		}
		if _, e := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO personal_job_items(job_id,ordinal,item_id) SELECT ?,x,x FROM n WHERE ?>0`, j.items, j.id, j.items); e != nil {
			t.Fatal(e)
		}
		if _, e := s.db.Exec(`INSERT INTO personal_job_failures(job_id,ordinal,item_id,code) VALUES(?,1,1,'not_found')`, j.id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.db.Exec(`INSERT INTO personal_job_destinations(job_id,kind,target_id,revision) VALUES('old-done','playlist-add','pl',1)`); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 20; i++ {
		if e := s.CleanupBulkJobs(context.Background(), time.Second); e != nil {
			t.Fatal(e)
		}
	}
	count := func(q string, args ...any) int {
		var n int
		if e := s.db.QueryRow(q, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	for _, id := range []string{"old-done", "old-failed"} {
		if n := count(`SELECT count(*) FROM personal_jobs WHERE id=?`, id) + count(`SELECT count(*) FROM personal_job_items WHERE job_id=?`, id) + count(`SELECT count(*) FROM personal_job_failures WHERE job_id=?`, id) + count(`SELECT count(*) FROM personal_job_destinations WHERE job_id=?`, id); n != 0 {
			t.Fatalf("%s: %d rows left", id, n)
		}
	}
	for _, id := range []string{"recent-done", "old-running"} {
		if count(`SELECT count(*) FROM personal_jobs WHERE id=?`, id) != 1 || count(`SELECT count(*) FROM personal_job_items WHERE job_id=?`, id) != 3 {
			t.Fatalf("%s was removed", id)
		}
	}
}

func bulkQueryPlan(t *testing.T, s *Service, query string, args ...any) string {
	t.Helper()
	rows, e := s.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		plan += detail + "\n"
	}
	return plan
}

// A crash in the middle of the purge loses nothing: the operation reports
// "cleanup" (never succeeded) while membership rows remain, a restarted
// service carries on from what is left, and only then does it complete.
func TestBulkPurgeResumesAfterRestartAndGatesCompletion(t *testing.T) {
	testtier.Media(t, "a 4,500-item bulk job run to completion")
	s, scheduler, p, v, access, _, names := bulkFixture(t, 4500)
	yes := true
	j, e := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "purge-restart", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	for j.State != "complete" {
		if j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access); e != nil {
			t.Fatal(e)
		}
		if j.State == "failed" || j.State == "partial" {
			t.Fatalf("job ended %+v", j)
		}
	}
	// One purge batch per step: the deadline is already past when it starts.
	grace := bulkPurgeGrace
	bulkPurgeGrace = -time.Hour
	defer func() { bulkPurgeGrace = grace }()
	left := func() int {
		var n int
		if e := s.db.QueryRow(`SELECT count(*) FROM personal_job_items WHERE job_id=?`, j.JobID).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	obs, e := s.BulkAdapter(access).Step(context.Background(), j.JobID)
	if e != nil || obs.State != "running" || obs.Phase != "cleanup" || left() != 4500-bulkPurgeBatch {
		t.Fatalf("first purge step %+v %v, %d rows left", obs, e, left())
	}
	s = restartBulkService(t, s)
	steps := 0
	for {
		obs, e = s.BulkAdapter(access).Step(context.Background(), j.JobID)
		if e != nil {
			t.Fatal(e)
		}
		steps++
		if obs.State != "running" {
			break
		}
		if obs.Phase != "cleanup" || left() == 0 {
			t.Fatalf("step %d: %+v with %d rows left", steps, obs, left())
		}
	}
	if obs.State != "succeeded" || obs.Phase != "complete" || left() != 0 || steps != 2 {
		t.Fatalf("after restart: %+v, %d rows left, %d steps", obs, left(), steps)
	}
}
