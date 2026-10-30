package metadata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestMBSameReleaseAdoptionConflictRollsBackFinalAndRetryConverges(t *testing.T) {
	db, names := mbLaneDB(t)
	c := catalogtest.New(t, db)
	artist := c.Artist(c.Handle("lib"), "Second local artist")
	second := c.Album(artist, "Second local album", 2020)
	s := New(db, "")
	first := mbStagingJob(t, s, "album")
	if first.base.Entity != names["album"].ID {
		t.Fatal("wrong first fixture job", first.base.Entity)
	}
	value := mbLaneReleaseFixture()
	lookup := metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value}
	if err := s.mbPublishAlbum(context.Background(), first, lookup, "2026-10-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind='album' AND entity_id<>?`, second.ID)
	c.Exec(`UPDATE mb_jobs SET status='pending' WHERE kind='album' AND entity_id=?`, second.ID)
	job, err := s.claimMB(context.Background())
	if err != nil || job == nil || job.base.Entity != second.ID {
		t.Fatal(job, err)
	}
	beforeRecon := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?`, second.ID)
	refused := s.mbPublishAlbum(context.Background(), *job, lookup, "2026-10-28T01:00:00Z")
	if !errors.Is(refused, errTVDBStale) || !errors.Is(refused, errMBAdoptionPending) {
		t.Fatalf("competing same release was not stale/retry: %v", refused)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_links WHERE album_id=?`, second.ID); n != 0 {
		t.Fatal("second pin escaped final rollback", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?`, second.ID); n != beforeRecon {
		t.Fatal("second reconciliation escaped rollback", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='mb-adoption' AND cause_operation=?`, job.operation); n != 0 {
		t.Fatal("second adoption escaped rollback", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='album' AND entity_id=? AND status='matched'`, second.ID); n != 0 {
		t.Fatal("job outcome escaped rollback")
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_publication_operations WHERE id=? AND status='staged'`, job.operation); n != 1 {
		t.Fatal("separately committed stage changed", n)
	}
	var release, observed string
	if err = db.QueryRow(`SELECT release_id FROM catalog_albums WHERE entity_id=?`, second.ID).Scan(&release); err != nil || release != "" {
		t.Fatal("catalog effect escaped rollback", release, err)
	}
	if err = db.QueryRow(`SELECT observed_at FROM mb_release_aliases WHERE requested_id=?`, value.ID).Scan(&observed); err != nil || observed != "2026-10-28T00:00:00Z" {
		t.Fatal("alias effect escaped rollback", observed, err)
	}
	assertMBAccounting(t, db)
	if err = s.mbResult(context.Background(), *job, refused); err != nil {
		t.Fatal(err)
	}
	// The production provider loop runs accounting maintenance before the
	// next admission. This direct publisher fixture must also drain the
	// rejected publication's durable abort-fold, rather than asking a fresh
	// operation to publish while that owner still holds the singleton.
	var phase, owner string
	var published bool
	if err = db.QueryRow(`SELECT phase,owner_operation,published_next FROM mb_accounting_state WHERE singleton=1`).Scan(&phase, &owner, &published); err != nil || phase != "fold" || owner != job.operation || published {
		t.Fatal("refused publication did not retain its private abort-fold", phase, owner, published, err)
	}
	facts := readPickCount(t, db, `SELECT count(*) FROM mb_recording_accounting_next WHERE transition_id=(SELECT transition_id FROM mb_accounting_state WHERE singleton=1)`)
	foldTurns := max(1, (facts+mbaccounting.FactsPerTurn-1)/mbaccounting.FactsPerTurn)
	idle := false
	for turn := int64(0); turn <= foldTurns; turn++ {
		held, worked, maintenanceErr := s.mbAccountingMaintenance(context.Background())
		if maintenanceErr != nil {
			t.Fatal(maintenanceErr)
		}
		assertMBAccounting(t, db)
		if !held {
			idle = true
			break
		}
		if !worked {
			t.Fatal("private abort-fold stopped progressing")
		}
	}
	if !idle {
		t.Fatal("private abort-fold exceeded its actual fact windows")
	}
	_, retryProgress := metadataProgress(context.Background())
	if delay := s.providerPassRetry(retryProgress); delay <= 0 || delay > 30*time.Second {
		t.Fatal("pending adoption lost its due-time wake", delay)
	}

	var retryAt string
	if err = db.QueryRow(`SELECT next_attempt FROM mb_jobs WHERE kind='album' AND entity_id=?`, second.ID).Scan(&retryAt); err != nil || retryAt <= s.publicationTime().Format(time.RFC3339) {
		t.Fatal("contended head did not yield", retryAt, err)
	}
	thirdArtist := c.Artist(c.Handle("lib"), "Unrelated artist")
	third := c.Album(thirdArtist, "Unrelated album", 2020)
	unrelated, claimErr := s.claimMB(context.Background())
	if claimErr != nil || unrelated == nil || unrelated.base.Entity != third.ID {
		t.Fatal("contended head blocked unrelated provider work", unrelated, claimErr)
	}
	driveArtistHandoffOperations(t, db)
	// Make the existing retry due without sleeping; its operation/base is freshly
	// claimed rather than reusing the rejected publication's generation.
	c.Exec(`UPDATE mb_jobs SET next_attempt='' WHERE kind='album' AND entity_id=?`, second.ID)
	job, err = s.claimMB(context.Background())
	if err != nil || job == nil || job.base.Entity != second.ID {
		t.Fatal(job, err)
	}

	if err = s.mbPublishAlbum(context.Background(), *job, lookup, "2026-10-28T01:00:00Z"); err != nil {
		t.Fatal("second publication did not retry", err)
	}
	driveArtistHandoffOperations(t, db)
	if n := readPickCount(t, db, `SELECT count(*) FROM catalog_entity_mutations WHERE status<>'terminal'`); n != 0 {
		t.Fatal("same release adoption did not converge", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_links WHERE release_id=?`, value.ID); n != 1 {
		t.Fatal("same release albums were not merged", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_links WHERE album_id=? AND release_id=?`, second.ID, value.ID); n != 1 {
		t.Fatal("current publication survivor not retained", n)
	}
	assertMBAccounting(t, db)
}
