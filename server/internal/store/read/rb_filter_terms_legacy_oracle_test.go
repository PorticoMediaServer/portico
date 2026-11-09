package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// rbTermsLegacyVector is one query's frozen output from the public legacy
// compiler: catalog.ParseBrowseQuery -> CompileBrowseSelection. Freezing the
// SQL outside this package keeps the native tests from acquiring the legacy
// catalog initialization graph while still executing the real contract. The
// catalog package's TestRBFilterTermsLegacyOracleVectorsMatchCompiler recompiles
// every vector against this file, so the freeze cannot drift from the compiler.
type rbTermsLegacyVector struct {
	Query string
	Where string
	Args  []any
}

func rbTermsLegacyVectors(t *testing.T) []rbTermsLegacyVector {
	t.Helper()
	data, err := os.ReadFile("testdata/rb-filter-terms-legacy-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source  string
		Vectors []rbTermsLegacyVector
	}
	if err = json.Unmarshal(plain, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Source == "" || len(fixture.Vectors) != 27 {
		t.Fatalf("incomplete legacy browse oracle: %q %d", fixture.Source, len(fixture.Vectors))
	}
	return fixture.Vectors
}

// rbTermsLegacyFixture builds the legacy-shaped title pivot over the terms
// fixture with the membership shape the compact writer actually produces:
// catalog_browse_memberships maps a container to its members, and only a
// playable kind is its own member (compactcatalog/browse_members_work.go source
// 0 reads catalog_kinds.playable). A show and a season are therefore not their
// own members; their members are their episodes (sources 1 and 2). The frozen
// legacy WHERE runs unchanged over this table. Stale and unsealed credit images
// are removed here because the legacy contract does not read sealed images at
// all; this oracle only compares how ancestry scopes a proved fact.
func rbTermsLegacyFixture(t *testing.T) *browseFixture {
	t.Helper()
	f := rbTermsFixture(t)
	for _, q := range []string{
		`CREATE TABLE catalog_browse_memberships(entity_id INTEGER NOT NULL,item_id INTEGER NOT NULL,source INTEGER NOT NULL,PRIMARY KEY(entity_id,item_id,source))`,
		`CREATE TABLE catalog_episodes(entity_id INTEGER PRIMARY KEY,show_id INTEGER NOT NULL,season_id INTEGER,numbering TEXT NOT NULL,number INTEGER NOT NULL)`,
		`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT e.id,e.id,0 FROM catalog_entities AS e JOIN catalog_kinds AS k ON k.id=e.kind WHERE k.playable=1`,
		`INSERT INTO catalog_episodes(entity_id,show_id,season_id,numbering,number) SELECT id,show_id,season_id,COALESCE(numbering,'seasonal'),COALESCE(episode_number,0) FROM catalog_entities WHERE kind=4`,
		`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT show_id,entity_id,1 FROM catalog_episodes`,
		`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT season_id,entity_id,2 FROM catalog_episodes WHERE season_id IS NOT NULL`,
		`UPDATE catalog_entities SET credit_image_id=NULL WHERE id=5`,
		`DELETE FROM catalog_credits WHERE entity_id=5`,
		`DELETE FROM catalog_credit_images WHERE entity_id=5`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(4,'person-dana','dana','Dana Director','Director, Dana',zeroblob(16))`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(5,'person-dave','dave','Dave Actor','Actor, Dave',zeroblob(16))`,
		`INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES
(103,7,1,2,3,4,'Bob Director','test','c10',10),
(103,7,2,3,5,6,'Carol Writer','test','c11',11),
(103,7,3,4,3,4,'Dana Director','test','c12',12),
(104,9,1,5,1,2,'Dave Actor','test','c13',1)`,
		// Reproductions from the round-2 review: these facts exist only on
		// episode 9, never on show 7 or season 8.
		`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) VALUES(9,10,'Noir',2)`,
		`INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(9,2)`,
		`INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override) VALUES(9,2,'owner','b',NULL)`,
	} {
		f.exec(t, q)
	}
	return f
}

// rbTermsLegacyMatches executes the frozen public WHERE over the same fixture
// and returns title ids in the accepted article-aware order.
func rbTermsLegacyMatches(t *testing.T, f *browseFixture, in BrowseInput, where string, args []any) []int64 {
	t.Helper()
	key, direction := "sort_key", "ASC"
	if in.KeepArticles {
		key = "sort_key_keep_articles"
	}
	if in.Descending {
		direction = "DESC"
	}
	// The frozen SQL calls the legacy portico_fold_text scalar, which this
	// native test binary deliberately does not register. Every frozen value is
	// ASCII, where that fold is exactly lower(); substituting only the function
	// name leaves every join and ancestry arm byte-for-byte as compiled.
	where = strings.ReplaceAll(where, "portico_fold_text(", "lower(")
	query := "SELECT e.entity_id FROM (SELECT id AS entity_id,library_id,kind,retired,sort_key,sort_key_keep_articles FROM catalog_entities) AS e WHERE e.library_id=? AND e.kind=? AND e.retired=0 AND (" + where + ") ORDER BY e." + key + " COLLATE NOCASE " + direction + ",e.entity_id " + direction
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

// TestRBFilterTermsLegacyOracle compares the native reader with the frozen
// legacy browse compiler executed over the real membership shape. It is
// independent of the native ancestry: the WHERE text and argument order come
// from CompileBrowseSelection, not from a hand-written copy of the new
// bindings. Movies and episodes are compared; shows and seasons must return
// the named member-ancestry refusal because the legacy pivot reads their
// member episodes and this recipe has no member walk.
func TestRBFilterTermsLegacyOracle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsLegacyFixture(t)
		for _, vector := range rbTermsLegacyVectors(t) {
			for _, kind := range []int64{1, 2, 3, 4} {
				for _, keep := range []bool{false, true} {
					in := TermsFilterInput{View: f.input, Query: json.RawMessage(vector.Query)}
					in.View.Kind, in.View.KeepArticles, in.View.Limit = kind, keep, 2
					if kind == 2 || kind == 3 {
						page, _, err := rbTermsRead(t, f, in)
						if !errors.Is(err, ErrTermsMemberAncestry) || !reflect.DeepEqual(page, TermsFilterPage{}) {
							t.Fatalf("container pivot answered %s kind=%d keep=%v: %v %v", vector.Query, kind, keep, page, err)
						}
						continue
					}
					got := rbTermsTraverse(t, f, in)
					want := rbTermsLegacyMatches(t, f, in.View, vector.Where, vector.Args)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("legacy oracle differs %s kind=%d keep=%v: native %v legacy %v", vector.Query, kind, keep, got, want)
					}
				}
			}
		}
	})
}

