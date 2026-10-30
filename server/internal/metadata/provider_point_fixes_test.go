package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// tvdbPointClaim writes the provider evidence a credit claim is picked from.
func tvdbPointClaim(t *testing.T, db *sql.DB, item, providerID int64) {
	t.Helper()
	mbCurrentExec(t, db, `INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,'tvdb',?,'{}','now')`, item, providerID)
}

// A claim is only a read-pick: the episode can be rematched, its library's
// policy disabled or its kind changed while the fetch is out. The publishing
// transaction must recheck the pair and drop a result that is no longer
// current, leaving no credits, result or attempt behind. Seeded unfinished
// evidence is synchronous pair bookkeeping. Sibling
// claims picked in the same step stay independent.
func TestTVDBEpisodeCreditsDropARematchedFetch(t *testing.T) {
	t.Run("rematched", func(t *testing.T) {
		db, _ := tvdbDB(t)
		defer db.Close()
		names := tvdbNames(t, db)
		tvdbPointClaim(t, db, names["one"].ID, 100)
		tvdbPointClaim(t, db, names["two"].ID, 200)
		s := New(db, "")
		fetched := []int64{}
		s.tvdb = tvdbEpisodeFixture{characters: func(_ context.Context, id int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
			fetched = append(fetched, id)
			if id == 100 {
				// The catalogue rematches this episode while its fetch is out.
				mbCurrentExec(t, db, `UPDATE provider_evidence SET provider_id=999 WHERE item_id=? AND provider='tvdb'`, names["one"].ID)
			}
			return []metadataprovider.TVDBEpisodeCharacter{{PersonID: id, PersonName: "Guest", Type: "Actor", Character: "Captain"}}, nil
		}}
		before := backgroundGateAcquisitions()
		if err := s.tvdbEpisodeCreditsStep(readPickContext(t)); err != nil {
			t.Fatal(err)
		}
		if len(fetched) != 2 || fetched[0] != 100 || fetched[1] != 200 {
			t.Fatalf("fetches=%v, want the claimed page [100 200]", fetched)
		}
		if n := backgroundGateAcquisitions() - before; n != 1 {
			t.Fatalf("step acquired %d writers, want 1", n)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'`, names["one"].ID); n != 0 {
			t.Fatalf("rematched fetch wrote %d credits for the old pair", n)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100 AND (credits IS NOT NULL OR attempts<>0)`, names["one"].ID); n != 0 {
			t.Fatalf("rematched pair left %d results or attempts", n)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=999 AND credits IS NULL AND attempts=0`, names["one"].ID); n != 1 {
			t.Fatal("new-pair bookkeeping missing")
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM provider_evidence WHERE item_id=? AND provider='tvdb' AND provider_id=999`, names["one"].ID); n != 1 {
			t.Fatal("fixture rematch did not take effect")
		}
		// The sibling pair in the same step is still current and publishes.
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=200 AND credits=1 AND attempts=0`, names["two"].ID); n != 1 {
			t.Fatal("current sibling evidence missing or changed")
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'`, names["two"].ID); n != 1 {
			t.Fatal("current sibling credits missing")
		}
	})
	t.Run("policy disabled", func(t *testing.T) {
		db, _ := tvdbDB(t)
		defer db.Close()
		names := tvdbNames(t, db)
		tvdbPointClaim(t, db, names["one"].ID, 100)
		disabled := false
		s := New(db, "")
		s.tvdb = tvdbEpisodeFixture{characters: func(_ context.Context, id int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
			// TVDB is turned off for the library while the fetch is out.
			mbCurrentExec(t, db, `UPDATE tvdb_provider_policies SET enabled=0`)
			disabled = true
			return []metadataprovider.TVDBEpisodeCharacter{{PersonID: id, PersonName: "Guest", Type: "Actor", Character: "Captain"}}, nil
		}}
		if err := s.tvdbEpisodeCreditsStep(readPickContext(t)); err != nil {
			t.Fatal(err)
		}
		if !disabled {
			t.Fatal("claimed fetch never ran; the drop was not exercised")
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_provider_policies WHERE enabled=0`); n != 1 {
			t.Fatal("fixture policy change did not take effect")
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'`, names["one"].ID); n != 0 {
			t.Fatalf("disabled policy still wrote %d credits", n)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100 AND (credits IS NOT NULL OR attempts<>0)`, names["one"].ID); n != 0 {
			t.Fatalf("disabled policy left %d results or attempts", n)
		}
	})
}

// Cleanup of orphaned evidence runs inside the writer, so it removes one
// writer-sized batch per step instead of every orphan at once.
func TestTVDBEpisodeCreditsCleanupIsWriterSized(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	for i := 0; i < 40; i++ {
		mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,?,'now',NULL,0)`, names["one"].ID, 1000+i)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence`); n != 40 {
		t.Fatalf("fixture orphans=%d, want 40", n)
	}
	s := New(db, "")
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		t.Fatal("orphan cleanup fetched an episode")
		return nil, nil
	}}
	before := backgroundGateAcquisitions()
	if err := s.tvdbEpisodeCreditsStep(readPickContext(t)); err != nil {
		t.Fatal(err)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence`); n != 8 {
		t.Fatalf("first step left %d orphans, want 8 (32 removed)", n)
	}
	// The kept batch is the tail of the ordered keys, not an arbitrary 32.
	if n := readPickCount(t, db, `SELECT min(provider_id) FROM tvdb_episode_credit_evidence`); n != 1032 {
		t.Fatalf("first step kept provider_id from %d, want 1032", n)
	}
	if n := backgroundGateAcquisitions() - before; n != 1 {
		t.Fatalf("first cleanup acquired %d writers, want 1", n)
	}
	if err := s.tvdbEpisodeCreditsStep(readPickContext(t)); err != nil {
		t.Fatal(err)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence`); n != 0 {
		t.Fatalf("second step left %d orphans", n)
	}
	if n := backgroundGateAcquisitions() - before; n != 2 {
		t.Fatalf("cleanup acquired %d writers over two steps, want 2", n)
	}
}

// Satisfied pairs and pairs at the attempt cap are neither claims nor orphans.
// A step with nothing to do must not take the writer gate at all.
func TestTVDBEpisodeCreditsIdleStepTakesNoWriter(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	tvdbPointClaim(t, db, names["one"].ID, 100)
	mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,100,'now',2,0) ON CONFLICT(item_id,provider_id) DO UPDATE SET credits=excluded.credits,attempts=excluded.attempts`, names["one"].ID)
	tvdbPointClaim(t, db, names["two"].ID, 200)
	mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,200,'now',NULL,?) ON CONFLICT(item_id,provider_id) DO UPDATE SET credits=excluded.credits,attempts=excluded.attempts`, names["two"].ID, tvdbEpisodeCreditAttempts)
	fetched := 0
	s := New(db, "")
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		fetched++
		return nil, nil
	}}
	before := backgroundGateAcquisitions()
	if err := s.tvdbEpisodeCreditsStep(readPickContext(t)); err != nil {
		t.Fatal(err)
	}
	if fetched != 0 {
		t.Fatalf("idle step fetched %d episodes", fetched)
	}
	if n := backgroundGateAcquisitions() - before; n != 0 {
		t.Fatalf("idle step acquired %d writers", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence`); n != 2 {
		t.Fatalf("idle step changed evidence: %d rows left", n)
	}
}

