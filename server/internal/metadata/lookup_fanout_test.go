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
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func requestLookupTest(t *testing.T, db *sql.DB, effects ...string) {
	t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return requestLookupFanoutTx(context.Background(), tx, effects...) }); err != nil {
		t.Fatal(err)
	}
}
func settleLookupFanout(t *testing.T, s *Service) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if publicationScalar(t, s.db, `SELECT count(*) FROM metadata_lookup_fanout WHERE pending=1`) == "0" {
			return
		}
		if err := s.LookupFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("lookup fanout did not settle within fixture bound")
}
func saveLookupTest(t *testing.T, s *Service, change func(*MetadataSettingsUpdate)) dbwork.Cost {
	t.Helper()
	current, err := s.MetadataSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	in := MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult}
	change(&in)
	ctx, cost := dbwork.Measure(context.Background())
	if _, err = s.UpdateMetadataSettings(ctx, in, nil); err != nil {
		t.Fatal(err)
	}
	return cost()
}
func lookupTestSnapshot(t *testing.T, q interface {
	Query(string, ...any) (*sql.Rows, error)
}) []string {
	t.Helper()
	out := fanoutSnapshot(t, q)
	for _, query := range []string{
		`SELECT 'mb:'||kind||':'||entity_id||':'||status||':'||generation||':'||revision||':'||lease||':'||lease_until||':'||next_attempt||':'||error FROM mb_jobs ORDER BY kind,entity_id`,
		`SELECT 'tvdb:'||show_id||':'||status||':'||generation||':'||revision||':'||lease||':'||lease_until||':'||next_attempt||':'||error FROM tvdb_jobs ORDER BY show_id`,
		`SELECT 'subtitle:'||id FROM subtitle_provider_candidates ORDER BY id`,
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

func TestLookupFanoutSettlementParity(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Exec(fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive))
			films := c.Library("films", "Films", "movie", "/films")
			music := c.Library("music", "Music", "music", "/music")
			tv := c.Library("tv", "TV", "tv", "/tv")
			artist := c.Artist(music, "Artist")
			var movie int64
			for i := 0; i < lookupFanoutRowLimit+3; i++ {
				movie = c.Movie(films, fmt.Sprintf("/films/%d.mp4", i), fmt.Sprintf("Film %d", i), 2000).ID
				album := c.Album(artist, fmt.Sprintf("Album %d", i), 2000)
				c.Song(album, 1, fmt.Sprintf("/music/%d.flac", i), "Song")
				show := c.Show(tv, fmt.Sprintf("Show %d", i), 2000)
				c.Exec(`INSERT INTO tvdb_jobs(show_id,status) VALUES(?,'pending_episodes') ON CONFLICT(show_id) DO UPDATE SET status='pending_episodes'`, show.ID)
			}
			s := New(c.DB, "")
			settleScreenFanout(t, s)
			c.Exec(`UPDATE screen_metadata_work SET status=CASE WHEN target_id%2=0 THEN 'provider_disabled' ELSE 'unavailable' END,lease='lease',lease_until='later',next_attempt='later',error='old';UPDATE mb_jobs SET status='needs_selection',lease='lease',lease_until='later',next_attempt='later',error='old';UPDATE tvdb_jobs SET lease='lease',lease_until='later',next_attempt='later',error='old';DELETE FROM artwork_dirty;`)
			c.Exec(`INSERT INTO subtitle_provider_candidates(id,actor,item_id,source_id,source_size,source_modified_ns,provider,file_id,language,title,attribution,expires_at) VALUES('old','actor',?,'source',1,1,'test',1,'en','Subtitle','','later')`, movie)
			tx, err := c.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`DELETE FROM subtitle_provider_candidates`,
				`UPDATE screen_metadata_work SET status=CASE WHEN status='provider_disabled' THEN 'pending' ELSE status END,generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error=''`,
				`UPDATE mb_jobs SET generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error=''`,
				`UPDATE tvdb_jobs SET generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error=''`,
				`INSERT INTO artwork_dirty(kind,entity_id) SELECT kind,entity_id FROM artwork_entity_heads WHERE true ON CONFLICT(kind,entity_id) DO NOTHING`,
			} {
				if _, err = tx.Exec(statement); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
			}
			want := lookupTestSnapshot(t, tx)
			tx.Rollback()
			saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { off := false; in.OnlineLookups = &off })
			if got := publicationScalar(t, c.DB, `SELECT count(*) FROM screen_metadata_work WHERE lease='lease'`); got == "0" {
				t.Fatal("accepting save traversed work")
			}
			dbwork.ResetPlans()
			settleLookupFanout(t, s)
			if cost := dbwork.Plans().Largest; cost.Rows > lookupFanoutPlan.Rows || cost.VMOps > lookupFanoutPlan.VMOps || cost.Pages > lookupFanoutPlan.Pages {
				t.Fatalf("quantum exceeded plan: %+v", cost)
			}
			if got := lookupTestSnapshot(t, c.DB); !reflect.DeepEqual(got, want) {
				t.Fatalf("settlement differs\ngot %v\nwant %v", got, want)
			}
			if got := publicationScalar(t, c.DB, `SELECT count(*) FROM metadata_lookup_fanout WHERE served_generation<>generation OR pass_generation<>0 OR pending<>0`); got != "0" {
				t.Fatal("obsolete completion", got)
			}
		})
	}
}

