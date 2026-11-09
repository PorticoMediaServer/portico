package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_FILTER_MEMBERSHIP_JOURNAL
func rbMembershipPublicID(id int64) []byte {
	out := make([]byte, 16)
	binary.BigEndian.PutUint64(out[8:], uint64(id))
	return out
}

func newRBFilterMembershipSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	run := func(q string, args ...any) {
		t.Helper()
		if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	run(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,0),(9,'audiobook_file',1,0,1,1,1),(10,'collection',0,1,1,1,0)`)
	// The compact membership, episode and song families are writer-side
	// derived tables; the read contract source owns only the canonical
	// catalogue. The real-shape fixture installs the exact baseline shapes
	// these readers consume.
	run(`CREATE TABLE catalog_episodes(entity_id INTEGER PRIMARY KEY REFERENCES catalog_entities(id) ON DELETE CASCADE,show_id INTEGER NOT NULL REFERENCES catalog_entities(id),season_id INTEGER REFERENCES catalog_entities(id),numbering TEXT NOT NULL,number INTEGER NOT NULL,air_date INTEGER)`)
	run(`CREATE TABLE catalog_seasons(entity_id INTEGER PRIMARY KEY REFERENCES catalog_entities(id) ON DELETE CASCADE,show_id INTEGER NOT NULL REFERENCES catalog_entities(id),number INTEGER NOT NULL)`)
	run(`CREATE TABLE catalog_albums(entity_id INTEGER PRIMARY KEY REFERENCES catalog_entities(id) ON DELETE CASCADE,artist_id INTEGER NOT NULL REFERENCES catalog_entities(id),local_key TEXT NOT NULL)`)
	run(`CREATE TABLE catalog_songs(entity_id INTEGER PRIMARY KEY REFERENCES catalog_entities(id) ON DELETE CASCADE,album_id INTEGER NOT NULL REFERENCES catalog_entities(id),disc_number INTEGER,track_number INTEGER)`)
	run(`CREATE TABLE catalog_browse_memberships(entity_id INTEGER NOT NULL REFERENCES catalog_entities(id) ON DELETE CASCADE,item_id INTEGER NOT NULL REFERENCES catalog_entities(id) ON DELETE CASCADE,source INTEGER NOT NULL CHECK(source BETWEEN 0 AND 8),PRIMARY KEY(entity_id,item_id,source)) WITHOUT ROWID, STRICT`)
	run(`CREATE INDEX catalog_browse_membership_item ON catalog_browse_memberships(item_id,entity_id)`)

	run(`DELETE FROM catalog_entities WHERE id>6`)
	run(`DELETE FROM catalog_subjects WHERE id>6`)
	for id, title := range map[int64]string{1: "alpha", 2: "bravo", 3: "charlie", 4: "delta", 5: "echo", 6: "foxtrot"} {
		run(`UPDATE catalog_entities SET title=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=?,retired=CASE WHEN ?=6 THEN 1 ELSE 0 END,admission_own_age=NULL WHERE id=?`, title, title, title, title, title, id, id)
	}

	type entity struct {
		id            int64
		kind          int64
		title         string
		show, season  any
		seasonNumber  any
		numbering     any
		episodeNumber any
		album, artist any
	}
	entities := []entity{
		{id: 7, kind: 2, title: "show one"},
		{id: 8, kind: 3, title: "season one", show: int64(7), seasonNumber: int64(1)},
		{id: 9, kind: 4, title: "episode one", show: int64(7), season: int64(8), numbering: "seasonal", episodeNumber: int64(1)},
		{id: 10, kind: 4, title: "episode two", show: int64(7), season: int64(8), numbering: "seasonal", episodeNumber: int64(2)},
		// The composite show/season/album/artist references are immediate, so
		// every parent row is inserted before the row that names it.
		{id: 13, kind: 3, title: "specials", show: int64(7), seasonNumber: int64(0)},
		{id: 11, kind: 4, title: "special one", show: int64(7), season: int64(13), numbering: "seasonal", episodeNumber: int64(1)},
		{id: 12, kind: 4, title: "absolute one", show: int64(7), numbering: "absolute", episodeNumber: int64(1)},
		{id: 17, kind: 5, title: "artist a"},
		{id: 14, kind: 6, title: "album a", artist: int64(17)},
		{id: 15, kind: 7, title: "song a", album: int64(14)},
		{id: 16, kind: 7, title: "song b", album: int64(14)},
		{id: 18, kind: 5, title: "artist b"},
		{id: 20, kind: 10, title: "collection one"},
		{id: 21, kind: 10, title: "collection two"},
	}
	for _, e := range entities {
		public := rbMembershipPublicID(e.id)
		run(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, e.id, public, e.kind)
		run(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,show_id,season_id,season_number,numbering,episode_number,album_id,artist_id) VALUES(?,?,1,?,?,?,?,?,?,0,?,?,0,NULL,?,?,?,?,?,?,?)`,
			e.id, public, e.kind, e.title, e.title, e.title, e.title, e.title, public, public,
			e.show, e.season, e.seasonNumber, e.numbering, e.episodeNumber, e.album, e.artist)
	}
	run(`INSERT INTO catalog_episodes(entity_id,show_id,season_id,numbering,number) VALUES(9,7,8,'seasonal',1),(10,7,8,'seasonal',2),(11,7,13,'seasonal',1),(12,7,NULL,'absolute',1)`)
	run(`INSERT INTO catalog_seasons(entity_id,show_id,number) VALUES(8,7,1),(13,7,0)`)
	run(`INSERT INTO catalog_albums(entity_id,artist_id,local_key) VALUES(14,17,'album-a')`)
	run(`INSERT INTO catalog_songs(entity_id,album_id,disc_number,track_number) VALUES(15,14,1,1),(16,14,1,2)`)
	run(`INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(15,17),(16,18)`)
	// These writer-derived shapes are not part of the read schema binding.
	f.derivedSchema = []string{"catalog_albums", "catalog_browse_memberships", "catalog_episodes", "catalog_seasons", "catalog_songs"}

	// The real writer shape: playable items are their own member (source 0);
	// containers hold their members (sources 1-5, 8). A show and a season have
	// no self row.
	run(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES
(1,1,0),(2,2,0),(9,9,0),(10,10,0),(11,11,0),(12,12,0),(15,15,0),(16,16,0),
(7,9,1),(7,10,1),(7,11,1),(7,12,1),(8,9,2),(8,10,2),(13,11,2),
(14,15,3),(14,16,3),(17,15,4),(17,16,4),(17,15,5),(18,16,5),
(20,1,8),(20,9,8),(20,15,8),(21,2,8),(21,10,8)`)

	run(`INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(20,1,'a'),(20,9,'b'),(20,15,'c'),(21,2,'a'),(21,10,'b')`)

	owner := rbMembershipTestOwnerKey()
	run(`INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES
(1,'list-mine','local','a','p','Mine','op1','h1','now'),
(2,'list-shared','local','b','q','Shared','op2','h2','now'),
(3,'list-other','local','b','q','Other','op3','h3','now')`)
	run(`INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) VALUES
