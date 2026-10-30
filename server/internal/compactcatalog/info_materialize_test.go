package compactcatalog_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These tests exercise the bounded canonical materializer against the real
// fresh-install schema (catalogtest.Open applies 0001_baseline.sql, including
// the normalized source staging tables and their immutability/ledger triggers).
// They cover the reconciler contract only: the producer, the holder pre-fence,
// activation, apply and every reader conversion stay owned elsewhere.

// infoTestChange inserts one active source change in phase 0 with a single
// witness naming objectID, so staging rows can be written before the input is
// sealed.
func infoTestChangeObject(t *testing.T, db *sql.DB, asset int64, token, objectID string, mask int64) int64 {
	t.Helper()
	var epoch int64
	if err := db.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, asset).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,family_mask,fingerprint,created_ms)
 VALUES(?,?,0,0,?,?,?,1)`, asset, token, epoch, mask, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	change, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,fingerprint)
 VALUES(?,0,0,'job','src',1,'root',?,'revision',1,?)`, change, objectID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	// The real row triggers maintain each manifest. The reconciler fixture
	// declares spare capacity first and seals only the exercised family below.
	for family := 1; family <= 18; family++ {
		if _, err = db.Exec(`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(?,?,0,1000000,0,0)`, change, family); err != nil {
			t.Fatal(err)
		}
	}
	return change
}

func infoTestChange(t *testing.T, db *sql.DB, asset int64, token string, mask int64) int64 {
	t.Helper()
	return infoTestChangeObject(t, db, asset, token, "object", mask)
}

// infoTestSeal writes one sealed family manifest and moves the change into the
// materialization phase, exactly as the producer's phase transition would.
func infoTestSeal(t *testing.T, db *sql.DB, change int64, family, rows int) {
	t.Helper()
	result, err := db.Exec(`UPDATE catalog_info_input_families SET rows_expected=?,sealed=1 WHERE change_id=? AND family=? AND rows_staged=? AND sealed=0`, rows, change, family, rows)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("seal family%d expected%d: rows%d %v", family, rows, n, err)
	}
}

