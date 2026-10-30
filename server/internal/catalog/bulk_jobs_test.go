package catalog

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"sort"
	"strings"
	"testing"
	"time"
)

func bulkFixture(t *testing.T, n int) (*Service, *operations.Scheduler, identity.Principal, Viewer, BulkAccess, *catalogtest.Catalog, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("lib", "Library", "tv", "/media")
	show := c.Show(library, "Show", 0)
	seasons := make([]catalogtest.Item, (n+999)/1000)
	names := catalogtest.Names{"show": show}
	for i := range seasons {
		seasons[i] = c.Season(show, i+1)
		names[fmt.Sprintf("season-%03d", i+1)] = seasons[i]
	}
	entityIDs := make([]int64, n)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("ep%06d", i)
			path := "/media/" + key + ".mkv"
			itemID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: library, Kind: compactcatalog.Episode, Parent: seasons[i/1000].ID,
				Key: compactcatalog.ItemKey("/media", path, 0), Title: key, Added: "2026-01-01T00:00:00.000Z",
			})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, itemID, map[string]any{
				"show_id": show.ID, "season_id": seasons[i/1000].ID,
				"numbering": "seasonal", "number": i%1000 + 1, "local_identity_status": "parsed",
			}); err != nil {
				return err
			}
			assetID, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, itemID, assetID, compactcatalog.Link{}); err != nil {
				return err
			}
			entityIDs[i] = itemID
		}
		return nil
	})
	for i, id := range entityIDs {
		key := fmt.Sprintf("ep%06d", i)
		names[key] = catalogtest.Item{ID: id, Public: c.Public(id)}
	}
	c.Drain()
	db := c.DB
	s := New(db)
	scheduler := operations.NewScheduler(operations.New(db))
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p", Role: "owner"}}
	v := Viewer{Profile: identity.PersonalKey(p.Viewer), Libraries: []string{"lib"}}
	access := func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
		return "1", nil, nil
	}
	if e := scheduler.Register(s.BulkAdapter(access)); e != nil {
		t.Fatal(e)
	}
	return s, scheduler, p, v, access, c, names
}

func bulkFixtureName(names catalogtest.Names, id int64) string {
	for name, item := range names {
		if item.ID == id {
			return name
		}
	}
	return ""
}

func finishCapture(t *testing.T, s *Service, j BulkJob, access BulkAccess) BulkJob {
	t.Helper()
	for !j.TotalKnown && j.State != "failed" {
		var e error
		j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
		if e != nil {
			t.Fatal(e)
		}
	}
	return j
}

func TestBulkJobReceiptRestartAndAtomicProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, _, names := bulkFixture(t, 205)
	yes := true
	in := JobRequest{OperationID: "test", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes}}
	j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil || j.TotalKnown || j.State != "queued" {
		t.Fatal(j, e)
	}
	replay, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil || replay.JobID != j.JobID {
		t.Fatal(replay, e)
	}
	no := false
	changed := in
	changed.Args.Watched = &no
	if _, e = s.CreateBulkJob(context.Background(), p, v, changed, scheduler, access); !errors.Is(e, ErrOperationConflict) {
		t.Fatal(e)
	}
	j = finishCapture(t, s, j, access)
	j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
	if e != nil || j.Done != bulkApplyBatch {
		t.Fatal(j, e)
	}
	s = restartBulkService(t, s)
	for j.State == "running" {
		j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
		if e != nil {
			t.Fatal(e)
		}
	}
	if j.State != "complete" || j.Done != 205 {
		t.Fatal(j)
	}
	var receipts, revision int
	if e = s.db.QueryRow(`SELECT count(*) FROM personal_receipts`).Scan(&receipts); e != nil || receipts != 0 {
		t.Fatal(receipts, e)
	}
	if e = s.db.QueryRow(`SELECT revision FROM personal_items WHERE profile_id=? AND item_id=?`, v.Profile, names["ep000000"].ID).Scan(&revision); e != nil || revision != 1 {
		t.Fatal(revision, e)
	}
	if _, e = s.BulkJob("someone-else", j.JobID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}
