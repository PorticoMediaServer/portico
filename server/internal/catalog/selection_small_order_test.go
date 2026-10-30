package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func smallOrderBasis(t *testing.T, s *Service, library, profile string) (string, SelectionBatchWitness) {
	t.Helper()
	witness := func(read *Service) (string, error) {
		return read.browseSelectionWitness(library, profile, Viewer{}.EffectiveRestrictions())
	}
	var basis string
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error { var err error; basis, err = witness(s.WithContext(ctx)); return err }); err != nil {
		t.Fatal(err)
	}
	return basis, witness
}
func finishSmallOrderTest(t *testing.T, s *Service, p *SmallSelectionOrder, witness SelectionBatchWitness, limits SmallSelectionOrderLimits) ([]int64, int) {
	t.Helper()
	units := 0
	for turns := 0; !p.Complete(); turns++ {
		if turns > 30000 {
			t.Fatal("small order did not reach EOF")
		}
		before := p.clone()
		next, work, err := s.StepSmallSelectionOrder(context.Background(), p, witness, limits)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p, before) {
			t.Fatal("step mutated its input")
		}
		if limits.Rows > 0 && work.Rows > limits.Rows {
			t.Fatalf("raw turn escaped row bound: %+v", work)
		}
		if work.Bytes > SelectionBatchMaximumBytes {
			t.Fatalf("retained state escaped cap: %+v", work)
		}
		units += work.Rows
		p = next
	}
	ids, err := p.IDs()
	if err != nil {
		t.Fatal(err)
	}
	return ids, units
}

