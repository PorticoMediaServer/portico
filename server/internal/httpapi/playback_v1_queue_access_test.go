package httpapi

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// queueAccessFixture gives the member (kids library only, profile limited to
// age 13) a film, an R-rated film, a show whose every episode is R, a show with
// one R episode, and an artist whose albums sit in the kids library and in the
// owner's library.
func queueAccessFixture(t *testing.T) (*v1Fixture, string, map[string]string) {
	f := newV1Fixture(t, 1)
	member, film := f.member()
	filmID := f.catalogTest.ID(film)
	var kids int64
	if err := f.db.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, filmID).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{"film": film}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	rated := func(item, rating string) {
		f.catalogTest.Attributes(f.catalogTest.ID(item), "contentRating", rating)
	}
	// Classified ratings (an unclassified spelling is withheld until the
	// classifier publishes its age, like VisibleItemTx).
	exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8),('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('member-profile',13,1)`)
	kidsR := newPlaybackMovieEntity(f, kids, "kids-r", "Grown-up film", 2000)
	f.names["restricted"] = kidsR
	rated(kidsR.Public, "R")
	ids["restricted"] = kidsR.Public
	for _, show := range []struct {
		id, title string
		ratings   []string
	}{{"all-r", "Late Night Show", []string{"R", "R"}}, {"mixed", "Family Show", []string{"PG", "R", "PG"}}} {
		showEntity := f.catalogTest.Show(kids, show.title, 2026)
		season := f.catalogTest.Season(showEntity, 1)
		f.names[show.id] = showEntity
		ids[show.id] = showEntity.Public
		for n, rating := range show.ratings {
			name := fmt.Sprintf("%s-e%d", show.id, n+1)
			path := filepath.Join(f.root, "kids", name+".mp4")
			item := f.catalogTest.Episode(showEntity, season, n+1, path)
			f.catalogTest.Fields(item.ID, map[string]any{"title": fmt.Sprintf("%s %d", show.title, n+1)})
			f.names[name] = item
			ids[name] = item.Public
			rated(item.Public, rating)
		}
	}
	artist := f.catalogTest.Artist(kids, "The Band")
	f.names["band"] = artist
	ids["band"] = artist.Public
	for _, album := range []struct {
		id      string
		library int64
	}{{"kids-album", kids}, {"owner-album", f.libraryHandle}} {
		local := album.id
		albumEntity := f.catalogTest.Entity(compactcatalog.Entity{Library: album.library, Kind: compactcatalog.Album, Parent: artist.ID, Key: compactcatalog.AlbumKey(local), Title: local, Year: 2000}, map[string]any{"artist_id": artist.ID, "local_key": local})
		f.names[album.id] = albumEntity
		ids[album.id] = albumEntity.Public
		for n := 1; n <= 2; n++ {
			name := fmt.Sprintf("%s-t%d", album.id, n)
			path := filepath.Join(f.root, album.id, fmt.Sprintf("%02d.mp3", n))
			item := f.catalogTest.Song(albumEntity, n, path, name)
			f.names[name] = item
			ids[name] = item.Public
			rated(item.Public, "PG")
		}
	}
	f.catalogTest.Drain()
	return f, member, ids
}

func containerSource(kind, id string) map[string]any {
	return map[string]any{"container": map[string]any{"kind": kind, "id": id}}
}

// P11: an item the member can't play (another library, or restricted for the
// profile) is indistinguishable from one that doesn't exist: dropped, never
// counted, and a queue of nothing else is 404 like a queue of unknown ids.
func TestPlaybackV1QueueItemsInaccessibleEqualsAbsent(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	n := 0
	create := func(source map[string]any) (int, playbackv1.QueueReply) {
		n++
		var out playbackv1.QueueReply
		w := f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": fmt.Sprintf("access-queue-%06d", n)}, map[string]any{"segments": []any{map[string]any{"source": source}}}, 0, &out)
		return w.Code, out
	}
	absent, _ := create(queueItems("no-such-item", "nor-this"))
	hidden, _ := create(queueItems(f.items[0], ids["restricted"]))
	if absent != 404 || hidden != 404 {
		t.Fatalf("only absent ids: %d; only inaccessible ids: %d", absent, hidden)
	}
	code, mixed := create(queueItems(f.items[0], ids["film"], ids["restricted"], "no-such-item"))
	if code != 201 || mixed.Queue.Total != 1 || len(mixed.Window) != 1 || mixed.Window[0].ItemID != ids["film"] {
		t.Fatalf("mixed: %d total %d window %+v", code, mixed.Queue.Total, mixed.Window)
	}
	if label := mixed.Queue.Segments[0].Label; label != "1 items" {
		t.Fatalf("label counts what the member can't play: %q", label)
	}
}