func TestContainerWatermarkAndReset(t *testing.T) {
	s, scheduler, p, v, access, c, names := bulkFixture(t, 3)
	yes, no := true, false
	if _, e := s.SetPersonal("a", v.Profile, names["ep000000"].Public, PersonalMutation{OperationID: "explicit", Watched: &no}, nil); e != nil {
		t.Fatal(e)
	}
	state, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	for i, want := range []bool{true, true, true} {
		got, e := s.Personal(v.Profile, names[fmt.Sprintf("ep%06d", i)].Public)
		if e != nil || got.Watched != want {
			t.Fatal(i, got, e)
		}
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, names["ep000002"].ID, map[string]any{"added_text": future})
	})
	c.Drain()
	got, e := s.Personal(v.Profile, names["ep000002"].Public)
	if e != nil || !got.Watched {
		t.Fatal(got, e)
	}
	newEpisode := c.Episode(names["show"], names["season-001"], 4, "/media/new-after-mark.mkv")
	c.Fields(newEpisode.ID, map[string]any{"added_text": future})
	c.Drain()
	if got, e := s.Personal(v.Profile, newEpisode.Public); e != nil || got.Watched {
		t.Fatal("a later arrival inherited the mark", got, e)
	}
	count, e := s.ContainerPersonalCounts(v, "show", names["show"].Public)
	if e != nil || count.WatchedCount != 3 || count.UnwatchedCount != 1 {
		t.Fatal(count, e)
	}
	state, e = s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{ExpectedRevision: state.Revision, Watched: &no}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.Personal(v.Profile, names["ep000001"].Public)
	if e != nil || got.Watched {
		t.Fatal(got, e)
	}
	var intents int
	s.db.QueryRow(`SELECT count(*) FROM personal_watched_intents`).Scan(&intents)
	if intents != 4 {
		t.Fatal("every captured member needs the explicit mark", intents)
	}

}

func TestBulkJobRevocationConflictAndFailurePaging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, _, names := bulkFixture(t, 205)
	yes := true
	in := JobRequest{OperationID: "failures", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}
	j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	deny := func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
		return "0", nil, nil
	}
	for j.State == "queued" || j.State == "running" || j.State == "building" {
		j, e = s.AdvanceBulkJob(context.Background(), j.JobID, deny)
		if e != nil {
			t.Fatal(e)
		}
	}
	if j.Failed != 205 || j.Done != 0 || j.State != "failed" {
		t.Fatal(j)
	}
	page, e := s.BulkFailures(v.Profile, j.JobID, "", v)
	if e != nil || len(page.Items) != 100 || page.NextCursor == "" {
		t.Fatal(page, e)
	}
	page2, e := s.BulkFailures(v.Profile, j.JobID, page.NextCursor, v)
	if e != nil || len(page2.Items) != 100 || page2.Items[0].ItemID == page.Items[0].ItemID {
		t.Fatal(page2, e)
	}
	hidden := v
	hidden.Libraries = nil
	page, e = s.BulkFailures(v.Profile, j.JobID, "", hidden)
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&n)
	if n != 0 {
		t.Fatal("revoked items changed", n)
	}
}
func TestBulkSnapshotDoesNotGrowOrFilterItself(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, c, names := bulkFixture(t, 101)
	yes := true
	in := JobRequest{OperationID: "snapshot", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes}}
	j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	j = finishCapture(t, s, j, access)
	newEpisode := c.Episode(names["show"], names["season-001"], 200, "/media/new.mkv")
	c.Drain()
	for j.State == "queued" || j.State == "running" || j.State == "building" {
		j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
		if e != nil {
			t.Fatal(e)
		}
	}
	got, e := s.Personal(v.Profile, newEpisode.Public)
	if e != nil || got.Watched || j.Total != 101 {
		t.Fatal(j, got, e)
	}
}
func TestContainerMarkPreservesNewExplicitIntent(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	no, yes := false, true
	if _, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &no}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetPersonal("a", v.Profile, names["ep000000"].Public, PersonalMutation{OperationID: "later", ExpectedRevision: 1, Watched: &yes}, nil); e != nil {
		t.Fatal(e)
	}
	got, e := s.Personal(v.Profile, names["ep000000"].Public)
	if e != nil || !got.Watched {
		t.Fatal(got, e)
	}
}

var bulkScale = flag.Bool("bulk-scale", false, "run the 5k bulk acceptance fixture on the Linux runner")

