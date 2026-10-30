package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Only durable mutation intent is stored. Every parity result below is held
// in test memory and is never materialized as a query-result table.
func settleScreenFanout(t *testing.T, s *Service) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		var due bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM screen_metadata_fanout WHERE pending=1) OR EXISTS(SELECT 1 FROM metadata_lookup_fanout WHERE effect='screen_consent' AND pending=1)`).Scan(&due); err != nil {
			t.Fatal(err)
		}
		if !due {
			return
		}
		if err := s.LookupFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.ScreenFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("fanout failed to settle within fixture bound")
}

func fanoutMovies(t *testing.T, n int) (*Service, *sql.DB, []int64) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("lib", "Films", "movie", "/films")
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, c.Movie(library, fmt.Sprintf("/films/%d.mp4", i), fmt.Sprintf("Film %d", i), 2000).ID)
	}
	c.Exec(`UPDATE library_configuration SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE library_id='lib'`)
	s := New(c.DB, "")
	settleScreenFanout(t, s)
	c.Exec(`DELETE FROM artwork_dirty`)
	return s, c.DB, ids
}

func fanoutSnapshot(t *testing.T, q interface {
	Query(string, ...any) (*sql.Rows, error)
}) []string {
	t.Helper()
	out := []string{}
	for _, query := range []string{
		`SELECT 'work:'||target_kind||':'||target_id||':'||status||':'||provider||':'||provider_type||':'||provider_id||':'||selection_mode||':'||episode_order||':'||requested_provider||':'||child_cursor||':'||selection_revision||':'||lease||':'||lease_until||':'||next_attempt||':'||error FROM screen_metadata_work ORDER BY target_kind,target_id`,
		`SELECT 'lock:'||kind||':'||entity_id||':'||relationship||':'||locked FROM metadata_relationship_decisions ORDER BY kind,entity_id,relationship`,
		`SELECT 'attention:'||entity_id||':'||target_kind||':'||reason||':'||confidence||':'||(dismissed_generation=generation)||':'||retired FROM screen_metadata_attention ORDER BY entity_id`,
		`SELECT 'count:'||library_id||':'||total FROM screen_metadata_attention_counts ORDER BY library_id`,
		`SELECT 'dirty:'||kind||':'||entity_id FROM artwork_dirty ORDER BY kind,entity_id`,
	} {
		rows, err := q.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestScreenFanoutSemanticSettlementParity(t *testing.T) {
	s, db, _ := fanoutMovies(t, 11)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unavailable',provider='tmdb',provider_type='movie',provider_id='42',selection_mode='owner',lease='old',lease_until='later',next_attempt='later',error='old';
 INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) SELECT target_kind,target_id,'identity','{}','owner',1,'owner','now' FROM screen_metadata_work;
 INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) SELECT target_kind,target_id,'genre','{}','owner',1,'owner','now' FROM screen_metadata_work;
 DELETE FROM artwork_dirty;
 UPDATE screen_metadata_policies SET providers='["tvdb"]',revision=revision+1 WHERE library_id='lib';
 UPDATE screen_metadata_fanout SET rematch=1 WHERE library_id='lib' AND effect='screen'`)
	// Independent old synchronous SQL, rolled back after reading the oracle.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE metadata_relationship_decisions SET locked=0 WHERE relationship='identity' AND locked=1 AND (kind,entity_id) IN(SELECT target_kind,target_id FROM screen_metadata_work WHERE library_id='lib' AND provider<>'' AND provider NOT IN('tvdb'))`,
		`UPDATE screen_metadata_work SET provider='',provider_type='',provider_id='',selection_mode='none',episode_order='official',requested_provider='',child_cursor='',selection_revision=selection_revision+1 WHERE library_id='lib' AND provider<>'' AND provider NOT IN('tvdb')`,
		`UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE library_id='lib'`,
	} {
		if _, err = tx.Exec(query); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	want := fanoutSnapshot(t, tx)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	settleScreenFanout(t, s)
	if got := fanoutSnapshot(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("settlement differs from old SQL\ngot %v\nwant %v", got, want)
	}
}

