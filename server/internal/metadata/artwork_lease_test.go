package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const artworkLeaseTestOrigin = "https://image.tmdb.org/t/p/w780/x.jpg"

// artworkLeaseTestSetup is the ordinary screen fixture: a movie whose library
// has TMDB artwork enabled through confirmed screen consent.
func artworkLeaseTestSetup(t *testing.T) (*Service, *sql.DB, RepairTarget, int64) {
	t.Helper()
	s, db, target, _ := integrationScreen(t)
	return s, db, target, resolveArtworkTestEntity(t, db, target)
}

func artworkLeaseTestFence(t *testing.T, db *sql.DB, target RepairTarget) string {
	t.Helper()
	ctx := context.Background()
	var fence string
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var err error
		fence, err = artworkFence(ctx, tx, target)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return fence
}

func artworkLeaseTestCandidate(t *testing.T, db *sql.DB, target RepairTarget, fence, imageID, origin string) string {
	t.Helper()
	ctx := context.Background()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	var id string
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var err error
		id, err = insertArtworkCandidate(ctx, tx, target, "poster", "", "tmdb", imageID, origin, "en", artworkAttribution("tmdb"), fence, stamp, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func artworkLeaseTestInsertJob(t *testing.T, db *sql.DB, id string, entity int64, candidate, fence, status string, attempts int, lease, leaseUntil string) {
	t.Helper()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO artwork_jobs(id,kind,entity_id,role,subject,candidate_id,origin,provider,source_fence,selection_revision,status,attempts,next_attempt,lease,lease_until,error,actor,locked,created_at)
 VALUES(?,'item',?,'poster','',?,?,'tmdb',?,0,?,?,'',?,?,'','',0,?)`,
		id, entity, candidate, artworkLeaseTestOrigin, fence, status, attempts, lease, leaseUntil, stamp); err != nil {
		t.Fatal(err)
	}
}

// artworkLeaseTestReclaim marks a job running again with an expired lease, the
// shape a crashed or abandoned claim leaves behind.
func artworkLeaseTestReclaim(t *testing.T, db *sql.DB, id string, attempts int) {
	t.Helper()
	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE artwork_jobs SET status='running',attempts=?,next_attempt='',lease='expired-lease',lease_until=?,error='' WHERE id=?`, attempts, expired, id); err != nil {
		t.Fatal(err)
	}
}

type artworkLeaseTestJobState struct {
	status    string
	code      string
	lease     string
	leaseTill string
	attempts  int
}

func artworkLeaseTestJob(t *testing.T, db *sql.DB, id string) artworkLeaseTestJobState {
	t.Helper()
	var st artworkLeaseTestJobState
	if err := db.QueryRow(`SELECT status,error,lease,lease_until,attempts FROM artwork_jobs WHERE id=?`, id).Scan(&st.status, &st.code, &st.lease, &st.leaseTill, &st.attempts); err != nil {
		t.Fatal(err)
	}
	return st
}

func artworkLeaseTestSelections(t *testing.T, db *sql.DB, entity int64, candidate string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='poster' AND candidate_id=?`, entity, candidate).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func artworkLeaseTestPNG(t *testing.T) []byte {
	t.Helper()
	return p08cPNG(t, 20, 20)
}

// A fetch that is still running keeps its claim: the watcher extends
// lease_until on the real row before the download finishes, and the finished
// job publishes normally.
func TestArtworkJobLeaseRenewsWhileFetchRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	previous := artworkLeaseRenewal
	artworkLeaseRenewal = 0
	t.Cleanup(func() { artworkLeaseRenewal = previous })

	s, db, target, entity := artworkLeaseTestSetup(t)
	ctx := context.Background()
	fence := artworkLeaseTestFence(t, db, target)
	candidate := artworkLeaseTestCandidate(t, db, target, fence, "renew-image", artworkLeaseTestOrigin)
	artworkLeaseTestInsertJob(t, db, "job-renew", entity, candidate, fence, "pending", 0, "", "")
	claimed := artworkLeaseTestJob(t, db, "job-renew")
	if claimed.status != "pending" || claimed.attempts != 0 {
		t.Fatalf("fixture job is %q attempts=%d", claimed.status, claimed.attempts)
	}

	body := artworkLeaseTestPNG(t)
	var renewed atomic.Bool
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// The claim this fetch runs under, then poll the real row until the
		// watcher has moved lease_until past it.
		var held time.Time
		deadline := time.Now().Add(10 * time.Second)
		for {
			var raw string
			if err := db.QueryRow(`SELECT lease_until FROM artwork_jobs WHERE id='job-renew'`).Scan(&raw); err != nil {
				return nil, err
			}
			until, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return nil, err
			}
			if held.IsZero() {
				held = until
			} else if until.After(held) {
				renewed.Store(true)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
			}
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(50 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				return nil, errors.New("the artwork lease was not renewed while the fetch was running")
			}
		}
	})}

	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	if !renewed.Load() {
		t.Fatal("the fetch finished without observing a renewed lease")
	}
	state := artworkLeaseTestJob(t, db, "job-renew")
	if state.status != "complete" || state.code != "" {
		t.Fatalf("renewed job ended %q/%q", state.status, state.code)
	}
	if state.attempts != 1 || state.lease != "" || state.leaseTill != "" {
		t.Fatalf("renewed job kept attempts=%d lease=%q until=%q", state.attempts, state.lease, state.leaseTill)
	}
	if n := artworkLeaseTestSelections(t, db, entity, candidate); n != 1 {
		t.Fatal("the renewed job did not publish its artwork")
	}
}

// Losing the lease while the fetch runs cancels the work context and nothing
// from that fetch is published.
func TestArtworkJobLostLeaseCancelsFetch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	previous := artworkLeaseRenewal
	artworkLeaseRenewal = 0
	t.Cleanup(func() { artworkLeaseRenewal = previous })

	s, db, target, entity := artworkLeaseTestSetup(t)
	ctx := context.Background()
	fence := artworkLeaseTestFence(t, db, target)
	candidate := artworkLeaseTestCandidate(t, db, target, fence, "lost-image", artworkLeaseTestOrigin)
	artworkLeaseTestInsertJob(t, db, "job-lost", entity, candidate, fence, "pending", 0, "", "")

	var cancelled atomic.Bool
	var stole atomic.Bool
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Another claim replaces the lease while this fetch is still open.
		if _, err := db.Exec(`UPDATE artwork_jobs SET lease='replacement-lease' WHERE id='job-lost'`); err != nil {
			return nil, err
		}
		stole.Store(true)
		select {
		case <-r.Context().Done():
			cancelled.Store(true)
			return nil, r.Context().Err()
		case <-time.After(10 * time.Second):
			return nil, errors.New("the fetch context was not cancelled after the lease was lost")
		}
	})}

	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	if !stole.Load() {
		t.Fatal("fixture never ran the fetch")
	}
	if !cancelled.Load() {
		t.Fatal("the fetch kept running after its lease was replaced")
	}
	if n := artworkLeaseTestSelections(t, db, entity, candidate); n != 0 {
		t.Fatal("artwork from a lost lease was published")
	}
	state := artworkLeaseTestJob(t, db, "job-lost")
	if state.status == "complete" {
		t.Fatal("a lost lease published a completed job")
	}
	if state.lease != "replacement-lease" {
		t.Fatalf("the step overwrote the replacement lease: %q", state.lease)
	}
}

// A job reclaimed after its lease expired without a publication gets no more
// than artworkAttemptLimit turns: at the cap it fails without a fetch, below it
// it runs again.
func TestArtworkReclaimedExpiredJobStopsAtAttemptCap(t *testing.T) {
	s, db, target, entity := artworkLeaseTestSetup(t)
	ctx := context.Background()
	fence := artworkLeaseTestFence(t, db, target)
	candidate := artworkLeaseTestCandidate(t, db, target, fence, "cap-image", artworkLeaseTestOrigin)

	var fetches atomic.Int32
	body := artworkLeaseTestPNG(t)
	s.artClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fetches.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	artworkLeaseTestInsertJob(t, db, "job-cap", entity, candidate, fence, "running", artworkAttemptLimit, "expired-lease", expired)
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	state := artworkLeaseTestJob(t, db, "job-cap")
	if state.status != "failed" || state.code != "acquisition_failed" {
		t.Fatalf("capped job ended %q/%q", state.status, state.code)
	}
	if state.attempts != artworkAttemptLimit {
		t.Fatalf("the capped job spent another attempt: %d", state.attempts)
	}
	if fetches.Load() != 0 {
		t.Fatal("a job at the attempt cap still fetched")
	}
	if n := artworkLeaseTestSelections(t, db, entity, candidate); n != 0 {
		t.Fatal("a capped job published artwork")
	}

	// One turn below the cap the same expired row is reclaimed and publishes.
	artworkLeaseTestReclaim(t, db, "job-cap", 2)
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("an expired job below the cap fetched %d times", fetches.Load())
	}
	state = artworkLeaseTestJob(t, db, "job-cap")
	if state.status != "complete" || state.attempts != 3 {
		t.Fatalf("reclaimed job ended %q attempts=%d", state.status, state.attempts)
	}
	if n := artworkLeaseTestSelections(t, db, entity, candidate); n != 1 {
		t.Fatal("the reclaimed job did not publish")
	}
}

func TestArtworkReclaimedExpiredDiscoveryStopsAtAttemptCap(t *testing.T) {
	s, db, target, entity := artworkLeaseTestSetup(t)
	ctx := context.Background()
	fence := artworkLeaseTestFence(t, db, target)

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/movie/42" {
			t.Errorf("unexpected discovery path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"id":42,"images":{"posters":[{"file_path":"/discovered.jpg","iso_639_1":"en","vote_average":8,"vote_count":10}]}}`)
	}))
	defer server.Close()
	s.base = server.URL
	s.client = server.Client()

	expired := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	insert := func(attempts int) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO artwork_discovery(kind,entity_id,provider,identity,source_fence,status,attempts,next_attempt,lease,lease_until,error)
 VALUES('item',?,'tmdb','42',?,'running',?,'','expired-lease',?,'')`, entity, fence, attempts, expired); err != nil {
			t.Fatal(err)
		}
	}
	discovery := func() (string, string, int) {
		t.Helper()
		var status, code string
		var attempts int
		if err := db.QueryRow(`SELECT status,error,attempts FROM artwork_discovery WHERE kind='item' AND entity_id=? AND provider='tmdb'`, entity).Scan(&status, &code, &attempts); err != nil {
			t.Fatal(err)
		}
		return status, code, attempts
	}
	reset := func(attempts int) {
		t.Helper()
		if _, err := db.Exec(`UPDATE artwork_discovery SET status='running',attempts=?,next_attempt='',lease='expired-lease',lease_until=?,error='' WHERE kind='item' AND entity_id=? AND provider='tmdb'`, attempts, expired, entity); err != nil {
			t.Fatal(err)
		}
	}

	insert(artworkAttemptLimit)
	if err := s.ArtworkDiscoveryStep(ctx); err != nil {
		t.Fatal(err)
	}
	status, code, attempts := discovery()
	if status != "failed" || code != "provider_unavailable" {
		t.Fatalf("capped discovery ended %q/%q", status, code)
	}
	if attempts != artworkAttemptLimit {
		t.Fatalf("the capped discovery spent another attempt: %d", attempts)
	}
	if calls.Load() != 0 {
		t.Fatal("a discovery at the attempt cap still called the provider")
	}

	reset(2)
	if err := s.ArtworkDiscoveryStep(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("an expired discovery below the cap called the provider %d times", calls.Load())
	}
	status, code, attempts = discovery()
	if status != "complete" || code != "" || attempts != 3 {
		t.Fatalf("reclaimed discovery ended %q/%q attempts=%d", status, code, attempts)
	}
	var discovered int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE kind='item' AND entity_id=? AND provider='tmdb' AND image_id='/discovered.jpg'`, entity).Scan(&discovered); err != nil || discovered != 1 {
		t.Fatal("the reclaimed discovery did not record its provider image", discovered, err)
	}
}