func TestBulkFiveThousandEpisodes(t *testing.T) {
	if testing.Short() || !*bulkScale {
		t.Skip("runner acceptance fixture")
	}
	started := time.Now()
	s, scheduler, p, v, access, _, names := bulkFixture(t, 5000)
	fixtureTime := time.Since(started)
	dbwork.WriteGate().ResetPeak()
	yes := true
	started = time.Now()
	state, e := s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access)
	containerTime := time.Since(started)
	if e != nil {
		t.Fatal(e)
	}
	counts, e := s.ContainerPersonalCounts(v, "show", names["show"].Public)
	if e != nil || counts.WatchedCount != 5000 || counts.UnwatchedCount != 0 {
		t.Fatal(state, counts, e)
	}
	no := false
	if _, e = s.SetContainerPersonal(context.Background(), p, "show", names["show"].Public, ContainerPersonalMutation{ExpectedRevision: state.Revision, Watched: &no}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	in := JobRequest{OperationID: "five-thousand", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes, Favorite: &yes}}
	started = time.Now()
	j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	capture := time.Since(started)
	if e != nil {
		t.Fatal(e)
	}
	longest := time.Duration(0)
	durations := map[string][]time.Duration{}
	ctx := context.WithValue(context.Background(), bulkTimingObserverKey{}, func(phase string, d time.Duration) { durations[phase] = append(durations[phase], d) })
	batches := 0
	started = time.Now()
	for j.State == "queued" || j.State == "running" || j.State == "building" {
		batchStart := time.Now()
		j, e = s.AdvanceBulkJob(ctx, j.JobID, access)
		elapsed := time.Since(batchStart)
		if elapsed > longest {
			longest = elapsed
		}
		batches++
		if e != nil {
			t.Fatal(e)
		}
	}
	if j.State != "complete" || j.Done != 5000 || j.Failed != 0 {
		t.Fatal(j)
	}
	var watchedItems int
	if e = s.db.QueryRow(`SELECT count(*) FROM personal_items WHERE profile_id=? AND watched=1 AND favorite=1`, v.Profile).Scan(&watchedItems); e != nil || watchedItems != 5000 {
		t.Fatal(watchedItems, e)
	}
	var receipts, operations int
	s.db.QueryRow(`SELECT count(*) FROM personal_receipts`).Scan(&receipts)
	s.db.QueryRow(`SELECT count(*) FROM personal_jobs`).Scan(&operations)
	if receipts != 0 || operations != 1 {
		t.Fatal(receipts, operations)
	}
	var pages, pageSize int64
	s.db.QueryRow(`PRAGMA page_count`).Scan(&pages)
	s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
	for _, phase := range []string{"capture", "apply"} {
		values := durations[phase]
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		t.Logf("%s transaction SQL body: median=%s p95=%s max=%s", phase, values[len(values)/2], values[len(values)*95/100], values[len(values)-1])
	}
	t.Logf("maximum transaction gate hold: %d ms", dbwork.WriteGate().Stats().MaxHeldMilli)
	t.Logf("5k fixture=%s container_request=%s capture=%s execution=%s batches=%d longest_batch_with_gate_and_commit=%s database_bytes=%d job_receipts=%d item_receipts=%d", fixtureTime, containerTime, capture, time.Since(started), batches, longest, pages*pageSize, operations, receipts)
}

func TestBulkCaptureRestartAndRevisionFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, scheduler, p, v, access, c, names := bulkFixture(t, 205)
	yes := true
	in := JobRequest{OperationID: "capture-restart", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}
	j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
	if e != nil || j.Total != bulkBatchSize || j.TotalKnown {
		t.Fatal(j, e)
	}
	s = restartBulkService(t, s)
	c = catalogtest.New(t, s.db)
	j = finishCapture(t, s, j, access)
	if j.Total != 205 || j.Done != 0 {
		t.Fatal(j)
	}
	// A second job whose source changes mid-capture fails before all writes.
	in.OperationID = "capture-stale"
	j, e = s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
	if e != nil {
		t.Fatal(e)
	}
	c.Fields(names["ep000000"].ID, map[string]any{"title": "Changed title"})
	c.Drain()
	j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
	if e != nil || j.State != "failed" || j.ErrorCode != "selection_changed" || j.Done != 0 {
		t.Fatal(j, e)
	}
	var count int
	if e = s.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestBulkPermanentFailureRollsBackBatch(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 3)
	yes := true
	j, e := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "rollback", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	j = finishCapture(t, s, j, access)
	trigger := fmt.Sprintf(`CREATE TRIGGER reject_personal BEFORE INSERT ON personal_items WHEN NEW.item_id=%d BEGIN SELECT RAISE(ABORT,'injected batch rejection'); END`, names["ep000001"].ID)
	if _, e = s.db.Exec(trigger); e != nil {
		t.Fatal(e)
	}
	result, e := s.BulkAdapter(access).Step(context.Background(), j.JobID)
	if e != nil || result.State != "failed" {
		t.Fatal(result, e)
	}
	j, e = s.BulkJob(v.Profile, j.JobID)
	if e != nil || j.Done != 0 || j.ErrorCode != "execution_failed" {
		t.Fatal(j, e)
	}
	var count int
	if e = s.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&count); e != nil || count != 0 {
		t.Fatal("batch partially committed", count, e)
	}
}