func TestSmallGroupOrderMatchesRealSource(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	a := c.Movie(movies, "/m/a.mkv", "Alpha", 2001)
	b := c.Movie(movies, "/m/b.mkv", "Bravo", 2002)
	d := c.Movie(movies, "/m/d.mkv", "Delta", 2003)
	collection := c.Collection(movies, "Shelf", d, a, b)
	tv := c.Library("t", "TV", "tv", "/t")
	show := c.Show(tv, "Show", 2000)
	special := c.Season(show, 0)
	one := c.Season(show, 1)
	e1 := c.Episode(show, one, 2, "/t/e2.mkv")
	e2 := c.Episode(show, special, 1, "/t/s.mkv")
	e3 := c.Episode(show, one, 1, "/t/e1.mkv")
	music := c.Library("u", "Music", "music", "/u")
	artist := c.Artist(music, "Band")
	album := c.Album(artist, "Record", 2001)
	s1 := c.Song(album, 2, "/u/2.flac", "Second")
	s2 := c.Song(album, 1, "/u/1.flac", "First")
	books := c.Library("b", "Books", "audiobook", "/b")
	book := c.Book(books, "Saga", "Writer")
	p1 := c.BookFile(book, 2, "/b/2.m4b")
	p2 := c.BookFile(book, 1, "/b/1.m4b")
	c.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES('mine','local','a','p','Mine','op1','hash','now'),('other','local','a','p','Other','op2','hash','now')`)
	for i, item := range []catalogtest.Item{b, a, b, d} {
		c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,id,?,?,? FROM catalog_playlists WHERE token='mine'`, fmt.Sprintf("mine%d", i), item.ID, i+1, fmt.Sprintf("k%03d", i))
	}
	// Many entries for the same item in another list must be raw charged
	// rejects, not disappear behind a matching-output LIMIT.
	for i := 0; i < 17; i++ {
		c.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,id,?,?,? FROM catalog_playlists WHERE token='other'`, fmt.Sprintf("other%d", i), b.ID, i+1, "outside")
	}
	c.Drain()
	s := New(c.DB)
	var authorID int64
	var author string
	if err := c.DB.QueryRow(`SELECT e.id,pid(e.public_id) FROM catalog_entities e JOIN catalog_browse_memberships m ON m.entity_id=e.id WHERE e.kind=? AND m.item_id=? LIMIT 1`, int(compactcatalog.Author), p1.ID).Scan(&authorID, &author); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, library string
		target        BrowseNode
		ids           []int64
	}{
		{"collection", "m", BrowseNode{Field: "collection", Operator: "equals", Value: collection.Public}, []int64{a.ID, b.ID, d.ID}},
		{"list", "m", BrowseNode{Field: "list", Operator: "equals", Value: "mine"}, []int64{a.ID, b.ID, d.ID}},
		{"show", "t", BrowseNode{Field: "memberOf", Operator: "equals", Value: show.Public}, []int64{e1.ID, e2.ID, e3.ID}},
		{"season", "t", BrowseNode{Field: "memberOf", Operator: "equals", Value: one.Public}, []int64{e1.ID, e3.ID}},
		{"episodeGroup", "t", BrowseNode{Field: "episodeGroup", Operator: "equals", Value: show.Public + "/all"}, []int64{e1.ID, e2.ID, e3.ID}},
		{"album", "u", BrowseNode{Field: "memberOf", Operator: "equals", Value: album.Public}, []int64{s1.ID, s2.ID}},
		{"artist", "u", BrowseNode{Field: "memberOf", Operator: "equals", Value: artist.Public}, []int64{s1.ID, s2.ID}},
		{"book", "b", BrowseNode{Field: "memberOf", Operator: "equals", Value: book.Public}, []int64{p1.ID, p2.ID}},
		{"author", "b", BrowseNode{Field: "memberOf", Operator: "equals", Value: author}, []int64{p1.ID, p2.ID}},
		// Both show and season take the same first source member's place;
		// equal minima must tie by displayed identity, not input order.
		{"upward-minimum", "t", BrowseNode{Field: "memberOf", Operator: "equals", Value: show.Public}, []int64{one.ID, show.ID}},
	}
	for _, tc := range cases {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(tc.name+"/"+direction, func(t *testing.T) {
				basis, witness := smallOrderBasis(t, s, tc.library, "p")
				p, err := NewSmallGroupOrder(tc.ids, &tc.target, direction, basis)
				if err != nil {
					t.Fatal(err)
				}
				got, units := finishSmallOrderTest(t, s, p, witness, SmallSelectionOrderLimits{Rows: 1, Bytes: 128})
				var want []int64
				err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
					read := s.WithContext(ctx)
					source, args, err := read.groupOrderSource(&tc.target, nil)
					if err != nil {
						return err
					}
					raw, _ := json.Marshal(tc.ids)
					args = append(args, string(raw))
					rows, err := read.read().Query(`SELECT id FROM (`+source+`) WHERE id IN (SELECT value FROM json_each(?)) ORDER BY pos `+direction+`,id `+direction, args...)
					if err != nil {
						return err
					}
					defer rows.Close()
					for rows.Next() {
						var id int64
						if err = rows.Scan(&id); err != nil {
							return err
						}
						want = append(want, id)
					}
					return rows.Err()
				})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v, real source %v", got, want)
				}
				if tc.name == "list" && units < 17 {
					t.Fatalf("unrelated raw entries disappeared: %d units", units)
				}
			})
		}
	}
}

func TestSmallForYouMatchesBrowseWithExactOverlays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	now := time.Now()
	w.s.Clock = func() time.Time { return now }
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	check := func(t *testing.T) {
		t.Helper()
		oracle, err := w.s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "forYou", Direction: "desc"}}, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, 0, len(oracle.Entries))
		for _, entry := range oracle.Entries {
			ids = append(ids, w.c.ID(entry.ID))
		}
		// Deliberately detach input order from the oracle's rank.
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
		basis, witness := smallOrderBasis(t, w.s, "m", "p")
		p, err := NewSmallForYouOrder(ids, "p", "m", basis, now)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.BindForYouMemoScope(*oracle.Bound, viewer); err != nil {
			t.Fatal(err)
		}
		got, _ := finishSmallOrderTest(t, w.s, p, witness, SmallSelectionOrderLimits{Rows: 7, Bytes: 1024})
		var want []int64
		for _, entry := range oracle.Entries {
			want = append(want, w.c.ID(entry.ID))
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v\nBrowse %v", got, want)
		}
	}
	t.Run("cold", check)
	for _, film := range w.scifi[:3] {
		w.watch("p", film)
	}
	w.personal("p", w.romance[0], "not_interested", 1)
	w.c.Drain()
	t.Run("stored-watched-hidden", check)
	w.personal("p", w.scifi[0], "favorite", 1)
	w.personal("p", w.romance[0], "not_interested", 0)
	w.personal("p", w.dramas[0], "rating", 1)
	t.Run("pending-retraction", check)
	w.c.Drain()
	// Profile taste outside the selected films must still affect the norm.
	w.c.Exec(`INSERT INTO rec_profile_taste(profile_id,facet,long,short) VALUES('p','g:unshared',100,2)`)
	w.c.Exec(`UPDATE rec_profile_revisions SET revision=revision+1 WHERE profile_id='p'`)
	old := now.AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano)
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'pb')`, w.others[0].ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, w.others[0].ID, old)
	t.Run("unshared-taste-and-abandonment", check)
	w.c.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, w.dramas[1].ID)
	w.c.Exec(`UPDATE catalog_browse_rows SET available=0 WHERE entity_id=?`, w.dramas[1].ID)
	// The 257th job causes the same deliberate backlog exclusion as the
	// production engine, without hydrating the profile's signal history.
	w.c.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<257) INSERT OR IGNORE INTO rec_profile_jobs(profile_id,work_id) SELECT 'p',900000000+i FROM ids`)
	t.Run("bulk-backlog-and-unavailable-rest", check)
}

func TestSmallGroupOrderRetainedMemberRejects(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	a := c.Movie(lib, "/m/a.mkv", "A", 2000)
	shelf := c.Collection(lib, "Shelf", a)
	c.Drain()
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	for i := 1; i <= 23; i++ {
		c.Exec(`INSERT INTO catalog_browse_memberships_previous(generation,present,entity_id,item_id,source) SELECT generation,0,?,?,0 FROM catalog_generations`, a.ID, a.ID+int64(i))
	}
	s := New(c.DB)
	basis, witness := smallOrderBasis(t, s, "m", "p")
	p, err := NewSmallGroupOrder([]int64{a.ID}, &BrowseNode{Field: "collection", Operator: "equals", Value: shelf.Public}, "asc", basis)
	if err != nil {
		t.Fatal(err)
	}
	got, units := finishSmallOrderTest(t, s, p, witness, SmallSelectionOrderLimits{Rows: 1, Bytes: 64})
	if !reflect.DeepEqual(got, []int64{a.ID}) || units < 23*3 {
		t.Fatalf("retained rejects not charged: %v / %d units", got, units)
	}
}

func TestSmallOrderRawSourcePlans(t *testing.T) {
	c := catalogtest.Open(t)
	for _, query := range []string{
		`SELECT facet FROM rec_profile_taste WHERE profile_id='p' AND facet>'a' ORDER BY facet LIMIT 1`,
		`SELECT facet FROM catalog_rec_postings INDEXED BY catalog_rec_postings_entity WHERE entity_id=1 AND (facet,library_id,quality)>('a',1,0) ORDER BY facet,library_id,quality LIMIT 1`,
		`SELECT id,playlist_id FROM catalog_playlist_entries INDEXED BY catalog_playlist_entries_item WHERE item_id=1 AND id>0 ORDER BY id LIMIT 1`,
		`SELECT rowid FROM personal_history INDEXED BY personal_history_item_id_fk WHERE item_id=1 AND rowid>0 ORDER BY rowid LIMIT 1`,
		`SELECT song_id FROM catalog_song_artists INDEXED BY catalog_song_artists_artist WHERE artist_id=1 AND song_id>0 ORDER BY song_id LIMIT 1`,
	} {
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN ` + query)
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
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "SEARCH ") {
			t.Fatalf("unbounded raw key source %s:\n%s", query, plan)
		}
	}
}