// P12: a container is checked against the profile's restrictions too. A show
// whose episodes are all restricted is 404 (its title never reaches a segment
// label); a show with some restricted episodes plays only the others.
func TestPlaybackV1QueueContainersApplyRestrictions(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	w := f.raw("POST", "/v1/queues", member, map[string]string{"Idempotency-Key": "access-queue-show-1"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("show", f.names["all-r"].Public)}}})
	if w.Code != 404 || strings.Contains(w.Body.String(), "Late Night Show") {
		t.Fatalf("restricted show: %d %s", w.Code, w.Body.String())
	}
	var mixed playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "access-queue-show-2"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("show", f.names["mixed"].Public)}}}, 201, &mixed)
	if mixed.Queue.Total != 2 || mixed.Queue.Segments[0].Count != 2 {
		t.Fatalf("mixed show: %+v", mixed.Queue)
	}
	for _, e := range mixed.Window {
		if e.ItemID == ids["mixed-e2"] {
			t.Fatalf("restricted episode in the window: %+v", mixed.Window)
		}
	}
	// Restricted after the snapshot: a placeholder with no id and no title.
	f.catalogTest.Attributes(f.catalogTest.ID(ids["mixed-e3"]), "contentRating", "R")
	f.catalogTest.Drain()
	var page struct {
		Items []playbackv1.QueueEntry `json:"items"`
	}
	f.callAs(member, "GET", "/v1/queues/"+mixed.Queue.ID+"/entries?limit=10", nil, nil, 200, &page)
	if len(page.Items) != 2 || page.Items[1].Available || page.Items[1].ItemID != "" || page.Items[1].Title != "" {
		t.Fatalf("entries after the restriction changed: %+v", page.Items)
	}
}

// P13: an artist spans libraries. Only songs in libraries the member can use
// are queued; the artist is neither refused wholesale nor leaks the others.
func TestPlaybackV1QueueArtistKeepsOnlyAccessibleSongs(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "access-queue-artist-1"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("artist", f.names["band"].Public)}}}, 201, &q)
	if q.Queue.Total != 2 {
		t.Fatalf("artist total %d: %+v", q.Queue.Total, q.Window)
	}
	for _, e := range q.Window {
		if e.ItemID != ids["kids-album-t1"] && e.ItemID != ids["kids-album-t2"] {
			t.Fatalf("song from another library: %+v", q.Window)
		}
	}
	// The owner, who can use both libraries, gets all four.
	var all playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "access-queue-artist-2"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("artist", f.names["band"].Public)}}}, 201, &all)
	if all.Queue.Total != 4 {
		t.Fatalf("owner artist total %d", all.Queue.Total)
	}
}

