package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// The original indivisible candidate/timing tests are retained on
// phase2/P2-52b-candidate at6380440fe. Production acceptance now exercises
// staged construction and the complete final writer in mb_staging_test.go.
func mbAccountingLargeRelease(side int) metadataprovider.ReleaseLookup {
	id := fmt.Sprintf("%08x-0000-0000-0000-000000000000", side)
	r := metadataprovider.ReleaseLookup{RequestedID: id, Release: metadataprovider.Release{
		ID: id, ReleaseGroup: metadataprovider.ReleaseGroup{ID: mbLaneGroup},
		Media: []metadataprovider.Medium{{Position: 1}},
	}}
	for n := 0; n < mbEvidenceTracks; n++ {
		r.Release.Media[0].Tracks = append(r.Release.Media[0].Tracks, metadataprovider.ReleaseTrack{
			ID: fmt.Sprintf("%08x-0000-0000-0000-%012x", side+100, n), Position: n + 1,
			Recording: metadataprovider.Recording{ID: fmt.Sprintf("%08x-0000-0000-0000-%012x", side+200, n)},
		})
	}
	return r
}

// mbFixturePinIntent is the explicit intended end state of one fixture pin.
// An empty recording or release means that reference is absent afterwards.
// Callers that only clear a song recording must pass the retained release
// explicitly; an unmatched song keeps its old release throughout.
type mbFixturePinIntent struct {
	Link         string // "album" or "song"
	Entity       int64
	NewRecording string // explicit canonical recording revision, empty when released
	NewRelease   string // explicit canonical release revision, empty when released
}

// mbFixturePinTurns bounds the fixture's real preparation/fold loops. It is a
// test guard, not a production cap: one transition carries at most the private
// fact ceiling of the coordinator.
const mbFixturePinTurns = 20002/mbaccounting.FactsPerTurn + 8

// mbFixturePinTx seeds one pin through the real private accounting coordinator
// inside the caller's existing fixture transaction. It is fixture seeding only:
// it claims no publication authority, performs no domain work, and makes no
// runtime-bound claim. apply must run the fixture's original pin statement
// (the INSERT/UPDATE/DELETE on mb_album_links or mb_song_links). BeginTx
// captures the actual old album/song pin by entity, PrepareTx reads the real
// raw track windows, FlipTx runs the original statement, and FoldTx settles
// every private fact before this helper returns. An independent aggregate
// oracle therefore only ever observes a completed transition, never a
// half-flipped one.
//
// The fixture preserves the original raw seed's admission policy exactly: the
// raw SQL previously carried no implicit budget check, so FlipTx is given the
// documented no-op cap callback. Only the tests' existing explicit
// checkMBEvidenceBudget calls keep deciding admission, at the same prospective
// boundaries as before (notably, releasing an accepted pin must not newly
// refuse merely because evicted evidence becomes over the cache budget). The
// production publisher still checks the exact cap after its own deltas.
func mbFixturePinTx(tx *sql.Tx, intent mbFixturePinIntent, apply func(*sql.Tx) error) error {
	if apply == nil {
		return errors.New("fixture pin requires its original statement")
	}
	if intent.Link != "album" && intent.Link != "song" {
		return fmt.Errorf("fixture pin link %q", intent.Link)
	}
	if intent.Entity <= 0 {
		return fmt.Errorf("fixture pin entity %d", intent.Entity)
	}
	ctx := context.Background()
	oldRelease, oldRecording, err := mbFixtureActualPin(tx, intent)
	if err != nil {
		return fmt.Errorf("read actual %s pin for %d: %w", intent.Link, intent.Entity, err)
	}
	change := mbaccounting.Change{
		Owner:        mbaccounting.Owner{Kind: "entity-mutation", Operation: fmt.Sprintf("fixture-pin-%s-%d", intent.Link, intent.Entity), Generation: 1},
		Entity:       intent.Entity,
		Link:         intent.Link,
		OldRelease:   oldRelease,
		OldRecording: oldRecording,
		NewRelease:   intent.NewRelease,
		NewRecording: intent.NewRecording,
	}
	// The fixture fingerprint covers the actual captured pin plus the explicit
	// intended revisions and the synthetic fixture owner. It is not a domain
	// digest and grants nothing on its own.
	sum := sha256.Sum256([]byte(fmt.Sprintf("fixture/%q/%d/%q/%q/%q/%q", change.Owner.Operation, change.Entity, oldRecording, oldRelease, change.NewRecording, change.NewRelease)))
	change.Digest = fmt.Sprintf("%x", sum)
	change.ID = "fixture:" + change.Digest
	if _, err = mbaccounting.BeginTx(ctx, tx, change); err != nil {
		return fmt.Errorf("begin fixture %s pin for %d: %w", intent.Link, intent.Entity, err)
	}
	for turn := 0; ; turn++ {
		ready, err := mbaccounting.PrepareTx(ctx, tx, change)
		if err != nil {
			return fmt.Errorf("prepare fixture %s pin for %d: %w", intent.Link, intent.Entity, err)
		}
		if ready {
			break
		}
		if turn >= mbFixturePinTurns {
			return fmt.Errorf("fixture %s pin for %d did not prepare", intent.Link, intent.Entity)
		}
	}
	// FlipTx requires a cap callback. A pure fixture seed keeps the raw seed's
	// admission policy, so it passes the documented no-op: this grants no
	// runtime claim and adds no refusal the raw fixture never had.
	if err = mbaccounting.FlipTx(ctx, tx, change, func() error { return apply(tx) }, func() error { return nil }); err != nil {
		return fmt.Errorf("flip fixture %s pin for %d: %w", intent.Link, intent.Entity, err)
	}
	for turn := 0; ; turn++ {
		done, err := mbaccounting.FoldTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("fold fixture %s pin for %d: %w", intent.Link, intent.Entity, err)
		}
		if done {
			return nil
		}
		if turn >= mbFixturePinTurns {
			return fmt.Errorf("fixture %s pin for %d did not fold", intent.Link, intent.Entity)
		}
	}
}

// mbFixturePin runs one fixture pin in its own transaction.
func mbFixturePin(t *testing.T, db *sql.DB, intent mbFixturePinIntent, apply func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = mbFixturePinTx(tx, intent, apply); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// mbFixtureActualPin reads the pin the fixture actually accepted for the
// entity, exactly as the production change builder does.
func mbFixtureActualPin(tx *sql.Tx, intent mbFixturePinIntent) (release, recording string, err error) {
	var releaseValue, recordingValue sql.NullString
	if intent.Link == "album" {
		err = tx.QueryRow(`SELECT release_revision FROM mb_album_links WHERE album_id=?`, intent.Entity).Scan(&releaseValue)
	} else {
		err = tx.QueryRow(`SELECT recording_revision,release_revision FROM mb_song_links WHERE item_id=?`, intent.Entity).Scan(&recordingValue, &releaseValue)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if releaseValue.Valid {
		release = releaseValue.String
	}
	if recordingValue.Valid {
		recording = recordingValue.String
	}
	return release, recording, nil
}
