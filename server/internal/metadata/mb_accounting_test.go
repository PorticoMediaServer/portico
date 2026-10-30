package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type mbAccountingQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

func assertMBAccounting(t *testing.T, q mbAccountingQuerier) {
	t.Helper()
	var got, want [3]int64
	if err := q.QueryRow(`SELECT releases,recordings,charge FROM mb_evidence_totals WHERE singleton=1`).Scan(&got[0], &got[1], &got[2]); err != nil {
		t.Fatal(err)
	}
	if err := q.QueryRow(mbAccountingOracleSQL).Scan(&want[0], &want[1], &want[2]); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("maintained totals %v, original aggregate %v", got, want)
	}
	var bad int
	// These deliberately expensive independent queries are test oracles only.
	if err := q.QueryRow(`SELECT count(*) FROM mb_release_accounting a WHERE
 direct_refs<>(SELECT count(*) FROM mb_album_links l WHERE l.release_revision=a.revision_id)+(SELECT count(*) FROM mb_song_links l WHERE l.release_revision=a.revision_id)
 OR charge<>(SELECT length(CAST(payload AS BLOB))*8+512*(1+
 (SELECT count(*) FROM mb_release_credits WHERE revision_id=a.revision_id)+
 (SELECT count(*) FROM mb_release_group_types WHERE release_revision=a.revision_id)+
 (SELECT count(*) FROM mb_release_media WHERE release_revision=a.revision_id)+
 (SELECT count(*) FROM mb_release_tracks WHERE release_revision=a.revision_id)+
 (SELECT count(*) FROM mb_track_credits WHERE release_revision=a.revision_id)) FROM mb_release_evidence WHERE revision_id=a.revision_id)`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("release accounting drift=%d: %v", bad, err)
	}
	if err := q.QueryRow(`SELECT count(*) FROM mb_recording_accounting a WHERE
 direct_refs<>(SELECT count(*) FROM mb_song_links l WHERE l.recording_revision=a.revision_id)
 OR release_refs<>(SELECT count(*) FROM mb_release_tracks t WHERE t.recording_revision=a.revision_id AND
 (EXISTS(SELECT 1 FROM mb_album_links l WHERE l.release_revision=t.release_revision) OR EXISTS(SELECT 1 FROM mb_song_links l WHERE l.release_revision=t.release_revision)))
 OR charge<>(SELECT length(CAST(payload AS BLOB))*8+512*(1+(SELECT count(*) FROM mb_recording_credits WHERE revision_id=a.revision_id)) FROM mb_recording_evidence WHERE revision_id=a.revision_id)`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("recording accounting drift=%d: %v", bad, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM mb_release_accounting a JOIN mb_release_evidence e USING(revision_id) WHERE a.observed_at<>e.observed_at OR a.retention_refs<>(SELECT count(*) FROM mb_release_aliases x WHERE x.evidence_revision=a.revision_id)+(SELECT count(*) FROM mb_album_reconciliations x WHERE x.release_revision=a.revision_id)+(SELECT count(*) FROM mb_publication_operations x WHERE x.accepted_release_revision=a.revision_id)+(SELECT count(*) FROM mb_publication_operations x WHERE x.result_release_revision=a.revision_id)`,
		`SELECT count(*) FROM mb_recording_accounting a JOIN mb_recording_evidence e USING(revision_id) WHERE a.observed_at<>e.observed_at OR a.retention_refs<>(SELECT count(*) FROM mb_recording_aliases x WHERE x.evidence_revision=a.revision_id)+(SELECT count(*) FROM mb_release_tracks x WHERE x.recording_revision=a.revision_id)+(SELECT count(*) FROM mb_publication_operations x WHERE x.accepted_recording_revision=a.revision_id)+(SELECT count(*) FROM mb_publication_operations x WHERE x.result_recording_revision=a.revision_id)`,
	} {
		if err := q.QueryRow(query).Scan(&bad); err != nil || bad != 0 {
			t.Fatalf("retirement base-fact drift=%d: %v", bad, err)
		}
	}
}

func mbAccountingSave(t *testing.T, tx *sql.Tx, value metadataprovider.ReleaseLookup) string {
	t.Helper()
	stage, err := stageMBRelease(value)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := saveMBReleaseEvidence(context.Background(), tx, stage, "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestMBAccountingTransitions(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			db, names := mbLaneDB(t)
			other := catalogtest.New(t, db).Album(names["artist"], "Other", 0)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec(fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive)); err != nil {
				t.Fatal(err)
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(q, args...); err != nil {
					t.Fatal(err, q)
				}
				assertMBAccounting(t, tx)
			}
			// A seed that changes release acceptance runs through the real
			// coordinator with its original statement, then the independent
			// aggregate oracle observes the completed flip and fold.
			pin := func(intent mbFixturePinIntent, q string, args ...any) {
				t.Helper()
				if err := mbFixturePinTx(tx, intent, func(tx *sql.Tx) error {
					_, err := tx.Exec(q, args...)
					return err
				}); err != nil {
					t.Fatal(err, q)
				}
				assertMBAccounting(t, tx)
			}
			value := mbEvidenceFixture()
			value.Release.ReleaseGroup.SecondaryTypes = []string{"Live"}
			value.Release.Media[0].Tracks[0].ArtistCredit = value.Release.ArtistCredit
			value.Release.Media[0].Tracks[0].Recording.ArtistCredit = value.Release.ArtistCredit
			repeated := value.Release.Media[0].Tracks[0]
			repeated.ID = "40000000-0000-0000-0000-000000000002"
			repeated.Position = 2
			value.Release.Media[0].Tracks = append(value.Release.Media[0].Tracks, repeated)
			old := mbAccountingSave(t, tx, value)
			assertMBAccounting(t, tx)
			if again := mbAccountingSave(t, tx, value); again != old {
				t.Fatal("dedupe changed revision")
			}
			assertMBAccounting(t, tx)
			value.Release.Title = "Shared recordings, different release revision"
			next := mbAccountingSave(t, tx, value)
			var record string
			if err = tx.QueryRow(`SELECT recording_revision FROM mb_release_tracks WHERE release_revision=? LIMIT 1`, old).Scan(&record); err != nil {
				t.Fatal(err)
			}
			provider := value.Release.Media[0].Tracks[0].Recording.ID
			exec(`INSERT INTO mb_song_links(item_id,recording_revision,recording_id,observed_at) VALUES(?,?,?,'now')`, names["song"].ID, record, provider)
			pin(mbFixturePinIntent{Link: "album", Entity: names["album"].ID, NewRelease: old}, `INSERT INTO mb_album_links VALUES(?,?,?,?,'now')`, names["album"].ID, old, value.Release.ID, value.RequestedID)
			pin(mbFixturePinIntent{Link: "song", Entity: names["song"].ID, NewRecording: record, NewRelease: next}, `UPDATE mb_song_links SET release_revision=?,release_id=? WHERE item_id=?`, next, value.Release.ID, names["song"].ID)
			exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'now')`, other.ID, old, value.Release.ID, value.RequestedID)
			// Repeated tracks count twice per accepted release, regardless of pin cardinality.
			var refs int
			if err = tx.QueryRow(`SELECT release_refs FROM mb_recording_accounting WHERE revision_id=?`, record).Scan(&refs); err != nil || refs != 4 {
				t.Fatal(refs, err)
			}
			exec(`UPDATE mb_album_links SET release_revision=release_revision,observed_at='later'`)
			exec(`UPDATE mb_song_links SET recording_revision=recording_revision,release_revision=release_revision`)
			exec(`UPDATE mb_album_links SET release_revision=? WHERE album_id=?`, next, names["album"].ID)
			pin(mbFixturePinIntent{Link: "album", Entity: other.ID}, `DELETE FROM mb_album_links WHERE album_id=?`, other.ID)
			exec(`UPDATE mb_song_links SET recording_revision=NULL,recording_id=NULL WHERE item_id=?`, names["song"].ID)
			// Physical deletion now requires pin-free subjects; exercise each
			// symmetric pin decrement before the guarded physical cascade.
			exec(`DELETE FROM mb_song_links WHERE item_id=?`, names["song"].ID)
			pin(mbFixturePinIntent{Link: "album", Entity: names["album"].ID}, `DELETE FROM mb_album_links WHERE album_id=?`, names["album"].ID)
			exec(`DELETE FROM catalog_entities WHERE id=?`, names["song"].ID)
			exec(`DELETE FROM catalog_entities WHERE id=?`, names["album"].ID)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			assertMBAccounting(t, db)
			mbRetireTestMark(t, db, "release", old, "aborting", "", "")
			mbRetireTestMark(t, db, "release", next, "aborting", "", "")
			service := New(db, "")
			for n := 0; n < 30; n++ {
				if err = service.mbRetireEvidence(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertMBAccounting(t, db)
			}
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_release_evidence WHERE revision_id IN(?,?)`, old, next); n != 0 {
				t.Fatal("release retirement incomplete", n)
			}
			mbRetireTestMark(t, db, "recording", record, "aborting", "", "")
			for n := 0; n < 4; n++ {
				if err = service.mbRetireEvidence(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertMBAccounting(t, db)
			}
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_recording_evidence WHERE revision_id=?`, record); n != 0 {
				t.Fatal("recording retirement incomplete", n)
			}
		})
	}
}