// The retention check reads only this target's own journal: a candidate named
// by its own history survives, one named only by another entity's history does
// not, because nothing moves candidates or history between targets.
func TestArtworkDiscoveryKeepsCandidatesNamedInTheirOwnHistory(t *testing.T) {
	s, db, target, entity := artworkLeaseTestSetup(t)
	ctx := context.Background()
	// A second entity in the same library proves the history lookup is scoped.
	c := catalogtest.New(t, db)
	other := c.Movie(c.Handle("lib"), "/local/other.mkv", "Other", 2001)
	fence := artworkLeaseTestFence(t, db, target)

	own := artworkLeaseTestCandidate(t, db, target, fence, "own-image", "https://image.tmdb.org/t/p/w780/own.jpg")
	foreign := artworkLeaseTestCandidate(t, db, target, fence, "foreign-image", "https://image.tmdb.org/t/p/w780/foreign.jpg")
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	integrationExec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',?,'r1','r2','edit','owner',?,?,?)`,
		entity, stamp, `{"candidate_id":"`+own+`"}`, `{}`)
	integrationExec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',?,'r1','r2','edit','owner',?,?,?)`,
		other.ID, stamp, `{}`, `{"candidate_id":"`+foreign+`"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":42,"images":{"posters":[{"file_path":"/discovered.jpg","iso_639_1":"en","vote_average":8,"vote_count":10}]}}`)
	}))
	defer server.Close()
	s.base = server.URL
	s.client = server.Client()
	if _, err := db.Exec(`INSERT INTO artwork_discovery(kind,entity_id,provider,identity,source_fence,status,attempts,next_attempt) VALUES('item',?,'tmdb','42',?,'pending',0,'')`, entity, fence); err != nil {
		t.Fatal(err)
	}

	if err := s.ArtworkDiscoveryStep(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM artwork_discovery WHERE kind='item' AND entity_id=? AND provider='tmdb'`, entity).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "complete" {
		t.Fatalf("discovery did not complete: %q", status)
	}
	exists := func(id string) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !exists(own) {
		t.Fatal("a candidate named in its own history was retired")
	}
	if exists(foreign) {
		t.Fatal("a candidate named only in another entity's history survived")
	}
	var discovered int
	if err := db.QueryRow(`SELECT count(*) FROM artwork_candidates WHERE kind='item' AND entity_id=? AND provider='tmdb' AND image_id='/discovered.jpg'`, entity).Scan(&discovered); err != nil || discovered != 1 {
		t.Fatal("the completed discovery did not record its provider image", discovered, err)
	}
}
