package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Compare the maintained projections with authoritative facts after every
// producer transition. This oracle is intentionally a full read, never a worker.
func tvdbDirtyOracle(t *testing.T, tx *sql.Tx) {
	t.Helper()
	const eligible = `SELECT pe.item_id,pe.provider_id,e.library_id FROM provider_evidence pe JOIN catalog_episodes ep ON ep.entity_id=pe.item_id JOIN catalog_entities e ON e.id=pe.item_id JOIN tvdb_episode_credit_evidence ce ON ce.item_id=pe.item_id AND ce.provider_id=pe.provider_id WHERE pe.provider='tvdb' AND pe.provider_id>0 AND e.kind=4 AND ce.credits IS NULL AND ce.attempts<5`
	for _, query := range []string{
		`SELECT count(*) FROM (` + eligible + ` EXCEPT SELECT item_id,provider_id,library_id FROM tvdb_episode_credit_pending)`,
		`SELECT count(*) FROM (SELECT item_id,provider_id,library_id FROM tvdb_episode_credit_pending EXCEPT ` + eligible + `)`,
		`SELECT count(*) FROM tvdb_episode_credit_heads h JOIN catalog_libraries cl ON cl.id=h.library_id WHERE h.enabled IS NOT COALESCE((SELECT enabled FROM tvdb_provider_policies WHERE library_id=cl.library_id),0) OR (h.next_item,h.next_provider) IS NOT (SELECT item_id,provider_id FROM tvdb_episode_credit_pending WHERE library_id=h.library_id ORDER BY item_id,provider_id LIMIT 1)`,
		`SELECT count(*) FROM catalog_libraries cl WHERE NOT EXISTS(SELECT 1 FROM tvdb_episode_credit_heads h WHERE h.library_id=cl.id)`,
		`SELECT count(*) FROM tvdb_episode_credit_evidence ce WHERE NOT EXISTS(SELECT 1 FROM provider_evidence pe WHERE pe.item_id=ce.item_id AND pe.provider='tvdb' AND pe.provider_id=ce.provider_id) AND NOT EXISTS(SELECT 1 FROM tvdb_episode_credit_cleanup c WHERE c.item_id=ce.item_id AND c.provider_id=ce.provider_id)`,
		`SELECT count(*) FROM provider_evidence pe JOIN catalog_episodes ep ON ep.entity_id=pe.item_id JOIN catalog_entities e ON e.id=pe.item_id WHERE pe.provider='tvdb' AND pe.provider_id>0 AND e.kind=4 AND NOT EXISTS(SELECT 1 FROM tvdb_episode_credit_evidence ce WHERE ce.item_id=pe.item_id AND ce.provider_id=pe.provider_id)`,
	} {
		var n int
		if err := tx.QueryRow(query).Scan(&n); err != nil || n != 0 {
			t.Fatalf("projection mismatch count=%d err=%v\n%s", n, err, query)
		}
	}
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var row sql.NullInt64
		var key int
		if err := rows.Scan(&table, &row, &parent, &key); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("producer left a foreign key violation: table=%s row=%v parent=%s key=%d", table, row, parent, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTVDBDirtyWorkProducerMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprintf("recursive_%d", recursive), func(t *testing.T) {
			db, _ := tvdbDB(t)
			defer db.Close()
			names := tvdbNames(t, db)
			other := catalogtest.New(t, db).Library("other", "Other", "tv", "/other")
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), db, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			var old int
			if err := conn.QueryRowContext(ctx, `PRAGMA recursive_triggers`).Scan(&old); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA recursive_triggers=%d`, old))
			if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive)); err != nil {
				t.Fatal(err)
			}
			w, err := dbwork.BeginConn(ctx, conn, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			tx := w.Tx()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(q, args...); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				tvdbDirtyOracle(t, tx)
			}
			one, two, third := names["one"].ID, names["two"].ID, names["manual"].ID
			exec(`INSERT INTO provider_evidence VALUES(?,'tvdb',100,'{}','now')`, one) // pair last
			for _, terminal := range []string{`credits=0,attempts=0`, `credits=NULL,attempts=5`} {
				exec(`UPDATE tvdb_episode_credit_evidence SET `+terminal+` WHERE item_id=? AND provider_id=100`, one)
				exec(`DELETE FROM provider_evidence WHERE item_id=? AND provider='tvdb'`, one)
				exec(`INSERT INTO provider_evidence VALUES(?,'tvdb',100,'{}','later')`, one)
				var due int
				if err := tx.QueryRow(`SELECT count(*) FROM tvdb_episode_credit_pending WHERE item_id=?`, one).Scan(&due); err != nil || due != 0 {
					t.Fatal("same-pair publication reset terminal evidence", due, err)
				}
			}
			exec(`UPDATE provider_evidence SET payload='changed',observed_at='later',provider_id=provider_id WHERE item_id=?`, one)
			exec(`UPDATE tvdb_episode_credit_evidence SET attempts=4 WHERE item_id=? AND provider_id=100`, one)
			exec(`UPDATE tvdb_provider_policies SET enabled=0 WHERE library_id='lib'`)
			exec(`UPDATE tvdb_provider_policies SET enabled=1 WHERE library_id='lib'`)
			exec(`UPDATE tvdb_provider_policies SET enabled=enabled WHERE library_id='lib'`)
			exec(`DELETE FROM tvdb_provider_policies WHERE library_id='lib'`)
			exec(`INSERT INTO tvdb_provider_policies(library_id,enabled) VALUES('lib',1)`)
			exec(`UPDATE catalog_entities SET kind=1 WHERE id=?`, one)
			exec(`UPDATE catalog_entities SET kind=4 WHERE id=?`, one) // kind last
			exec(`UPDATE catalog_entities SET library_id=? WHERE id=?`, other, one)
			exec(`UPDATE catalog_entities SET library_id=(SELECT id FROM catalog_libraries WHERE library_id='lib') WHERE id=?`, one)
			exec(`DELETE FROM catalog_episodes WHERE entity_id=?`, one)
			exec(`INSERT INTO catalog_episodes(entity_id,show_id,season_id,numbering,number) VALUES(?,?,?,'seasonal',1)`, one, names["show"].ID, names["season"].ID) // episode last
			exec(`UPDATE catalog_episodes SET number=number WHERE entity_id=?`, one)
			exec(`DELETE FROM catalog_episodes WHERE entity_id=?`, third)
			exec(`UPDATE catalog_episodes SET entity_id=? WHERE entity_id=?`, third, one)
			exec(`UPDATE catalog_episodes SET entity_id=? WHERE entity_id=?`, one, third)
			exec(`UPDATE provider_evidence SET provider_id=101 WHERE item_id=? AND provider='tvdb'`, one)
			exec(`UPDATE provider_evidence SET provider_id=0 WHERE item_id=? AND provider='tvdb'`, one)
			exec(`UPDATE provider_evidence SET provider_id=102 WHERE item_id=? AND provider='tvdb'`, one)
			exec(`UPDATE provider_evidence SET provider='tmdb' WHERE item_id=?`, one)
			exec(`UPDATE provider_evidence SET provider='tvdb' WHERE item_id=?`, one)
			exec(`UPDATE provider_evidence SET item_id=? WHERE item_id=?`, two, one)
			exec(`UPDATE tvdb_episode_credit_evidence SET credits=0 WHERE item_id=? AND provider_id=102`, two)
			exec(`UPDATE tvdb_episode_credit_evidence SET provider_id=103 WHERE item_id=? AND provider_id=102`, two)
			exec(`UPDATE tvdb_episode_credit_evidence SET item_id=? WHERE item_id=? AND provider_id=103`, third, two)
			exec(`DELETE FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=102`, two)
			exec(`SAVEPOINT mutation`)
			exec(`UPDATE provider_evidence SET provider_id=999 WHERE item_id=?`, two)
			exec(`ROLLBACK TO mutation`)
			exec(`RELEASE mutation`)
			exec(`DELETE FROM catalog_entities WHERE id=?`, two) // cascaded pe/ce cannot enqueue invalid FKs
			// removeLibraryRowTx clears these non-cascading library references
			// before deleting the public row. The TVDB head itself must still
			// disappear through its catalog-library FK cascade.
			exec(`DELETE FROM inventory_policy_pending WHERE library_id='other'`)
			exec(`DELETE FROM library_scan_policies WHERE library_id='other'`)
			exec(`DELETE FROM library_sources WHERE library_id='other'`)
			exec(`DELETE FROM libraries WHERE id='other'`)
			var remaining int
			if err := tx.QueryRow(`SELECT count(*) FROM tvdb_episode_credit_heads WHERE library_id=?`, other).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatal("library deletion retained TVDB head", remaining, err)
			}
		})
	}
}

func tvdbDirtyClaims(t *testing.T, s *Service) []tvdbEpisodeCreditClaim {
	t.Helper()
	var claims []tvdbEpisodeCreditClaim
	ctx, cost := dbwork.Measure(dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia))
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		var err error
		claims, err = s.tvdbEpisodeCreditClaims(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cost(); got.Statements > 24 || got.VMSteps > 20_000 {
		t.Fatalf("eight-head picker exceeded bounded read work: %+v", got)
	} else {
		t.Logf("eight-head picker: %+v", got)
	}
	return claims
}

func TestTVDBDirtyWorkGlobalEightHeadsAndIndexedReads(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	var shows, seasons []catalogtest.Item
	for i := 0; i < 11; i++ {
		lib := c.Library(fmt.Sprintf("merge-%02d", i), "Merge", "tv", fmt.Sprintf("/merge/%d", i))
		show := c.Show(lib, fmt.Sprintf("Merge %d", i), 2020)
		shows = append(shows, show)
		seasons = append(seasons, c.Season(show, 1))
	}
	var want []tvdbEpisodeCreditClaim
	// The disabled prefix stays pending; settled pairs never enter pending.
	for round := 0; round < 3; round++ {
		for i := range shows {
			item := c.Episode(shows[i], seasons[i], round+1, fmt.Sprintf("/merge/%d/%d.mkv", i, round))
			id := int64(1000 + round*100 + i)
			tvdbPointClaim(t, db, item.ID, id)
			if round == 0 {
				mbCurrentExec(t, db, `UPDATE tvdb_episode_credit_evidence SET credits=0 WHERE item_id=?`, item.ID)
			} else if i < 2 {
				mbCurrentExec(t, db, `UPDATE tvdb_provider_policies SET enabled=0 WHERE library_id=?`, fmt.Sprintf("merge-%02d", i))
			} else if len(want) < 8 {
				want = append(want, tvdbEpisodeCreditClaim{item.ID, id})
			}
		}
	}
	if got := tvdbDirtyClaims(t, New(db, "")); !reflect.DeepEqual(got, want) {
		t.Fatalf("global merge=%v want=%v", got, want)
	}
	// Completing all but one library lets multiple successors from that stream
	// enter the same page, rather than taking only one item from each head.
	mbCurrentExec(t, db, `UPDATE tvdb_episode_credit_evidence SET credits=0 WHERE item_id IN(SELECT pe.item_id FROM provider_evidence pe JOIN catalog_entities e ON e.id=pe.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id<>'merge-02')`)
	if got := tvdbDirtyClaims(t, New(db, "")); len(got) != 2 || got[0] != want[0] || got[1].providerID != 1202 {
		t.Fatalf("same-library successors=%v", got)
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	if err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), db, func(ctx context.Context) error {
		_, err := New(db, "").tvdbEpisodeCreditClaims(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range trace() {
		args := []any{tvdbEpisodeCreditPage}
		index := "tvdb_episode_credit_heads_due"
		if strings.Contains(q, "FROM tvdb_episode_credit_pending") {
			args = []any{1, 0, 0}
			index = "tvdb_episode_credit_pending_library"
		}
		rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		joined := strings.Join(plan, "; ")
		if err != nil || !strings.Contains(joined, index) || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("unbounded picker plan err=%v: %s", err, joined)
		}
	}
}

func TestTVDBDirtyWorkCleanupSurvivesRestartAndRollback(t *testing.T) {
	db, path := tvdbDB(t)
	names := tvdbNames(t, db)
	tvdbPointClaim(t, db, names["one"].ID, 100)
	mbCurrentExec(t, db, `UPDATE tvdb_episode_credit_evidence SET credits=0 WHERE item_id=?`, names["one"].ID)
	// Exact restoration retains terminal evidence even with an outstanding key.
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM provider_evidence WHERE item_id=?`, names["one"].ID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO provider_evidence VALUES(?,'tvdb',100,'{}','later')`, names["one"].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,?,'now',NULL,0)`, names["two"].ID, i+1000)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "tvdb_*")
	defer unregister()
	rolledBack := errors.New("rollback exact cleanup and ack")
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=1000`, names["two"].ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tvdb_episode_credit_cleanup WHERE item_id=? AND provider_id=1000`, names["two"].ID); err != nil {
			return err
		}
		return rolledBack
	}); !errors.Is(err, rolledBack) {
		t.Fatal(err)
	}
	if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_cleanup`) != 41 {
		t.Fatal("rollback lost durable cleanup")
	}
	if wake.Take() {
		t.Fatal("rolled back cleanup sent a commit wake")
	}
	s := New(db, "")
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		t.Fatal("terminal/orphan pair fetched")
		return nil, nil
	}}
	for _, remaining := range []int64{9, 0} {
		if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_cleanup`); n != remaining {
			t.Fatalf("cleanup remaining=%d want=%d", n, remaining)
		}
		if !wake.Take() {
			t.Fatal("cleanup commit did not wake existing metadata subscription")
		}
	}
	if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100 AND credits=0 AND attempts=0`, names["one"].ID) != 1 {
		t.Fatal("same-pair restoration lost terminal evidence")
	}
	before := backgroundGateAcquisitions()
	if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if backgroundGateAcquisitions() != before {
		t.Fatal("idle cleanup acquired writer")
	}
}

func TestTVDBDirtyWorkStalePublicationAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"rematch", "failure_rematch", "episode_deleted", "kind_changed", "library_policy_off", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			db, _ := tvdbDB(t)
			defer db.Close()
			item := tvdbNames(t, db)["one"].ID
			other := catalogtest.New(t, db).Library("off", "Off", "tv", "/off")
			mbCurrentExec(t, db, `UPDATE tvdb_provider_policies SET enabled=0 WHERE library_id='off'`)
			tvdbPointClaim(t, db, item, 100)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := New(db, "")
			calls := 0
			s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
				calls++
				switch change {
				case "rematch", "failure_rematch":
					mbCurrentExec(t, db, `UPDATE provider_evidence SET provider_id=101 WHERE item_id=? AND provider='tvdb'`, item)
				case "episode_deleted":
					mbCurrentExec(t, db, `DELETE FROM catalog_episodes WHERE entity_id=?`, item)
				case "kind_changed":
					mbCurrentExec(t, db, `UPDATE catalog_entities SET kind=1 WHERE id=?`, item)
				case "library_policy_off":
					mbCurrentExec(t, db, `UPDATE catalog_entities SET library_id=? WHERE id=?`, other, item)
				case "cancelled":
					cancel()
				}
				if change == "failure_rematch" {
					return nil, errors.New("upstream failed after rematch")
				}
				return []metadataprovider.TVDBEpisodeCharacter{{PersonID: 1, PersonName: "Stale", Type: "Actor"}}, nil
			}}
			err := s.tvdbEpisodeCreditsStep(ctx)
			if calls != 1 || (change == "cancelled" && !errors.Is(err, context.Canceled)) || (change != "cancelled" && err != nil) {
				t.Fatal("fetch/drop", calls, err)
			}
			if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100 AND (credits IS NOT NULL OR attempts<>0)`, item) != 0 || readPickCount(t, db, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'`, item) != 0 {
				t.Fatal("stale/cancelled result or attempt published")
			}
			if change == "rematch" || change == "failure_rematch" {
				// A restart has only a durable exact cleanup key for the old
				// pair and unfinished new-pair bookkeeping. Settle the new pair
				// to isolate the next turn's orphan cleanup from acquisition.
				mbCurrentExec(t, db, `UPDATE tvdb_episode_credit_evidence SET credits=0 WHERE item_id=? AND provider_id=101`, item)
				if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_cleanup WHERE item_id=? AND provider_id=100`, item) != 1 {
					t.Fatal("stale pair has no durable cleanup key")
				}
				if err := s.tvdbEpisodeCreditsStep(context.Background()); err != nil {
					t.Fatal(err)
				}
				if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=100`, item) != 0 || calls != 1 {
					t.Fatal("stale pair escaped cleanup or was fetched again")
				}
			}
		})
	}
}
