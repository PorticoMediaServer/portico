package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func replayJobHooks(t *testing.T, s *Service) {
	t.Helper()
	s.BulkCommands = BulkCommandHooks{
		Validate:  func(string, JobPersonalArgs) error { return nil },
		Authorize: func(context.Context, *sql.Tx, identity.Principal, string) error { return nil },
		Capture: func(ctx context.Context, tx *sql.Tx, p identity.Principal, command, id string, args JobPersonalArgs) (string, error) {
			var kind string
			if err := tx.QueryRowContext(ctx, `SELECT k.name FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind WHERE i.public_id=pid_blob(?)`, id).Scan(&kind); err != nil {
				return "", err
			}
			if kind != "episode" {
				return "", fmt.Errorf("mixed metadata kind %s", kind)
			}
			return "captured:" + id, nil
		},
		Apply: func(ctx context.Context, tx *sql.Tx, p identity.Principal, command, id, expected string, args JobPersonalArgs) error {
			if expected != "captured:"+id {
				return fmt.Errorf("capture revision changed")
			}
			return nil
		},
	}
}

func advanceReplayJob(t *testing.T, s *Service, job BulkJob, access BulkAccess, until func(BulkJob) bool) BulkJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		next, err := s.AdvanceBulkJob(context.Background(), job.JobID, access)
		if err != nil {
			t.Fatal(err)
		}
		job = next
		if until(job) {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no replay progress", job)
	return job
}