// The set-based fence must admit exactly what the per-item fence admits: the
// queue's predicate (contentaccess.VisibleItemsSQL) against VisibleItemTx plus
// the library check, for every item, for the member and the owner.
func TestVisibleItemsSQLAgreesWithVisibleItemTx(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	kidsFilm := f.catalogTest.ID(ids["restricted"])
	var kids int64
	if err := f.db.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, kidsFilm).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	pending := newPlaybackMovieEntity(f, kids, "kids-pending", "Unclassified", 2000)
	f.catalogTest.Attributes(pending.ID, "contentRating", "ZZ-9")
	// Member ceilings too: rated at most PG, nothing unrated, no "scary" label; one
	// title carries its rating only on the item row (no catalog attribute).
	if _, err := f.db.Exec(`INSERT INTO access_limits VALUES('member',1,0,'{"maxContentRating":"PG","allowUnrated":false,"tagPolicy":{"deniedLabels":["scary"]}}')`); err != nil {
		t.Fatal(err)
	}
	rowRated := newPlaybackMovieEntity(f, kids, "kids-row-pg", "Row-rated", 2000)
	f.catalogTest.Fields(rowRated.ID, map[string]any{"content_rating": "PG"})
	scary := newPlaybackMovieEntity(f, kids, "kids-scary", "Scary", 2000)
	f.catalogTest.Attributes(scary.ID, "contentRating", "PG")
	f.catalogTest.Attributes(scary.ID, "label", "Scary")
	f.catalogTest.Drain()
	d := Dependencies{DB: f.db, Identity: f.id}
	for _, token := range []string{member, f.owner.AccessToken} {
		p, err := f.id.Authenticate(token)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		allowed := func(library string) error { return d.allowedLibraryTx(context.Background(), p, library, tx) }
		clause, args, err := contentaccess.VisibleItemsSQL(context.Background(), tx, p, "e.id", allowed)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(`SELECT pid(e.public_id),cl.library_id,`+clause+` FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 ORDER BY e.id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			id, library string
			set         bool
		}
		all := []row{}
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.library, &r.set); err != nil {
				t.Fatal(err)
			}
			all = append(all, r)
		}
		rows.Close()
		denied := 0
		for _, r := range all {
			single := allowed(r.library) == nil && contentaccess.VisibleItemTx(context.Background(), tx, p, r.id) == nil
			if single != r.set {
				t.Fatalf("%s (%s): per-item %v, set-based %v", f.names.Of(r.id), p.AccountID, single, r.set)
			}
			if !single {
				denied++
			}
		}
		_ = tx.Rollback()
		if token == member && denied < 4 {
			t.Fatalf("the member fixture restricts too little to prove anything (%d denied)", denied)
		}
	}
}

// P16: a large selection is scanned outside the queue's write and written in
// bounded background batches. When it can't be used (past the queue's bound)
// nothing it wrote survives: no parked queue, snapshot or chunk, and the
// device's current queue is untouched. Past the bound is 422 queue_too_large,
// for a new queue and for an add.
func TestPlaybackV1QueueLargeSelectionsAreBoundedAndLeaveNothingBehind(t *testing.T) {
	testtier.Media(t, "large selections projected per test")
	f := newV1Fixture(t, 1)
	const n = 12_000 // 47 chunks: more than one write batch
	collection, _ := createPlaybackCollection(t, f, n, "bulk", "Bulk")
	big := map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", collection.Public)}}}
	var small playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "bound-queue-000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}, 201, &small)
	counts := func() (queues, snapshots, chunks int) {
		if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM queues_v1),(SELECT count(*) FROM queue_v1_snapshots),(SELECT count(*) FROM queue_v1_chunks)`).Scan(&queues, &snapshots, &chunks); err != nil {
			t.Fatal(err)
		}
		return
	}
	q0, s0, c0 := counts()
	f.v1.QueueKeyLimit = 10_000
	if w := f.raw("POST", "/v1/queues", f.owner.AccessToken, map[string]string{"Idempotency-Key": "bound-queue-000002"}, big); w.Code != 422 || v1Code(w) != "queue_too_large" {
		t.Fatalf("past the bound: %d %s", w.Code, w.Body.String())
	}
	if q, sn, c := counts(); q != q0 || sn != s0 || c != c0 {
		t.Fatalf("a refused create left queues %d→%d, snapshots %d→%d, chunks %d→%d", q0, q, s0, sn, c0, c)
	}
	var still playbackv1.QueueView
	f.callAs(f.owner.AccessToken, "GET", "/v1/queues/"+small.Queue.ID, nil, nil, 200, &still)
	if w := f.raw("POST", "/v1/queues/"+small.Queue.ID+"/segments", f.owner.AccessToken, map[string]string{"If-Match": still.Revision}, map[string]any{"placement": "end", "source": containerSource("collection", collection.Public)}); w.Code != 422 || v1Code(w) != "queue_too_large" {
		t.Fatalf("add past the bound: %d %s", w.Code, w.Body.String())
	}
	if q, sn, c := counts(); q != q0 || sn != s0 || c != c0 {
		t.Fatalf("a refused add left snapshots %d→%d, chunks %d→%d (queues %d→%d)", s0, sn, c0, c, q0, q)
	}
	// Within the bound, the batched snapshot is complete and in order.
	f.v1.QueueKeyLimit = 0
	var full playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "bound-queue-000003"}, big, 201, &full)
	var page struct {
		Items []playbackv1.QueueEntry `json:"items"`
	}
	f.callAs(f.owner.AccessToken, "GET", fmt.Sprintf("/v1/queues/%s/entries?from=%d&limit=3", full.Queue.ID, n-3), nil, nil, 200, &page)
	if full.Queue.Total != n || len(page.Items) != 3 || page.Items[2].ItemID != f.names[fmt.Sprintf("bulk%07d", n-1)].Public {
		t.Fatalf("total %d tail %+v", full.Queue.Total, page.Items)
	}
}

