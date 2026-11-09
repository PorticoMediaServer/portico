package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_RELATIONSHIP_PREPARE_JOURNAL
// The fixture keeps more than one raw window and more than 256 rows in a single
// parent. Equal folded keys (A/a) and prefix keys (a/aa/ab) recur across many
// artists and albums, so an order that concatenates per-parent runs is visibly
// wrong. Retired, age-restricted, foreign-library and missing-ancestor rows stay
// in the raw streams; a rejected row must advance the private cursor.
func newRBRelationshipSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`)
	type entity struct {
		id                int64
		kind              int64
		key, keep         string
		retired           int64
		age               any
		library           int64
		artist, album     any
		disc, track, part any
	}
	add := func(e entity) {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(e.id))
		library := e.library
		if library == 0 {
			library = 1
		}
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, e.id, public, library, e.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,artist_id,album_id,disc_number,track_number) VALUES(?,?,?,?,?,?,'h',?,'h',0,?,?,?,?,?,?,?,?)`,
			e.id, public, library, e.kind, fmt.Sprintf("Title %d", e.id), e.key, e.keep, public, public, e.retired, e.age, e.artist, e.album, e.disc, e.track)
	}
	keys := []string{"A", "a", "aa", "ab", "B", "b"}
	keeps := []string{"The A", "the a", "The Aa", "The Ab", "B", "b"}
	// Three hundred artists: many share a folded key, and a few are retired.
	for i := int64(0); i < 300; i++ {
		retired := int64(0)
		if i%41 == 0 {
			retired = 1
		}
		add(entity{id: 100000 + i, kind: 5, key: keys[i%6], keep: keeps[i%6], retired: retired})
	}
	// Two albums per artist, also with repeated and prefix keys.
	for i := int64(0); i < 600; i++ {
		retired := int64(0)
		if i%53 == 0 {
			retired = 1
		}
		add(entity{id: 200000 + i, kind: 6, key: keys[i%6], keep: keeps[i%6], retired: retired, artist: 100000 + i%300})
	}
	// Two songs per album, plus >256 songs under one album.
	for i := int64(0); i < 600; i++ {
		for j := int64(0); j < 2; j++ {
			add(entity{id: 300000 + i*2 + j, kind: 7, key: "song", keep: "song", album: 200000 + i, disc: 1 + j, track: i % 7})
		}
	}
	for i := int64(0); i < 300; i++ {
		add(entity{id: 310000 + i, kind: 7, key: "song", keep: "song", album: 200001, disc: 1, track: i})
	}
	// Missing and invalid ancestors: dangling album/artist, foreign-library
	// album, retired artist, retired album, retired song.
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(900010,x'00000000000000000000000000000000',2,6)`)
	// A foreign-library album needs an artist in its own library: the composite
	// (library, artist) FK forbids pointing at this library's artist.
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(900009,x'00000000000000000000000000000009',2,5)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(900009,x'00000000000000000000000000000009',2,5,'Foreign artist','foreign artist','f','foreign artist','f',0,zeroblob(16),zeroblob(16))`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) VALUES(900010,x'00000000000000000000000000000000',2,6,'Foreign album','foreign','f','foreign','f',0,zeroblob(16),zeroblob(16),900009)`)
	// Missing ancestors cannot exist under the live foreign keys, so inject the
	// dangling parent identities with checks off; a torn/partial snapshot may
	// still present them and the reducer must refuse or skip them.
	f.exec(t, `PRAGMA foreign_keys=OFF`)
	add(entity{id: 900000, kind: 7, key: "song", keep: "song", album: 900099})
	add(entity{id: 900001, kind: 7, key: "song", keep: "song", album: 200053})
	add(entity{id: 900002, kind: 7, key: "song", keep: "song", album: 900010, library: 2})
	add(entity{id: 900003, kind: 6, key: "orphan", keep: "orphan", artist: 900098})
	add(entity{id: 900004, kind: 7, key: "song", keep: "song", album: 900003})
	f.exec(t, `PRAGMA foreign_keys=ON`)
	add(entity{id: 900005, kind: 6, key: "retired artist album", keep: "retired artist album", artist: 100041})
	add(entity{id: 900006, kind: 7, key: "song", keep: "song", album: 900005})
	add(entity{id: 900011, kind: 6, key: "retired album", keep: "retired album", retired: 1, artist: 100000})
	add(entity{id: 900012, kind: 7, key: "song", keep: "song", album: 900011})
	add(entity{id: 900013, kind: 7, key: "song", keep: "song", album: 200000, retired: 1})
	// One non-ASCII key exercises the ASCII-only SQLite NOCASE fold.
	add(entity{id: 900014, kind: 5, key: "ÉZéa", keep: "ÉZéa"})
	add(entity{id: 900015, kind: 6, key: "ÉZéa", keep: "ÉZéa", artist: 900014})
	add(entity{id: 900016, kind: 7, key: "song", keep: "song", album: 900015})
	return f
}

// END_RB_RELATIONSHIP_PREPARE_JOURNAL

// rbRelationshipTestEntity inserts one relationship row for the sparse/NULL
// regressions. artist, album, disc and track may be nil; a caller that needs a
// literal NULL the schema CHECK forbids enables ignore_check_constraints.
func rbRelationshipTestEntity(t *testing.T, f *browseFixture, id, kind, library int64, key string, artist, album, disc, track any) {
	t.Helper()
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, uint64(id))
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, public, library, kind)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id,album_id,disc_number,track_number) VALUES(?,?,?,?,?,?,'h',?,'h',0,?,?,?,?,?,?)`,
		id, public, library, kind, fmt.Sprintf("Title %d", id), key, key, public, public, artist, album, disc, track)
}

func rbRelationshipFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_relationship_prepare_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_RELATIONSHIP_PREPARE_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_RELATIONSHIP_PREPARE_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbRelationshipConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate PHASE2 logical test profile. Prep 16MiB is included in non-LIVE
	// demand; M grows to fund it plus its 20% contingency. This arithmetic is
	// not a whole-server/2GB physical qualification.
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 80 << 20
	cfg.Partitions[governor.Foreground][governor.WALBytes] += 1 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func newRBRelationshipFixture(t *testing.T) (*browseFixture, *RBRelationshipPrepareReader) {
	t.Helper()
	f := composeReadFixture(t, newRBRelationshipSource(t), RBRelationshipPrepareDefinitions(), rbRelationshipConfig(), false, rbRelationshipFixtureHash(t))
	r, err := NewRBRelationshipPrepareReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbRelationshipRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 394, ID: "rb-relationship-prepare-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbRelationshipBegin(t *testing.T, r *RBRelationshipPrepareReader, view BrowseInput, sort RBRelationshipSort) *RBRelationshipPreparation {
	t.Helper()
	in := RBRelationshipPrepareInput{View: view, Sort: sort}
	job, err := r.BeginRBRelationshipPrepare(t.Context(), 1, 1<<20, in)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func rbRelationshipAdvanceAll(t *testing.T, job *RBRelationshipPreparation) RBRelationshipPrepareAdvance {
	t.Helper()
	var last RBRelationshipPrepareAdvance
	for n := 0; n < 128; n++ {
		advance, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last
		}
	}
	t.Fatal("relationship preparation did not finish")
	return last
}

// rbRelationshipAdvanceThroughBusy polls one preparation to readiness while
// tolerating a mid-window governor refusal. A refusal must keep the private
// cursors, so the next poll resumes; losing them would truncate the order or
// fail to terminate. It reports whether a refusal was actually observed.
func rbRelationshipAdvanceThroughBusy(t *testing.T, job *RBRelationshipPreparation) (RBRelationshipPrepareAdvance, bool) {
	t.Helper()
	var last RBRelationshipPrepareAdvance
	busy := false
	for n := 0; n < 512; n++ {
		advance, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second)
		if errors.Is(err, governor.ErrBusy) {
			busy = true
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last, busy
		}
	}
	t.Fatal("relationship preparation did not finish after refusals")
	return last, busy
}