func TestScreenFanoutProviderChangeMidpassKeepsRematch(t *testing.T) {
	s, db, ids := fanoutMovies(t, screenFanoutRowLimit+1)
	publicationExec(t, db, `UPDATE screen_metadata_work SET provider='tmdb',provider_type='movie',provider_id='42',selection_mode='owner';
 INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) SELECT target_kind,target_id,'identity','{}','owner',1,'owner','now' FROM screen_metadata_work;
 UPDATE screen_metadata_policies SET revision=revision+1 WHERE library_id='lib'`)
	if err := s.ScreenFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT after_id FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != fmt.Sprint(ids[screenFanoutRowLimit-1]) {
		t.Fatal("first bounded cursor", got)
	}
	publicationExec(t, db, `UPDATE screen_metadata_policies SET providers='["tvdb"]',revision=revision+1 WHERE library_id='lib';UPDATE screen_metadata_fanout SET rematch=1 WHERE library_id='lib' AND effect='screen'`)
	// Complete the old pass. Its pass_rematch=0 must not consume the newer flag.
	for i := 0; i < (len(ids)-1)/screenFanoutRowLimit; i++ {
		if err := s.ScreenFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, db, `SELECT pass_generation||':'||rematch||':'||pending FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != "0:1:1" {
		t.Fatal("new rematch lost at old pass settlement", got)
	}
	// Resume using a fresh service: only committed marker state is needed.
	settleScreenFanout(t, New(db, ""))
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_work WHERE provider<>'' OR selection_mode<>'none'`); got != "0" {
		t.Fatal("departed identities survived", got)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM metadata_relationship_decisions WHERE relationship='identity' AND locked=1`); got != "0" {
		t.Fatal("identity locks survived", got)
	}
	if got := publicationScalar(t, db, `SELECT rematch FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != "0" {
		t.Fatal("served flag stayed armed", got)
	}
}

func TestScreenFanoutRematchReadsLiveProviders(t *testing.T) {
	s, db, _ := fanoutMovies(t, screenFanoutRowLimit+5)
	publicationExec(t, db, `UPDATE screen_metadata_work SET provider='tmdb',provider_id='42',selection_mode='owner';UPDATE screen_metadata_policies SET providers='["tvdb"]',revision=revision+1;UPDATE screen_metadata_fanout SET rematch=1 WHERE effect='screen'`)
	if err := s.ScreenFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	publicationExec(t, db, `UPDATE screen_metadata_policies SET providers='["tmdb"]',revision=revision+1`)
	settleScreenFanout(t, s)
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_work WHERE provider='tmdb'`); got != "5" {
		t.Fatal("pass used captured providers instead of live set", got)
	}
}

func TestScreenFanoutNonScreenArtworkAndRejectedPrefix(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	for i := 0; i < 72; i++ {
		c.Artist(library, fmt.Sprintf("Artist %03d", i))
	}
	s := New(c.DB, "")
	settleScreenFanout(t, s)
	c.Exec(`DELETE FROM artwork_entity_heads WHERE entity_id IN(SELECT id FROM catalog_entities WHERE library_id=? ORDER BY kind,id LIMIT 65)`, library)
	c.Exec(`DELETE FROM artwork_dirty;UPDATE library_configuration SET revision=revision+1,metadata_revision=metadata_revision+1 WHERE library_id='music'`)
	// The absent screen-policy marker is discarded independently; artwork survives.
	for i := 0; i < 2; i++ {
		if err := s.ScreenFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, c.DB, `SELECT count(*) FROM artwork_dirty`); got != "0" {
		t.Fatal("head filter moved ahead of raw LIMIT", got)
	}
	if got := publicationScalar(t, c.DB, `SELECT after_id FROM screen_metadata_fanout WHERE library_id='music' AND effect='artwork'`); got == "0" {
		t.Fatal("rejected prefix did not advance")
	}
	settleScreenFanout(t, s)
	if got := publicationScalar(t, c.DB, `SELECT count(*) FROM (SELECT h.kind,h.entity_id FROM artwork_entity_heads h JOIN catalog_entities ce ON ce.id=h.entity_id WHERE ce.library_id=? EXCEPT SELECT kind,entity_id FROM artwork_dirty)`, library); got != "0" {
		t.Fatal("non-screen heads missing", got)
	}
	if got := publicationScalar(t, c.DB, `SELECT count(*) FROM artwork_dirty`); got != "7" {
		t.Fatal("wrong head kind mapping/library axis", got)
	}
}

func TestScreenFanoutRenameOnlyCAS(t *testing.T) {
	s, db, _ := fanoutMovies(t, 2)
	before := fanoutSnapshot(t, db)
	semantic := publicationScalar(t, db, `SELECT metadata_revision FROM library_configuration WHERE library_id='lib'`)
	cas := publicationScalar(t, db, `SELECT revision FROM library_configuration WHERE library_id='lib'`)
	markers := publicationScalar(t, db, `SELECT group_concat(effect||':'||generation,'|') FROM (SELECT effect,generation FROM screen_metadata_fanout WHERE library_id='lib' ORDER BY effect)`)
	work := publicationScalar(t, db, `SELECT sum(generation) FROM screen_metadata_work`)
	publicationExec(t, db, `UPDATE libraries SET name='Renamed' WHERE id='lib';UPDATE libraries SET name=name WHERE id='lib'`)
	if got := publicationScalar(t, db, `SELECT revision FROM library_configuration WHERE library_id='lib'`); got == cas {
		t.Fatal("rename lost existing CAS behavior")
	}
	if got := publicationScalar(t, db, `SELECT metadata_revision FROM library_configuration WHERE library_id='lib'`); got != semantic {
		t.Fatal("rename moved semantic revision", got)
	}
	if got := publicationScalar(t, db, `SELECT group_concat(effect||':'||generation,'|') FROM (SELECT effect,generation FROM screen_metadata_fanout WHERE library_id='lib' ORDER BY effect)`); got != markers {
		t.Fatal("rename advanced marker", got)
	}
	if got := publicationScalar(t, db, `SELECT sum(generation) FROM screen_metadata_work`); got != work {
		t.Fatal("rename changed work", got)
	}
	settleScreenFanout(t, s)
	if got := fanoutSnapshot(t, db); !reflect.DeepEqual(got, before) {
		t.Fatal("rename dirtied semantic state", got, before)
	}
	publicationExec(t, db, `UPDATE libraries SET root='/new-root' WHERE id='lib'`)
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fanout WHERE library_id='lib' AND pending=1`); got != "2" {
		t.Fatal("real root edit failed to request both effects", got)
	}
}