// P14: a 200-entry window of a shuffled 20k queue reads its chunks, items and
// visibility in a constant number of statements, not one per entry or chunk.
func TestPlaybackV1QueueWindowStatementsAreConstant(t *testing.T) {
	testtier.Media(t, "a large collection projected per test")
	f := newV1Fixture(t, 1)
	const n = 20_000
	collection, _ := createPlaybackCollection(t, f, n, "bulk", "Bulk")
	var q playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "window-cost-000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", collection.Public), "order": map[string]any{"mode": "shuffle", "seed": "7"}}}}, 201, &q)
	p, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx, statements := dbwork.TraceStatements(context.Background())
	page, err := f.v1.Entries(ctx, playbackv1.Caller{Principal: p, DeviceID: "any"}, q.Queue.ID, 5000, playbackv1.WindowMax)
	if err != nil || len(page.Items) != playbackv1.WindowMax {
		t.Fatalf("entries: %v (%d)", err, len(page.Items))
	}
	got := len(statements())
	if got > 40 {
		t.Fatalf("a shuffled 200-entry window took %d statements:\n%s", got, strings.Join(statements(), "\n"))
	}
	t.Logf("a shuffled 200-entry window: %d statements", got)
}

// P17: a queue belongs to its profile. Another profile signed in on the same
// device can neither read nor control it.
func TestPlaybackV1QueueIsNotSharedAcrossProfilesOnOneDevice(t *testing.T) {
	f := newV1Fixture(t, 1)
	var q playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "profile-queue-000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}, 201, &q)
	owner, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	other := owner
	other.ProfileID = "another-profile"
	var device string
	if err = f.db.QueryRow(`SELECT owner_id FROM queues_v1 WHERE id=?`, q.Queue.ID).Scan(&device); err != nil {
		t.Fatal(err)
	}
	if _, err = f.v1.Queue(context.Background(), playbackv1.Caller{Principal: other, DeviceID: device}, q.Queue.ID); !errors.Is(err, playbackv1.ErrNotFound) {
		t.Fatalf("another profile on the same device read the queue: %v", err)
	}
	if _, err = f.v1.Queue(context.Background(), playbackv1.Caller{Principal: owner, DeviceID: device}, q.Queue.ID); err != nil {
		t.Fatalf("its own profile: %v", err)
	}
}