func TestBulkSelectionPlans(t *testing.T) {
	s, _, p, v, _, _, names := bulkFixture(t, 3)
	query, args, e := s.bulkSelection(p, v, JobRequest{Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}})
	if e != nil {
		t.Fatal(e)
	}
	rows, e := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM (`+query+`) selected WHERE id>? ORDER BY id LIMIT 100`, append(args, "")...)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var membershipSeek, itemSeek bool
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		t.Log(detail)
		membershipSeek = membershipSeek || strings.Contains(detail, "SEARCH bm USING PRIMARY KEY (entity_id=? AND item_id>?)")
		itemSeek = itemSeek || strings.Contains(detail, "SEARCH i USING INTEGER PRIMARY KEY")
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if !membershipSeek || !itemSeek {
		t.Fatal("bulk container selection must seek compact membership and item indexes")
	}
}

func TestLatestContainerWatermarkWins(t *testing.T) {
	s, scheduler, p, v, access, _, names := bulkFixture(t, 2)
	ctx := context.Background()
	yes, no := true, false
	if _, e := s.SetContainerPersonal(ctx, p, "season", names["season-001"].Public, ContainerPersonalMutation{Watched: &yes}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetContainerPersonal(ctx, p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &no}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	got, e := s.Personal(v.Profile, names["ep000000"].Public)
	if e != nil || got.Watched {
		t.Fatal("old season state defeated later whole-show reset", got, e)
	}
	if _, e := s.SetContainerPersonal(ctx, p, "show", names["show"].Public, ContainerPersonalMutation{ExpectedRevision: 1, Watched: &yes}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	got, e = s.Personal(v.Profile, names["ep000000"].Public)
	if e != nil || !got.Watched {
		t.Fatal(got, e)
	}
}

func restartBulkService(t *testing.T, s *Service) *Service {
	t.Helper()
	var seq int
	var name, path string
	if e := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	if e := s.db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestBulkSchedulerRunsDurableAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	testBulkSchedulerRunsDurableAdapter(t)
}

func TestBulkSchedulerCompletesWithForegroundPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dbwork.RegisterForegroundProbe(t.Name(), func() bool { return true })
	defer dbwork.RegisterForegroundProbe(t.Name(), nil)
	testBulkSchedulerRunsDurableAdapter(t)
}

func testBulkSchedulerRunsDurableAdapter(t *testing.T) {
	t.Helper()
	s, scheduler, p, v, access, _, names := bulkFixture(t, 205)
	yes := true
	j, e := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "scheduled", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watchlist: &yes}}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() { defer close(exited); scheduler.Run(ctx) }()
	defer func() { cancel(); <-exited }()
	completionBudget := 20 * time.Second
	if raceDetector {
		// Modernc's pure-Go SQLite VM is heavily instrumented by the race
		// detector. Keep the ordinary completion bound and allow identical
		// full-work assertions to finish in a detector build.
		completionBudget = 2 * time.Minute
	}
	deadline := time.NewTimer(completionBudget)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("scheduler did not complete", j)
		case <-tick.C:
			j, e = s.BulkJob(v.Profile, j.JobID)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "failed" {
				t.Fatal(j)
			}
			if j.State == "complete" {
				if j.Done != 205 {
					t.Fatal(j)
				}
				var revisions int
				if e = s.db.QueryRow(`SELECT revision FROM personal_jobs WHERE id=?`, j.JobID).Scan(&revisions); e != nil || revisions < 6 {
					t.Fatal(revisions, e)
				}
				return
			}
		}
	}
}

func TestBulkEveryTransactionBoundedAcrossRestart(t *testing.T) {
	testtier.Media(t, "a large bulk job run across a restart")
	s, scheduler, p, v, access, _, names := bulkFixture(t, 650)
	phases := map[string]int{}
	touched := map[string]int{}
	ctx := context.WithValue(context.Background(), bulkBatchObserverKey{}, func(phase string, n int) {
		if n > 500 {
			t.Fatalf("%s transaction handled %d members", phase, n)
		}
		phases[phase]++
		touched[phase] += n
	})
	yes := true
	j, e := s.CreateBulkJob(ctx, p, v, JobRequest{OperationID: "bounded-restart", Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobPersonalArgs{Watched: &yes}}, scheduler, access)
	if e != nil {
		t.Fatal(e)
	}
	restarted := false
	for j.State == "queued" || j.State == "building" || j.State == "running" {
		j, e = s.AdvanceBulkJob(ctx, j.JobID, access)
		if e != nil {
			t.Fatal(e)
		}
		if j.Done >= 200 && !restarted {
			s = restartBulkService(t, s)
			restarted = true
		}
	}
	if !restarted || j.State != "complete" || j.Done != 650 || phases["capture"] < 7 || phases["apply"] < 7 || touched["capture"] != 650 || touched["apply"] != 650 {
		t.Fatal(j, phases, touched, restarted)
	}
	no := false
	if _, e = s.SetContainerPersonal(ctx, p, "show", names["show"].Public, ContainerPersonalMutation{Watched: &no}, scheduler, access); e != nil {
		t.Fatal(e)
	}
	for _, item := range names {
		if item.ID == names["show"].ID || item.ID == names["season-001"].ID {
			continue
		}
		if got, e := s.Personal(v.Profile, item.Public); e != nil || got.Watched {
			t.Fatal(got, e)
		}
	}

}
