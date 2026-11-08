package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
// catalog initialization graph while still executing the real contract.
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
	if fixture.Source == "" || len(fixture.Vectors) != 23 {
		t.Fatalf("incomplete legacy browse oracle: %q %d", fixture.Source, len(fixture.Vectors))
	}
	return fixture.Vectors
}

// rbTermsLegacyFixture builds the legacy-shaped title pivot over the terms
// fixture. catalog_browse_memberships gives every title its one self membership
// ("maps an item to itself"), and catalog_episodes mirrors each episode's show,
// so the frozen legacy WHERE runs unchanged. Stale and unsealed credit images
// are removed here because the legacy contract does not read sealed images at
// all; this oracle only compares how ancestry scopes a proved fact.
func rbTermsLegacyFixture(t *testing.T) *browseFixture {
	t.Helper()
	f := rbTermsFixture(t)
	for _, q := range []string{
		`CREATE TABLE catalog_browse_memberships(entity_id INTEGER NOT NULL,item_id INTEGER NOT NULL,source INTEGER NOT NULL,PRIMARY KEY(entity_id,item_id,source))`,
		`CREATE TABLE catalog_episodes(entity_id INTEGER PRIMARY KEY,show_id INTEGER NOT NULL,season_id INTEGER,numbering TEXT NOT NULL,number INTEGER NOT NULL)`,
		`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT id,id,0 FROM catalog_entities`,
		`INSERT INTO catalog_episodes(entity_id,show_id,season_id,numbering,number) SELECT id,show_id,season_id,COALESCE(numbering,'seasonal'),COALESCE(episode_number,0) FROM catalog_entities WHERE kind=4`,
		`UPDATE catalog_entities SET credit_image_id=NULL WHERE id=5`,
		`DELETE FROM catalog_credits WHERE entity_id=5`,
		`DELETE FROM catalog_credit_images WHERE entity_id=5`,
		`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(4,'person-dana','dana','Dana Director','Director, Dana',zeroblob(16))`,
		`INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES
(103,7,1,2,3,4,'Bob Director','test','c10',10),
(103,7,2,3,5,6,'Carol Writer','test','c11',11),
(103,7,3,4,3,4,'Dana Director','test','c12',12)`,
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
// legacy browse compiler executed over the fixture. It is independent of the
// native ancestry: the WHERE text and argument order come from
// CompileBrowseSelection, not from a hand-written copy of the new bindings.
func TestRBFilterTermsLegacyOracle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsLegacyFixture(t)
		for _, vector := range rbTermsLegacyVectors(t) {
			for _, kind := range []int64{1, 2, 4} {
				for _, keep := range []bool{false, true} {
					in := TermsFilterInput{View: f.input, Query: json.RawMessage(vector.Query)}
					in.View.Kind, in.View.KeepArticles, in.View.Limit = kind, keep, 2
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