(1,'entry-mine-a',1,1,'a'),(2,'entry-mine-b',1,9,'b'),(3,'entry-shared',2,10,'a'),(4,'entry-other',3,2,'a')`)
	run(`INSERT INTO playlist_shares(playlist_id,authority,account_id,profile_id,role) VALUES('list-shared','local','a','p','viewer')`)

	run(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES
('saved-mine','collection',?,'local','a','p','Mine','now'),
('saved-shared','collection','viewer:other','local','b','q','Shared','now'),
('saved-server','collection','viewer:other','local','b','q','Server','now'),
('saved-other','collection','viewer:other','local','b','q','Other','now'),
('saved-view','view','viewer:other','local','b','q','View','now'),
('saved-show','collection',?,'local','a','p','Show','now'),
('saved-episode','collection',?,'local','a','p','Episode','now')`, owner, owner, owner)
	run(`UPDATE saved_resources SET visibility='server' WHERE id='saved-server'`)
	run(`INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key) VALUES
('e1','saved-mine',1,'a'),('e2','saved-mine',14,'b'),('e3','saved-shared',9,'a'),('e4','saved-server',15,'a'),
('e5','saved-other',2,'a'),('e6','saved-view',1,'a'),('e7','saved-show',7,'a'),('e8','saved-episode',9,'a')`)
	run(`INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('saved-shared','local','a','p','viewer')`)
	return f
}