func rbRelationshipRead(t *testing.T, f *browseFixture, job *RBRelationshipPreparation, view BrowseInput, after *RBRelationshipPrepareCursor, limit int) (RBRelationshipPreparePage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests(fmt.Sprintf("rb-relationship-page-%d", rbRelationshipPosition(after)))
	var page RBRelationshipPreparePage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = job.Page(ctx, request, time.Second, out, view, after, limit)
		return err
	})
	if err == nil {
		if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
			t.Fatal("relationship page retained LIVE custody", u)
		}
	}
	return page, cost, err
}

func rbRelationshipPosition(after *RBRelationshipPrepareCursor) int {
	if after == nil {
		return 0
	}
	return after.position
}

func rbRelationshipCollect(t *testing.T, f *browseFixture, job *RBRelationshipPreparation, view BrowseInput) []int64 {
	t.Helper()
	var got []int64
	var after *RBRelationshipPrepareCursor
	for n := 0; n < 64; n++ {
		page, _, err := rbRelationshipRead(t, f, job, view, after, 200)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.IDs...)
		if page.Exhausted {
			if page.After != nil {
				t.Fatal("terminal relationship page has a cursor")
			}
			return got
		}
		if page.After == nil || len(page.IDs) == 0 {
			t.Fatal("unfinished relationship page has no continuation", page)
		}
		after = page.After
	}
	t.Fatal("relationship pages never exhausted")
	return nil
}

// rbRelationshipOracle is the independent plain-SQL expectation over the same
// fixture. It uses SQLite's own NOCASE ordering and entity-ID tie and never
// shares this task's walk or reducer.
func rbRelationshipOracle(t *testing.T, f *browseFixture, sort RBRelationshipSort, kind int64, keep, desc bool) []int64 {
	t.Helper()
	key := "sort_key"
	if keep {
		key = "sort_key_keep_articles"
	}
	var query string
	switch {
	case sort == RBRelationshipAlbum && kind == 7:
		query = `SELECT s.id FROM catalog_entities AS s JOIN catalog_entities AS a ON a.id=s.album_id
		 WHERE s.kind=7 AND s.library_id=1 AND s.retired=0
		   AND a.kind=6 AND a.library_id=1 AND a.retired=0
		 ORDER BY a.` + key + ` COLLATE NOCASE ASC,s.id ASC`
	case sort == RBRelationshipArtist && kind == 7:
		query = `SELECT s.id FROM catalog_entities AS s JOIN catalog_entities AS a ON a.id=s.album_id JOIN catalog_entities AS r ON r.id=a.artist_id
		 WHERE s.kind=7 AND s.library_id=1 AND s.retired=0
		   AND a.kind=6 AND a.library_id=1 AND a.retired=0
		   AND r.kind=5 AND r.library_id=1 AND r.retired=0
		 ORDER BY r.` + key + ` COLLATE NOCASE ASC,s.id ASC`
	case sort == RBRelationshipArtist && kind == 6:
		query = `SELECT a.id FROM catalog_entities AS a JOIN catalog_entities AS r ON r.id=a.artist_id
		 WHERE a.kind=6 AND a.library_id=1 AND a.retired=0
		   AND r.kind=5 AND r.library_id=1 AND r.retired=0
		 ORDER BY r.` + key + ` COLLATE NOCASE ASC,a.id ASC`
	default:
		t.Fatalf("unsupported relationship combination sort=%d kind=%d", sort, kind)
	}
	rows, err := f.conn.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if desc {
		slices.Reverse(ids)
	}
	return ids
}

func TestRBRelationshipPrepareCompleteTraversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		for _, combination := range []struct {
			sort RBRelationshipSort
			kind int64
		}{{RBRelationshipAlbum, 7}, {RBRelationshipArtist, 6}, {RBRelationshipArtist, 7}} {
			for _, keep := range []bool{false, true} {
				for _, desc := range []bool{false, true} {
					view := f.input
					view.Kind = combination.kind
					view.KeepArticles, view.Descending = keep, desc
					job := rbRelationshipBegin(t, r, view, combination.sort)
					done := rbRelationshipAdvanceAll(t, job)
					if !done.Ready || done.RawRows < 257 {
						t.Fatal("preparation did not exhaust the complete raw stream", done)
					}
					got := rbRelationshipCollect(t, f, job, view)
					want := rbRelationshipOracle(t, f, combination.sort, combination.kind, keep, desc)
					if !slices.Equal(got, want) {
						t.Fatalf("relationship order sort=%d kind=%d keep=%v desc=%v differs: got %d want %d", combination.sort, combination.kind, keep, desc, len(got), len(want))
					}
					if err := job.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	})
}

func TestRBRelationshipPrepareLargeParentAndEqualKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		// The special album alone owns 300 songs, and many artists/albums share
		// a folded key. Verify both properties independently of the reducer.
		var special int64
		if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities WHERE kind=7 AND album_id=200001 AND retired=0`).Scan(&special); err != nil || special < 257 {
			t.Fatal("fixture lost its >256-row parent", special, err)
		}
		var equal int64
		if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM (SELECT lower(sort_key) AS folded FROM catalog_entities WHERE kind=5 AND library_id=1 GROUP BY folded HAVING count(*)>1)`).Scan(&equal); err != nil || equal == 0 {
			t.Fatal("fixture lost its equal folded parent keys", equal, err)
		}
		for _, sort := range []RBRelationshipSort{RBRelationshipAlbum, RBRelationshipArtist} {
			for _, keep := range []bool{false, true} {
				view := f.input
				view.Kind, view.KeepArticles = 7, keep
				job := rbRelationshipBegin(t, r, view, sort)
				rbRelationshipAdvanceAll(t, job)
				got := rbRelationshipCollect(t, f, job, view)
				if !slices.Contains(got, int64(310000)) || !slices.Contains(got, int64(310299)) {
					t.Fatalf("large parent rows missing sort=%d keep=%v", sort, keep)
				}
				// Verify the equal-key merge itself: for every folded artist key
				// shared by more than one artist, all of those artists' output IDs
				// must appear in ascending ID order with no intervening smaller ID
				// from the same run. The oracle comparison above proves the exact
				// membership; this checks the no-concatenation property directly.
				if sort == RBRelationshipArtist {
					if err := rbRelationshipCheckEqualRuns(t, f, got, 7, keep); err != nil {
						t.Fatal(err)
					}
				}
				if err := job.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

// rbRelationshipCheckEqualRuns re-derives each output entity's folded artist
// key with plain SQL and checks the finished page has a non-decreasing key and
// strictly ascending IDs inside every equal-key run.
func rbRelationshipCheckEqualRuns(t *testing.T, f *browseFixture, got []int64, kind int64, keep bool) error {
	t.Helper()
	key := "sort_key"
	if keep {
		key = "sort_key_keep_articles"
	}
	var query string
	if kind == 7 {
		query = `SELECT s.id,lower(r.` + key + `) FROM catalog_entities AS s JOIN catalog_entities AS a ON a.id=s.album_id JOIN catalog_entities AS r ON r.id=a.artist_id
		 WHERE s.kind=7 AND s.library_id=1 AND s.retired=0
		   AND a.kind=6 AND a.library_id=1 AND a.retired=0
		   AND r.kind=5 AND r.library_id=1 AND r.retired=0`
	} else {
		query = `SELECT a.id,lower(r.` + key + `) FROM catalog_entities AS a JOIN catalog_entities AS r ON r.id=a.artist_id
		 WHERE a.kind=6 AND a.library_id=1 AND a.retired=0
		   AND r.kind=5 AND r.library_id=1 AND r.retired=0`
	}
	rows, err := f.conn.QueryContext(t.Context(), query)
	if err != nil {
		return err
	}
	defer rows.Close()
	folded := map[int64]string{}
	for rows.Next() {
		var id int64
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			return err
		}
		folded[id] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := 1; i < len(got); i++ {
		a, okA := folded[got[i-1]]
		b, okB := folded[got[i]]
		if !okA || !okB {
			continue
		}
		if a > b {
			return fmt.Errorf("relationship key run is descending at %d", i)
		}
		if a == b && got[i] < got[i-1] {
			return fmt.Errorf("equal parent keys were concatenated instead of merged by output ID at %d", i)
		}
	}
	return nil
}

func TestRBRelationshipPrepareSparseRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		defer job.Close()
		done := rbRelationshipAdvanceAll(t, job)
		want := rbRelationshipOracle(t, f, RBRelationshipAlbum, 7, false, false)
		if !done.Ready || int64(len(want)) >= int64(done.RawRows) {
			t.Fatalf("rejected/ancestor rows did not stay in the raw stream: total %d raw %d", len(want), done.RawRows)
		}
		got := rbRelationshipCollect(t, f, job, view)
		if !slices.Equal(got, want) {
			t.Fatal("sparse continuation lost or duplicated rows")
		}
	})
}