func TestSmallOrderNoCaseMatchesSQLite(t *testing.T) {
	c := catalogtest.Open(t)
	for _, a := range []string{"A", "a", "a\x00x", "a\x00y", "a\x00xx", "a\x00", "a b", "Ä", "ä"} {
		for _, b := range []string{"a", "a\x00x", "a\x00y", "a\x00", "a b", "Ä", "ä"} {
			var want int
			if err := c.DB.QueryRow(`SELECT CASE WHEN ?1 COLLATE NOCASE<?2 THEN -1 WHEN ?1 COLLATE NOCASE>?2 THEN 1 ELSE 0 END`, a, b).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if got := smallOrderNoCase(a, b); got != want {
				t.Fatalf("NOCASE %q / %q: %d, SQLite %d", a, b, got, want)
			}
		}
	}
}

func TestSmallForYouChargesFullTasteBeforeDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	outside := "g:" + strings.Repeat("unshared", 150)
	w.c.Exec(`INSERT INTO rec_profile_taste(profile_id,facet,long,short) VALUES('p',?,1,0)`, outside)
	w.c.Exec(`UPDATE rec_profile_revisions SET revision=revision+1 WHERE profile_id='p'`)
	basis, witness := smallOrderBasis(t, w.s, "m", "p")
	p, err := NewSmallForYouOrder([]int64{w.scifi[1].ID, w.scifi[2].ID}, "p", "m", basis, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saw := false
	for !p.Complete() {
		before := p.clone()
		next, work, err := w.s.StepSmallSelectionOrder(context.Background(), p, witness, SmallSelectionOrderLimits{Rows: 4, Bytes: 256, ReserveTransient: func(n int64) error {
			if n < 0 {
				t.Fatal("negative reservation")
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if work.TransientScalarBytes >= len(outside) {
			saw = true
			if work.Rows > 4 || work.DecodedBytes < len(outside) || next.Bytes() <= before.Bytes() {
				t.Fatalf("unshared source bytes omitted: %+v", work)
			}
			sentinel := errors.New("reservation refused")
			_, _, err = w.s.StepSmallSelectionOrder(context.Background(), p, witness, SmallSelectionOrderLimits{Rows: 4, Bytes: 256, ReserveTransient: func(n int64) error {
				if n > int64(2*p.Bytes()+65536) {
					return sentinel
				}
				return nil
			}})
			if !errors.Is(err, sentinel) {
				t.Fatalf("source allocation preceded reservation: %v", err)
			}
			if !reflect.DeepEqual(p, before) {
				t.Fatal("failed admission changed input")
			}
		}
		p = next
	}
	if !saw {
		t.Fatal("full profile taste preload escaped byte accounting")
	}
}

func TestSmallSignalMatchesWorkerOracle(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("t", "TV", "tv", "/t")
	show := c.Show(lib, "Show", 2000)
	season := c.Season(show, 1)
	e1 := c.Episode(show, season, 2, "/t/2.mkv")
	e2 := c.Episode(show, season, 1, "/t/1.mkv")
	now := time.Now()
	at := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	for i, item := range []catalogtest.Item{e1, e1, e2} {
		c.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,'p',?,?,?,1,1,0,1)`, fmt.Sprintf("mine%d", i), item.ID, at, at)
	}
	for i := 0; i < 19; i++ {
		c.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,'other',?,?,?,1,1,0,1)`, fmt.Sprintf("other%d", i), e1.ID, at, at)
	}
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,rating,revision) VALUES('p',?,1,9,1)`, e2.ID)
	c.Drain()
	s := New(c.DB)
	err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		read := s.WithContext(ctx)
		r := smallRecSignal{work: show.ID, now: now}
		units := 0
		for {
			done, err := read.smallRecSignalUnit("p", &r, func(int64) error { return nil })
			if err != nil {
				return err
			}
			units++
			if done {
				break
			}
			if units > 10000 {
				t.Fatal("no signal EOF")
			}
		}
		want, err := compactcatalog.RecSignalFor(ctx, read.read(), "p", show.ID, now)
		if err != nil {
			return err
		}
		if got := r.result(); got != want {
			t.Fatalf("got %+v, worker %+v", got, want)
		}
		if units < 19 {
			t.Fatal("foreign history not charged")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSmallOrderCapacityDriftAndScalarYield(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	a := c.Movie(lib, "/m/a.mkv", "A", 2000)
	shelf := c.Collection(lib, "Shelf", a)
	c.Drain()
	s := New(c.DB)
	c.Exec(`UPDATE catalog_collection_members SET order_key=? WHERE collection_id=?`, strings.Repeat("x", 700), shelf.ID)
	basis, witness := smallOrderBasis(t, s, "m", "p")
	p, err := NewSmallGroupOrder([]int64{a.ID}, &BrowseNode{Field: "collection", Operator: "equals", Value: shelf.Public}, "asc", basis)
	if err != nil {
		t.Fatal(err)
	}
	initial := p.clone()
	sawScalar := false
	for !p.Complete() {
		next, work, err := s.StepSmallSelectionOrder(context.Background(), p, witness, SmallSelectionOrderLimits{Rows: 1, Bytes: 64})
		if err != nil {
			t.Fatal(err)
		}
		if work.DecodedBytes >= 700 {
			sawScalar = true
			if work.Rows != 1 {
				t.Fatal("next unit ran after oversize scalar")
			}
		}
		p = next
	}
	if !sawScalar {
		t.Fatal("source scalar was not accounted")
	}
	_, _, err = s.StepSmallSelectionOrder(context.Background(), initial, witness, SmallSelectionOrderLimits{RetainedBytes: initial.Bytes() - 1})
	if !errors.Is(err, ErrSelectionBatchCapacity) {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_collection_members SET order_key='changed' WHERE collection_id=?`, shelf.ID)
	for _, state := range []*SmallSelectionOrder{initial, p} {
		_, _, err = s.StepSmallSelectionOrder(context.Background(), state, witness, SmallSelectionOrderLimits{})
		if !errors.Is(err, ErrSelectionBatchStale) {
			t.Fatalf("drift accepted: %v", err)
		}
	}
	if _, err = NewSmallGroupOrder([]int64{a.ID, a.ID}, &BrowseNode{Field: "collection", Operator: "equals", Value: shelf.Public}, "asc", basis); !errors.Is(err, ErrSelectionBatchUnsupported) {
		t.Fatal(err)
	}
}
