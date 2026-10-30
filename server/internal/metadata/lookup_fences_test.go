package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func lookupABATest(t *testing.T, s *Service) {
	t.Helper()
	for _, online := range []bool{false, true} {
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { in.OnlineLookups = &online })
	}
}

func TestLookupFanoutScreenPublicationFenceBeforeSettlement(t *testing.T) {
	s, db, provider := screenFixture(t)
	settleScreenFanout(t, s)
	claim, err := s.claimScreen(context.Background())
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	generation := publicationScalar(t, db, `SELECT generation FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, claim.Base.TargetKind, claim.Base.Entity)
	lookupABATest(t, s)
	if got := publicationScalar(t, db, `SELECT generation FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, claim.Base.TargetKind, claim.Base.Entity); got != generation {
		t.Fatal("save touched work before fanout", got, generation)
	}
	w, err := dbwork.Begin(context.Background(), db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.screenCurrent(context.Background(), w.Tx(), claim)
	w.Rollback()
	if err != nil || ok {
		t.Fatal("pre-toggle screen claim retained authority", ok, err)
	}
	if err = s.publishScreen(context.Background(), claim, []screenCandidate{scoreScreen(provider.record, nil, 0, true)}, 0, "matched"); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_publications`); got != "0" {
		t.Fatal("stale screen result published", got)
	}
}

func TestLookupFanoutMBPublicationFenceBeforeSettlement(t *testing.T) {
	db, names := mbLaneDB(t)
	publicationExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album';INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_trackid','embedded',?);UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`, names["song"].Token, mbLaneRecord)
	s := New(db, "")
	called := false
	s.mb = mbLaneFixture{record: func(context.Context, string) (metadataprovider.RecordingLookup, error) {
		called = true
		lookupABATest(t, s)
		return metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()}, nil
	}}
	if err := s.MusicBrainzStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("fixture did not acquire provider result")
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM mb_song_links`); got != "0" {
		t.Fatal("stale MB result published", got)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM mb_publication_operations WHERE status='stale' AND result_recording_revision IS NULL`); got != "1" {
		t.Fatal("stale MB result was not retired", got)
	}
}

func TestLookupFanoutTVDBPublicationFenceBeforeSettlement(t *testing.T) {
	s, db := tvdbPublicationDB(t)
	claim := tvdbPublicationClaim(t, s)
	generation := publicationScalar(t, db, `SELECT generation FROM tvdb_jobs WHERE show_id=?`, claim.ShowID)
	lookupABATest(t, New(db, ""))
	if got := publicationScalar(t, db, `SELECT generation FROM tvdb_jobs WHERE show_id=?`, claim.ShowID); got != generation {
		t.Fatal("save touched TVDB work before fanout", got, generation)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = s.activeTVDBPublication(context.Background(), tx, *claim); !errors.Is(err, errTVDBStale) {
		t.Fatal("pre-toggle TVDB claim retained authority", err)
	}
}

func TestLookupFanoutArtworkFenceBeforeSettlement(t *testing.T) {
	s, db, target := repairFixture(t)
	read := func() string {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fence, err := artworkFence(context.Background(), tx, target)
		if err != nil {
			t.Fatal(err)
		}
		return fence
	}
	before := read()
	lookupABATest(t, s)
	if got := read(); got == before {
		t.Fatal("artwork authority survived lookup ABA")
	}
	if got := publicationScalar(t, db, `SELECT pass_generation FROM metadata_lookup_fanout WHERE effect='artwork'`); got != "0" {
		t.Fatal("fixture unexpectedly ran fanout", got)
	}
}

func TestLookupFanoutAcceptingWriteBound(t *testing.T) {
	var writes []int64
	for _, n := range []int{1, 37} {
		s, db, _ := fanoutMovies(t, n)
		current, err := s.MetadataSettings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				return err
			}
			off := false
			if _, err := ApplyMetadataSettingsTx(context.Background(), tx, MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult, OnlineLookups: &off}, s.publicationTime()); err != nil {
				return err
			}
			return tx.QueryRow(`SELECT total_changes()`).Scan(&after)
		})
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, after-before)
	}
	if writes[0] != writes[1] || writes[0] > 10 {
		t.Fatal("accepting write grew with catalogue", writes)
	}
	t.Logf("1 / 37 row accepting write totals: %v", writes)
}

func TestLookupFanoutSettingsAndIntentRollbackTogether(t *testing.T) {
	s, db, _ := fanoutMovies(t, 2)
	before := publicationScalar(t, db, `SELECT revision||':'||lookup_epoch||':'||online_lookups FROM metadata_runtime_settings WHERE singleton=1`)
	current, err := s.MetadataSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	publicationExec(t, db, `CREATE TRIGGER lookup_intent_fail BEFORE INSERT ON metadata_lookup_fanout WHEN NEW.effect='mb' BEGIN SELECT RAISE(ABORT,'fixture intent failure');END`)
	off := false
	if _, err = s.UpdateMetadataSettings(context.Background(), MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult, OnlineLookups: &off}, nil); err == nil {
		t.Fatal("incomplete intent was accepted")
	}
	if got := publicationScalar(t, db, `SELECT revision||':'||lookup_epoch||':'||online_lookups FROM metadata_runtime_settings WHERE singleton=1`); got != before {
		t.Fatal("failed save changed immediate policy", got, before)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM metadata_lookup_fanout`); got != "0" {
		t.Fatal("failed save left partial intent", got)
	}
}