// Open construction permits deleting individual child rows; sealed evidence
// permits child deletion only through the bounded retirement protocol above.
func TestMBAccountingEveryChildCharge(t *testing.T) {
	c := catalogtest.Open(t)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string) {
		t.Helper()
		if _, err := tx.Exec(q); err != nil {
			t.Fatal(err, q)
		}
		assertMBAccounting(t, tx)
	}
	exec(`INSERT INTO mb_recording_evidence(revision_id,provider_id,digest,title,artist,disambiguation,observed_at,payload) VALUES('r','r',printf('%064d',0),'','','','now','é')`)
	exec(`INSERT INTO mb_recording_credits VALUES('r',0,'a','','','','','')`)
	exec(`DELETE FROM mb_recording_credits WHERE revision_id='r'`)
	exec(`UPDATE mb_recording_evidence SET sealed=1 WHERE revision_id='r'`)
	exec(`INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES('e','e',printf('%064d',0),'','','','','','','','','','now','é')`)
	for _, q := range []string{
		`INSERT INTO mb_release_credits VALUES('e',0,'a','','','','','')`,
		`INSERT INTO mb_release_group_types VALUES('e',0,'Live')`,
		`INSERT INTO mb_release_media VALUES('e',1,'','')`,
		`INSERT INTO mb_release_tracks VALUES('e','t',1,1,'','','',NULL,'r')`,
		`INSERT INTO mb_track_credits VALUES('e','t',0,'a','','','','','')`,
		`DELETE FROM mb_track_credits`, `DELETE FROM mb_release_tracks`,
		`DELETE FROM mb_release_media`, `DELETE FROM mb_release_group_types`,
		`DELETE FROM mb_release_credits`, `DELETE FROM mb_release_evidence`,
		`DELETE FROM mb_recording_evidence`,
	} {
		exec(q)
	}
}