func rbMembershipTestOwnerKey() string {
	raw, _ := json.Marshal([3]string{"local", "a", "p"})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}

// END_RB_FILTER_MEMBERSHIP_JOURNAL

func rbMembershipFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_filter_membership_test.go")
	if e != nil {
		t.Fatal(e)
	}
	a := strings.Index(string(data), "// BEGIN_RB_FILTER_MEMBERSHIP_JOURNAL\n")
	b := strings.Index(string(data), "// END_RB_FILTER_MEMBERSHIP_JOURNAL\n")
	if a < 0 || b <= a {
		t.Fatal("membership source journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[a:b]...)))
}

func rbMembershipFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBFilterMembershipSource(t), append(browseDefinitions(), RBFilterMembershipDefinitions()...), readfixture.LiveConfig(time.Now), false, rbMembershipFixtureHash(t))
}

func rbMembershipRead(t *testing.T, f *browseFixture, in MembershipFilterInput) (MembershipFilterPage, BrowseReadResult, error) {
	t.Helper()
	return rbMembershipReadWithRows(t, f, in, 2*MaxRaw+10)
}

func rbMembershipReadWithRows(t *testing.T, f *browseFixture, in MembershipFilterInput, rawRows uint32) (MembershipFilterPage, BrowseReadResult, error) {
	t.Helper()
	var page MembershipFilterPage
	var cost BrowseReadResult
	live, request, output := liveRequests("rb-filter-membership")
	request.Budget.RawRows = rawRows
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, cost, e = f.reader.MembershipFilterWindow(ctx, request, time.Second, out, in)
		if e == nil {
			assertLiveUsage(t, f, 1, 0)
		}
		return e
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("membership source retained LIVE custody", u)
	}
	return page, cost, err
}

func rbMembershipTraverse(t *testing.T, f *browseFixture, in MembershipFilterInput) []int64 {
	t.Helper()
	var got []int64
	for n := 0; ; n++ {
		if n > 64 {
			t.Fatal("cursor stalled")
		}
		page, _, err := rbMembershipRead(t, f, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.RawRows > MaxRaw || page.RawPositions > page.RawRows {
			t.Fatal("raw cap", page)
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if page.Exhausted {
			if page.After != nil {
				t.Fatal("EOF retained cursor")
			}
			return got
		}
		if page.After == nil || page.RawPositions == 0 {
			t.Fatal("lost raw continuation", page)
		}
		in.After = page.After
	}
}

func rbMembershipSorted(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func rbMembershipWant(ids ...int64) []int64 { return rbMembershipSorted(ids) }

func rbMembershipMatch(set string, got []int64, want ...int64) (string, bool) {
	sorted, expected := rbMembershipSorted(got), rbMembershipWant(want...)
	return fmt.Sprintf("%s: got %v want %v", set, sorted, expected), reflect.DeepEqual(sorted, expected)
}

// rbMembershipOracle evaluates a plain-SQL clause over the same fixture, in
// the accepted article-aware title order.
func rbMembershipOracle(t *testing.T, f *browseFixture, in BrowseInput, where string, args ...any) []int64 {
	t.Helper()
	key, direction := "sort_key", "ASC"
	if in.KeepArticles {
		key = "sort_key_keep_articles"
	}
	if in.Descending {
		direction = "DESC"
	}
	query := "SELECT e.id FROM (SELECT id,id AS entity_id,library_id,kind,retired,sort_key,sort_key_keep_articles FROM catalog_entities) AS e WHERE e.library_id=? AND e.kind=? AND e.retired=0 AND (" + where + ") ORDER BY e." + key + " COLLATE NOCASE " + direction + ",e.id " + direction
	rows, err := f.conn.QueryContext(t.Context(), query, append([]any{in.Library, in.Kind}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRBFilterMembershipRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		for _, tc := range []struct {
			query string
			err   error
		}{
			{`{"variant":"predicate","field":"homeRow","operator":"equals","value":"row-1"}`, ErrMembershipHomeRowSource},
			{`{"variant":"group","not":{"variant":"predicate","field":"search","operator":"contains","value":"alpha"}}`, ErrMembershipSearchSource},
			{`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAABw/range:0"}`, ErrMembershipEpisodeRange},
		} {
			in := MembershipFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
			in.View.Kind = 4
			page, cost, err := rbMembershipRead(t, f, in)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(page, MembershipFilterPage{}) || cost.PublicationStatements != 0 {
				t.Fatalf("named source refusal %s: %v %v %v", tc.query, page, cost, err)
			}
		}
		in := MembershipFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"title","operator":"equals","value":"Alpha"}}`)}
		page, _, err := rbMembershipRead(t, f, in)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, MembershipFilterPage{}) {
			t.Fatal("unsupported NOT became permission", page, err)
		}
		in = MembershipFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"album","operator":"equals","value":"album a"}`)}
		page, _, err = rbMembershipRead(t, f, in)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, MembershipFilterPage{}) {
			t.Fatal("album on a movie pivot was accepted", page, err)
		}
		// A show and a season are answered through their member rows, not the
		// container's own age or self membership.
		for kind, want := range map[int64]int64{2: 7, 3: 8} {
			in = MembershipFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"}`)}
			in.View.Kind = kind
			page, _, err = rbMembershipRead(t, f, in)
			if err != nil || !page.Exhausted || len(page.Items) != 1 || page.Items[0].ID != want {
				t.Fatalf("container ancestry kind=%d: %v %v", kind, page, err)
			}
		}
	})
}