func infoTestBeginPhase2(t *testing.T, db *sql.DB, change int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE catalog_info_changes SET phase=2 WHERE id=?`, change); err != nil {
		t.Fatal(err)
	}
}

// infoTestQuantum runs one materialize quantum in its own write transaction.
func infoTestQuantum(t *testing.T, db *sql.DB, change int64) bool {
	t.Helper()
	var done bool
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var err error
		done, err = compactcatalog.MaterializeInfoChangeTx(context.Background(), tx, change)
		return err
	})
	if err != nil {
		t.Fatalf("materialize quantum: %v", err)
	}
	return done
}

// infoTestQuantumError runs one quantum and returns the error the reconciler
// refused the change with, rolling the transaction back.
func infoTestQuantumError(t *testing.T, db *sql.DB, change int64) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.MaterializeInfoChangeTx(context.Background(), tx, change)
		return err
	})
	if err == nil {
		t.Fatal("the reconciler accepted an input it must refuse")
	}
	return err
}

// infoTestFinish runs quanta until the reconciler reports done and returns how
// many transactions it took.
func infoTestFinish(t *testing.T, db *sql.DB, change int64) int {
	t.Helper()
	for i := 1; i <= 600; i++ {
		if infoTestQuantum(t, db, change) {
			return i
		}
	}
	t.Fatal("the reconciler did not finish in 600 bounded quanta")
	return 0
}

func infoTestCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func infoTestString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// A changed stream and chapter family is installed in the canonical tables
// while a family outside the mask keeps its canonical rows untouched, and the
// header is handed back with its copy counters clear.
func TestInfoMaterializeCopiesOnlyChangedFamilies(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,1,'known','unchanged')`, movie.Token, size, modified)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('kept-subtitle',?,'embedded','0',0,NULL,'srt','eng','',0,0,'',0)`, movie.Token)

	const mask = 1<<0 | 1<<1 | 1<<2 | 1<<3 | 1<<4 // stream facts, streams, breadth, chapter facts, chapters
	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, mask)
	c.Exec(`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,0,?,3,?,?, 'streams-v2',1)`, change, movie.Token, size, modified)
	c.Exec(`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,1,?,0,'video','hevc','','',0,'',1,0)`, change, movie.Token)
	c.Exec(`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,2,?,1,'audio','truehd','eng','',8,'7.1',0,0)`, change, movie.Token)
	c.Exec(`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index,audio_description) VALUES(?,3,?,0,0)`, change, movie.Token)
	c.Exec(`INSERT INTO catalog_info_new_stream_breadth(change_id,input_ordinal,asset_id,stream_index,audio_description) VALUES(?,4,?,1,1)`, change, movie.Token)
	c.Exec(`INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,5,?,2,?,?,'known','chapters-v2')`, change, movie.Token, size, modified)
	c.Exec(`INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,6,?,0,'Opening',0,60)`, change, movie.Token)
	c.Exec(`INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,7,?,1,'Middle',60,120)`, change, movie.Token)
	for family, rows := range map[int]int{1: 1, 2: 2, 3: 2, 4: 1, 5: 2} {
		infoTestSeal(t, c.DB, change, family, rows)
	}
	infoTestBeginPhase2(t, c.DB, change)

	quanta := infoTestFinish(t, c.DB, change)
	if quanta < 2 {
		t.Fatalf("eight raw rows were installed in %d quanta", quanta)
	}
	if got := infoTestString(t, c.DB, `SELECT fingerprint FROM asset_stream_facts WHERE asset_id=?`, movie.Token); got != "streams-v2" {
		t.Fatalf("stream facts fingerprint is %q", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT revision FROM asset_stream_facts WHERE asset_id=?`, movie.Token); got != 3 {
		t.Fatalf("stream facts revision is %d, want the staged 3", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token); got != 2 {
		t.Fatalf("asset_streams holds %d rows", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT audio_description FROM asset_stream_breadth WHERE asset_id=? AND stream_index=1`, movie.Token); got != 1 {
		t.Fatalf("breadth row was not installed: audio_description %d", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_chapters WHERE asset_id=?`, movie.Token); got != 2 {
		t.Fatalf("asset_chapters holds %d rows", got)
	}
	// Unchanged families: the subtitle family is not in the mask.
	if got := infoTestCount(t, c.DB, `SELECT revision FROM asset_subtitle_facts WHERE asset_id=?`, movie.Token); got != 1 {
		t.Fatalf("unchanged subtitle facts revision is %d", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_subtitles WHERE asset_id=?`, movie.Token); got != 1 {
		t.Fatalf("unchanged subtitle rows are %d", got)
	}
	var phase, copyFamily, copyPhase, copyID, copyAux int64
	var copyKey string
	if err := c.DB.QueryRow(`SELECT phase,copy_family,copy_phase,copy_id,copy_aux,copy_key FROM catalog_info_changes WHERE id=?`, change).
		Scan(&phase, &copyFamily, &copyPhase, &copyID, &copyAux, &copyKey); err != nil {
		t.Fatal(err)
	}
	if phase != 2 || copyFamily != 0 || copyPhase != 0 || copyID != 0 || copyAux != 0 || copyKey != "" {
		t.Fatalf("header after materialization is phase %d copy %d/%d/%d/%d/%q", phase, copyFamily, copyPhase, copyID, copyAux, copyKey)
	}
}

// A kept prefix longer than a thousand raw keys is walked one index seek per
// key: the reconciler must reach EOF over the kept prefix at the fixed quantum
// rate instead of scanning the prefix in one unbounded filtered delete.
func TestInfoMaterializeWalksKeptPrefixToEOF(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,?,?,'old')`, movie.Token, size, modified)
	c.Exec(`WITH RECURSIVE seq(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM seq WHERE n<1000)
 INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced)
 SELECT ?,n,'audio','aac','','',2,'',0,0 FROM seq`, movie.Token)

	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<0|1<<1)
	c.Exec(`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint) VALUES(?,0,?,2,?,?,'new')`, change, movie.Token, size, modified)
	// The complete NEW set keeps lanes 0..999 and drops the single lane 1000, so
	// the obsolete row sits behind a kept prefix of a thousand keys.
	c.Exec(`WITH RECURSIVE seq(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM seq WHERE n<999)
 INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced)
 SELECT ?,n+1,?,n,'audio','aac','','',2,'',0,0 FROM seq`, change, movie.Token)
	infoTestSeal(t, c.DB, change, 1, 1)
	infoTestSeal(t, c.DB, change, 2, 1000)
	infoTestBeginPhase2(t, c.DB, change)

	quanta := infoTestFinish(t, c.DB, change)
	if quanta < 126 {
		t.Fatalf("1001 canonical keys were reconciled in %d quanta; the kept prefix was not walked at the fixed rate", quanta)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token); got != 1000 {
		t.Fatalf("%d canonical lanes survive, want 1000", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=? AND stream_index=1000`, movie.Token); got != 0 {
		t.Fatal("the obsolete tail lane survived")
	}
	if got := infoTestString(t, c.DB, `SELECT fingerprint FROM asset_stream_facts WHERE asset_id=?`, movie.Token); got != "new" {
		t.Fatalf("stream facts fingerprint is %q", got)
	}
}

// No quantum installs more than eight raw rows, and obsolete canonical rows of
// a changed family are removed instead of accumulating.
func TestInfoMaterializeIsBoundedAndRemovesObsoleteRows(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	// Twenty obsolete canonical lanes, of which only lane 7 survives.
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,?,?,'old')`, movie.Token, size, modified)
	for index := 0; index < 20; index++ {
		c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,'audio','aac','','',2,'',0,0)`, movie.Token, index)
	}

	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<0|1<<1)
	c.Exec(`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint) VALUES(?,0,?,2,?,?,'new')`, change, movie.Token, size, modified)
	c.Exec(`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,1,?,7,'audio','eac3','','',6,'',0,0)`, change, movie.Token)
	infoTestSeal(t, c.DB, change, 1, 1)
	infoTestSeal(t, c.DB, change, 2, 1)
	infoTestBeginPhase2(t, c.DB, change)

	previous := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token)
	for i := 0; i < 200; i++ {
		done := infoTestQuantum(t, c.DB, change)
		current := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token)
		if current-previous > 8 || previous-current > 8 {
			t.Fatalf("quantum %d changed the lane count by %d rows", i+1, current-previous)
		}
		previous = current
		if done {
			break
		}
		if i == 199 {
			t.Fatal("the reconciler did not finish")
		}
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token); got != 1 {
		t.Fatalf("%d canonical lanes survive, want only the staged one", got)
	}
	if got := infoTestString(t, c.DB, `SELECT codec FROM asset_streams WHERE asset_id=?`, movie.Token); got != "eac3" {
		t.Fatalf("surviving lane codec is %q", got)
	}
}

// An obsolete parent whose child family is outside the mask is refused, never
// deleted through an uncharged FK cascade, no matter how many children it has.
func TestInfoMaterializeRefusesUnmaskedChildCascade(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,?,?,'known','old')`, movie.Token, size, modified)
	for index := 0; index < 9; index++ {
		c.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,'',0,60)`, movie.Token, index)
	}
	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<3) // chapter facts only
	infoTestSeal(t, c.DB, change, 4, 0)                               // the complete NEW set is empty
	infoTestBeginPhase2(t, c.DB, change)

	if err := infoTestQuantumError(t, c.DB, change); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("an obsolete parent with an unmasked child family was refused with %v", err)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_chapters WHERE asset_id=?`, movie.Token); got != 9 {
		t.Fatalf("the refused change cascaded %d child rows away", 9-got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_chapter_facts WHERE asset_id=?`, movie.Token); got != 1 {
		t.Fatalf("the refused change removed the parent facts row")
	}
}

// A masked child family is reconciled first, so an obsolete parent with many
// children is removed only after every child is gone, and never by cascade.
func TestInfoMaterializeRemovesChildrenBeforeParent(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,?,?,'known','old')`, movie.Token, size, modified)
	for index := 0; index < 9; index++ {
		c.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,'',0,60)`, movie.Token, index)
	}
	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<3|1<<4)
	infoTestSeal(t, c.DB, change, 4, 0)
	infoTestSeal(t, c.DB, change, 5, 0)
	infoTestBeginPhase2(t, c.DB, change)

	if quanta := infoTestFinish(t, c.DB, change); quanta < 2 {
		t.Fatalf("nine child rows and their parent were removed in %d quanta", quanta)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_chapters WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("%d canonical chapters survive", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_chapter_facts WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("the obsolete chapter facts row survives")
	}
}

// A single giant raw key stops the quantum after that one key: the budget is
// charged per key, so seven more giant keys are never read eagerly.
func TestInfoMaterializeWideKeyStopsQuantum(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,1,'known','old')`, movie.Token, size, modified)
	huge := "0" + strings.Repeat("x", 300000)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('huge',?,'embedded',?,0,NULL,'srt','eng','',0,0,'',0)`, movie.Token, huge)
	for index := 0; index < 9; index++ {
		locator := string(rune('a' + index))
		c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(?,?,'embedded',?,0,NULL,'srt','eng','',0,0,'',0)`, "small-"+locator, movie.Token, locator)
	}
	change := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<7) // subtitles only
	infoTestSeal(t, c.DB, change, 8, 0)
	infoTestBeginPhase2(t, c.DB, change)

	if done := infoTestQuantum(t, c.DB, change); done {
		t.Fatal("a single 300 KiB key finished the whole family in one quantum")
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_subtitles WHERE asset_id=?`, movie.Token); got != 9 {
		t.Fatalf("%d of 10 subtitle rows remain after the wide-key quantum", got)
	}
	var aux int64
	var key3 string
	if err := c.DB.QueryRow(`SELECT copy_aux,copy_key3 FROM catalog_info_changes WHERE id=?`, change).Scan(&aux, &key3); err != nil {
		t.Fatal(err)
	}
	if aux != 1 || key3 != huge {
		t.Fatalf("the wide key was not checkpointed: aux %d key length %d", aux, len(key3))
	}
	infoTestFinish(t, c.DB, change)
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_subtitles WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("%d subtitle rows survive an empty NEW set", got)
	}
}

// A shared subtitle sidecar keeps its canonical id and the other asset's track,
// because the reconciler upserts sidecars by path and never deletes one.
func TestInfoMaterializePreservesSharedSidecars(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	one := c.Movie(films, "/m/One.mkv", "One", 2020)
	two := c.Movie(films, "/m/Two.mkv", "Two", 2021)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, two.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES('canonical-sidecar','/m/shared.srt',10,20,'old-digest')`)
	c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,1,'known','one')`, one.Token, size, modified)
	c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES('one-track',?,'sidecar','/m/shared.srt',-1,'canonical-sidecar','srt','eng','',0,0,'',0)`, one.Token)

	change := infoTestChange(t, c.DB, two.Asset, two.Token, 1<<5|1<<6|1<<7) // facts, sidecars, subtitles
	c.Exec(`INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,0,?,1,?,?,0,1,'known','two')`, change, two.Token, size, modified)
	c.Exec(`INSERT INTO catalog_info_new_subtitle_sidecars(change_id,input_ordinal,id,path,size,modified_ns,digest) VALUES(?,1,'staged-sidecar','/m/shared.srt',11,21,'new-digest')`, change)
	c.Exec(`INSERT INTO catalog_info_new_subtitles(change_id,input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(?,2,'two-track',?,'sidecar','/m/shared.srt',-1,'staged-sidecar','srt','eng','',0,0,'',0)`, change, two.Token)
	infoTestSeal(t, c.DB, change, 6, 1)
	infoTestSeal(t, c.DB, change, 7, 1)
	infoTestSeal(t, c.DB, change, 8, 1)
	infoTestBeginPhase2(t, c.DB, change)

	infoTestFinish(t, c.DB, change)
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM subtitle_sidecars`); got != 1 {
		t.Fatalf("the shared sidecar table holds %d rows", got)
	}
	if got := infoTestString(t, c.DB, `SELECT id FROM subtitle_sidecars WHERE path='/m/shared.srt'`); got != "canonical-sidecar" {
		t.Fatalf("the sidecar id was replaced with %q", got)
	}
	if got := infoTestString(t, c.DB, `SELECT digest FROM subtitle_sidecars WHERE path='/m/shared.srt'`); got != "new-digest" {
		t.Fatalf("the sidecar digest is %q", got)
	}
	if got := infoTestString(t, c.DB, `SELECT sidecar_id FROM asset_subtitles WHERE id='one-track'`); got != "canonical-sidecar" {
		t.Fatalf("the other asset's track now references %q", got)
	}
	if got := infoTestString(t, c.DB, `SELECT sidecar_id FROM asset_subtitles WHERE id='two-track'`); got != "canonical-sidecar" {
		t.Fatalf("the new track references %q", got)
	}
}

// A changed disc family reconciles titles in dependency order: obsolete titles
// go, a swapped numbering is applied without tripping the title-number unique
// constraint, selection tokens survive, and the owner's newer choice is kept.
func TestInfoMaterializeDiscTitlesPreservesIdentityAndChoice(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/Disc.mkv", "Disc", 2020)
	c.Drain()
	c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('disc-source','m','/disc-fixture','/disc-fixture')`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT 'disc-object',src.id,a.token,src.incarnation,'/m/Disc','r','{}',a.size,a.modified_ns
 FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities item ON item.id=link.entity_id
 JOIN catalog_libraries library ON library.id=item.library_id JOIN library_sources src ON src.library_id=library.library_id WHERE a.id=? LIMIT 1`, movie.Asset)
	fingerprint := strings.Repeat("a", 64)
	c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision)
 VALUES('disc-object',?,'r',1,'root','bluray','iso','none',?,'tokA',5)`, movie.Token, fingerprint)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-object','A','tokA',1,0,600000,1,0)`)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-object','B','tokB',2,1,600000,1,1)`)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-object','C','tokC',3,2,600000,1,2)`)

	change := infoTestChangeObject(t, c.DB, movie.Asset, movie.Token, "disc-object", 1<<9|1<<10|1<<11) // facts, titles, clips
	c.Exec(`INSERT INTO catalog_info_new_disc_facts(change_id,input_ordinal,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision)
 VALUES(?,0,'disc-object',?,'r2',1,'root','bluray','iso','none',?,'tokB',2)`, change, movie.Token, fingerprint)
	c.Exec(`INSERT INTO catalog_info_new_disc_titles(change_id,input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES(?,1,'disc-object','A','tokA',2,0,600000,1,0)`, change)
	c.Exec(`INSERT INTO catalog_info_new_disc_titles(change_id,input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES(?,2,'disc-object','B','tokB',1,1,600000,1,1)`, change)
	c.Exec(`INSERT INTO catalog_info_new_disc_clips(change_id,input_ordinal,object_id,title_key,sequence,file,in_time_90k,out_time_90k) VALUES(?,3,'disc-object','A',0,'/BDMV/STREAM/00001.m2ts',0,90000)`, change)
	infoTestSeal(t, c.DB, change, 10, 1)
	infoTestSeal(t, c.DB, change, 11, 2)
	infoTestSeal(t, c.DB, change, 12, 1)
	infoTestBeginPhase2(t, c.DB, change)

	infoTestFinish(t, c.DB, change)
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_titles WHERE object_id='disc-object'`); got != 2 {
		t.Fatalf("%d titles survive, want 2", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT title_number FROM inventory_disc_titles WHERE object_id='disc-object' AND title_key='A'`); got != 2 {
		t.Fatalf("title A has number %d", got)
	}
	if got := infoTestString(t, c.DB, `SELECT selection_token FROM inventory_disc_titles WHERE object_id='disc-object' AND title_key='A'`); got != "tokA" {
		t.Fatalf("title A token is %q", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_titles WHERE title_number>=1000000`); got != 0 {
		t.Fatalf("%d parked title numbers survived", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_clips WHERE object_id='disc-object' AND title_key='A'`); got != 1 {
		t.Fatalf("%d clips installed", got)
	}
	if got := infoTestString(t, c.DB, `SELECT owner_title_key FROM inventory_disc_facts WHERE object_id='disc-object'`); got != "tokA" {
		t.Fatalf("the owner's choice was replaced with %q", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT choice_revision FROM inventory_disc_facts WHERE object_id='disc-object'`); got != 5 {
		t.Fatalf("the choice revision is %d", got)
	}
	if got := infoTestString(t, c.DB, `SELECT source_revision FROM inventory_disc_facts WHERE object_id='disc-object'`); got != "r2" {
		t.Fatalf("the probe revision is %q", got)
	}
}

// Disc families are scoped to the exact witness object, never to every object
// of the asset: publishing one observation of an asset must leave the other
// object's canonical facts and title tree exactly as they were.
func TestInfoMaterializeDiscObjectIsolation(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/Disc.mkv", "Disc", 2020)
	c.Drain()
	c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('disc-source','m','/disc-fixture','/disc-fixture')`)
	for _, object := range []string{"disc-a", "disc-b"} {
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT ?,src.id,a.token,src.incarnation,?,'r','{}',a.size,a.modified_ns
 FROM catalog_assets a JOIN catalog_asset_links link ON link.asset_id=a.id JOIN catalog_entities item ON item.id=link.entity_id
 JOIN catalog_libraries library ON library.id=item.library_id JOIN library_sources src ON src.library_id=library.library_id WHERE a.id=? LIMIT 1`, object, "/m/"+object, movie.Asset)
	}
	fingerprint := strings.Repeat("a", 64)
	for _, fact := range []struct{ object, owner string }{{"disc-a", "tokA"}, {"disc-b", "tokB"}} {
		c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision)
 VALUES(?,?,'r1',1,'root','bluray','iso','none',?,?,1)`, fact.object, movie.Token, fingerprint, fact.owner)
	}
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-a','A1','tok-a1',1,0,600000,1,0)`)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-a','A2','tok-a2',2,1,600000,1,1)`)
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES('disc-b','B1','tok-b1',1,0,600000,1,0)`)
	c.Exec(`INSERT INTO inventory_disc_clips(object_id,title_key,sequence,file,in_time_90k,out_time_90k) VALUES('disc-a','A1',0,'/BDMV/STREAM/a.m2ts',0,90000)`)
	c.Exec(`INSERT INTO inventory_disc_clips(object_id,title_key,sequence,file,in_time_90k,out_time_90k) VALUES('disc-b','B1',0,'/BDMV/STREAM/b.m2ts',0,90000)`)

	change := infoTestChangeObject(t, c.DB, movie.Asset, movie.Token, "disc-a", 1<<9|1<<10|1<<11)
	c.Exec(`INSERT INTO catalog_info_new_disc_facts(change_id,input_ordinal,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision)
 VALUES(?,0,'disc-a',?,'r2',1,'root','bluray','iso','none',?,'tokA',1)`, change, movie.Token, fingerprint)
	c.Exec(`INSERT INTO catalog_info_new_disc_titles(change_id,input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist) VALUES(?,1,'disc-a','A1','tok-a1',1,0,900000,1,0)`, change)
	c.Exec(`INSERT INTO catalog_info_new_disc_clips(change_id,input_ordinal,object_id,title_key,sequence,file,in_time_90k,out_time_90k) VALUES(?,2,'disc-a','A1',0,'/BDMV/STREAM/a.m2ts',0,90000)`, change)
	infoTestSeal(t, c.DB, change, 10, 1)
	infoTestSeal(t, c.DB, change, 11, 1)
	infoTestSeal(t, c.DB, change, 12, 1)
	infoTestBeginPhase2(t, c.DB, change)

	infoTestFinish(t, c.DB, change)
	// The published observation of object A is complete.
	if got := infoTestString(t, c.DB, `SELECT source_revision FROM inventory_disc_facts WHERE object_id='disc-a'`); got != "r2" {
		t.Fatalf("object A revision is %q", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_titles WHERE object_id='disc-a'`); got != 1 {
		t.Fatalf("object A holds %d titles, want 1", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT duration_ms FROM inventory_disc_titles WHERE object_id='disc-a' AND title_key='A1'`); got != 900000 {
		t.Fatalf("object A title duration is %d", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_clips WHERE object_id='disc-a'`); got != 1 {
		t.Fatalf("object A holds %d clips", got)
	}
	// The other valid observation of the same asset is untouched.
	if got := infoTestString(t, c.DB, `SELECT source_revision FROM inventory_disc_facts WHERE object_id='disc-b'`); got != "r1" {
		t.Fatalf("object B revision was rewritten to %q", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_titles WHERE object_id='disc-b'`); got != 1 {
		t.Fatalf("object B holds %d titles", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT title_number FROM inventory_disc_titles WHERE object_id='disc-b' AND title_key='B1'`); got != 1 {
		t.Fatalf("object B title was renumbered to %d", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_clips WHERE object_id='disc-b'`); got != 1 {
		t.Fatalf("object B holds %d clips", got)
	}
}

// The reconciler refuses to write anything from an input that is not sealed at
// its expected count, and from a changed child family whose canonical parent is
// not there.
func TestInfoMaterializeRefusesIncompleteInput(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}

	short := infoTestChange(t, c.DB, movie.Asset, movie.Token, 1<<0)
	c.Exec(`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint) VALUES(?,0,?,1,?,?,'new')`, short, movie.Token, size, modified)
	c.Exec(`UPDATE catalog_info_input_families SET rows_expected=2 WHERE change_id=? AND family=1`, short)
	infoTestBeginPhase2(t, c.DB, short)
	if err := infoTestQuantumError(t, c.DB, short); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("an unsealed family was refused with %v", err)
	}

	orphanAsset := c.Movie(films, "/m/Two.mkv", "Two", 2021)
	orphan := infoTestChange(t, c.DB, orphanAsset.Asset, orphanAsset.Token, 1<<1)
	// The staging schema itself refuses a child row without its staged parent, so
	// stage the parent but declare only the child in the change mask: the
	// reconciler must still refuse a child whose canonical parent does not exist.
	c.Exec(`INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint) VALUES(?,0,?,1,?,?,'new')`, orphan, orphanAsset.Token, size, modified)
	c.Exec(`INSERT INTO catalog_info_new_streams(change_id,input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,1,?,0,'video','hevc','','',0,'',1,0)`, orphan, orphanAsset.Token)
	infoTestSeal(t, c.DB, orphan, 2, 1)
	infoTestBeginPhase2(t, c.DB, orphan)
	if err := infoTestQuantumError(t, c.DB, orphan); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("a child family without its canonical parent was refused with %v", err)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, orphanAsset.Token); got != 0 {
		t.Fatalf("a refused input still wrote %d canonical lanes", got)
	}
}