func TestMBAccountingStrictReleaseAndByteCaps(t *testing.T) {
	for _, dimension := range []string{"release", "bytes"} {
		t.Run(dimension, func(t *testing.T) {
			c := catalogtest.Open(t)
			tx, err := c.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			count := 512
			if dimension == "bytes" {
				count = 8
			}
			for n := 0; n < count; n++ {
				payload := ""
				if dimension == "bytes" {
					bytes := mbEvidenceBytes
					if n == 0 {
						bytes -= 512
					}
					payload = strings.Repeat("x", bytes)
				}
				if _, err = tx.Exec(`INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES(?,?,printf('%064d',0),'','','','','','','','','','now',?)`, fmt.Sprint(n), fmt.Sprint(n), payload); err != nil {
					t.Fatal(err)
				}
			}
			assertMBAccounting(t, tx)
			if err = checkMBEvidenceBudget(context.Background(), tx); err != nil {
				t.Fatal("exact capacity refused", err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			tx, err = c.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(`INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES('overflow','overflow',printf('%064d',0),'','','','','','','','','','now','')`); err != nil {
				t.Fatal(err)
			}
			if err = checkMBEvidenceBudget(context.Background(), tx); !errors.Is(err, errMBBudget) {
				t.Fatal("overflow admitted", err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertMBAccounting(t, c.DB)
		})
	}
}

// Original aggregate admission SQL, retained verbatim as an independent oracle.
const mbAccountingOracleSQL = `WITH unaccepted_releases AS (
 SELECT r.revision_id,length(CAST(r.payload AS BLOB))*8+512*(1+
 (SELECT count(*) FROM mb_release_credits c WHERE c.revision_id=r.revision_id)+
 (SELECT count(*) FROM mb_release_group_types c WHERE c.release_revision=r.revision_id)+
 (SELECT count(*) FROM mb_release_media c WHERE c.release_revision=r.revision_id)+
 (SELECT count(*) FROM mb_release_tracks c WHERE c.release_revision=r.revision_id)+
 (SELECT count(*) FROM mb_track_credits c WHERE c.release_revision=r.revision_id)) AS charge
 FROM mb_release_evidence r WHERE NOT EXISTS(SELECT 1 FROM mb_album_links l WHERE l.release_revision=r.revision_id) AND NOT EXISTS(SELECT 1 FROM mb_song_links l WHERE l.release_revision=r.revision_id)
 ),unaccepted_recordings AS (
 SELECT r.revision_id,length(CAST(r.payload AS BLOB))*8+512*(1+(SELECT count(*) FROM mb_recording_credits c WHERE c.revision_id=r.revision_id)) AS charge
 FROM mb_recording_evidence r WHERE NOT EXISTS(SELECT 1 FROM mb_song_links l WHERE l.recording_revision=r.revision_id)
 AND NOT EXISTS(SELECT 1 FROM mb_release_tracks t WHERE t.recording_revision=r.revision_id AND (EXISTS(SELECT 1 FROM mb_album_links l WHERE l.release_revision=t.release_revision) OR EXISTS(SELECT 1 FROM mb_song_links l WHERE l.release_revision=t.release_revision)))
 ) SELECT (SELECT count(*) FROM unaccepted_releases),(SELECT count(*) FROM unaccepted_recordings),COALESCE((SELECT sum(charge) FROM unaccepted_releases),0)+COALESCE((SELECT sum(charge) FROM unaccepted_recordings),0)`