func rbMembershipVectorKinds(t *testing.T, vector rbMembershipLegacyVector) []int64 {
	t.Helper()
	node, err := parseRBClosedNode(json.RawMessage(vector.Query), rbParseMembershipPredicate)
	if err != nil {
		t.Fatal(err)
	}
	fields := rbMembershipUsedFields(node)
	var out []int64
	for kind := int64(1); kind <= 10; kind++ {
		ok := true
		for field := range fields {
			if !rbMembershipKindApplies(field, kind) {
				ok = false
			}
		}
		if ok {
			out = append(out, kind)
		}
	}
	return out
}

func TestRBFilterMembershipFencesAndBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		in := MembershipFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"}`)}
		in.View.Limit = 1
		first, _, err := rbMembershipRead(t, f, in)
		if err != nil || len(first.Items) != 1 || first.After == nil {
			t.Fatal(first, err)
		}
		in.After = first.After
		in.Query = json.RawMessage(`{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFQ"}`)
		page, _, err := rbMembershipRead(t, f, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, MembershipFilterPage{}) {
			t.Fatal("cursor accepted another query", page, err)
		}
		in.After = nil
		in.Query = json.RawMessage(`{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"}`)
		_, request, _ := liveRequests("membership-bypass")
		page, _, err = f.reader.MembershipFilterWindow(t.Context(), request, time.Second, BrowseOutput{}, in)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, MembershipFilterPage{}) {
			t.Fatal("LIVE bypass", page, err)
		}
		// A revocation between the first and final windows is denied, not
		// answered from the earlier fence.
		f3 := rbMembershipFixture(t)
		f3.reader.snapshots = &browseBoundarySnapshots{Snapshots: f3.snapshots, afterBusiness: func() { f3.exec(t, "UPDATE authorization_session_families SET revoked=1") }}
		in = MembershipFilterInput{View: f3.input, Query: json.RawMessage(`{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"}`)}
		page, _, err = rbMembershipRead(t, f3, in)
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, MembershipFilterPage{}) {
			t.Fatal("revoked page escaped", page, err)
		}
		// A full fact window is incomplete evidence, never a sampled exact
		// answer.
		f2 := rbMembershipFixture(t)
		f2.exec(t, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<2048)