func TestScreenFanoutRollbackCancellationAndBackoff(t *testing.T) {
	s, db, _ := fanoutMovies(t, 9)
	publicationExec(t, db, `UPDATE screen_metadata_policies SET revision=revision+1;CREATE TRIGGER fanout_fail BEFORE UPDATE OF generation ON screen_metadata_work BEGIN SELECT RAISE(ABORT,'test fanout failure');END;`)
	before := fanoutSnapshot(t, db)
	if err := s.ScreenFanoutStep(context.Background()); err == nil {
		t.Fatal("injected failure succeeded")
	}
	if got := fanoutSnapshot(t, db); !reflect.DeepEqual(got, before) {
		t.Fatal("failed turn leaked effects")
	}
	if got := publicationScalar(t, db, `SELECT pass_generation||':'||after_id||':'||attempts||':'||(next_attempt_ms>0) FROM screen_metadata_fanout WHERE effect='screen'`); got != "0:0:1:1" {
		t.Fatal("failed cursor/retry state", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ScreenFanoutStep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled turn", err)
	}
	publicationExec(t, db, `DROP TRIGGER fanout_fail;UPDATE screen_metadata_fanout SET next_attempt_ms=0`)
	settleScreenFanout(t, s)
	for _, n := range []int{-1, 0, 1, 11, 1000000} {
		d := screenFanoutRetryDelay(n)
		if d < 30*time.Second || d > 6*time.Hour {
			t.Fatal("invalid bounded backoff", n, d)
		}
	}
}

func TestScreenFanoutFairTurnsAndConcurrentCallers(t *testing.T) {
	s, db, _ := fanoutMovies(t, 9)
	c := catalogtest.New(t, db)
	library := c.Library("other", "Other", "movie", "/other")
	for i := 0; i < 9; i++ {
		c.Movie(library, fmt.Sprintf("/other/%d.mp4", i), "Other", 2000)
	}
	settleScreenFanout(t, s)
	var initialGeneration int64
	if err := db.QueryRow(`SELECT sum(generation) FROM screen_metadata_work`).Scan(&initialGeneration); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE library_configuration SET revision=revision+1,metadata_revision=metadata_revision+1;UPDATE screen_metadata_fanout SET last_served_ms=0`)
	for i := 0; i < 4; i++ {
		if err := s.ScreenFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fanout WHERE last_served_ms>0`); got != "4" {
		t.Fatal("library/effect starvation", got)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ScreenFanoutStep(context.Background()) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	settleScreenFanout(t, s)
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_fanout WHERE served_generation<>generation OR pass_generation<>0 OR pending<>0`); got != "0" {
		t.Fatal("concurrent callers failed settlement", got)
	}
	if got := publicationScalar(t, db, `SELECT sum(generation)-? FROM screen_metadata_work`, initialGeneration); got != "18" {
		t.Fatal("overlapping callers duplicated or skipped row effects", got)
	}
}

func TestScreenFanoutCapturedUpperAndCoalescedFollowup(t *testing.T) {
	s, db, _ := fanoutMovies(t, 3*screenFanoutRowLimit)
	publicationExec(t, db, `UPDATE screen_metadata_policies SET revision=revision+1 WHERE library_id='lib'`)
	if err := s.ScreenFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	upper := publicationScalar(t, db, `SELECT upper_id FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`)
	c := catalogtest.New(t, db)
	added := c.Movie(c.Handle("lib"), "/films/new.mp4", "New", 2000)
	before := publicationScalar(t, db, `SELECT generation FROM screen_metadata_work WHERE target_id=?`, added.ID)
	// Several new saves coalesce without discarding the finite pass in flight.
	for i := 0; i < 5; i++ {
		publicationExec(t, db, `UPDATE screen_metadata_policies SET revision=revision+1 WHERE library_id='lib'`)
	}
	if got := publicationScalar(t, db, `SELECT upper_id FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != upper {
		t.Fatal("save moved a running upper key", got, upper)
	}
	for i := 0; i < 2; i++ {
		if err := s.ScreenFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, db, `SELECT pass_generation||':'||pending FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != "0:1" {
		t.Fatal("saves restarted the old pass or lost followup", got)
	}
	if got := publicationScalar(t, db, `SELECT generation FROM screen_metadata_work WHERE target_id=?`, added.ID); got != before {
		t.Fatal("running pass crossed its captured upper key", got)
	}
	settleScreenFanout(t, s)
	if got := publicationScalar(t, db, `SELECT generation-? FROM screen_metadata_work WHERE target_id=?`, before, added.ID); got != "1" {
		t.Fatal("new saves were not coalesced into one followup", got)
	}
}

func TestScreenFanoutKeysetPlans(t *testing.T) {
	_, db, _ := fanoutMovies(t, 2)
	for _, tc := range []struct {
		q, index string
		args     []any
	}{
		{`SELECT target_kind,target_id FROM screen_metadata_work INDEXED BY screen_metadata_fanout_walk WHERE library_id=? AND (target_kind,target_id)>(?,?) AND (target_kind,target_id)<=(?,?) ORDER BY target_kind,target_id LIMIT 12`, "screen_metadata_fanout_walk", []any{"lib", "item", 1, "show", 999}},
		{`SELECT ce.kind,ce.id,k.name FROM catalog_entities ce INDEXED BY catalog_entities_library JOIN catalog_kinds k ON k.id=ce.kind WHERE ce.library_id=? AND (ce.kind,ce.id)>(?,?) AND (ce.kind,ce.id)<=(?,?) ORDER BY ce.kind,ce.id LIMIT 64`, "catalog_entities_library", []any{1, 1, 1, 20, 999}},
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+tc.q, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, tc.index) || strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, ">") {
			t.Fatal("walk does not seek bounded row keys", plan)
		}
	}
}

func TestScreenFanoutStalePublicationBeforeSettlement(t *testing.T) {
	s, db, provider := screenFixture(t)
	settleScreenFanout(t, s)
	claim, err := s.claimScreen(context.Background())
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	p, err := s.ScreenPolicy(context.Background(), "lib")
	if err != nil {
		t.Fatal(err)
	}
	no := false
	if err = s.UpdateScreenPolicy(context.Background(), "lib", ScreenPolicyUpdate{ExpectedRevision: p.Revision, ExpectedConsentRevision: p.ConsentRevision, ConfirmRemote: &no, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); err != nil {
		t.Fatal(err)
	}
	// The old row has not been reset. Live consent/policy must still fence it.
	if got := publicationScalar(t, db, `SELECT lease FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, claim.Base.TargetKind, claim.Base.Entity); got != claim.Token {
		t.Fatal("acceptance synchronously changed work", got)
	}
	w, err := dbwork.Begin(context.Background(), db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.screenCurrent(context.Background(), w.Tx(), claim)
	w.Rollback()
	if err != nil || ok {
		t.Fatal("stale publication retained authority before settlement", ok, err)
	}
	if err = s.publishScreen(context.Background(), claim, []screenCandidate{scoreScreen(provider.record, nil, 0, true)}, 0, "matched"); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_publications`); got != "0" {
		t.Fatal("stale provider result published before fanout settlement", got)
	}
}

func TestScreenFanoutTurnIncludesTransitiveEffects(t *testing.T) {
	s, db, _ := fanoutMovies(t, screenFanoutRowLimit+2)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unavailable',provider='tmdb',provider_id='42',selection_mode='owner';INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) SELECT target_kind,target_id,'identity','{}','owner',1,'owner','now' FROM screen_metadata_work;DELETE FROM artwork_dirty;UPDATE screen_metadata_policies SET revision=revision+1,providers='["tvdb"]';UPDATE screen_metadata_fanout SET rematch=1 WHERE effect='screen'`)
	publicationExec(t, db, `INSERT INTO screen_metadata_publications(id,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,source_kind,input_digest,query_digest,observed_at,language,region,selection_revision,generation,decision)
 SELECT 'fanout-'||target_id,target_kind,target_id,'tmdb','movie','42','{}',0.5,'[]','provider','inputs','query','now','en','US',selection_revision,generation,'accepted' FROM screen_metadata_work;
 UPDATE screen_metadata_work SET accepted_publication='fanout-'||target_id;DELETE FROM artwork_dirty;`)
	ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	w, err := dbwork.BeginConn(ctx, conn, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	tx := w.Tx()
	var m screenFanoutMarker
	err = tx.QueryRow(`SELECT library_id,effect,generation,pass_generation,rematch,pass_rematch,after_kind,after_id,upper_kind,upper_id FROM screen_metadata_fanout WHERE effect='screen'`).Scan(&m.library, &m.effect, &m.generation, &m.passGeneration, &m.rematch, &m.passRematch, &m.afterKind, &m.afterID, &m.upperKind, &m.upperID)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err = tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.screenFanoutQuantum(ctx, tx, &m, s.publicationTime().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after-before > 160 {
		t.Fatalf("turn changed %d rows including triggers, above 160", after-before)
	}
	children := after - before - int64(screenFanoutRowLimit) - 2 // work rows + start/checkpoint markers
	if children > 128 {
		t.Fatalf("turn changed %d child rows, above 128", children)
	}
	t.Logf("screen turn: %d base work rows +2 marker writes +%d child effects =%d total writes", screenFanoutRowLimit, children, after-before)
	var changed int
	if err = tx.QueryRow(`SELECT count(*) FROM screen_metadata_work WHERE provider=''`).Scan(&changed); err != nil || changed != screenFanoutRowLimit {
		t.Fatal("screen turn cap", changed, err)
	}
}

func TestScreenFanoutProducerMatrix(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			_, db, _ := fanoutMovies(t, 1)
			publicationExec(t, db, `INSERT INTO library_preferences(library_id) VALUES('lib') ON CONFLICT(library_id) DO NOTHING`)
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), db, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			var previous int
			if err = conn.QueryRowContext(ctx, `PRAGMA recursive_triggers`).Scan(&previous); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA recursive_triggers=%d`, previous))
			if _, err = conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive)); err != nil {
				t.Fatal(err)
			}
			w, err := dbwork.BeginConn(ctx, conn, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			tx := w.Tx()
			for _, tc := range []struct {
				q           string
				screen, art bool
			}{
				{`UPDATE screen_metadata_policies SET revision=revision+1`, true, false},
				{`UPDATE screen_metadata_consent SET revision=revision+1`, false, false},
				{`UPDATE library_preferences SET local_tags='off',revision=revision+1`, true, true},
				{`UPDATE library_scan_policies SET revision=revision+1`, true, true},
				{`UPDATE library_sources SET generation=generation+1`, true, true},
				{`UPDATE library_sources SET root_identity='adopted',identity_confirmed=1`, true, true},
				{`UPDATE library_sources SET health='inventory_running',last_complete_at='now',next_scan_at='later'`, false, false},
				{`UPDATE library_sources SET root_identity=root_identity`, true, true}, // configuration producer conservatively invalidates
				{`UPDATE libraries SET kind='tv' WHERE id='lib'`, true, true},
				{`UPDATE libraries SET root='/new' WHERE id='lib'`, true, true},
				{`UPDATE libraries SET name='Rename' WHERE id='lib'`, false, false},
				{`UPDATE libraries SET name=name WHERE id='lib'`, false, false},
			} {
				if _, err = tx.Exec(`UPDATE screen_metadata_fanout SET pending=0,rerun=0`); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(tc.q); err != nil {
					t.Fatal(tc.q, err)
				}
				if strings.Contains(tc.q, "UPDATE screen_metadata_consent") {
					var pending bool
					if err = tx.QueryRow(`SELECT pending FROM metadata_lookup_fanout WHERE effect='screen_consent'`).Scan(&pending); err != nil || !pending {
						t.Fatal("missing global consent intent", err)
					}
				}
				for _, effect := range []string{"screen", "artwork"} {
					var pending bool
					if err = tx.QueryRow(`SELECT pending FROM screen_metadata_fanout WHERE library_id='lib' AND effect=?`, effect).Scan(&pending); err != nil {
						t.Fatal(tc.q, err)
					}
					want := tc.screen
					if effect == "artwork" {
						want = tc.art
					}
					if pending != want {
						t.Fatalf("%s %s pending=%v want %v", tc.q, effect, pending, want)
					}
				}
			}
		})
	}
}

func TestScreenFanoutAcceptanceOwnsOneGenerationAndAuthority(t *testing.T) {
	s, db, _ := fanoutMovies(t, 1)
	ctx := context.Background()
	policy, err := s.ScreenPolicy(ctx, "lib")
	if err != nil {
		t.Fatal(err)
	}
	before := publicationScalar(t, db, `SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`)
	denied := errors.New("authorization denied")
	body := ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, Enabled: true, Providers: []string{"tvdb"}, Language: "en-US", RefreshMode: "replace_unlocked"}
	if err = s.UpdateScreenPolicy(ctx, "lib", body, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("authority boundary", err)
	}
	if got := publicationScalar(t, db, `SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`); got != before {
		t.Fatal("denied write changed marker")
	}
	if err = s.UpdateScreenPolicy(ctx, "lib", body, nil); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT generation-? FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`, before); got != "1" {
		t.Fatal("policy advanced twice", got)
	}
	if err = s.UpdateScreenPolicy(ctx, "lib", body, nil); !errors.Is(err, ErrScreenConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	before = publicationScalar(t, db, `SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`)
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return ApplyAdministrationPolicyTx(ctx, tx, "lib", "movie", LibraryProviderPolicy{Providers: []string{"tmdb"}, Language: "en-US", RefreshMode: "replace_unlocked"})
	}); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT generation-? FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`, before); got != "1" {
		t.Fatal("admin advanced twice", got)
	}
	settleScreenFanout(t, s)
	before = publicationScalar(t, db, `SELECT generation FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`)
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return ApplyLibraryAgentTx(ctx, tx, "lib", AgentLocal) }); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT (generation-?)||':'||rematch FROM screen_metadata_fanout WHERE library_id='lib' AND effect='screen'`, before); got != "1:0" {
		t.Fatal("agent advanced twice or added rematch", got)
	}
}