// mbReconcileTurnStatus is one album's reconciliation row.
type mbReconcileTurnStatus struct {
	status   string
	revision int64
}

func mbReconcileTurn(t *testing.T, db *sql.DB, album int64) mbReconcileTurnStatus {
	t.Helper()
	var got mbReconcileTurnStatus
	if err := db.QueryRow(`SELECT status,revision FROM mb_album_reconciliations WHERE album_id=?`, album).Scan(&got.status, &got.revision); err != nil {
		t.Fatal(err)
	}
	return got
}

// Albums take turns. An album whose membership keeps changing becomes due
// again every turn, but the pick resumes after the last album served and
// wraps, so later albums still get reconciled.
func TestMBReconcileAlbumsTakeTurns(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	first := names["album"].ID
	second := c.Album(names["artist"], "Second Album", 2021)
	c.Song(second, 1, "/music/second.flac", "Second Song")
	c.Drain()
	release := readPickRelease(t, db, "Turns")
	readPickPin(t, db, first, release, "pending")
	readPickPin(t, db, second.ID, release, "pending")
	s := New(db, "")
	// Turn 1: the lowest due album.
	if done, err := s.mbReconcileAlbums(readPickContext(t)); err != nil || !done {
		t.Fatal("turn 1", done, err)
	}
	if got := mbReconcileTurn(t, db, first); got != (mbReconcileTurnStatus{"complete", 2}) {
		t.Fatalf("turn 1 served %+v for the first album", got)
	}
	if got := mbReconcileTurn(t, db, second.ID); got != (mbReconcileTurnStatus{"pending", 1}) {
		t.Fatalf("turn 1 also served the second album: %+v", got)
	}
	// The first album is due again, but the second album still gets its turn.
	mbCurrentExec(t, db, `UPDATE mb_album_heads SET membership_revision=membership_revision+1 WHERE album_id=?`, first)
	if done, err := s.mbReconcileAlbums(readPickContext(t)); err != nil || !done {
		t.Fatal("turn 2", done, err)
	}
	if got := mbReconcileTurn(t, db, second.ID); got != (mbReconcileTurnStatus{"complete", 2}) {
		t.Fatalf("turn 2 did not serve the second album: %+v", got)
	}
	if got := mbReconcileTurn(t, db, first); got.revision != 2 {
		t.Fatalf("turn 2 served the first album again: %+v", got)
	}
	// Nothing due after the last album: the pick wraps to the lowest due one.
	mbCurrentExec(t, db, `UPDATE mb_album_heads SET membership_revision=membership_revision+1 WHERE album_id=?`, first)
	if done, err := s.mbReconcileAlbums(readPickContext(t)); err != nil || !done {
		t.Fatal("turn 3", done, err)
	}
	if got := mbReconcileTurn(t, db, first); got != (mbReconcileTurnStatus{"complete", 3}) {
		t.Fatalf("turn 3 did not wrap to the first album: %+v", got)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations c JOIN mb_album_heads h ON h.album_id=c.album_id WHERE c.album_id=? AND c.membership_revision=h.membership_revision`, first); n != 1 {
		t.Fatal("wrap turn did not record the current membership")
	}
	if n := s.mbReconcileAfter.Load(); n != first {
		t.Fatalf("last served album=%d, want %d", n, first)
	}
}

// One turn reads at most a bounded page of an album's songs, but only songs
// that actually have a job cost an effect. An album worth of jobless songs
// therefore finishes in a single turn instead of spending a turn per song.
func TestMBReconcileAlbumSongsWithoutJobsCostNoEffect(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	for i := 0; i < 59; i++ {
		c.Song(names["album"], i+10, fmt.Sprintf("/music/plain-%02d.flac", i), fmt.Sprintf("Track %02d", i))
	}
	c.Drain()
	rows, err := db.Query(`SELECT entity_id FROM catalog_songs WHERE album_id=? ORDER BY entity_id`, names["album"].ID)
	if err != nil {
		t.Fatal(err)
	}
	songs := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		songs = append(songs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(songs) != 60 || len(songs) > mbReconcileRaw {
		t.Fatalf("songs=%d, want one bounded page under %d", len(songs), mbReconcileRaw)
	}
	keep := []int64{songs[0], songs[20], songs[59]}
	mbCurrentExec(t, db, `DELETE FROM mb_jobs WHERE kind='song' AND entity_id IN(SELECT entity_id FROM catalog_songs WHERE album_id=? AND entity_id NOT IN(?,?,?))`, names["album"].ID, keep[0], keep[1], keep[2])
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='song' AND entity_id IN(SELECT entity_id FROM catalog_songs WHERE album_id=?)`, names["album"].ID); n != 3 {
		t.Fatalf("fixture kept %d song jobs, want 3", n)
	}
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='matched',attempts=3,next_attempt='later',error='old',lease='old',lease_until='later',generation=7,revision=9 WHERE kind='song' AND entity_id IN(?,?,?)`, keep[0], keep[1], keep[2])
	release := readPickRelease(t, db, "Jobless")
	readPickPin(t, db, names["album"].ID, release, "pending")
	before := backgroundGateAcquisitions()
	done, err := New(db, "").mbReconcileAlbums(readPickContext(t))
	if err != nil || !done {
		t.Fatal("turn", done, err)
	}
	if n := backgroundGateAcquisitions() - before; n != 1 {
		t.Fatalf("one turn acquired %d writers, want 1", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='song' AND entity_id IN(SELECT entity_id FROM catalog_songs WHERE album_id=?)`, names["album"].ID); n != 3 {
		t.Fatalf("jobless songs grew %d job rows", n-3)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='song' AND entity_id IN(SELECT entity_id FROM catalog_songs WHERE album_id=?) AND status='pending' AND attempts=0 AND next_attempt='' AND error='' AND lease='' AND lease_until='' AND generation=8 AND revision=10`, names["album"].ID); n != 3 {
		t.Fatalf("requeued %d songs, want the 3 with jobs", n)
	}
	// The jobless songs still advance the cursor, so the turn is complete.
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete' AND cursor=?`, names["album"].ID, fmt.Sprint(songs[len(songs)-1])); n != 1 {
		t.Fatal("album did not complete at the last examined song")
	}
}
