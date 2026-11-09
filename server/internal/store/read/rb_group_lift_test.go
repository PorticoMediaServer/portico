package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_GROUP_LIFT_JOURNAL
// The fixture holds mixed-kind authored members whose public place is their
// first accepted parent: episodes -> season/show, songs -> album, book parts ->
// book, plus direct movie/show/season/book members. It deliberately includes
// repeated playlist occurrences, a NULL playlist entry, a retired member, a
// member of a retired show, a foreign-library member, a retired show target and
// an automatic collection identity.
func newRBGroupLiftSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1),(10,'collection',0,1,1,1,0),(13,'author',0,1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(1,'list','local','a','p','List','create','hash','2026-11-06T00:00:00Z'),(2,'shared','local','a','q','Shared','create2','hash','2026-11-06T00:00:00Z'),(3,'long','local','a','p','Long','create3','hash','2026-11-06T00:00:00Z')`)
	add := func(id, kind int64, title, extra string, args ...any) {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, id, public, kind)
		q := `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation` + extra + `) VALUES(?,?,1,?,?,?,'a',?,'a',0,?,?`
		values := []any{id, public, kind, title, title, title, public, public}
		for range args {
			q += ",?"
		}
		q += ")"
		f.exec(t, q, append(values, args...)...)
	}
	member := func(collection, item int64, key string) {
		f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(?,?,?)`, collection, item, key)
	}
	entry := func(id, playlist int64, item any, key string) {
		f.exec(t, `INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) VALUES(?,?,?,?,?)`, id, fmt.Sprintf("entry-%d", id), playlist, item, key)
	}
	// item<->album<->artist, show<->season<->episode, book<->part.
	show, retiredShow := int64(6001), int64(6002)
	seasonOne, seasonTwo := int64(6010), int64(6011)
	albumOne, albumTwo := int64(6300), int64(6301)
	book := int64(6500)
	f.exec(t, "BEGIN")
	add(show, 2, "Show", ",parent_id", nil)
	add(retiredShow, 2, "Retired Show", ",parent_id,retired", nil, int64(1))
	add(seasonOne, 3, "Season One", ",parent_id,show_id,season_number", show, show, int64(1))
	add(seasonTwo, 3, "Season Two", ",parent_id,show_id,season_number", show, show, int64(2))
	for n := int64(0); n < 10; n++ {
		add(6100+n, 4, fmt.Sprintf("Episode %d", n), ",parent_id,show_id,season_id,episode_number", seasonOne, show, seasonOne, n+1)
	}
	for n := int64(0); n < 5; n++ {
		add(6110+n, 4, fmt.Sprintf("Episode Two %d", n), ",parent_id,show_id,season_id,episode_number", seasonTwo, show, seasonTwo, n+1)
	}
	add(6120, 4, "Retired Episode", ",parent_id,show_id,season_id,episode_number,retired", seasonOne, show, seasonOne, int64(99), int64(1))
	add(6130, 4, "Episode Of Retired Show", ",parent_id,show_id,numbering,episode_number", retiredShow, retiredShow, "seasonal", int64(1))
	add(6400, 5, "Artist", ",parent_id", nil)
	add(albumOne, 6, "Album One", ",parent_id,artist_id", int64(6400), int64(6400))
	add(albumTwo, 6, "Album Two", ",parent_id,artist_id", int64(6400), int64(6400))
	for n := int64(0); n < 20; n++ {
		add(6310+n, 7, fmt.Sprintf("Song One %d", n), ",parent_id,album_id,disc_number,track_number", albumOne, albumOne, int64(1), n+1)
	}
	for n := int64(0); n < 10; n++ {
		add(6340+n, 7, fmt.Sprintf("Song Two %d", n), ",parent_id,album_id,disc_number,track_number", albumTwo, albumTwo, int64(1), n+1)
	}
	add(6360, 7, "Retired Song", ",parent_id,album_id,retired", albumOne, albumOne, int64(1))
	add(book, 8, "Book", ",parent_id", nil)
	for n := int64(0); n < 5; n++ {
		add(6510+n, 9, fmt.Sprintf("Part %d", n), ",parent_id,book_id,part_number", book, book, n+1)
	}
	add(6600, 1, "Movie One", ",parent_id", nil)
	add(6601, 1, "Movie Two", ",parent_id", nil)
	add(6602, 1, "Movie Three", ",parent_id", nil)
	// A long song source for the far-page walk.
	for n := int64(0); n < 100; n++ {
		add(6700+n, 7, fmt.Sprintf("Bulk Song %d", n), ",parent_id,album_id,disc_number,track_number", albumOne, albumOne, int64(1), n+1)
	}
	add(7000, 10, "Collection", ",parent_id", nil)
	add(7001, 10, "Automatic Collection", ",parent_id", nil)
	add(7002, 10, "Empty Collection", ",parent_id", nil)
	add(7003, 10, "Window Collection", ",parent_id", nil)
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'provider','automatic',7001)`)

	// Collection 7000: mixed kinds, repeated parents, duplicate authored keys,
	// retired/foreign members and a member of a retired show.
	key := func(n int) string { return fmt.Sprintf("a%04d", n) }
	for n, item := range []int64{6600, 6100, 6310, 6101, 6510, 6102, 6120, 6311, 1040, 6110, 1010, show, 6130, 6360, 6601} {
		member(7000, item, key(n))
	}
	// Songs sharing Album One and Album Two across many windows. 6310/6311 are
	// already members above, so this adds the rest of Album One's songs.
	for n := int64(0); n < 18; n++ {
		member(7000, 6312+n, key(100+int(n)))
	}
	for n := int64(0); n < 10; n++ {
		member(7000, 6340+n, key(200+int(n)))
	}
	for n := int64(0); n < 100; n++ {
		member(7000, 6700+n, key(300+int(n)))
	}
	for n := int64(0); n < 4; n++ {
		member(7000, 6511+n, key(500+int(n)))
	}
	member(7000, 6602, key(600))
	// One authored key names two different kinds: the item identity is the
	// deterministic tie, exactly as the collection page index orders them.
	member(7000, 1011, "a0100")
	// Collection 7003 has exactly cap+1 direct members (base entities 108/117
	// are retired by the fixture journal, so skip them).
	n := int64(100)
	for added := 0; added < rbGroupLiftCollectionRaw+1; n++ {
		if n%9 == 0 {
			continue
		}
		member(7003, n, fmt.Sprintf("w%04d", added))
		added++
	}

	// Playlist 1 (owner a/p): duplicate subject occurrences, a NULL entry, mixed
	// kinds and a long song tail.
	entry(1, 1, int64(6310), "k0000")
	entry(2, 1, nil, "k0001")
	entry(3, 1, int64(6310), "k0002") // same subject, later occurrence
	entry(4, 1, int64(6340), "k0003")
	entry(5, 1, int64(6100), "k0004")
	entry(6, 1, int64(6600), "k0005")
	entry(7, 1, int64(6311), "k0006")
	entry(8, 1, int64(6311), "k0007")
	entry(9, 1, int64(6120), "k0008") // retired member
	for n := int64(0); n < 60; n++ {
		entry(100+int64(n), 1, 6700+n, fmt.Sprintf("k%04d", 100+int(n)))
	}
	// A second, larger playlist proves the common 64-row window on its own.
	for n := int64(0); n < rbGroupLiftPlaylistRaw+1; n++ {
		entry(1000+int64(n), 3, 6700+int64(n%100), fmt.Sprintf("z%04d", n))
	}
	// A playlist owned by another profile is never shared into this viewer.
	entry(2000, 2, int64(6310), "s0000")
	f.exec(t, "COMMIT")
	return f
}

// END_RB_GROUP_LIFT_JOURNAL

func rbGroupLiftFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_group_lift_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_GROUP_LIFT_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_GROUP_LIFT_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbGroupLiftConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	// Separate logical PHASE2 test profile. Prep 16MiB is included in non-LIVE
	// demand; M grows to fund it plus its 20% contingency. This arithmetic is
	// not a whole-server physical qualification.
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

func newRBGroupLiftFixture(t *testing.T) (*browseFixture, *RBGroupLiftReader) {
	t.Helper()
	f := composeReadFixture(t, newRBGroupLiftSource(t), RBGroupLiftDefinitions(), rbGroupLiftConfig(), false, rbGroupLiftFixtureHash(t))
	r, err := NewRBGroupLiftReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbGroupLiftRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 396, ID: "rb-group-lift-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 12 << 20}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 200000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbGroupLiftRun(t *testing.T, job *RBGroupLiftPreparation) RBGroupLiftAdvance {
	t.Helper()
	var last RBGroupLiftAdvance
	for n := 0; n < 128; n++ {
		advance, err := job.Advance(t.Context(), rbGroupLiftRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.RawRows > rbGroupLiftQuantaPerAdvance*MaxRaw {
			t.Fatalf("window exceeded its raw bound: %+v", advance)
		}
		if advance.Ready {
			last.Ready, last.SourceOrdered = true, advance.SourceOrdered
			return last
		}
	}
	t.Fatal("lift preparation did not finish")
	return last
}

func rbGroupLiftPosition(after *RBGroupLiftCursor) int {
	if after == nil {
		return 0
	}
	return after.position
}

func rbGroupLiftRead(t *testing.T, f *browseFixture, job *RBGroupLiftPreparation, view BrowseInput, after *RBGroupLiftCursor, limit int) (RBGroupLiftPage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests(fmt.Sprintf("rb-group-lift-page-%p-%d", job, rbGroupLiftPosition(after)))
	var page RBGroupLiftPage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, cost, e = job.Page(ctx, request, time.Second, out, view, after, limit)
		return e
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatalf("lift page retained LIVE custody: %+v", u)
	}
	return page, cost, err
}

func rbGroupLiftPages(t *testing.T, f *browseFixture, job *RBGroupLiftPreparation, view BrowseInput, limit int) []int64 {
	t.Helper()
	var ids []int64
	var after *RBGroupLiftCursor
	for n := 0; n < 256; n++ {
		page, cost, err := rbGroupLiftRead(t, f, job, view, after, limit)
		if err != nil {
			t.Fatalf("page %d: %v", n, err)
		}
		if !page.SourceOrdered || page.Statements != cost.PublicationStatements || cost.PublicationStatements < 5 {
			t.Fatalf("page %d lost its complete publication proof: %+v %+v", n, page, cost)
		}
		ids = append(ids, page.IDs...)
		if page.Exhausted {
			if page.Next != nil {
				t.Fatal("terminal page has a cursor")
			}
			return ids
		}
		if page.Next == nil || len(page.IDs) == 0 {
			t.Fatalf("unfinished page has no continuation: %+v", page)
		}
		after = page.Next
	}
	t.Fatal("lift pages never exhausted")
	return nil
}

func rbGroupLiftOracle(t *testing.T, f *browseFixture, source RBGroupLiftSource, subject, kind int64, desc bool) []int64 {
	t.Helper()
	var sql string
	var args []any
	if source == RBGroupLiftCollection {
		sql = `WITH RECURSIVE walk(raw_pos,src,id,depth) AS (
 SELECT row_number() OVER (ORDER BY m.order_key,m.item_id),m.item_id,m.item_id,0 FROM catalog_collection_members AS m WHERE m.collection_id=?1
 UNION ALL
 SELECT w.raw_pos,w.src,e.parent_id,w.depth+1 FROM walk AS w JOIN catalog_entities AS e ON e.id=w.id WHERE w.depth<4 AND e.parent_id IS NOT NULL)
 SELECT c.id FROM (SELECT w.raw_pos AS pos,e.id AS id,e.kind AS kind,e.library_id AS library,e.retired AS retired,w.src AS src,w.depth AS depth FROM walk AS w JOIN catalog_entities AS e ON e.id=w.id) AS c
 WHERE c.kind=?2 AND c.library=1 AND c.retired=0 AND (c.depth=0 OR c.src IN (SELECT m.item_id FROM catalog_collection_members AS m JOIN catalog_entities AS me ON me.id=m.item_id WHERE m.collection_id=?1 AND me.library_id=1 AND me.retired=0))
 GROUP BY c.id ORDER BY min(c.pos)`
		args = []any{subject, kind}
	} else {
		sql = `WITH RECURSIVE walk(raw_pos,src,id,depth) AS (
 SELECT row_number() OVER (ORDER BY entry.order_key,entry.id),entry.id,entry.item_id,0 FROM catalog_playlist_entries AS entry WHERE entry.playlist_id=?1 AND entry.item_id IS NOT NULL
 UNION ALL
 SELECT w.raw_pos,w.src,e.parent_id,w.depth+1 FROM walk AS w JOIN catalog_entities AS e ON e.id=w.id WHERE w.depth<4 AND e.parent_id IS NOT NULL)
 SELECT c.id FROM (SELECT w.raw_pos AS pos,e.id AS id,e.kind AS kind,e.library_id AS library,e.retired AS retired,w.src AS src,w.depth AS depth FROM walk AS w JOIN catalog_entities AS e ON e.id=w.id) AS c
 WHERE c.kind=?2 AND c.library=1 AND c.retired=0 AND (c.depth=0 OR c.src IN (SELECT entry.id FROM catalog_playlist_entries AS entry JOIN catalog_entities AS me ON me.id=entry.item_id WHERE entry.playlist_id=?1 AND entry.item_id IS NOT NULL AND me.library_id=1 AND me.retired=0))
 GROUP BY c.id ORDER BY min(c.pos)`
		args = []any{subject, kind}
	}
	rows, err := f.conn.QueryContext(t.Context(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if desc {
		slices.Reverse(ids)
	}
	return ids
}

func rbGroupLiftSQLCount(t *testing.T, f *browseFixture, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.conn.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRBGroupLiftCompleteCollectionOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		raw := rbGroupLiftSQLCount(t, f, `SELECT count(*) FROM catalog_collection_members WHERE collection_id=7000`)
		for _, kind := range []int64{1, 2, 3, 4, 6, 7, 8} {
			for _, desc := range []bool{false, true} {
				in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
				in.View.Kind, in.View.Descending = kind, desc
				job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
				if err != nil {
					t.Fatal(err)
				}
				done := rbGroupLiftRun(t, job)
				if !done.Ready || !done.SourceOrdered || done.Restarts != 0 || int64(done.RawRows) != raw {
					t.Fatalf("collection kind=%d desc=%v did not walk the complete authored source: %+v want raw=%d", kind, desc, done, raw)
				}
				got := rbGroupLiftPages(t, f, job, in.View, 7)
				want := rbGroupLiftOracle(t, f, RBGroupLiftCollection, 7000, kind, desc)
				if !slices.Equal(got, want) {
					t.Fatalf("collection kind=%d desc=%v differs from the independent oracle: got %v want %v", kind, desc, got, want)
				}
				if err := job.Close(); err != nil {
					t.Fatal(err)
				}
				if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
					t.Fatal("closed lift retained physical bytes")
				}
			}
		}
	})
}

func TestRBGroupLiftPlaylistOccurrencesAndNullEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		raw := rbGroupLiftSQLCount(t, f, `SELECT count(*) FROM catalog_playlist_entries WHERE playlist_id=1`)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftPlaylist, Subject: 1}
		in.View.Kind = 6
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		done := rbGroupLiftRun(t, job)
		if !done.Ready || !done.SourceOrdered || int64(done.RawRows) != raw {
			t.Fatalf("playlist did not walk every raw occurrence: %+v want %d", done, raw)
		}
		got := rbGroupLiftPages(t, f, job, in.View, 7)
		want := rbGroupLiftOracle(t, f, RBGroupLiftPlaylist, 1, 6, false)
		if !slices.Equal(got, want) {
			t.Fatalf("playlist album order differs from the oracle: got %v want %v", got, want)
		}
		// Two entries name one song; its album takes the FIRST occurrence and is
		// never duplicated in the public order.
		albums := 0
		for _, id := range got {
			if id == 6300 {
				albums++
			}
		}
		if albums != 1 {
			t.Fatalf("repeated subject became %d public rows", albums)
		}
		// A retired member and a retired-song occurrence contribute no place but
		// still consume their raw positions.
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		in.View.Kind = 7
		job, err = r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		rbGroupLiftRun(t, job)
		songs := rbGroupLiftPages(t, f, job, in.View, 40)
		want = rbGroupLiftOracle(t, f, RBGroupLiftPlaylist, 1, 7, false)
		if !slices.Equal(songs, want) {
			t.Fatalf("playlist song order differs from the oracle: got %v want %v", songs, want)
		}
		seen := map[int64]int{}
		for _, id := range songs {
			seen[id]++
		}
		for id, count := range seen {
			if count != 1 {
				t.Fatalf("song %d appeared %d times in the public occurrence order", id, count)
			}
		}
	})
}

func TestRBGroupLiftAutomaticCollectionRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7001}
		in.View.Kind = 1
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		_, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second)
		if !errors.Is(err, ErrRBGroupLiftAutomatic) {
			t.Fatalf("automatic collection was not refused explicitly: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("automatic refusal retained values or bytes")
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		// An authored collection with no members is an exact empty public order,
		// not a pending or invented result.
		empty, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7002})
		if err != nil {
			t.Fatal(err)
		}
		rbGroupLiftRun(t, empty)
		got := rbGroupLiftPages(t, f, empty, f.input, 7)
		if len(got) != 0 {
			t.Fatalf("authored empty collection published members: %v", got)
		}
		if err := empty.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRBGroupLiftPlaylistAuthorityAndRevision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		// A playlist shared into another profile is never this viewer's order.
		shared := RBGroupLiftInput{View: f.input, Source: RBGroupLiftPlaylist, Subject: 2}
		shared.View.Kind = 7
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, shared)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); !errors.Is(err, ErrDenied) {
			t.Fatalf("foreign playlist was not denied: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("playlist denial retained values or bytes")
		}
		// A mid-stream revision restarts from the beginning and publishes the
		// fresh authored order, never a mixed one.
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftPlaylist, Subject: 1}
		in.View.Kind = 7
		job, err = r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE catalog_playlists SET revision=revision+1 WHERE id=1`)
		f.exec(t, `INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) VALUES(3000,'entry-3000',1,6700,'k0000a')`)
		done := rbGroupLiftRun(t, job)
		if !done.Ready || done.Restarts != 1 {
			t.Fatalf("playlist revision did not restart exactly once: %+v", done)
		}
		got := rbGroupLiftPages(t, f, job, in.View, 40)
		want := rbGroupLiftOracle(t, f, RBGroupLiftPlaylist, 1, 7, false)
		if !slices.Equal(got, want) {
			t.Fatalf("revised playlist order is mixed: got %v want %v", got, want)
		}
		// A delivery after another revision is a stale token, never a re-walk.
		f.exec(t, `UPDATE catalog_playlists SET revision=revision+1 WHERE id=1`)
		if _, _, err = rbGroupLiftRead(t, f, job, in.View, nil, 40); !errors.Is(err, ErrStale) {
			t.Fatalf("page accepted a drifted playlist revision: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("drifted playlist page retained values or bytes")
		}
	})
}

func TestRBGroupLiftAncestorRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
		in.View.Kind = 2
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=6001`)
		f.exec(t, `UPDATE libraries SET data_metadata_revision=data_metadata_revision+1 WHERE id='movies'`)
		done := rbGroupLiftRun(t, job)
		if !done.Ready {
			t.Fatalf("retired ancestor did not finish: %+v", done)
		}
		got := rbGroupLiftPages(t, f, job, in.View, 7)
		want := rbGroupLiftOracle(t, f, RBGroupLiftCollection, 7000, 2, false)
		if len(want) != 0 || len(got) != 0 {
			t.Fatalf("retired show was still lifted: got %v oracle %v", got, want)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		// The member's own identity still advances even though its ancestor is
		// retired, so the episode walk is not truncated.
		in.View.Kind = 4
		job, err = r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		done = rbGroupLiftRun(t, job)
		got = rbGroupLiftPages(t, f, job, in.View, 40)
		want = rbGroupLiftOracle(t, f, RBGroupLiftCollection, 7000, 4, false)
		if !slices.Equal(got, want) {
			t.Fatalf("retired ancestor truncated the member identity walk: got %v want %v", got, want)
		}
	})
}

func TestRBGroupLiftWindowBoundAndFirstFarPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7003}
		in.View.Kind = 1
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		first, err := job.Advance(t.Context(), rbGroupLiftRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		// cap+1 is read, exactly cap is reduced, and a full window is not EOF.
		if first.Ready || first.RawRows != rbGroupLiftCollectionRaw {
			t.Fatalf("collection window did not bind cap+1: %+v", first)
		}
		done := rbGroupLiftRun(t, job)
		if !done.Ready || first.RawRows+done.RawRows != rbGroupLiftCollectionRaw+1 {
			t.Fatalf("collection window lost its lookahead row: %+v %+v", first, done)
		}
		got := rbGroupLiftPages(t, f, job, in.View, 7)
		if len(got) != rbGroupLiftCollectionRaw+1 {
			t.Fatalf("collection far pages lost members: %d", len(got))
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		// The common playlist window binds cap+1 the same way.
		play := RBGroupLiftInput{View: f.input, Source: RBGroupLiftPlaylist, Subject: 3}
		play.View.Kind = 7
		job, err = r.BeginRBGroupLift(t.Context(), 1, 1<<20, play)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		first, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if first.Ready || first.RawRows != rbGroupLiftPlaylistRaw {
			t.Fatalf("playlist window did not bind cap+1: %+v", first)
		}
		done = rbGroupLiftRun(t, job)
		if !done.Ready || first.RawRows+done.RawRows != rbGroupLiftPlaylistRaw+1 {
			t.Fatalf("playlist window lost its lookahead row: %+v %+v", first, done)
		}
	})
}

func TestRBGroupLiftCapacityBeforeAppend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
		in.View.Kind = 7
		// payload 12KiB: the metadata reserve leaves room for 64 kept entities,
		// fewer than this authored song source proves.
		job, err := r.BeginRBGroupLift(t.Context(), 1, 16384, in)
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for n := 0; n < 32 && failure == nil; n++ {
			advance, err := job.Advance(t.Context(), rbGroupLiftRequest(), time.Second)
			if err != nil {
				failure = err
				break
			}
			if advance.Ready {
				t.Fatal("undersized admission published a truncated public order")
			}
		}
		if !errors.Is(failure, errRBPreparationCapacity) {
			t.Fatalf("capacity refusal missing: %v", failure)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("capacity refusal retained a partial set or bytes")
		}
		if _, _, err := rbGroupLiftRead(t, f, job, in.View, nil, 40); err == nil {
			t.Fatal("closed lift still paged")
		}
	})
}

func TestRBGroupLiftSameSnapshotPreflightRefusal(t *testing.T) {
	p := &RBGroupLiftPreparation{copyLimit: 20000, nativeLimit: 8 << 20}
	var calls []string
	query := func(name string, args ...any) ([][]any, error) {
		calls = append(calls, name)
		if !strings.HasSuffix(name, "_octets") {
			t.Fatal("payload requested before the byte preflight")
		}
		var rows [][]any
		for n := 0; n < 4; n++ {
			rows = append(rows, []any{int64(n + 1), int64(4096)})
		}
		return rows, nil
	}
	rows, err := p.readLeg(query, "collection", "first", 7000, 5)
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || len(calls) != 1 {
		t.Fatalf("octet preflight did not refuse before payload: %v %v %v", rows, err, calls)
	}
	// The native row bound is enforced before the payload too.
	p.copied, p.copyLimit, p.nativeLimit = 0, 1<<20, 1024
	if rows, err = p.readLeg(query, "collection", "first", 7000, 5); !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil {
		t.Fatalf("native row bound was not enforced: %v %v", rows, err)
	}
	// A torn measured window (payload count differs from the preflight) is stale
	// before any value is copied.
	p.copied = 0
	query = func(name string, args ...any) ([][]any, error) {
		if strings.HasSuffix(name, "_octets") {
			return [][]any{{int64(1), int64(2)}}, nil
		}
		return [][]any{{int64(1), "ab"}, {int64(2), "cd"}}, nil
	}
	if rows, err = p.readLeg(query, "collection", "first", 7000, 2); !errors.Is(err, ErrStale) || rows != nil {
		t.Fatalf("torn payload was not refused: %v %v", rows, err)
	}
}

func TestRBGroupLiftCrossPollCancellationAndDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
		in.View.Kind = 1
		job, err := r.BeginRBGroupLift(ctx, 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lift poll continued: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("cross-poll cancellation retained values or bytes")
		}
		job, err = r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(governor.PreparationLifetime + time.Second)
		if _, err = job.Advance(t.Context(), rbGroupLiftRequest(), time.Second); !errors.Is(err, governor.ErrPreparationExpired) {
			t.Fatalf("lift outlived the original deadline: %v", err)
		}
		if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("expired lift retained values or bytes")
		}
	})
}

func rbGroupLiftWriter(class governor.Class, now time.Time) governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 395, ID: "rb-group-lift-publication"}, Owner: 2, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: now.Add(time.Minute), Budget: governor.UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}, Footprint: []governor.Scope{{Library: 1}}}
}

func TestRBGroupLiftFirstHandoffAndForegroundDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
		in.View.Kind = 7
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		rbGroupLiftRun(t, job)
		if d := f.governor.Try(rbGroupLiftWriter(governor.Background, time.Now())); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.Conflict {
			t.Fatal("ready lift released the parked writer before first handoff", d)
		}
		first, _, err := rbGroupLiftRead(t, f, job, in.View, nil, 40)
		if err != nil || len(first.IDs) != 40 || first.Exhausted {
			t.Fatal("first handoff", first, err)
		}
		d := f.governor.Try(rbGroupLiftWriter(governor.Background, time.Now()))
		if d.Grant == nil {
			t.Fatal("publication did not resume after first handoff", d.Refusal)
		}
		if err := f.authority.Retired(d.Grant); err != nil {
			t.Fatal(err)
		}
		second, _, err := rbGroupLiftRead(t, f, job, in.View, first.Next, 40)
		if err != nil || len(second.IDs) != 40 || second.IDs[0] == first.IDs[len(first.IDs)-1] {
			t.Fatal("later handoff replayed or skipped values", second, err)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("disposal retained physical bytes")
		}

		// A foreground edit after a computing poll is a denial, not a restart.
		job2, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = job2.Advance(t.Context(), rbGroupLiftRequest(), time.Second); err != nil {
			t.Fatal(err)
		}
		edit := f.governor.Try(rbGroupLiftWriter(governor.Foreground, time.Now()))
		if edit.Grant == nil {
			t.Fatal(edit.Refusal)
		}
		if err = f.authority.Retired(edit.Grant); err != nil {
			t.Fatal(err)
		}
		if _, err = job2.Advance(t.Context(), rbGroupLiftRequest(), time.Second); !errors.Is(err, governor.ErrPreparationStale) {
			t.Fatal("security change restarted instead of denying", err)
		}
		if !job2.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
			t.Fatal("denied lift retained values or bytes")
		}
	})
}

func TestRBGroupLiftPublicLIVE(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftPlaylist, Subject: 1}
		in.View.Kind = 7
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		rbGroupLiftRun(t, job)
		live, request, output := liveRequests("rb-group-lift-live")
		page, _, err := job.Page(t.Context(), request, time.Second, BrowseOutput{}, in.View, nil, 40)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, RBGroupLiftPage{}) {
			t.Fatal("LIVE bypass", err, page)
		}
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
			page, _, err = job.Page(ctx, request, time.Second, out, in.View, nil, 40)
			if err != nil {
				return err
			}
			if len(page.IDs) != 40 || !page.SourceOrdered {
				t.Fatal("LIVE values/admission")
			}
			// The preparation's reserved bytes stay held until physical exit
			// (Close), alongside the one LIVE response page.
			rbGroupLiftLiveUsage(t, f, 1, 1<<20)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		rbGroupLiftLiveUsage(t, f, 0, 1<<20)
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
		rbGroupLiftLiveUsage(t, f, 0, 0)
	})
}

func TestRBGroupLiftForeignMemberAndRetiredSkip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRBGroupLiftFixture(t)
		// A foreign-library member is never a raw skip that hides the rest: the
		// complete walk continues and the foreign identity never appears.
		in := RBGroupLiftInput{View: f.input, Source: RBGroupLiftCollection, Subject: 7000}
		in.View.Kind = 1
		job, err := r.BeginRBGroupLift(t.Context(), 1, 1<<20, in)
		if err != nil {
			t.Fatal(err)
		}
		done := rbGroupLiftRun(t, job)
		got := rbGroupLiftPages(t, f, job, in.View, 7)
		if slices.Contains(got, int64(1040)) {
			t.Fatal("foreign member escaped into the public order")
		}
		if int64(done.RawRows) != rbGroupLiftSQLCount(t, f, `SELECT count(*) FROM catalog_collection_members WHERE collection_id=7000`) || len(got) == 0 {
			t.Fatalf("foreign/retired raw positions truncated the walk: %+v %v", done, got)
		}
		if err := job.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// rbGroupLiftLiveUsage adds the preparation reservation to the shared LIVE
// custody assertion; the governor refunds it only at physical exit.
func rbGroupLiftLiveUsage(t *testing.T, f *browseFixture, live int, reserved int64) {
	t.Helper()
	if got := f.authority.ForegroundUsage(); got.Live != live || got.Waiters != 0 {
		t.Fatalf("live custody %+v want%d", got, live)
	}
	usage := f.authority.Usage()[governor.Foreground]
	if usage[governor.Reader] != 0 || usage[governor.MemoryBytes] != int64(live)*((8<<20)+4096)+reserved || usage[governor.QueueBytes] != int64(live)*4096 {
		t.Fatalf("payload/metadata custody %+v live%d reserved%d", usage, live, reserved)
	}
}