// TestRBRelationshipPrepareSparseParentsAndLiteralNulls fills one raw window
// with empty outer parents so the 48-statement proof recipe is genuinely
// exhausted mid-window, then checks that the private cursor resumes without
// truncation. It also covers schema-forbidden literal NULL album/artist shapes
// from a torn snapshot and the legal NULL disc/track coalescing.
func TestRBRelationshipPrepareSparseParentsAndLiteralNulls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		// Seventy empty albums and seventy empty artists sort before every
		// recorded title, so the first window spends its statement budget on
		// parents that produce no output at all.
		for i := int64(0); i < 70; i++ {
			key := fmt.Sprintf("%04d", i)
			rbRelationshipTestEntity(t, f, 700000+i, 6, 1, key, int64(100000), nil, nil, nil)
			rbRelationshipTestEntity(t, f, 710000+i, 5, 1, key, nil, nil, nil, nil)
		}
		// A torn snapshot can present a literal NULL artist on an album or a
		// literal NULL album on a song even though the schema CHECK forbids
		// them. They must be skipped, never fused into another parent.
		f.exec(t, `PRAGMA ignore_check_constraints=ON`)
		rbRelationshipTestEntity(t, f, 720000, 6, 1, "zz-null-artist", nil, nil, nil, nil)
		rbRelationshipTestEntity(t, f, 720010, 7, 1, "song", nil, nil, nil, nil)
		f.exec(t, `PRAGMA ignore_check_constraints=OFF`)
		// NULL disc/track are legal: the song legs coalesce them to (1,0) for
		// traversal while the published order stays by output ID.
		rbRelationshipTestEntity(t, f, 720001, 7, 1, "song", nil, int64(200001), nil, nil)
		rbRelationshipTestEntity(t, f, 720002, 7, 1, "song", nil, int64(200001), int64(2), nil)
		rbRelationshipTestEntity(t, f, 720003, 7, 1, "song", nil, int64(200001), nil, int64(5))
		// One song under the NULL-artist album: album order can reach it, but no
		// artist order may attribute it to an artist it does not have.
		rbRelationshipTestEntity(t, f, 720004, 7, 1, "song", nil, int64(720000), int64(1), int64(1))

		cases := []struct {
			name       string
			sort       RBRelationshipSort
			kind       int64
			descending bool
		}{
			{"album_empty_albums", RBRelationshipAlbum, 7, false},
			{"artist_empty_artists_albums", RBRelationshipArtist, 6, false},
			{"artist_empty_artists_songs_desc", RBRelationshipArtist, 7, true},
		}
		for _, tc := range cases {
			view := f.input
			view.Kind, view.Descending = tc.kind, tc.descending
			job := rbRelationshipBegin(t, r, view, tc.sort)
			done, busy := rbRelationshipAdvanceThroughBusy(t, job)
			if !done.Ready {
				t.Fatalf("%s: preparation never became ready: %+v", tc.name, done)
			}
			if !busy {
				t.Fatalf("%s: 48-statement window headroom was not exercised", tc.name)
			}
			if done.Restarts != 0 {
				t.Fatalf("%s: progress was discarded and restarted instead of resumed: %+v", tc.name, done)
			}
			got := rbRelationshipCollect(t, f, job, view)
			want := rbRelationshipOracle(t, f, tc.sort, tc.kind, false, tc.descending)
			if !slices.Equal(got, want) {
				t.Fatalf("%s: sparse continuation truncated or reordered: got %d want %d", tc.name, len(got), len(want))
			}
			// Song outputs only: the NULL-album song is absent and the
			// NULL-artist album's song is present only in album order. Membership
			// equality above proves it; these checks make the literals explicit.
			if tc.kind == 7 {
				if slices.Contains(got, int64(720010)) {
					t.Fatalf("%s: NULL-album song was published", tc.name)
				}
				for _, id := range []int64{720001, 720002, 720003} {
					if !slices.Contains(got, id) {
						t.Fatalf("%s: NULL disc/track song %d was dropped", tc.name, id)
					}
				}
				if tc.sort == RBRelationshipAlbum {
					if !slices.Contains(got, int64(720004)) {
						t.Fatalf("%s: NULL-artist album song was dropped from album order", tc.name)
					}
				} else if slices.Contains(got, int64(720004)) {
					t.Fatalf("%s: NULL-artist album song was published in artist order", tc.name)
				}
			}
			if err := job.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRBRelationshipPrepareCapacityBeforeAppend(t *testing.T) {
	rows := make([][]any, 0, 4)
	if err := func() error {
		out, exhausted, err := rbRelationshipSlice(rows, 2)
		if err != nil || len(out) != 0 || !exhausted {
			return fmt.Errorf("short window: %v %v %v", out, exhausted, err)
		}
		full := [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
		out, exhausted, err = rbRelationshipSlice(full, 2)
		if err != nil || len(out) != 2 || exhausted {
			return fmt.Errorf("full window: %v %v %v", out, exhausted, err)
		}
		over := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}
		if _, _, err := rbRelationshipSlice(over, 2); !errors.Is(err, ErrUnqualified) {
			return fmt.Errorf("unbounded window accepted: %v", err)
		}
		return nil
	}(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job, err := r.BeginRBRelationshipPrepare(t.Context(), 1, 8192, RBRelationshipPrepareInput{View: view, Sort: RBRelationshipAlbum})
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for n := 0; n < 64 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized admission published a truncated set")
			}
		}
		if !errors.Is(failure, errRBPreparationCapacity) {
			t.Fatal("capacity refusal missing", failure)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained a partial set or bytes")
		}
		if _, _, err := rbRelationshipRead(t, f, job, view, nil, 40); err == nil {
			t.Fatal("closed preparation still paged")
		}
	})
}

func TestRBRelationshipPrepareCrossPollCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		view := f.input
		view.Kind = 7
		job, err := r.BeginRBRelationshipPrepare(ctx, 1, 1<<20, RBRelationshipPrepareInput{View: view, Sort: RBRelationshipAlbum})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled preparation poll continued", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("cross-poll cancellation retained values or bytes")
		}
	})
}

func rbRelationshipWriter(class governor.Class, now time.Time) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 393, ID: "rb-relationship-publication"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 1}}}
}

func TestRBRelationshipPrepareFirstHandoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		rbRelationshipAdvanceAll(t, job)
		if d := f.governor.Try(rbRelationshipWriter(governor.Background, time.Now())); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
			t.Fatal("ready preparation released the parked writer before first handoff", d)
		}
		first, _, err := rbRelationshipRead(t, f, job, view, nil, 40)
		if err != nil || len(first.IDs) != 40 || first.Exhausted {
			t.Fatal("first handoff", first, err)
		}
		d := f.governor.Try(rbRelationshipWriter(governor.Background, time.Now()))
		if d.Grant == nil {
			t.Fatal("publication did not resume after first handoff", d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		second, _, err := rbRelationshipRead(t, f, job, view, first.After, 40)
		if err != nil || len(second.IDs) != 40 || second.IDs[0] <= first.IDs[len(first.IDs)-1] {
			t.Fatal("later handoff replayed or skipped values", second, err)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("disposal retained physical bytes")
		}
	})
}