func TestInfoMaterializeRotatesTitleIdentityWithBoundedRequestRetirement(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("rotation", "Rotation", "movie", "/rotation")
	movie := c.Movie(films, "/rotation/one.iso", "One", 2026)
	c.Drain()
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'rotation-object',s.id,a.token,s.incarnation,'one.iso','r','{}',a.size,a.modified_ns FROM catalog_assets a JOIN library_sources s ON s.library_id='rotation' WHERE a.id=?`, movie.Asset)
	c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint) VALUES('rotation-object',?,'r',1,'old-root','dvd','iso','none',?)`, movie.Token, strings.Repeat("a", 64))
	c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,dvd_title,vts,pgc) VALUES('rotation-object','1','old-title-token',1,0,600000,1,1,1,1)`)
	c.Exec(`INSERT INTO inventory_disc_chapters(object_id,title_key,chapter_order,start_ms) VALUES('rotation-object','1',0,0)`)
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('rotation-job','rotation','running','unit')`)
	c.Exec(`INSERT INTO inventory_disc_title_requests(job_id,object_id,title_key,source_revision,fingerprint) VALUES('rotation-job','rotation-object','1','r',?)`, strings.Repeat("a", 64))
	change := infoTestChangeObject(t, c.DB, movie.Asset, movie.Token, "rotation-object", ((1<<18)-1)&^((1<<9)-1))
	c.Exec(`INSERT INTO catalog_info_new_disc_facts(change_id,input_ordinal,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint) VALUES(?,0,'rotation-object',?,'r',2,'new-root','dvd','iso','none',?)`, change, movie.Token, strings.Repeat("b", 64))
	c.Exec(`INSERT INTO catalog_info_new_disc_titles(change_id,input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,dvd_title,vts,pgc) VALUES(?,0,'rotation-object','1','new-title-token',1,0,600000,1,1,1,1)`, change)
	c.Exec(`INSERT INTO catalog_info_new_disc_chapters(change_id,input_ordinal,object_id,title_key,chapter_order,start_ms) VALUES(?,0,'rotation-object','1',0,1)`, change)
	for family := 10; family <= 18; family++ {
		count := 0
		if family == 10 || family == 11 || family == 14 {
			count = 1
		}
		infoTestSeal(t, c.DB, change, family, count)
	}
	infoTestBeginPhase2(t, c.DB, change)
	infoTestFinish(t, c.DB, change)
	if got := infoTestString(t, c.DB, `SELECT selection_token FROM inventory_disc_titles WHERE object_id='rotation-object'`); got != "new-title-token" {
		t.Fatal("new incarnation reused OLD title identity", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_title_requests WHERE object_id='rotation-object'`); got != 0 {
		t.Fatal("old title request survived identity retirement", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT start_ms FROM inventory_disc_chapters WHERE object_id='rotation-object'`); got != 1 {
		t.Fatal("old child retained across identity replacement", got)
	}
}