// P15: editing a queue doesn't grow what every request reads. Moving an entry
// away and back merges its ranges again, removing a whole added segment drops
// it, entry ids and order are unchanged throughout, and the header lists at
// most HeaderSegments segments (with the count of all of them).
func TestPlaybackV1QueueStructureStaysBounded(t *testing.T) {
	f := newV1Fixture(t, 3)
	var q playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "compact-queue-00001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}}}, 201, &q)
	base := "/v1/queues/" + q.Queue.ID
	rev := q.Queue.Revision
	entries := func() []playbackv1.QueueEntry {
		var page struct {
			Items []playbackv1.QueueEntry `json:"items"`
		}
		f.callAs(f.owner.AccessToken, "GET", base+"/entries?limit=200", nil, nil, 200, &page)
		return page.Items
	}
	var reply playbackv1.QueueReply
	for i := 0; i < 20; i++ {
		e := entries()
		// The first entry to the end, then the (new) last entry back before the first.
		f.callAs(f.owner.AccessToken, "POST", base+"/entries/"+e[0].EntryID+":move", map[string]string{"If-Match": rev}, map[string]any{"after": e[2].EntryID}, 200, &reply)
		rev = reply.Queue.Revision
		e = entries()
		f.callAs(f.owner.AccessToken, "POST", base+"/entries/"+e[2].EntryID+":move", map[string]string{"If-Match": rev}, map[string]any{"before": e[0].EntryID}, 200, &reply)
		rev = reply.Queue.Revision
	}
	if got := reply.Queue.SegmentCount; got > 4 || reply.Queue.Total != 3 {
		t.Fatalf("after 40 moves: %d segments, total %d", got, reply.Queue.Total)
	}
	for i, e := range entries() {
		if e.ItemID != f.items[i] {
			t.Fatalf("order after moving away and back: %+v", entries())
		}
	}
	// Add 70 one-item segments at the end; then remove the last one entirely.
	for i := 0; i < 70; i++ {
		f.callAs(f.owner.AccessToken, "POST", base+"/segments", map[string]string{"If-Match": rev}, map[string]any{"placement": "end", "source": queueItems(f.items[i%3])}, 200, &reply)
		rev = reply.Queue.Revision
	}
	if len(reply.Queue.Segments) != playbackv1.HeaderSegments || reply.Queue.SegmentCount < 70 {
		t.Fatalf("header lists %d of %d segments", len(reply.Queue.Segments), reply.Queue.SegmentCount)
	}
	before := reply.Queue.SegmentCount
	all := entries()
	f.callAs(f.owner.AccessToken, "DELETE", base+"/entries/"+all[len(all)-1].EntryID, map[string]string{"If-Match": rev}, nil, 200, &reply)
	var leftovers int
	if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_removals r JOIN queue_v1_snapshots s ON s.id=r.snapshot_id WHERE s.queue_id=?`, q.Queue.ID).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if reply.Queue.SegmentCount != before-1 || leftovers != 0 {
		t.Fatalf("a fully removed segment stays: %d → %d segments, %d tombstones", before, reply.Queue.SegmentCount, leftovers)
	}
}

// P28: past the tombstone bound, removals keep working: the most-edited
// segment is rewritten into a fresh snapshot of its live keys, order and
// positions unchanged, and the tombstones are gone.
func TestPlaybackV1QueueRemovalsCompactInsteadOfRefusing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 1)
	ids := bigCollection(t, f, 400)
	var q playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "compact-tomb-000001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("collection", f.names["big"].Public)}}}, 201, &q)
	restore := playbackv1.MaxQueueRemovalsForTest(50)
	defer restore()
	base := "/v1/queues/" + q.Queue.ID
	rev := q.Queue.Revision
	var page struct {
		Items []playbackv1.QueueEntry `json:"items"`
	}
	var reply playbackv1.QueueReply
	// Remove every other entry among the first 200: 100 removals, twice the bound.
	for i := 0; i < 100; i++ {
		f.callAs(f.owner.AccessToken, "GET", base+"/entries?from="+fmt.Sprint(i+1)+"&limit=1", nil, nil, 200, &page)
		f.callAs(f.owner.AccessToken, "DELETE", base+"/entries/"+page.Items[0].EntryID, map[string]string{"If-Match": rev}, nil, 200, &reply)
		rev = reply.Queue.Revision
	}
	var tombstones int
	if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_removals r JOIN queue_v1_snapshots s ON s.id=r.snapshot_id WHERE s.queue_id=?`, q.Queue.ID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if reply.Queue.Total != 300 || tombstones > 50 {
		t.Fatalf("total %d, %d tombstones", reply.Queue.Total, tombstones)
	}
	want := []string{}
	for i, id := range ids {
		if i >= 200 || i%2 == 0 {
			want = append(want, id)
		}
	}
	got := queueItemsInOrder(t, f, f.owner.AccessToken, q.Queue.ID, 300)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: %s, want %s", i, got[i], want[i])
		}
	}
}

