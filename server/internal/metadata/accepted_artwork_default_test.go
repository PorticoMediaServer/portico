package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Queue-policy model proof: accepted keys are synthetic, and no image bytes
// are fetched or rendered. Real image acceptance has separate source tests.
func TestAcceptedArtworkDefaultQueuesOnlyMaterializedPreferredAndKeepsOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, target := repairFixture(t)
	c := catalogtest.New(t, db)
	item := integrationMovie(t, db)
	ctx := context.Background()
	var source, incarnation, fence string
	if err := db.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='lib'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('default-object',?,?,?,'movie.mkv','r1','{}',1,1)`, source, item.Token, incarnation)
	alt, canonical := strings.Repeat("7", 64), strings.Repeat("c", 64)
	accept := func(key, name string, rank int64) {
		t.Helper()
		c.Exec(`INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES('default-object','r1',?,'item',?,'poster',?,?,?)`, item.Token, item.ID, key, name, rank)
	}
	accept(alt, "poster-7.png", 1031)
	run := func(fn func(*sql.Tx) error) error {
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, fn)
	}
	if err := run(func(tx *sql.Tx) error { var err error; fence, err = artworkFence(ctx, tx, target); return err }); err != nil {
		t.Fatal(err)
	}
	materialize := func(key string) string {
		t.Helper()
		var id string
		if err := run(func(tx *sql.Tx) error {
			var err error
			origin := "local:" + key
			id, err = insertArtworkCandidate(ctx, tx, target, "poster", "", "local", publicationDigest(origin), origin, "", artworkAttribution("local"), fence, "2026-10-24", 0)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	queue := func() error {
		return run(func(tx *sql.Tx) error {
			return queueAcceptedLocalArtworkDefaultTx(ctx, tx, target, "poster", fence, "2026-10-24")
		})
	}
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	if n := acceptedArtworkDefaultCount(t, db, `SELECT count(*) FROM artwork_jobs`); n != 0 {
		t.Fatal("default bypassed bounded candidate materialization", n)
	}
	altID := materialize(alt)
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	var initial, candidate, lease string
	if err := db.QueryRow(`SELECT id,candidate_id FROM artwork_jobs WHERE role='poster'`).Scan(&initial, &candidate); err != nil || candidate != altID {
		t.Fatal("first actual default not queued", candidate, err)
	}
	c.Exec(`UPDATE artwork_jobs SET status='running',lease='alternate-lease' WHERE id=?`, initial)
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT lease FROM artwork_jobs WHERE id=?`, initial).Scan(&lease); err != nil || lease != "alternate-lease" {
		t.Fatal("repeated dirty pass reset lease", lease, err)
	}
	accept(canonical, "poster.jpg", 16)
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT candidate_id,lease FROM artwork_jobs WHERE id=?`, initial).Scan(&candidate, &lease); err != nil || candidate != altID || lease != "alternate-lease" {
		t.Fatal("unmaterialized preferred key changed job", candidate, lease, err)
	}
	canonicalID := materialize(canonical)
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	var replacement string
	if err := db.QueryRow(`SELECT id,candidate_id,lease FROM artwork_jobs WHERE role='poster'`).Scan(&replacement, &candidate, &lease); err != nil || replacement == initial || candidate != canonicalID || lease != "" {
		t.Fatal("later canonical did not replace automatic alternate job", candidate, lease, err)
	}
	c.Exec(`UPDATE artwork_jobs SET status='running',lease='canonical-lease' WHERE id=?`, replacement)
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT lease FROM artwork_jobs WHERE id=?`, replacement).Scan(&lease); err != nil || lease != "canonical-lease" {
		t.Fatal("preferred job lease changed", lease, err)
	}
	if n := acceptedArtworkDefaultCount(t, db, `SELECT count(*) FROM artwork_candidates`); n != 2 {
		t.Fatal("default queue inserted extra candidates", n)
	}
	// The real owner command supersedes the automatic job. A subsequent dirty
	// pass must not replace that explicit choice with the ranked default.
	authorizations := 0
	authorize := func(*sql.Tx) error { authorizations++; return nil }
	state, err := s.RepairState(ctx, target, authorize)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: state.Revision, Action: "select_artwork", CandidateID: altID, Confirm: true}, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, authorize)
	if err != nil || authorizations == 0 {
		t.Fatal("actual owner command", err)
	}
	var ownerJob string
	if err = db.QueryRow(`SELECT id FROM artwork_jobs WHERE actor<>'' AND locked=1`).Scan(&ownerJob); err != nil {
		t.Fatal(err)
	}
	if err = queue(); err != nil {
		t.Fatal(err)
	}
	if n := acceptedArtworkDefaultCount(t, db, `SELECT count(*) FROM artwork_jobs WHERE id=? AND candidate_id=? AND actor<>'' AND locked=1`, ownerJob, altID); n != 1 {
		t.Fatal("default displaced explicit owner job")
	}
}

func TestAcceptedArtworkDefaultRefusesActualSourceFenceDrift(t *testing.T) {
	_, db, target := repairFixture(t)
	c := catalogtest.New(t, db)
	item := integrationMovie(t, db)
	ctx := context.Background()
	var source, incarnation string
	if err := db.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='lib'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('default-object',?,?,?,'movie.mkv','r1','{}',1,1)`, source, item.Token, incarnation)
	c.Exec(`INSERT INTO inventory_artwork_candidates(object_id,source_revision,asset_id,kind,entity_id,role,art_key,source_name,source_rank) VALUES('default-object','r1',?,'item',?,'poster',?,'poster.jpg',16)`, item.Token, item.ID, key)
	var fence string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		fence, err = artworkFence(ctx, tx, target)
		if err != nil {
			return err
		}
		origin := "local:" + key
		_, err = insertArtworkCandidate(ctx, tx, target, "poster", "", "local", publicationDigest(origin), origin, "", artworkAttribution("local"), fence, "2026-10-24", 0)
		return err
	})
	c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, source)
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return queueAcceptedLocalArtworkDefaultTx(ctx, tx, target, "poster", fence, "2026-10-24")
	})
	if !errors.Is(err, ErrRepairConflict) {
		t.Fatal("changed source queued with captured old fence", err)
	}
	if n := acceptedArtworkDefaultCount(t, db, `SELECT count(*) FROM artwork_jobs`); n != 0 {
		t.Fatal("stale default escaped transaction", n)
	}
}

func acceptedArtworkDefaultCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