func TestRBRelationshipPrepareRenameAndReparentRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		defer job.Close()
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		original := job.entry.lease.Deadline()
		bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
		// Rename one album and reparent one song, then move the content revision.
		f.exec(t, `UPDATE catalog_entities SET sort_key='zz renamed',sort_key_keep_articles='zz renamed' WHERE id=200000`)
		f.exec(t, `UPDATE catalog_entities SET album_id=200001 WHERE id=300010`)
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='movies'`)
		done := rbRelationshipAdvanceAll(t, job)
		if !done.Ready || done.Restarts < 1 {
			t.Fatalf("content invalidation did not restart: %+v", done)
		}
		if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
			t.Fatal("restart renewed the deadline or re-admitted physical bytes")
		}
		got := rbRelationshipCollect(t, f, job, view)
		want := rbRelationshipOracle(t, f, RBRelationshipAlbum, 7, false, false)
		if !slices.Equal(got, want) {
			t.Fatal("restart published a truncated, duplicated or pre-rename set")
		}
	})
}

func TestRBRelationshipPrepareForegroundEditDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		d := f.governor.Try(rbRelationshipWriter(governor.Foreground, time.Now()))
		if d.Grant == nil {
			t.Fatal(d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); !errors.Is(err, governor.ErrPreparationStale) {
			t.Fatal("security change restarted instead of denying", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied preparation retained values or bytes")
		}
	})
}

func TestRBRelationshipPrepareOriginalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err := job.Advance(t.Context(), rbRelationshipRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatal("restart outlived the original deadline", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired preparation retained values or bytes")
		}
	})
}

func TestRBRelationshipPrepareSameSnapshotPreflightRefusal(t *testing.T) {
	p := &RBRelationshipPreparation{view: BrowseInput{Library: 1, Kind: 7}, sort: RBRelationshipAlbum, copyLimit: 1 << 20, nativeLimit: 8 << 20, entry: &rbPreparedEntry{fence: browseFence{}}}
	var calls []string
	query := func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, ".bytes") {
			t.Fatal("payload requested before the byte preflight")
		}
		return [][]any{{int64(1), int64(3 << 20)}}, nil
	}
	rows, _, err := p.readOuter(query, browseFence{}, 8)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("octet preflight did not refuse before payload", rows, err, calls)
	}
	calls = nil
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		return [][]any{{int64(1), int64(9 << 20)}}, nil
	}
	rows, _, err = p.readOuter(query, browseFence{}, 8)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatal("native row bound was not enforced", rows, err, calls)
	}
	calls = nil
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		return [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}, nil
	}
	if _, _, err = p.readOuter(query, browseFence{}, 1); !errors.Is(err, ErrUnqualified) || len(calls) != 1 {
		t.Fatal("source that ignored its bound was accepted", err, calls)
	}
}

func TestRBRelationshipPreparePageDriftAndDisposal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		job := rbRelationshipBegin(t, r, view, RBRelationshipAlbum)
		rbRelationshipAdvanceAll(t, job)
		first, _, err := rbRelationshipRead(t, f, job, view, nil, 40)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='movies'`)
		if _, _, err := rbRelationshipRead(t, f, job, view, first.After, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a drifted fence", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("drifted page retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRBRelationshipPreparePageForeignCursorRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBRelationshipFixture(t)
		view := f.input
		view.Kind = 7
		begin := func(keep bool) *RBRelationshipPreparation {
			t.Helper()
			v := view
			v.KeepArticles = keep
			job := rbRelationshipBegin(t, r, v, RBRelationshipAlbum)
			rbRelationshipAdvanceAll(t, job)
			return job
		}
		firstPage := func(job *RBRelationshipPreparation, v BrowseInput) *RBRelationshipPrepareCursor {
			t.Helper()
			page, _, err := rbRelationshipRead(t, f, job, v, nil, 40)
			if err != nil || page.After == nil {
				t.Fatalf("first page: %+v %v", page, err)
			}
			return page.After
		}
		ignore := begin(false)
		foreign := *firstPage(ignore, view)
		if err := ignore.Close(); err != nil {
			t.Fatal(err)
		}
		keep := begin(true)
		if _, _, err := rbRelationshipRead(t, f, keep, view, &foreign, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a foreign-query cursor", err)
		}
		if err := keep.Close(); err != nil {
			t.Fatal(err)
		}
		job := begin(false)
		foreign = *firstPage(job, view)
		foreign.fence.horizon = "2000-01-01T00:00:00Z"
		if _, _, err := rbRelationshipRead(t, f, job, view, &foreign, 40); !errors.Is(err, ErrStale) {
			t.Fatal("page accepted a foreign-fence cursor", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("foreign-fence cursor retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRBRelationshipPrepareTitleOrderMatchesSQLiteNoCase(t *testing.T) {
	// The expectation below comes from SQLite's own `ORDER BY ... COLLATE
	// NOCASE, id`, never from the production fold. Keys without an embedded NUL
	// are the reachable domain: sqlite3_strnicmp stops comparing at a NUL.
	f := newBrowseFixture(t)
	f.exec(t, `CREATE TEMP TABLE rb_relationship_oracle(id INTEGER PRIMARY KEY, key TEXT NOT NULL)`)
	keys := []string{"", " ", "A", "a", "AA", "aa", "ab", "ab c", "The A", "the a", "z", "Z", "~", "\u007f", "a!", "a0", "a_", "a\u00e9", "A\u00e9", "a\u0301", "\u00e9", "\u00c9", "\u00df", "\u03c3", "\u03a3", "\u4e2d", "\ufb01", "\U0001f600"}
	for i, key := range keys {
		f.exec(t, `INSERT INTO rb_relationship_oracle(id,key) VALUES(?,?)`, int64(i+1), key)
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id, key FROM rb_relationship_oracle ORDER BY key COLLATE NOCASE, id`)
	if err != nil {
		t.Fatal(err)
	}
	type pair struct {
		id  int64
		key string
	}
	var want, got []pair
	for rows.Next() {
		var p pair
		if err = rows.Scan(&p.id, &p.key); err != nil {
			t.Fatal(err)
		}
		want = append(want, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	got = slices.Clone(want)
	slices.SortFunc(got, func(a, b pair) int {
		if c := strings.Compare(rbRelationshipFold(a.key), rbRelationshipFold(b.key)); c != 0 {
			return c
		}
		return compareInt64(a.id, b.id)
	})
	if !slices.Equal(got, want) {
		t.Fatal("Go relationship order differs from SQLite NOCASE", got, want)
	}
	if rbRelationshipFold("ÉZéa") != "Ézéa" {
		t.Fatal("SQLite ASCII collation changed")
	}
}