INSERT INTO catalog_playlist_entries(id,token,playlist_id,item_id,order_key) SELECT 1000+i,'cap-'||i,1,1,'cap'||i FROM n`)
		in = MembershipFilterInput{View: f2.input, Query: json.RawMessage(`{"variant":"predicate","field":"list","operator":"equals","value":"list-mine"}`)}
		in.View.Limit = 10
		if _, _, err = rbMembershipReadWithRows(t, f2, in, 4096); !errors.Is(err, ErrNotReady) {
			t.Fatal("full fact window was not refused", err)
		}
	})
}

func TestRBFilterMembershipSharesAndWrongKinds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		cases := []struct {
			kind  int64
			query string
			want  []int64
		}{
			{1, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-mine"}`, []int64{1}},
			{6, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-mine"}`, []int64{14}},
			{2, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-shared"}`, []int64{7}},
			{3, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-shared"}`, []int64{8}},
			{4, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-shared"}`, []int64{9}},
			{6, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-server"}`, []int64{14}},
			{7, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-server"}`, []int64{15}},
			{1, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-other"}`, nil},
			{1, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-view"}`, nil},
			{4, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-show"}`, []int64{9, 10, 11, 12}},
			{2, `{"variant":"predicate","field":"savedCollection","operator":"equals","value":"saved-show"}`, []int64{7}},
			{1, `{"variant":"predicate","field":"list","operator":"equals","value":"list-mine"}`, []int64{1}},
			{2, `{"variant":"predicate","field":"list","operator":"equals","value":"list-mine"}`, []int64{7}},
			{4, `{"variant":"predicate","field":"list","operator":"equals","value":"list-shared"}`, []int64{10}},
			{1, `{"variant":"predicate","field":"list","operator":"equals","value":"list-other"}`, nil},
			{1, `{"variant":"group","any":[{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"},{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFQ"}]}`, []int64{1, 2}},
			{2, `{"variant":"group","any":[{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"},{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFQ"}]}`, []int64{7}},
			{7, `{"variant":"predicate","field":"album","operator":"equals","value":"album a"}`, []int64{15, 16}},
			{7, `{"variant":"predicate","field":"artist","operator":"equals","value":"artist a"}`, []int64{15}},
			{6, `{"variant":"predicate","field":"artist","operator":"equals","value":"artist b"}`, []int64{14}},
			{5, `{"variant":"predicate","field":"album","operator":"equals","value":"album a"}`, []int64{17, 18}},
		}
		for _, tc := range cases {
			in := MembershipFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
			in.View.Kind, in.View.Limit = tc.kind, 10
			if msg, ok := rbMembershipMatch(tc.query, rbMembershipTraverse(t, f, in), tc.want...); !ok {
				t.Fatal(msg)
			}
		}
		// Duplicate reasons never duplicate a page row: a collection may name the
		// same item once, and two selected collections may overlap.
		f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(20,2,'d')`)
		in := MembershipFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"group","any":[{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFA"},{"variant":"predicate","field":"collection","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAAFQ"}]}`)}
		in.View.Kind, in.View.Limit = 1, 10
		if msg, ok := rbMembershipMatch("duplicates", rbMembershipTraverse(t, f, in), 1, 2); !ok {
			t.Fatal(msg)
		}
	})
}

func TestRBFilterMembershipEpisodeGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		for query, want := range map[string][]int64{
			`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAABw/all"}`:                 {9, 10, 11, 12},
			`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAABw/specials"}`:            {11},
			`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAABw/unassigned_absolute"}`: {12},
			`{"variant":"predicate","field":"episodeGroup","operator":"equals","value":"AAAAAAAAAAAAAAAAAAAACA/all"}`:                 nil,
		} {
			in := MembershipFilterInput{View: f.input, Query: json.RawMessage(query)}
			in.View.Kind, in.View.Limit = 4, 10
			if msg, ok := rbMembershipMatch(query, rbMembershipTraverse(t, f, in), want...); !ok {
				t.Fatal(msg)
			}
		}
	})
}

// TestRBFilterMembershipLegacyOracleFrozen executes the frozen public legacy
// compiler's WHERE over this real-shape fixture. The SQL is not a copy of the
// native implementation; TestRBFilterMembershipLegacyOracleVectorsMatchCompiler
// in the catalog package recompiles every vector, so the freeze cannot drift.
func TestRBFilterMembershipLegacyOracleFrozen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbMembershipFixture(t)
		for _, vector := range rbMembershipLegacyVectors(t) {
			for _, kind := range rbMembershipVectorKinds(t, vector) {
				for _, keep := range []bool{false, true} {
					in := MembershipFilterInput{View: f.input, Query: json.RawMessage(vector.Query)}
					in.View.Kind, in.View.KeepArticles, in.View.Limit = kind, keep, 2
					got := rbMembershipTraverse(t, f, in)
					want := rbMembershipLegacyMatches(t, f, in.View, vector.Where, vector.Args)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("legacy oracle differs %s kind=%d keep=%v: native %v legacy %v", vector.Query, kind, keep, got, want)
					}
				}
			}
		}
	})
}