// TestRBFilterTermsContainerMembershipReproductions reruns the three round-2
// reproductions over the real membership shape. Each frozen legacy result is
// checked first, so the fixture provably still shows the leak shape, and then
// the native reader must refuse the pivot rather than answer from the container
// alone.
func TestRBFilterTermsContainerMembershipReproductions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsLegacyFixture(t)
		vectors := map[string]rbTermsLegacyVector{}
		for _, vector := range rbTermsLegacyVectors(t) {
			vectors[vector.Query] = vector
		}
		cases := []struct {
			query string
			kind  int64
			want  []int64
		}{
			// A show's own actor credit is not actor ancestry on the show
			// pivot; the members are its episodes.
			{`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}`, 2, nil},
			// A fact only on episode 9 reaches show 7 and season 8.
			{`{"variant":"predicate","field":"actor","operator":"equals","value":"Dave Actor"}`, 2, []int64{7}},
			{`{"variant":"predicate","field":"actor","operator":"equals","value":"Dave Actor"}`, 3, []int64{8}},
			{`{"variant":"predicate","field":"tag","operator":"equals","value":"NOIR"}`, 2, []int64{7}},
			{`{"variant":"predicate","field":"tag","operator":"equals","value":"NOIR"}`, 3, []int64{8}},
			{`{"variant":"predicate","field":"genre","operator":"equals","value":"Comedy"}`, 2, []int64{7}},
			{`{"variant":"predicate","field":"genre","operator":"equals","value":"Comedy"}`, 3, []int64{8}},
			// The season pivot also sees its member episode's person token.
			{`{"variant":"predicate","field":"person","operator":"equals","value":"person-bob"}`, 3, []int64{8}},
		}
		for _, tc := range cases {
			vector, ok := vectors[tc.query]
			if !ok {
				t.Fatalf("frozen vector missing: %s", tc.query)
			}
			in := TermsFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
			in.View.Kind, in.View.Limit = tc.kind, 10
			if want := rbTermsLegacyMatches(t, f, in.View, vector.Where, vector.Args); !reflect.DeepEqual(want, tc.want) {
				t.Fatalf("fixture no longer reproduces %s kind=%d: legacy %v want %v", tc.query, tc.kind, want, tc.want)
			}
			page, _, err := rbTermsRead(t, f, in)
			if !errors.Is(err, ErrTermsMemberAncestry) || !reflect.DeepEqual(page, TermsFilterPage{}) {
				t.Fatalf("container pivot answered instead of refusing %s kind=%d: %v %v", tc.query, tc.kind, page, err)
			}
		}
	})
}

// TestRBFilterTermsCreditAncestry pins the reported regression directly: a
// show's credit never makes its episodes match, while an item's own credit and
// the show's genre still do.
func TestRBFilterTermsCreditAncestry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsLegacyFixture(t)
		episodes := func(query string) []int64 {
			in := TermsFilterInput{View: f.input, Query: json.RawMessage(query)}
			in.View.Kind, in.View.Limit = 4, 10
			return rbTermsTraverse(t, f, in)
		}
		for _, query := range []string{
			`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}`,
			`{"variant":"predicate","field":"director","operator":"equals","value":"Dana Director"}`,
			`{"variant":"predicate","field":"writer","operator":"equals","value":"Carol Writer"}`,
			`{"variant":"predicate","field":"person","operator":"equals","value":"person-alice"}`,
			`{"variant":"predicate","field":"cast","operator":"equals","value":"person-alice"}`,
			`{"variant":"predicate","field":"crew","operator":"equals","value":"person-dana"}`,
		} {
			if got := episodes(query); len(got) != 0 {
				t.Fatalf("show-level credit reached episodes: %s %v", query, got)
			}
		}
		if got := episodes(`{"variant":"predicate","field":"director","operator":"equals","value":"Bob Director"}`); !reflect.DeepEqual(got, []int64{9}) {
			t.Fatalf("item-level director did not match its episode: %v", got)
		}
		if got := episodes(`{"variant":"predicate","field":"crew","operator":"equals","value":"person-bob"}`); !reflect.DeepEqual(got, []int64{9}) {
			t.Fatalf("item-level crew did not match its episode: %v", got)
		}
		if got := episodes(`{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}`); !reflect.DeepEqual(got, []int64{9, 10}) {
			t.Fatalf("episodes lost their show genre: %v", got)
		}
	})
}