// A timed probe replaces one title's complete technical frame. Other titles
// and objects retain their independent accepted observations. A structure reset
// with no staged title-media row still clears the whole object's timed sets.
func TestInfoMaterializeTimedTitleScopeAndEmptyStructureReset(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("timed", "Timed", "movie", "/timed")
	movie := c.Movie(lib, "/timed/disc.iso", "Disc", 2000)
	c.Drain()
	c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('timed-source','timed','/timed-source','/timed-source')`)
	for _, object := range []string{"object-a", "object-b"} {
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,'timed-source',token,'root',?,'r','{}',size,modified_ns FROM catalog_assets WHERE id=?`, object, object, movie.Asset)
		c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint) VALUES(?,?,'r',1,'root','dvd','iso','none',?)`, object, movie.Token, strings.Repeat("f", 64))
		for n, title := range []string{"A", "B"} {
			c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count) VALUES(?,?,?,?,0,60000,1)`, object, title, object+title, n+1)
			c.Exec(`INSERT INTO inventory_disc_title_media(object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms) VALUES(?,?,'r','fp','old','dvd','h264','aac',720,480,60000)`, object, title)
			c.Exec(`INSERT INTO inventory_disc_title_media_streams VALUES(?,?,0,'audio','aac','','',2,'stereo',1,0,'','','{}','{}')`, object, title)
			c.Exec(`INSERT INTO inventory_disc_title_media_members VALUES(?,?,'old.vob','identity',10,20,1,0)`, object, title)
		}
	}
	c.Drain()
	publish := func(object, title string, empty bool) {
		t.Helper()
		mask := int64(1<<15 | 1<<16 | 1<<17)
		if empty {
			mask |= 1 << 10
		}
		change := infoTestChangeObject(t, c.DB, movie.Asset, movie.Token, object, mask)
		count := 0
		if !empty {
			// Unchanged canonical parents are explicitly witnessed in staging; the
			// staging FK never permits a child to borrow another observation's tree.
			c.Exec(`INSERT INTO catalog_info_new_disc_facts(change_id,input_ordinal,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision) SELECT ?,0,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, change, object)
			c.Exec(`INSERT INTO catalog_info_new_disc_titles(change_id,input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc) SELECT ?,0,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, change, object, title)
			infoTestSeal(t, c.DB, change, 10, 1)
			infoTestSeal(t, c.DB, change, 11, 1)
			c.Exec(`INSERT INTO catalog_info_new_disc_title_media(change_id,input_ordinal,object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known) VALUES(?,0,?,?,'r','fp','new','dvd','h264','aac',720,480,60000,0,1)`, change, object, title)
			count = 1
		} else {
			infoTestSeal(t, c.DB, change, 11, 0)
		}
		infoTestSeal(t, c.DB, change, 16, count)
		infoTestSeal(t, c.DB, change, 17, 0)
		infoTestSeal(t, c.DB, change, 18, 0)
		infoTestBeginPhase2(t, c.DB, change)
		infoTestFinish(t, c.DB, change)
		c.Exec(`UPDATE catalog_info_changes SET phase=5 WHERE id=?`, change)

		retired := false
		for turn := 0; turn < 512; turn++ {
			err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				var e error
				retired, e = compactcatalog.AdvanceInfoPublicationTx(context.Background(), tx, movie.Asset, movie.Token, compactcatalog.NewInfoMaterializer())
				return e
			})
			if err != nil {
				t.Fatal("raw retirement", err)
			}
			if retired {
				break
			}
		}
		if !retired {
			t.Fatal("source input retirement did not finish")
		}
	}
	publish("object-a", "A", false)
	if got := infoTestString(t, c.DB, `SELECT revision FROM inventory_disc_title_media WHERE object_id='object-a' AND title_key='B'`); got != "old" {
		t.Fatal("alternate title lost", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_title_media_streams WHERE object_id='object-a' AND title_key='B'`); got != 1 {
		t.Fatal("alternate streams lost", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_title_media_members WHERE object_id='object-a' AND title_key='B'`); got != 1 {
		t.Fatal("alternate members lost", got)
	}
	publish("object-b", "B", false)
	if got := infoTestString(t, c.DB, `SELECT revision FROM inventory_disc_title_media WHERE object_id='object-a' AND title_key='A'`); got != "new" {
		t.Fatal("other object's accepted update lost", got)
	}
	publish("object-a", "", true)
	for _, table := range []string{"inventory_disc_titles", "inventory_disc_title_media", "inventory_disc_title_media_streams", "inventory_disc_title_media_members"} {
		if got := infoTestCount(t, c.DB, `SELECT count(*) FROM `+table+` WHERE object_id='object-a'`); got != 0 {
			t.Fatal("empty structure retained timed children", table, got)
		}
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM inventory_disc_title_media WHERE object_id='object-b'`); got != 2 {
		t.Fatal("other object changed during reset", got)
	}
	if got := infoTestString(t, c.DB, `SELECT revision FROM inventory_disc_title_media WHERE object_id='object-b' AND title_key='B'`); got != "new" {
		t.Fatal("mixed sequence lost final frame", got)
	}
}
