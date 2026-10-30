package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func mbRetireTestMark(t *testing.T, db *sql.DB, kind, rev, state, owner, parent string, expires ...string) {
	t.Helper()
	zero := strings.Repeat("0", 64)
	expiry := "2000-01-01T00:00:00Z"
	if len(expires) > 0 {
		expiry = expires[0]
	}
	_, err := db.Exec(`INSERT INTO mb_evidence_builds(build_id,kind,revision_id,provider_id,digest,state,phase,expected_shape_digest,expected_rows,materialized_rows,materialized_digest,parent_build,owner_operation,expires_at) SELECT revision_id,?,revision_id,provider_id,digest,?,'cleanup',?,1,1,?,?,?,? FROM mb_`+kind+`_evidence WHERE revision_id=?`, kind, state, zero, zero, parent, owner, expiry, rev)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMBRetireNormalizedWindowsAndParentGuard(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	value := mbLaneReleaseFixture()
	value.Media[0].Tracks = nil
	for i := 0; i < 97; i++ {
		r := mbLaneRecordingFixture()
		r.ID = fmt.Sprintf("%08x-0000-0000-0000-000000000000", i+1000)
		value.Media[0].Tracks = append(value.Media[0].Tracks, metadataprovider.ReleaseTrack{ID: fmt.Sprintf("%08x-0000-0000-0000-000000000000", i+2000), Position: i + 1, Recording: r})
	}
	stage, err := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: mbLaneRelease, Release: value})
	if err != nil {
		t.Fatal(err)
	}
	var rev string
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var e error
		rev, e = saveMBReleaseEvidence(context.Background(), tx, stage, "2000-01-01T00:00:00Z")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM mb_release_evidence WHERE revision_id=?`, rev); err == nil {
		t.Fatal("parent cascade bypassed normalized retirement")
	}
	s := New(db, "")
	for turn := 0; turn < 150; turn++ {
		if readPickCount(t, db, `SELECT count(*) FROM mb_release_evidence WHERE revision_id=?`, rev) == 0 {
			break
		}
		dbwork.ResetPlans()
		if err = s.mbRetireEvidence(context.Background()); err != nil {
			t.Fatal(turn, err)
		}
		assertMBAccounting(t, db)
		if got := dbwork.Plans().Largest; got.Rows > 160 {
			t.Fatalf("turn%d exceeded transitive plan: %+v", turn, got)
		}
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_release_evidence WHERE revision_id=?`, rev); n != 0 {
		t.Fatal("release did not finish", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_evidence_builds WHERE kind='release' AND revision_id=?`, rev); n != 0 {
		t.Fatal("finished build retained", n)
	}
}

func TestMBRetireLiveOwnerAndAbortedOriginChildren(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	r := mbLaneRecordingFixture()
	stage, err := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: r.ID, Recording: r})
	if err != nil {
		t.Fatal(err)
	}
	var child string
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var e error
		child, e = saveMBRecordingEvidence(context.Background(), tx, stage, "2000-01-01T00:00:00Z")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	mbRetireTestMark(t, db, "recording", child, "ready", j.operation, "")
	if err = s.mbRetireEvidence(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_recording_evidence WHERE revision_id=?`, child); n != 1 {
		t.Fatal("live inter-quantum child reservation retired")
	}
	// Expired owner is no authority. Its ready child is drained without waiting
	// thirty days, even though its parent never got as far as a track insert.
	mbCurrentExec(t, db, `UPDATE mb_jobs SET lease_until='2000-01-01T00:00:00Z'`)
	for i := 0; i < 8; i++ {
		if err = s.mbRetireEvidence(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertMBAccounting(t, db)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_recording_evidence WHERE revision_id=?`, child); n != 0 {
		t.Fatal("expired ready reservation leaked")
	}
	// Newly made child provenance survives owner clearing when its track is
	// attached; aborting the parent drains/detaches that origin before its row.
	release := readPickRelease(t, db, "Aborted origin")
	var origin string
	if err = db.QueryRow(`SELECT recording_revision FROM mb_release_tracks WHERE release_revision=? LIMIT 1`, release).Scan(&origin); err != nil {
		t.Fatal(err)
	}
	mbRetireTestMark(t, db, "release", release, "aborting", "", "")
	mbRetireTestMark(t, db, "recording", origin, "ready", "", release)
	for i := 0; i < 25; i++ {
		if err = s.mbRetireEvidence(context.Background()); err != nil {
			t.Fatal(i, err)
		}
		assertMBAccounting(t, db)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_evidence_builds WHERE revision_id IN(?,?)`, release, origin); n != 0 {
		t.Fatal("abort ancestry leaked", n)
	}
}

func TestMBRetireRollbackRetainsRowsAndCharge(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	rev := readPickRelease(t, db, "Rollback")
	mbRetireTestMark(t, db, "release", rev, "aborting", "", "")
	before := readPickCount(t, db, `SELECT charge FROM mb_evidence_totals`)
	w, err := dbwork.Begin(context.Background(), db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	target, err := mbRetirePick(context.Background(), w.Tx(), time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil || target == nil {
		t.Fatal(target, err)
	}
	if _, err = mbRetireRowsTx(context.Background(), w.Tx(), target); err != nil {
		t.Fatal(err)
	}
	if err = w.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := readPickCount(t, db, `SELECT charge FROM mb_evidence_totals`); n != before {
		t.Fatal("rollback lost charge", n, before)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_release_tracks WHERE release_revision=?`, rev); n != 1 {
		t.Fatal("rollback lost track", n)
	}
	assertMBAccounting(t, db)
}

func TestMBRetireProtectedReadyPrefixReleasesReservations(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	s := New(db, "")
	var orphan string
	for i := 0; i < 33; i++ {
		value := mbLaneRecordingFixture()
		value.ID = fmt.Sprintf("%08x-0000-0000-0000-000000000000", i+7000)
		stage, err := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: value.ID, Recording: value})
		if err != nil {
			t.Fatal(err)
		}
		var revision string
		if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			var e error
			revision, e = saveMBRecordingEvidence(context.Background(), tx, stage, time.Now().UTC().Format(time.RFC3339))
			return e
		}); err != nil {
			t.Fatal(err)
		}
		expiry := "2000-01-01T00:00:00Z"
		if i == 32 {
			expiry = "2001-01-01T00:00:00Z"
		}
		mbRetireTestMark(t, db, "recording", revision, "ready", "expired-foreign-owner", "", expiry)
		if i < 32 {
			if _, err = db.Exec(`INSERT INTO mb_recording_aliases VALUES(?,?,?,?)`, value.ID, value.ID, revision, time.Now().UTC().Format(time.RFC3339)); err != nil {
				t.Fatal(err)
			}
		} else {
			orphan = revision
		}
	}
	for turn := 0; turn < 42; turn++ {
		if err := s.mbRetireEvidence(context.Background()); err != nil {
			t.Fatal(turn, err)
		}
		assertMBAccounting(t, db)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_recording_evidence WHERE revision_id=?`, orphan); n != 0 {
		t.Fatal("protected prefix hid ready orphan", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_evidence_builds WHERE state='ready' AND owner_operation<>''`); n != 0 {
		t.Fatal("obsolete protected reservations retained", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_recording_aliases`); n != 32 {
		t.Fatal("protected evidence lost", n)
	}
}

func TestMBReceiptCleanupDrainsEveryNormalizedChild(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	if _, err := db.Exec(`DELETE FROM mb_operation_artists WHERE operation_id=?`, j.operation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM mb_operation_sources WHERE operation_id=?`, j.operation); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if _, err := db.Exec(`INSERT INTO mb_operation_artists VALUES(?,?,?,?)`, j.operation, i, "artist", "Artist"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO mb_operation_sources VALUES(?,?,?,0,0,NULL,'path',1,1,1)`, j.operation, i, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE mb_publication_operations SET status='failed',finished_at='2000-01-01T00:00:00Z' WHERE id=?`, j.operation); err != nil {
		t.Fatal(err)
	}
	children := int64(256)
	for turn := 0; turn < 16; turn++ {
		if err := s.mbCleanupReceipts(context.Background()); err != nil {
			t.Fatal(turn, err)
		}
		after := readPickCount(t, db, `SELECT (SELECT count(*) FROM mb_operation_artists WHERE operation_id=?)+(SELECT count(*) FROM mb_operation_sources WHERE operation_id=?)`, j.operation, j.operation)
		if children-after > mbRetireRows {
			t.Fatalf("turn%d cascaded%d children", turn, children-after)
		}
		children = after
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_publication_operations WHERE id=?`, j.operation); n != 0 {
		t.Fatal("empty operation not retired", n)
	}
	assertMBAccounting(t, db)
}