func TestLookupFanoutAcceptanceAndTruthfulWakes(t *testing.T) {
	var costs []dbwork.Cost
	for _, n := range []int{1, 37} {
		s, db, _ := fanoutMovies(t, n)
		cost := saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { off := false; in.OnlineLookups = &off })
		cost.ReadMillis = 0
		costs = append(costs, cost)
		before := publicationScalar(t, db, `SELECT sum(generation) FROM metadata_lookup_fanout`)
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) {
			off := false
			in.OnlineLookups = &off
			key := ""
			in.TMDBReadToken = &key
			language := "same"
			in.ArtworkLanguage = &language
		})
		if got := publicationScalar(t, db, `SELECT sum(generation) FROM metadata_lookup_fanout`); got != before {
			t.Fatal("unchanged settings woke work", before, got)
		}
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { key := "replacement"; in.TMDBReadToken = &key })
		if got := publicationScalar(t, db, `SELECT generation FROM metadata_lookup_fanout WHERE effect='screen_retry'`); got != "1" {
			t.Fatal("changed credential did not wake retry", got)
		}
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { key := "replacement"; in.TMDBReadToken = &key })
		if got := publicationScalar(t, db, `SELECT generation FROM metadata_lookup_fanout WHERE effect='screen_retry'`); got != "1" {
			t.Fatal("same credential woke retry", got)
		}
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { language := "en"; in.ArtworkLanguage = &language })
		if got := publicationScalar(t, db, `SELECT generation FROM metadata_lookup_fanout WHERE effect='artwork'`); got != "2" {
			t.Fatal("artwork change did not wake artwork", got)
		}
	}
	if !reflect.DeepEqual(costs[0], costs[1]) {
		t.Fatalf("save cost grew with catalogue: %+v", costs)
	}
	t.Logf("acceptance (1 / 37 rows): %+v", costs)
}

