package dvr

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type shortStorage struct {
	err error
	// checked runs on every check: a slow volume is one that lets time pass.
	checked func()
}

func (s *shortStorage) Measure(context.Context) (StorageMeasurement, error) {
	return StorageMeasurement{}, nil
}
func (s *shortStorage) CheckFloor(context.Context) error {
	if s.checked != nil {
		s.checked()
	}
	return s.err
}
func (s *shortStorage) SetPolicySource(func() StoragePolicy) {}

// reclaimingDriver can retire recordings: removing one frees the volume.
type reclaimingDriver struct {
	storage *shortStorage
	removed int
}

func (d *reclaimingDriver) Available() bool           { return true }
func (d *reclaimingDriver) RetirementAvailable() bool { return true }
func (d *reclaimingDriver) Capture(context.Context, CaptureRequest, func(CaptureCheckpoint) error) (CaptureResult, error) {
	return CaptureResult{}, ErrUnavailable
}
func (d *reclaimingDriver) Recover(context.Context, CaptureRequest, CaptureCheckpoint) (CaptureResult, error) {
	return CaptureResult{}, ErrUnavailable
}
func (d *reclaimingDriver) Remove(context.Context, livechannels.Owner, string, mediaartifact.Object) error {
	d.removed++
	d.storage.err = nil
	return nil
}

