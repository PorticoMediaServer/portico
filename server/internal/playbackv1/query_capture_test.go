package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"reflect"
	"testing"
	"time"
)

func queryBuildFixture(t *testing.T) (*Service, *catalogtest.Catalog, snapshot) {
	c := catalogtest.Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	for n := 0; n < 520; n++ {
		c.Movie(library, fmt.Sprintf("/movies/%04d.mp4", n), fmt.Sprintf("Film %04d", n), 2026)
	}
	c.Drain()
	s := New(c.DB, nil, nil)
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}
	s.QueryKeys = func(ctx context.Context, tx *sql.Tx, p identity.Principal, sel Selector) (catalog.QueueSelection, error) {
		return catalog.New(c.DB).QueueKeysTx(ctx, tx, catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: "f", Libraries: []string{"movies"}}, catalog.ResourceActor{}, sel.Query, "", nil)
	}
	in := SegmentInput{Source: Selector{Query: &catalog.QueueQuery{LibraryID: "movies", Pivot: "movies"}}}
	// Every read a build still holds is let go when the test ends, however it
	// ends: the two capture slots are process-wide, and a test that stops
	// early must not leave the next one without one.
	t.Cleanup(func() { releaseCaptures(s) })
	snap, err := s.buildSnapshot(context.Background(), p, "q_test", in, "source", MaxQueueKeys, nil, scanBudget{keys: 256})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.partial || snap.count != 256 {
		t.Fatalf("partial snapshot %+v", snap)
	}
	snap.caller = `{"viewer":{"authority":"local","accountId":"account","profileId":"profile"}}`
	if _, err = c.DB.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_test','device','device','local','account','profile',1,0,0)`); err != nil {
		t.Fatal(err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = storeSnapshot(context.Background(), tx, "q_test", &snap); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES('g_test','q_test','a',?,0,256)`, snap.id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, c, snap
}

// releaseCaptures lets go of every capture a service's builds still hold.
func releaseCaptures(s *Service) {
	s.build.mu.Lock()
	ids := make([]string, 0, len(s.build.captures))
	for id := range s.build.captures {
		ids = append(ids, id)
	}
	s.build.mu.Unlock()
	for _, id := range ids {
		s.releaseCapture(id)
	}
}

func TestQueryCaptureResumesAfterRestartAtTheSameRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, snap := queryBuildFixture(t)
	s.releaseCapture(snap.id) // A process restart loses the live WAL snapshot.
	if err := s.continueBuild(context.Background(), snap.id); err != nil {
		t.Fatal(err)
	}
	var count int
	var state string
	if err := c.DB.QueryRow(`SELECT count,state FROM queue_v1_snapshots WHERE id=?`, snap.id).Scan(&count, &state); err != nil {
		t.Fatal(err)
	}
	if count != 520 || state != "ready" {
		t.Fatalf("count %d state %s", count, state)
	}
}

func TestQueryCaptureDoesNotMixRevisionsAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, snap := queryBuildFixture(t)
	s.releaseCapture(snap.id)
	c.Movie(c.Handle("movies"), "/movies/new.mp4", "Earlier title", 2026)
	c.Drain()
	if err := s.continueBuild(context.Background(), snap.id); !errors.Is(err, errSelectionChanged) {
		t.Fatalf("changed source: %v", err)
	}
	s.failBuild(snap.id)
	var count int
	var state string
	if err := c.DB.QueryRow(`SELECT count,state FROM queue_v1_snapshots WHERE id=?`, snap.id).Scan(&count, &state); err != nil {
		t.Fatal(err)
	}
	if count != 256 || state != "failed" {
		t.Fatalf("mixed capture count %d state %s", count, state)
	}
}

func TestGrowingQueryShuffleKeepsTheSameSeededOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, snap := queryBuildFixture(t)
	s.releaseCapture(snap.id)
	s.HoldQueueBuilds()
	who := Caller{DeviceID: "shuffle_device", PlaybackProtocol: PlaybackProtocol, Principal: identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}}}
	// The queue belongs to a device that exists: the sweep a create starts in
	// the background removes the queues of devices that are gone, and once
	// raced the reply (an empty queue under load).
	if _, err := c.DB.Exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('shuffle_device','account','installation','Test','android','portico','1','2026-10-16T00:00:00Z','2026-10-16T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var previous []string
	for n := 0; n < 2; n++ {
		q, err := s.CreateQueue(context.Background(), who, fmt.Sprintf("seeded-query-%06d", n), CreateQueueRequest{Selector: &Selector{Query: &catalog.QueueQuery{LibraryID: "movies", Pivot: "movies"}}, Shuffle: &QueueSeed{Seed: "42"}})
		if err != nil {
			t.Fatal(err)
		}
		if q.Queue.Current == nil || q.Queue.Total != 256 {
			t.Fatalf("first shuffled batch %+v", q.Queue)
		}
		// The sweep runs now, rather than whenever the scheduler lets it: the
		// queue it must keep is still there to read.
		s.SweepQueues(context.Background())
		var id string
		if err = c.DB.QueryRow(`SELECT id FROM queue_v1_snapshots WHERE queue_id=?`, q.Queue.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err = s.continueBuild(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for from := int64(0); from < 520; from += WindowMax {
			page, err := s.Entries(context.Background(), who, q.Queue.ID, from, WindowMax)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				got = append(got, item.ItemID)
			}
		}
		unique := map[string]bool{}
		for _, id := range got {
			unique[id] = true
		}
		if len(unique) != 520 {
			t.Fatalf("shuffle lost matches: %d", len(unique))
		}
		if n > 0 && !reflect.DeepEqual(previous, got) {
			t.Fatal("same seed changed a growing queue's order")
		}
		previous = got
	}
}

func TestQueryCaptureConnectionAdmissionKeepsTheRequestDeadline(t *testing.T) {
	c := catalogtest.Open(t)
	background := dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)
	pool := dbwork.ReadHandle(background, c.DB)
	pool.SetMaxOpenConns(1)
	occupied, err := pool.Conn(background)
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	request, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	capture, err := New(c.DB, nil, nil).openCapture(request)
	if capture != nil {
		capture.close()
		t.Fatal("admitted a third reader")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("request waited for the detached build lifetime")
	}
	if len(queryCaptureSlots) != 0 {
		t.Fatal("failed acquisition leaked a capture permit")
	}
}

// A retained source transaction alone cannot prove a held aggregate after
// predicate-only churn. Fresh capture checks must refuse before extending it.
func TestQueryCaptureHeldBasisChecksFreshBatchAndFinalCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, snap := queryBuildFixture(t)
	s.build.mu.Lock()
	capture := s.build.captures[snap.id]
	if capture == nil {
		s.build.mu.Unlock()
		t.Fatal("missing retained capture")
	}
	capture.keys.checkBasis = func(ctx context.Context, tx *sql.Tx) error {
		var changed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM content_rating_ages WHERE value_key='queue-held-proof-drift')`).Scan(&changed); err != nil {
			return err
		}
		if changed {
			return ErrRevision
		}
		return nil
	}
	s.build.mu.Unlock()
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('queue-held-proof-drift',18)`)
	if err := s.continueBuild(context.Background(), snap.id); !errors.Is(err, ErrRevision) {
		t.Fatalf("pinned capture accepted new basis: %v", err)
	}
	var built int
	if err := c.DB.QueryRow(`SELECT built FROM queue_v1_snapshots WHERE id=?`, snap.id).Scan(&built); err != nil || built != 256 {
		t.Fatalf("extended stale capture built=%d %v", built, err)
	}
	// A completed small scan is checked again in its accepting writer, even
	// if it stayed entirely in memory and never wrote a capture batch.
	snap.id = "s_refused_final"
	snap.stored = false
	snap.checkBasis = func(context.Context, *sql.Tx) error { return ErrRevision }
	if err := dbwork.WithWriteTxContext(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error { return storeSnapshot(ctx, tx, "q_test", &snap) }); !errors.Is(err, ErrRevision) {
		t.Fatalf("final queue commit omitted held proof: %v", err)
	}
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM queue_v1_snapshots WHERE id=?`, snap.id).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused final snapshot persisted", count, err)
	}
}