// P27: a large create that crashed after staging its queue leaves a
// "~<device>" row; it's swept (with its snapshots) once it's old.
func TestPlaybackV1StagedQueuesAreSwept(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 1)
	old := time.Now().Add(-time.Hour).UnixMilli()
	for _, q := range []string{
		`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_stale','device','~dev','local','a','p',1,` + fmt.Sprint(old) + `,` + fmt.Sprint(old) + `)`,
		`INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width) VALUES('s_stale','q_stale','items','{}','',0,0)`,
		`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_fresh','device','~dev2','local','a','p',1,` + fmt.Sprint(time.Now().UnixMilli()) + `,0)`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var created playbackv1.QueueReply
	f.callAs(f.owner.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "sweep-queue-000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items[0])}}}, 201, &created)
	// The sweep runs in the background after the create.
	var stale, snaps, fresh int
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		_ = f.db.QueryRow(`SELECT (SELECT count(*) FROM queues_v1 WHERE id='q_stale'),(SELECT count(*) FROM queue_v1_snapshots WHERE id='s_stale'),(SELECT count(*) FROM queues_v1 WHERE id='q_fresh')`).Scan(&stale, &snaps, &fresh)
		if stale == 0 || time.Now().After(deadline) {
			break
		}
	}
	if stale != 0 || snaps != 0 || fresh != 1 {
		t.Fatalf("stale %d (snapshots %d), fresh %d", stale, snaps, fresh)
	}
}

func TestPlaybackV1QueueWindowDisplayFactsAndRestrictionRedaction(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "display-facts-00000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(ids["mixed-e1"], ids["kids-album-t1"], ids["film"])}}}, 201, &q)
	if len(q.Window) != 3 || q.Queue.RemainingCount != 2 {
		t.Fatalf("window/count %+v", q)
	}
	ep, song := q.Window[0], q.Window[1]
	if ep.SegmentID == "" || ep.ShowID != ids["mixed"] || ep.SeasonID == "" || ep.Episode == nil || ep.Episode.SeasonNumber != 1 || ep.Episode.Number != 1 {
		t.Fatalf("episode %+v", ep)
	}
	if song.AlbumID != ids["kids-album"] || song.Album != "kids-album" || song.ArtistID != ids["band"] || song.Artist != "The Band" || song.SegmentID != ep.SegmentID {
		t.Fatalf("song %+v", song)
	}
	f.catalogTest.Attributes(f.catalogTest.ID(ids["mixed-e1"]), "contentRating", "R")
	f.catalogTest.Drain()
	var page playbackv1.EntryPage
	f.callAs(member, "GET", "/v1/queues/"+q.Queue.ID+"/entries?from=0&limit=10", nil, nil, 200, &page)
	hidden := page.Items[0]
	if hidden.Available || hidden.ItemID != "" || hidden.Title != "" || hidden.ShowID != "" || hidden.SeasonID != "" || hidden.Episode != nil || hidden.BackdropURL != "" || hidden.DurationMs != 0 {
		t.Fatalf("restricted facts leaked: %+v", hidden)
	}
}

func TestPlaybackV1BatchQueuePreservesMixedOrderAndFences(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "batch-mixed-000001"}, map[string]any{"selector": queueItems(ids["film"])}, 201, &q)
	path := "/v1/queues/" + q.Queue.ID + "/batch"
	request := map[string]any{"placement": "next", "source": queueItems(ids["kids-album-t1"], ids["restricted"], f.items[0], ids["film"])}
	old := q.Queue.Revision
	f.callAs(member, "POST", path, map[string]string{"If-Match": old}, request, 200, &q)
	var page playbackv1.EntryPage
	f.callAs(member, "GET", "/v1/queues/"+q.Queue.ID+"/entries?from=0&limit=20", nil, nil, 200, &page)
	if len(page.Items) != 3 || page.Items[1].ItemID != ids["kids-album-t1"] || page.Items[2].ItemID != ids["film"] {
		t.Fatalf("mixed order: %+v", page.Items)
	}
	f.callAs(member, "POST", path, map[string]string{"If-Match": old}, request, 412, nil)
	f.callAs(member, "POST", path, nil, request, 428, nil)
	f.call("POST", path, map[string]string{"If-Match": q.Queue.Revision}, request, 404, nil)
	f.callAs(member, "POST", path, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"placement": "end", "source": queueItems(ids["restricted"])}, 404, nil)
	var after playbackv1.QueueView
	f.callAs(member, "GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &after)
	if after.Total != q.Queue.Total || after.Revision != q.Queue.Revision {
		t.Fatal("refused command changed queue")
	}
}