// While enablement is gated, the ordinary run verifies exact refusal. The
// qualification run overlays only the enablement constant; no product code or
// fixture matcher is substituted, and every action executes the real worker.
func TestBulkLocalSearchReplayActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, command := range []string{"personal-state", "playlist-add", "collection-add", "metadata-edit"} {
		t.Run(command, func(t *testing.T) {
			s, scheduler, p, v, access, c, names := bulkFixture(t, 145)
			store := replayTestStore(t, s)
			replayJobHooks(t, s)
			yes := true
			args := JobPersonalArgs{Favorite: &yes}
			target := "destination"
			if command == "metadata-edit" {
				args = JobPersonalArgs{}
			}
			if command == "playlist-add" || command == "collection-add" {
				actor := jobActor(p)
				name := "Destination"
				revision := int64(1)
				args = JobPersonalArgs{ExpectedRevision: &revision}
				if command == "playlist-add" {
					result, err := s.MutatePlaylist(actor, "", "create", "", PlaylistMutation{OperationID: "create-replay", Name: &name}, nil)
					if err != nil {
						t.Fatal(err)
					}
					target = result.PlaylistID
					args.PlaylistID = target
					args.Placement = json.RawMessage(`"end"`)
				} else {
					c.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'collection',?,?,?,?,?,'2026-10-28')`, target, actorKey(actor), actor.Authority, actor.AccountID, actor.ProfileID, name)
					args.CollectionID = target
				}
			}
			q := replayTestQuery(t, s, v, `{"variant":"predicate","field":"search","operator":"contains","value":"ep"}`)
			q.Sort = []BrowseSortSelection{{Field: "title", Direction: "desc"}}
			request := JobRequest{OperationID: "local-replay-" + command, Command: command, Selector: JobSelector{Query: &q}, Args: args}
			job, err := s.CreateBulkJob(context.Background(), p, v, request, scheduler, access)
			if !selectionBatchLocalSearchEnabled {
				if !errors.Is(err, ErrSelectionBatchUnsupported) {
					t.Fatal("unqualified action admitted", job, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var marker, cursor string
			if err = s.db.QueryRow(`SELECT selection_sql,selection_cursor FROM personal_jobs WHERE id=?`, job.JobID).Scan(&marker, &cursor); err != nil || marker != bulkReplaySelection || cursor != "" {
				t.Fatal("stored search plan/progress", marker, cursor, err)
			}
			job = advanceReplayJob(t, s, job, access, func(j BulkJob) bool { return j.Total > 0 || j.State == "failed" })
			if job.State == "failed" || job.Total > bulkBatchSize || job.TotalKnown {
				t.Fatal("unbounded writer or early EOF", job)
			}
			var accepted int64
			if err = s.db.QueryRow(`SELECT CAST(selection_cursor AS INTEGER) FROM personal_jobs WHERE id=?`, job.JobID).Scan(&accepted); err != nil {
				t.Fatal(err)
			}
			if accepted != names[fmt.Sprintf("ep%06d", job.Total-1)].ID {
				t.Fatal("cursor was rejected raw progress", accepted, job)
			}
			// Entire in-memory store loss: committed command targets remain authoritative.
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			s = New(s.db)
			store = replayTestStore(t, s)
			replayJobHooks(t, s)
			job = advanceReplayJob(t, s, job, access, func(j BulkJob) bool { return j.TotalKnown || j.State == "failed" })
			if job.State == "failed" || job.Total != 145 {
				t.Fatal("restart lost/duplicated targets", job)
			}
			var captured, distinct int
			if err = s.db.QueryRow(`SELECT count(*),count(DISTINCT item_id) FROM personal_job_items WHERE job_id=?`, job.JobID).Scan(&captured, &distinct); err != nil || captured != 145 || distinct != 145 {
				t.Fatal(captured, distinct, err)
			}
			job = advanceReplayJob(t, s, job, access, func(j BulkJob) bool { return bulkTerminal(j.State) })
			if job.State != "complete" || job.Done != 145 || job.Failed != 0 {
				t.Fatal("action did not execute", job)
			}
			switch command {
			case "personal-state":
				var favorites int
				if err = s.db.QueryRow(`SELECT count(*) FROM personal_items WHERE profile_id=? AND favorite=1`, v.Profile).Scan(&favorites); err != nil || favorites != 145 {
					t.Fatal(favorites, err)
				}
			case "playlist-add":
				rows, err := s.db.Query(`SELECT entry.item_id FROM catalog_playlist_entries entry JOIN catalog_playlists p ON p.id=entry.playlist_id WHERE p.token=? ORDER BY entry.order_key,entry.id`, target)
				if err != nil {
					t.Fatal(err)
				}
				var got, want []int64
				for rows.Next() {
					var id int64
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				for i := 144; i >= 0; i-- {
					want = append(want, names[fmt.Sprintf("ep%06d", i)].ID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("command order changed", got, want)
				}
			case "collection-add":
				var entries int
				if err = s.db.QueryRow(`SELECT count(*) FROM saved_resource_entries WHERE resource_id=?`, target).Scan(&entries); err != nil || entries != 145 {
					t.Fatal(entries, err)
				}
			}
			if store.Stats().Handles != 0 || store.Stats().Deliveries != 0 {
				t.Fatal("job lease leaked", store.Stats())
			}
		})
	}
}

func TestBulkLocalSearchReplayRollbackAndEOFDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if !selectionBatchLocalSearchEnabled {
		t.Skip("qualification overlay or final cohort enables new search forms")
	}
	s, scheduler, p, v, access, c, _ := bulkFixture(t, 25)
	store := replayTestStore(t, s)
	// Install the fault before capturing the complete witness: schema_version
	// itself is fenced. Toggle fixture-only admission data, not DDL, afterward.
	c.Exec(`CREATE TABLE replay_capture_gate(enabled INTEGER NOT NULL)`)
	c.Exec(`INSERT INTO replay_capture_gate VALUES(1)`)
	c.Exec(`CREATE TRIGGER refuse_replay_capture BEFORE INSERT ON personal_job_items WHEN (SELECT enabled FROM replay_capture_gate)=1 BEGIN SELECT RAISE(ABORT,'replay commit refused'); END`)
	yes := true
	q := replayTestQuery(t, s, v, `{"variant":"predicate","field":"search","operator":"contains","value":"ep"}`)
	job, err := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "capture-rollback", Command: "personal-state", Selector: JobSelector{Query: &q}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
		if err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err == nil || !strings.Contains(err.Error(), "replay commit refused") {
		var state, errorCode string
		_ = s.db.QueryRow(`SELECT state,error_code FROM personal_jobs WHERE id=?`, job.JobID).Scan(&state, &errorCode)
		t.Fatal("capture write not exercised", err, state, errorCode, store.Stats())
	}
	var count int
	var cursor string
	if err = s.db.QueryRow(`SELECT total,selection_cursor FROM personal_jobs WHERE id=?`, job.JobID).Scan(&count, &cursor); err != nil || count != 0 || cursor != "" {
		t.Fatal("failed write advanced cursor", count, cursor, err)
	}
	c.Exec(`UPDATE replay_capture_gate SET enabled=0`)
	job = advanceReplayJob(t, s, job, access, func(j BulkJob) bool { return j.Total > 0 })
	if job.Total > 20 {
		t.Fatal("writer widened", job)
	}
	// Drop memory again, then change the full witness before the final page.
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	s = New(s.db)
	replayTestStore(t, s)
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('replay-eof-drift',18)`)
	job, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
	if err != nil || job.State != "failed" || job.ErrorCode != "selection_changed" {
		t.Fatal("EOF drift accepted", job, err)
	}
	var personal int
	if err = s.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&personal); err != nil || personal != 0 {
		t.Fatal("stale capture applied", personal, err)
	}
}

func TestBulkReplayCursorRejectsRawProgress(t *testing.T) {
	for _, raw := range []string{"-1", "0", "junk", "9223372036854775808"} {
		if _, err := bulkReplayAfter(raw); !errors.Is(err, ErrCursor) {
			t.Fatal(raw, err)
		}
	}
	if n, err := bulkReplayAfter(strconv.FormatInt(123, 10)); err != nil || n != 123 {
		t.Fatal(n, err)
	}
}

func TestBulkReplayTargetSingletonChecks(t *testing.T) {
	s, _, _, v, _, _, names := bulkFixture(t, 2)
	q := replayTestQuery(t, s, v, `null`)
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		for _, command := range []string{"personal-state", "metadata-edit"} {
			if err := read.bulkReplayTarget(dbwork.Snapshot(ctx), v, JobSelector{Query: &q}, command, names["ep000000"].ID); err != nil {
				return err
			}
			if err := read.bulkReplayTarget(dbwork.Snapshot(ctx), v, JobSelector{Query: &q}, command, names["show"].ID); !errors.Is(err, ErrSelectionBatchStale) {
				t.Fatal("nonleaf target accepted", command, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBulkLocalSearchReplayRejectedTailAndPreparing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if !selectionBatchLocalSearchEnabled {
		t.Skip("qualification overlay or final cohort enables new search forms")
	}
	s, scheduler, p, v, access, _, names := bulkFixture(t, 145)
	store := replayTestStore(t, s)
	var demand atomic.Bool
	demand.Store(true)
	dbwork.RegisterForegroundProbe("bulk-replay-test", demand.Load)
	defer dbwork.RegisterForegroundProbe("bulk-replay-test", nil)
	yes := true
	q := replayTestQuery(t, s, v, `{"variant":"predicate","field":"search","operator":"contains","value":"ep00000"}`)
	job, err := s.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "rejected-tail", Command: "personal-state", Selector: JobSelector{Query: &q}, Args: JobPersonalArgs{Favorite: &yes}}, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	var initialRevision, revision int64
	if err = s.db.QueryRow(`SELECT revision FROM personal_jobs WHERE id=?`, job.JobID).Scan(&initialRevision); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		job, err = s.AdvanceBulkJob(context.Background(), job.JobID, access)
		if err != nil {
			t.Fatal(err)
		}
		var cursor string
		if err = s.db.QueryRow(`SELECT revision,selection_cursor FROM personal_jobs WHERE id=?`, job.JobID).Scan(&revision, &cursor); err != nil {
			t.Fatal(err)
		}
		if revision != initialRevision || cursor != "" || job.Total != 0 || job.TotalKnown {
			t.Fatal("preparing persisted query progress", job, revision, cursor)
		}
		if store.Stats().Handles != 1 {
			t.Fatal("retry leaked private handles", store.Stats())
		}
	}
	demand.Store(false)
	job = advanceReplayJob(t, s, job, access, func(j BulkJob) bool { return j.TotalKnown || j.State == "failed" })
	var cursor string
	if err = s.db.QueryRow(`SELECT selection_cursor FROM personal_jobs WHERE id=?`, job.JobID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if job.Total != 10 || job.State == "failed" || cursor != strconv.FormatInt(names["ep000009"].ID, 10) {
		t.Fatal("rejected tail advanced accepted cursor", job, cursor)
	}
	if store.Stats().Handles != 0 {
		t.Fatal("completed replay handle retained", store.Stats())
	}
}

func TestBulkReplaySortKeyMatchesExistingCommand(t *testing.T) {
	s, _, _, v, _, c, names := bulkFixture(t, 3)
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,rating,last_played_at,revision) VALUES(?,?,7,'2026-10-28T12:00:00Z',1)`, v.Profile, names["ep000000"].ID)
	c.Drain()
	selections := [][]BrowseSortSelection{}
	for _, field := range []string{"title", "added", "year", "duration", "communityRating", "personalRating", "lastPlayed", "playCount", "aired", "releaseDate", "criticRating", "fileSize", "resolution"} {
		for _, direction := range []string{"asc", "desc"} {
			selections = append(selections, []BrowseSortSelection{{Field: field, Direction: direction}})
		}
	}
	for _, sorts := range selections {
		q := JobQuery{LibraryID: "lib", Pivot: "episodes", Filter: json.RawMessage(`{"variant":"predicate","field":"search","operator":"contains","value":"ep"}`), Sort: sorts}
		oracle := q
		oracle.Filter = nil
		if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			for _, name := range []string{"ep000000", "ep000001"} {
				actual, err := s.WithContext(ctx).bulkSortKey(ctx, dbwork.Snapshot(ctx), v, JobSelector{Query: &q}, names[name].ID)
				if err != nil {
					return err
				}
				expected, err := s.WithContext(ctx).bulkSortKey(ctx, dbwork.Snapshot(ctx), v, JobSelector{Query: &oracle}, names[name].ID)
				if err != nil {
					return err
				}
				if actual != expected {
					t.Fatalf("%v %s: local command key %q != existing %q", sorts, name, actual, expected)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(sorts, err)
		}
	}
}

func TestBulkLocalSearchPlaylistGroupOrderAdmission(t *testing.T) {
	if !selectionBatchLocalSearchEnabled {
		t.Skip("qualification overlay or final cohort enables new search forms")
	}
	s, _, p, v, _, _, names := bulkFixture(t, 2)
	replayTestStore(t, s)
	filter := fmt.Sprintf(`{"variant":"group","all":[{"variant":"predicate","field":"memberOf","operator":"equals","value":%q},{"variant":"predicate","field":"search","operator":"contains","value":"averylongprefix"}]}`, names["show"].Public)
	q := replayTestQuery(t, s, v, filter)
	q.Sort = []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		marker, _, err := read.bulkSelection(p, v, JobRequest{Command: "playlist-add", Selector: JobSelector{Query: &q}})
		if err != nil || marker != bulkReplaySelection {
			t.Fatal("bounded group Playlist replay admission missing", marker, err)
		}
		for _, command := range []string{"personal-state", "collection-add"} {
			marker, _, err := read.bulkSelection(p, v, JobRequest{Command: command, Selector: JobSelector{Query: &q}})
			if err != nil || marker != bulkReplaySelection {
				t.Fatal("unordered action incorrectly disabled", command, marker, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBulkReplayForYouKeepsExistingCommandOrder(t *testing.T) {
	_, s, names := browseFixture(t)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	q := JobQuery{LibraryID: "a", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "forYou", Direction: "desc"}}, Filter: json.RawMessage(`{"variant":"predicate","field":"search","operator":"contains","value":"a"}`)}
	oracle := q
	oracle.Filter = nil
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		actual, err := s.WithContext(ctx).bulkSortKey(ctx, dbwork.Snapshot(ctx), v, JobSelector{Query: &q}, names["m1"].ID)
		if err != nil {
			return err
		}
		expected, err := s.WithContext(ctx).bulkSortKey(ctx, dbwork.Snapshot(ctx), v, JobSelector{Query: &oracle}, names["m1"].ID)
		if err != nil {
			return err
		}
		if actual != expected {
			t.Fatal("forYou changed legacy command ordering", actual, expected)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