// A recording that comes due on a full disk waits while a deletion is queued,
// because that space is on its way, and fails only once nothing more is
// coming or its own window has passed.
func TestStorageShortageWaitsForQueuedDeletionsBeforeFailingARecording(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recordingProfiles(t, db, "account", "profile")
	src, gen, ch := strings.Repeat("a", 48), strings.Repeat("b", 48), strings.Repeat("c", 64)
	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	programmes := []string{strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)}
	for _, q := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO live_source_identities VALUES(?)`, []any{src}},
		{`INSERT INTO live_generations VALUES(?,?,'Guide',2,1,1,?)`, []any{gen, src, now.Format(time.RFC3339)}},
		{`INSERT INTO live_sources VALUES(?,'Aerial',1,'active',2,?,?)`, []any{src, gen, now.Format(time.RFC3339)}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'channel','Channel','1',NULL,'',1,x'00','','')`, []any{gen, ch}},
	} {
		if _, err = db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for n, programme := range programmes {
		if _, err = db.Exec(`INSERT INTO live_programmes VALUES(?,?,?,?,'Episode',?,?,?)`, gen, programme, ch, "programme-"+string(rune('a'+n)), start.Format(time.RFC3339), end.Format(time.RFC3339), "lineage-"+string(rune('a'+n))); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO live_programme_metadata(generation_id,id,series_id,episode_id,new_evidence,description) VALUES(?,?,'','','','')`, gen, programme); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	clock := now
	storage := &shortStorage{err: ErrStorageFloor}
	s := &Store{db: db, live: &livechannels.Store{}, now: func() time.Time { return clock }, captureAvailable: true, storage: storage, durable: func(context.Context, *sql.Tx, livechannels.Owner, string, string) error { return nil }}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	schedule := func(n int) string {
		t.Helper()
		r, err := s.Schedule(ctx, auth, owner, ScheduleInput{RequestID: strings.Repeat(string(rune('1'+n)), 48), Occurrence: Occurrence{src, ch, gen, programmes[n]}, UseDefaults: true})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	state := func(id string) (state, reason string) {
		t.Helper()
		if err := db.QueryRow(`SELECT state,reason FROM dvr_recordings WHERE id=?`, id).Scan(&state, &reason); err != nil {
			t.Fatal(err)
		}
		return state, reason
	}
	attempt := func() {
		t.Helper()
		clock = clock.Add(4 * time.Second)
		if c, _, err := s.claimNext(ctx); err != nil || c != nil {
			t.Fatalf("claim on a full disk: %v %v", c, err)
		}
	}
	queued := schedule(1)
	if _, err = db.Exec(`UPDATE dvr_recordings SET state='pending-delete',start_ms=1,end_ms=2 WHERE id=?`, queued); err != nil {
		t.Fatal(err)
	}
	recording := schedule(0)
	clock = start.Add(time.Minute)

	// While a deletion is queued, space is on its way and the recording waits.
	if _, err = db.Exec(`INSERT INTO dvr_delete_work VALUES(?,1,?,?,'waiting',0,'')`, queued, clock.UnixMilli(), clock.Add(2*time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	attempt()
	attempt()
	if st, reason := state(recording); st != "scheduled" || reason != "storage-floor" {
		t.Fatalf("a recording was ended while a deletion was still queued: %s (%s)", st, reason)
	}
	// Nothing more is coming and the disk is still full: now it fails, and says why.
	if _, err = db.Exec(`DELETE FROM dvr_delete_work`); err != nil {
		t.Fatal(err)
	}
	attempt()
	if st, reason := state(recording); st != "failed" || reason != "storage-floor" {
		t.Fatalf("a recording with no space and nothing queued did not fail: %s (%s)", st, reason)
	}

	// A queued deletion never holds a recording past its own window.
	late := schedule(2)
	if _, err = db.Exec(`INSERT INTO dvr_delete_work VALUES(?,1,?,?,'waiting',0,'')`, queued, clock.UnixMilli(), clock.Add(2*time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	storage.err = ErrStorageCap
	attempt()
	attempt()
	if st, reason := state(late); st != "scheduled" || reason != "storage-cap" {
		t.Fatalf("a recording over the cap did not wait for the queued deletion: %s (%s)", st, reason)
	}
	clock = end.Add(time.Hour)
	attempt()
	if st, reason := state(late); st != "failed" || reason != "storage-cap" {
		t.Fatalf("a recording kept waiting after its window had passed: %s (%s)", st, reason)
	}
}

// With several recordings due on a slow, full volume, one pass can take longer
// than the wait before a recording is looked at again. Retention must still
// get its turn before any of them is judged: what it may delete is queued
// first, the recordings wait for it, and the first one starts once the space
// is there.
func TestRecordingsDueOnAFullDiskWaitForRetentionWithinOnePass(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recordingProfiles(t, db, "account", "profile")
	src, gen, ch := strings.Repeat("a", 48), strings.Repeat("b", 48), strings.Repeat("c", 64)
	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	for _, q := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO live_source_identities VALUES(?)`, []any{src}},
		{`INSERT INTO live_generations VALUES(?,?,'Guide',4,1,1,?)`, []any{gen, src, now.Format(time.RFC3339)}},
		{`INSERT INTO live_sources VALUES(?,'Aerial',1,'active',4,?,?)`, []any{src, gen, now.Format(time.RFC3339)}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'channel','Channel','1',NULL,'',1,CAST('https://provider.invalid/stream' AS BLOB),'','')`, []any{gen, ch}},
		{`INSERT OR REPLACE INTO dvr_storage_policy VALUES(1,1,1,0,0,0)`, nil},
	} {
		if _, err = db.Exec(q.q, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	clock := now
	storage := &shortStorage{err: ErrStorageFloor}
	driver := &reclaimingDriver{storage: storage}
	s := &Store{db: db, live: &livechannels.Store{}, now: func() time.Time { return clock }, captureAvailable: true, storage: storage, driver: driver, durable: func(context.Context, *sql.Tx, livechannels.Owner, string, string) error { return nil }}
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	schedule := func(n int) string {
		t.Helper()
		programme := strings.Repeat(string("def9"[n]), 64)
		if _, err := db.Exec(`INSERT INTO live_programmes VALUES(?,?,?,?,'Episode',?,?,?)`, gen, programme, ch, "programme-"+string(rune('a'+n)), start.Format(time.RFC3339), end.Format(time.RFC3339), "lineage-"+string(rune('a'+n))); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO live_programme_metadata(generation_id,id,series_id,episode_id,new_evidence,description) VALUES(?,?,'','','','')`, gen, programme); err != nil {
			t.Fatal(err)
		}
		r, err := s.Schedule(ctx, auth, owner, ScheduleInput{RequestID: strings.Repeat(string(rune('1'+n)), 48), Occurrence: Occurrence{src, ch, gen, programme}, UseDefaults: true})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	state := func(id string) (state, reason string) {
		t.Helper()
		if err := db.QueryRow(`SELECT state,reason FROM dvr_recordings WHERE id=?`, id).Scan(&state, &reason); err != nil {
			t.Fatal(err)
		}
		return state, reason
	}
	// An old finished recording the retention policy no longer keeps.
	old := schedule(0)
	if _, err = db.Exec(`UPDATE dvr_recordings SET state='completed',start_ms=1,end_ms=2,finished_ms=?,bytes=1000,options_json=json_set(options_json,'$.retentionDays',1) WHERE id=?`, now.Add(-72*time.Hour).UnixMilli(), old); err != nil {
		t.Fatal(err)
	}
	if err = unreserve(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	due := []string{schedule(1), schedule(2), schedule(3)}
	clock = start.Add(time.Minute)
	// Every look at the volume takes two seconds, so the pass outlasts the
	// three-second wait and meets the first recording a second time.
	storage.checked = func() { clock = clock.Add(2 * time.Second) }

	s.capturePass(ctx)
	for _, id := range due {
		if st, reason := state(id); st != "scheduled" {
			t.Fatalf("a recording was ended before retention had its turn: %s (%s)", st, reason)
		}
	}
	if st, _ := state(old); st != "pending-delete" {
		t.Fatalf("retention did not queue the recording it no longer keeps: %s", st)
	}

	// Once the queued deletion's grace has passed, a pass removes it.
	storage.checked = nil
	clock = clock.Add(3 * time.Minute)
	s.capturePass(ctx)
	if driver.removed != 1 || storage.err != nil {
		t.Fatalf("the queued deletion did not run: removed=%d storage=%v", driver.removed, storage.err)
	}
	for _, id := range due {
		if st, reason := state(id); st != "scheduled" {
			t.Fatalf("a recording was ended while space was being freed: %s (%s)", st, reason)
		}
	}
	// With room on the volume the waiting recording is claimed and starts.
	clock = clock.Add(4 * time.Second)
	c, _, err := s.claimNext(ctx)
	if err != nil || c == nil {
		t.Fatalf("the recording did not start once space was freed: %v %v", c, err)
	}
	if st, reason := state(c.request.Recording.ID); st != "preparing" || reason != "" {
		t.Fatalf("the claimed recording is %s (%s)", st, reason)
	}
}

func unreserve(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM live_reservations WHERE id=?`, id)
	return err
}