func TestPlaybackV1QueueDurationCoversBeyondWindowAndInvalidatesOnFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 201)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "duration-all-000001"}, map[string]any{"selector": queueItems(f.items...)}, 201, &q)
	if q.Queue.TotalDurationMs == nil || *q.Queue.TotalDurationMs != 201*600000 || !q.Queue.DurationKnown || q.Queue.RemainingCount != 200 {
		t.Fatalf("whole queue: %+v", q.Queue)
	}
	if _, err := f.db.Exec(`UPDATE catalog_assets SET duration=120 WHERE id=?`, f.records[200].Asset); err != nil {
		t.Fatal(err)
	}
	var after playbackv1.QueueView
	f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &after)
	if after.TotalDurationMs == nil || *after.TotalDurationMs != 200*600000+120000 {
		t.Fatalf("stale summary %+v", after)
	}
	// A second acceptance before source delivery must not reuse the summary
	// recomputed under the old library revision. Ordinary maintenance then
	// publishes a new revision and restores the memoized path.
	if _, err := f.db.Exec(`UPDATE catalog_assets SET duration=180 WHERE id=?`, f.records[200].Asset); err != nil {
		t.Fatal(err)
	}
	f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &after)
	if after.TotalDurationMs == nil || *after.TotalDurationMs != 200*600000+180000 {
		t.Fatalf("repeated source acceptance reused stale summary %+v", after)
	}
	f.catalogTest.Drain()
	f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &after)
	if after.TotalDurationMs == nil || *after.TotalDurationMs != 200*600000+180000 {
		t.Fatalf("settled summary %+v", after)
	}
}

func TestPlaybackV1WholeShowQueueResolvesAnotherSeasonWithCurrentAuthority(t *testing.T) {
	f, member, ids := queueAccessFixture(t)
	show := f.names["mixed"]
	season := f.catalogTest.Season(show, 2)
	episode := f.catalogTest.Episode(show, season, 1, filepath.Join(f.root, "kids", "season-two.mp4"))
	f.catalogTest.Attributes(episode.ID, "contentRating", "PG")
	f.catalogTest.Drain()
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "show-episodes-00001"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("show", ids["mixed"]), "anchor": map[string]any{"itemId": ids["mixed-e1"]}}}}, 201, &q)
	var found playbackv1.QueueItemView
	path := "/v1/queues/" + q.Queue.ID + "/entry?itemId=" + episode.Public
	f.callAs(member, "GET", path, nil, nil, 200, &found)
	if found.Entry.ItemID != episode.Public || found.Entry.Episode == nil || found.Entry.Episode.SeasonNumber != 2 || found.Revision != q.Queue.Revision {
		t.Fatalf("season jump %+v", found)
	}
	f.callAs(member, "GET", "/v1/queues/"+q.Queue.ID+"/entry?itemId="+ids["restricted"], nil, nil, 404, nil)
	f.call("GET", path, nil, nil, 404, nil)
	f.catalogTest.Attributes(episode.ID, "contentRating", "R")
	f.catalogTest.Drain()
	f.callAs(member, "GET", path, nil, nil, 404, nil)
}

func TestPlaybackV1RemoteReportUsesGenuineNextAndRechecksRestriction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, member, ids := queueAccessFixture(t)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "remote-next-000001"}, map[string]any{"selector": queueItems(ids["film"], ids["mixed-e1"]), "startPlayback": map[string]any{"state": "paused"}}, 201, &q)
	var report playbackv1.RemoteControlsDocument
	f.callAs(member, "GET", "/v1/playback/controls", nil, nil, 200, &report)
	if len(report.Items) != 1 || report.Items[0].Next == nil || report.Items[0].Next.ItemID != ids["mixed-e1"] || report.Items[0].Next.Episode == nil || report.Items[0].DurationMs != 600000 {
		t.Fatalf("report %+v", report)
	}
	f.catalogTest.Attributes(f.names["mixed-e1"].ID, "contentRating", "R")
	f.catalogTest.Drain()
	report = playbackv1.RemoteControlsDocument{}
	w := f.callAs(member, "GET", "/v1/playback/controls", nil, nil, 200, &report)
	if len(report.Items) != 1 || report.Items[0].Next != nil || strings.Contains(w.Body.String(), ids["mixed-e1"]) {
		t.Fatalf("restricted successor leaked %s", w.Body.String())
	}
}