func TestLookupFanoutRapidToggleFinitePassAndEpoch(t *testing.T) {
	s, db, _ := fanoutMovies(t, 3*lookupFanoutRowLimit)
	old := s.lookupContext(context.Background())
	saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { v := false; in.OnlineLookups = &v })
	// Make the screen effect first, then preserve its captured pass across saves.
	publicationExec(t, db, `UPDATE metadata_lookup_fanout SET last_served_ms=100 WHERE effect<>'screen'`)
	if err := s.LookupFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT pass_generation||':'||pending FROM metadata_lookup_fanout WHERE effect='screen'`); got != "1:1" {
		t.Fatal(got)
	}
	upper := publicationScalar(t, db, `SELECT upper_id FROM metadata_lookup_fanout WHERE effect='screen'`)
	for _, online := range []bool{true, false} {
		saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { in.OnlineLookups = &online })
		if got := publicationScalar(t, db, `SELECT upper_id FROM metadata_lookup_fanout WHERE effect='screen'`); got != upper {
			t.Fatal("save restarted finite pass", got, upper)
		}
	}
	// An off/on must never revive a pre-toggle network/publication capability.
	saveLookupTest(t, s, func(in *MetadataSettingsUpdate) { v := true; in.OnlineLookups = &v })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkLookupContextTx(old, tx); !errors.Is(err, localonly.Err) {
		t.Fatal("old lookup capability revived", err)
	}
	tx.Rollback()
	// Stop exactly at the first pass completion, before the coalesced rerun.
	for i := 0; i < 100; i++ {
		if publicationScalar(t, db, `SELECT pass_generation FROM metadata_lookup_fanout WHERE effect='screen'`) == "0" {
			break
		}
		if err = s.LookupFanoutStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := publicationScalar(t, db, `SELECT served_generation||':'||generation||':'||pending FROM metadata_lookup_fanout WHERE effect='screen'`); got != "1:4:1" {
		t.Fatal("obsolete pass marked newer intent clean", got)
	}
	settleLookupFanout(t, s)
	if got := publicationScalar(t, db, `SELECT served_generation||':'||generation||':'||pending FROM metadata_lookup_fanout WHERE effect='screen'`); got != "4:4:0" {
		t.Fatal(got)
	}
}

func TestLookupFanoutRollbackBackoffRestartAndConcurrency(t *testing.T) {
	s, db, _ := fanoutMovies(t, 2*lookupFanoutRowLimit+1)
	requestLookupTest(t, db, "screen")
	publicationExec(t, db, `CREATE TRIGGER lookup_failure BEFORE UPDATE OF generation ON screen_metadata_work BEGIN SELECT RAISE(ABORT,'fixture failure');END`)
	before := fanoutSnapshot(t, db)
	if err := s.LookupFanoutStep(context.Background()); err == nil {
		t.Fatal("failure not returned")
	}
	if got := fanoutSnapshot(t, db); !reflect.DeepEqual(got, before) {
		t.Fatal("failed quantum committed effects")
	}
	if got := publicationScalar(t, db, `SELECT pass_generation||':'||after_id||':'||attempts||':'||(next_attempt_ms>0) FROM metadata_lookup_fanout`); got != "0:0:1:1" {
		t.Fatal("failed quantum advanced cursor", got)
	}
	ctx, cost := dbwork.Measure(context.Background())
	if err := s.LookupFanoutStep(ctx); err != nil {
		t.Fatal(err)
	}
	if cost().Transactions != 0 {
		t.Fatal("backoff still acquired writer", cost())
	}
	publicationExec(t, db, `DROP TRIGGER lookup_failure;UPDATE metadata_lookup_fanout SET next_attempt_ms=0`)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.LookupFanoutStep(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.LookupFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	cursor := publicationScalar(t, db, `SELECT after_id FROM metadata_lookup_fanout`)
	if cursor == "0" {
		t.Fatal("first turn failed to move")
	}
	// Reopen the actual installer-backed database; no service memory is needed.
	var databasePath string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(databasePath)
	db = reopened
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if got := publicationScalar(t, db, `SELECT after_id FROM metadata_lookup_fanout`); got != cursor {
		t.Fatal("reopen lost cursor", got, cursor)
	}
	s = New(db, "")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.LookupFanoutStep(context.Background()) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	settleLookupFanout(t, s)
	if got := publicationScalar(t, db, `SELECT count(DISTINCT generation) FROM screen_metadata_work`); got != "1" {
		t.Fatal("overlapping turns double-applied work", got)
	}
	if screenFanoutRetryDelay(1000) != 6*time.Hour {
		t.Fatal("retry overflow")
	}
}

func TestLookupFanoutRetryRejectsAdvanceAndBudget(t *testing.T) {
	s, db, ids := fanoutMovies(t, 2*lookupFanoutRowLimit+1)
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='unavailable',error='unrelated',attempts=5;UPDATE screen_metadata_work SET error='request_rejected' WHERE target_id=?`, ids[len(ids)-1])
	requestLookupTest(t, db, "screen_retry")
	if err := s.LookupFanoutStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := publicationScalar(t, db, `SELECT after_id FROM metadata_lookup_fanout`); got != fmt.Sprint(ids[lookupFanoutRowLimit-1]) {
		t.Fatal("rejected prefix did not advance raw cursor", got)
	}
	settleLookupFanout(t, s)
	if got := publicationScalar(t, db, `SELECT status||':'||attempts||':'||error FROM screen_metadata_work WHERE target_id=?`, ids[len(ids)-1]); got != "pending:0:" {
		t.Fatal("retry wake lost", got)
	}
	if got := publicationScalar(t, db, `SELECT count(*) FROM screen_metadata_work WHERE error='unrelated' AND attempts=5`); got != fmt.Sprint(len(ids)-1) {
		t.Fatal("retry touched unrelated failure", got)
	}
	requestLookupTest(t, db, "screen")
	// Measure transitive writes on one connection, including both marker writes.
	w, err := dbwork.Begin(dbwork.WithPlan(context.Background(), lookupFanoutPlan), db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	var m lookupFanoutMarker
	m.effect = "screen"
	if err = w.Tx().QueryRow(`SELECT generation FROM metadata_lookup_fanout WHERE effect='screen'`).Scan(&m.generation); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err = w.Tx().QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.lookupFanoutQuantum(context.Background(), w.Tx(), &m, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = w.Tx().QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after-before > lookupFanoutPlan.Rows {
		t.Fatalf("transitive writes %d exceed plan %+v", after-before, lookupFanoutPlan)
	}
	t.Logf("screen quantum transitive writes: %d", after-before)
}

func TestLookupFanoutKeysetQueriesSeek(t *testing.T) {
	_, db, _ := fanoutMovies(t, 2)
	for _, effect := range []string{"screen", "screen_retry", "mb", "tvdb", "artwork", "subtitles"} {
		source, err := lookupFanoutSourceFor(effect)
		if err != nil {
			t.Fatal(err)
		}
		where, args := source.window(&lookupFanoutMarker{afterKind: "item", afterID: 5, upperKind: "show", upperID: 100})
		args = append(args, lookupFanoutRowLimit)
		rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT `+source.columns()+` FROM `+source.table+` WHERE `+where+` ORDER BY `+source.order(false)+` LIMIT ?`, args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var a, b, c int
			var d string
			if err = rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			details = append(details, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, " ")
		if !strings.Contains(plan, "SEARCH") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("%s does not seek PK: %s", effect, plan)
		}
	}
}
