package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// BEGIN_RB_FILTER_TERMS_JOURNAL
func newRBFilterTermsSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	run := func(q string, args ...any) {
		t.Helper()
		if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	run(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0)`)
	run(`DELETE FROM catalog_entities WHERE id>6`)
	run(`DELETE FROM catalog_subjects WHERE id>6`)
	run(`UPDATE catalog_entities SET sort_key=CASE id WHEN 1 THEN 'alpha' WHEN 2 THEN 'bravo' WHEN 3 THEN 'charlie' WHEN 4 THEN 'delta' WHEN 5 THEN 'echo' WHEN 6 THEN 'foxtrot' ELSE 'zz' END,
sort_key_keep_articles=CASE id WHEN 1 THEN 'alpha' WHEN 2 THEN 'bravo' WHEN 3 THEN 'charlie' WHEN 4 THEN 'delta' WHEN 5 THEN 'echo' WHEN 6 THEN 'foxtrot' ELSE 'zz' END,
retired=0,admission_own_age=NULL WHERE id<=6`)
	run(`UPDATE catalog_entities SET retired=1 WHERE id=6`)
	run(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES
(7,CAST(printf('%016x',7) AS BLOB),1,2),(8,CAST(printf('%016x',8) AS BLOB),1,3),(9,CAST(printf('%016x',9) AS BLOB),1,4),(10,CAST(printf('%016x',10) AS BLOB),1,4),(11,CAST(printf('%016x',11) AS BLOB),2,1),(12,CAST(printf('%016x',12) AS BLOB),1,1)`)
	run(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_id,season_number,numbering,episode_number) VALUES
(7,CAST(printf('%016x',7) AS BLOB),1,2,'Show','show','show','show','show',0,CAST(printf('%016x',7) AS BLOB),CAST(printf('%016x',7) AS BLOB),NULL,NULL,NULL,NULL,NULL),
(8,CAST(printf('%016x',8) AS BLOB),1,3,'Season One','season one','season one','season one','season one',0,CAST(printf('%016x',8) AS BLOB),CAST(printf('%016x',8) AS BLOB),7,NULL,1,NULL,NULL),
(9,CAST(printf('%016x',9) AS BLOB),1,4,'Episode One','episode one','episode one','episode one','episode one',0,CAST(printf('%016x',9) AS BLOB),CAST(printf('%016x',9) AS BLOB),7,8,NULL,'seasonal',1),
(10,CAST(printf('%016x',10) AS BLOB),1,4,'Episode Two','episode two','episode two','episode two','episode two',0,CAST(printf('%016x',10) AS BLOB),CAST(printf('%016x',10) AS BLOB),7,8,NULL,'seasonal',2),
(11,CAST(printf('%016x',11) AS BLOB),2,1,'Other Library','other library','other library','other library','other library',0,CAST(printf('%016x',11) AS BLOB),CAST(printf('%016x',11) AS BLOB),NULL,NULL,NULL,NULL,NULL),
(12,CAST(printf('%016x',12) AS BLOB),1,1,'Zulu','zulu','zulu','zulu','zulu',0,CAST(printf('%016x',12) AS BLOB),CAST(printf('%016x',12) AS BLOB),NULL,NULL,NULL,NULL,NULL)`)

	run(`INSERT INTO catalog_terms(id,vocab,key,label) VALUES(1,1,'drama','Drama'),(2,1,'comedy','Comedy'),(3,1,'mystery','Mystery'),(4,2,'wrong','Wrong vocabulary')`)
	run(`INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(1,1),(2,2),(3,1),(4,1),(7,1),(7,3),(9,3)`)
	run(`INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override) VALUES
(1,1,'owner','a',NULL),(1,1,'provider','b',NULL),(2,2,'owner','a',NULL),(3,1,'owner','a',NULL),
(4,1,'owner','a','Dramatic'),(7,1,'owner','a',NULL),(7,3,'owner','b',NULL),(9,3,'owner','a',NULL)`)
	run(`INSERT INTO catalog_attribute_fields(id,field) VALUES(1,'tag'),(2,'label'),(3,'contentRating'),(4,'studio'),(5,'network')`)
	run(`INSERT INTO catalog_attribute_terms(id,field_id,value_key) VALUES
(10,1,'noir'),(11,2,'blocked'),(12,3,'pg-13'),(13,4,'studio a'),(14,5,'net x'),(15,1,'noir duplicate')`)
	run(`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) VALUES
(1,10,'Noir',0),(1,15,'Noir',1),(2,11,'Blocked',0),(3,12,'PG-13',0),(4,13,'Studio A',0),(7,14,'Net X',0),(5,10,'noir',0)`)

	run(`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES
(1,'person-alice','alice','Alice Actor','Actor, Alice',zeroblob(16)),
(2,'person-bob','bob','Bob Director','Director, Bob',zeroblob(16)),
(3,'person-carol','carol','Carol Writer','Writer, Carol',zeroblob(16))`)
	run(`INSERT INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting'),(3,'Director'),(4,'Directing'),(5,'Writer'),(6,'Writing'),(7,'Screenplay')`)
	for _, v := range []struct {
		image, entity, revision, sealed int64
	}{{101, 1, 1, 1}, {102, 2, 1, 1}, {103, 7, 1, 1}, {104, 9, 1, 1}, {105, 3, 1, 1}, {106, 5, 1, 1}, {107, 6, 1, 0}} {
		run(`INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest)
SELECT ?,id,metadata_incarnation,1,1,1,?,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),?,zeroblob(32) FROM catalog_entities WHERE id=?`,
			v.image, v.revision, v.sealed, v.entity)
		run(`UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, v.image, v.entity)
	}
	// Entity 5's current credit revision moved past its sealed image; the old
	// image must never supply facts. Entity 6's image is not sealed.
	run(`UPDATE catalog_entities SET credit_owner_revision=2,credit_owner_next_revision=3 WHERE id=5`)
	run(`INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES
(101,1,0,1,1,2,'Alice Actor','test','c0',0),
(101,1,1,2,3,4,'Bob Director','test','c1',1),
(101,1,2,3,5,6,'Carol Writer','test','c2',2),
(101,1,3,1,1,2,'Alice Actor','test','c3',3),
(102,2,0,1,1,2,'Alice Actor','test','c4',0),
(103,7,0,1,1,2,'Alice Actor','test','c5',0),
(104,9,0,2,3,4,'Bob Director','test','c6',0),
(105,3,0,NULL,1,2,'Ghost Actor','test','c7',0),
(106,5,0,1,1,2,'Stale Actor','test','c8',0),
(107,6,0,1,1,2,'Unsealed Actor','test','c9',0)`)
	run(`UPDATE catalog_people SET name='Alice Renamed',sort_name='Renamed, Alice' WHERE id=1`)
	return f
}

// END_RB_FILTER_TERMS_JOURNAL

func rbTermsFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_filter_terms_test.go")
	if e != nil {
		t.Fatal(e)
	}
	a := strings.Index(string(data), "// BEGIN_RB_FILTER_TERMS_JOURNAL\n")
	b := strings.Index(string(data), "// END_RB_FILTER_TERMS_JOURNAL\n")
	if a < 0 || b <= a {
		t.Fatal("terms source journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[a:b]...)))
}

func rbTermsFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBFilterTermsSource(t), append(browseDefinitions(), RBFilterTermsDefinitions()...), readfixture.LiveConfig(time.Now), false, rbTermsFixtureHash(t))
}

func rbTermsRead(t *testing.T, f *browseFixture, in TermsFilterInput) (TermsFilterPage, BrowseReadResult, error) {
	t.Helper()
	var page TermsFilterPage
	var cost BrowseReadResult
	live, request, output := liveRequests("rb-filter-terms")
	request.Budget.RawRows = 2*MaxRaw + 10
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, cost, e = f.reader.TermsFilterWindow(ctx, request, time.Second, out, in)
		if e == nil {
			assertLiveUsage(t, f, 1, 0)
		}
		return e
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("terms source retained LIVE custody", u)
	}
	return page, cost, err
}

func rbTermsTraverse(t *testing.T, f *browseFixture, in TermsFilterInput) []int64 {
	t.Helper()
	var got []int64
	for n := 0; ; n++ {
		if n > 64 {
			t.Fatal("cursor stalled")
		}
		page, _, err := rbTermsRead(t, f, in)
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

// rbTermsOracle evaluates the same published semantics independently in SQLite.
func rbTermsOracle(t *testing.T, f *browseFixture, in BrowseInput, where string, args ...any) []int64 {
	t.Helper()
	key, direction := "sort_key", "ASC"
	if in.KeepArticles {
		key = "sort_key_keep_articles"
	}
	if in.Descending {
		direction = "DESC"
	}
	query := "SELECT e.id FROM catalog_entities AS e WHERE e.library_id=? AND e.kind=? AND e.retired=0 AND (" + where + ") ORDER BY e." + key + " COLLATE NOCASE " + direction + ",e.id " + direction
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

const rbTermsAncestry = `e.id,COALESCE(e.show_id,e.id)`

type rbClause struct {
	where string
	args  []any
}

func rbTermsTermClause(value string) rbClause {
	return rbClause{`EXISTS(SELECT 1 FROM catalog_term_sources s CROSS JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id IN(` + rbTermsAncestry + `) AND t.vocab=1 AND COALESCE(s.label_override,t.label)=?)`, []any{value}}
}
func rbTermsAttributeClause(field, value string) rbClause {
	return rbClause{`EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae CROSS JOIN catalog_attribute_terms at ON at.id=ae.term_id CROSS JOIN catalog_attribute_fields af ON af.id=at.field_id WHERE ae.item_id=e.id AND af.field=? AND lower(ae.source_value)=lower(?))`, []any{field, value}}
}
func rbTermsCreditClause(class, name string) rbClause {
	clause := map[string]string{"actor": "d.label='Acting'", "director": "(r.label='Director' OR d.label='Directing')", "writer": "(r.label IN('Writer','Screenplay') OR d.label='Writing')"}[class]
	value := `c.credited_name=?`
	args := []any{}
	if name != "" {
		args = append(args, name)
	} else {
		value = "1"
	}
	return rbClause{`EXISTS(SELECT 1 FROM catalog_credits c CROSS JOIN catalog_entities ce ON ce.id=c.entity_id CROSS JOIN catalog_credit_images h ON h.id=c.image_id AND h.entity_id=ce.id AND h.entity_incarnation=ce.metadata_incarnation AND h.owner_revision=ce.credit_owner_revision CROSS JOIN catalog_credit_labels r ON r.id=c.role_id CROSS JOIN catalog_credit_labels d ON d.id=c.department_id WHERE c.entity_id IN(` + rbTermsAncestry + `) AND h.sealed=1 AND ce.credit_image_id=c.image_id AND ce.retired=0 AND ` + value + ` AND ` + clause + `)`, args}
}
func rbTermsPersonClause(class, token string) rbClause {
	clause := map[string]string{"person": "1", "cast": "lower(d.label) IN('acting','cast')", "crew": "lower(d.label) NOT IN('acting','cast')"}[class]
	return rbClause{`EXISTS(SELECT 1 FROM catalog_credits c CROSS JOIN catalog_people p ON p.id=c.person_id CROSS JOIN catalog_entities ce ON ce.id=c.entity_id CROSS JOIN catalog_credit_images h ON h.id=c.image_id AND h.entity_id=ce.id AND h.entity_incarnation=ce.metadata_incarnation AND h.owner_revision=ce.credit_owner_revision CROSS JOIN catalog_credit_labels d ON d.id=c.department_id WHERE c.entity_id IN(` + rbTermsAncestry + `) AND h.sealed=1 AND ce.credit_image_id=c.image_id AND ce.retired=0 AND p.token=? AND ` + clause + `)`, []any{token}}
}
func rbTermsNot(c rbClause) rbClause { return rbClause{"NOT (" + c.where + ")", c.args} }
func rbTermsAnd(parts ...rbClause) rbClause {
	var clauses []string
	var args []any
	for _, part := range parts {
		clauses = append(clauses, part.where)
		args = append(args, part.args...)
	}
	return rbClause{strings.Join(clauses, " AND "), args}
}
func rbTermsOr(parts ...rbClause) rbClause {
	var clauses []string
	var args []any
	for _, part := range parts {
		clauses = append(clauses, "("+part.where+")")
		args = append(args, part.args...)
	}
	return rbClause{strings.Join(clauses, " OR "), args}
}

func TestRBFilterTermsTraversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsFixture(t)
		drama := rbTermsTermClause("Drama")
		alice := rbTermsCreditClause("actor", "Alice Actor")
		comedy := rbTermsTermClause("Comedy")
		bob := rbTermsCreditClause("actor", "Bob Director")
		noir := rbTermsAttributeClause("tag", "Noir")
		genrePresent := rbClause{`EXISTS(SELECT 1 FROM catalog_term_sources s CROSS JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id IN(` + rbTermsAncestry + `) AND t.vocab=1)`, nil}
		genrePrefix := rbClause{`EXISTS(SELECT 1 FROM catalog_term_sources s CROSS JOIN catalog_terms t ON t.id=s.term_id WHERE s.entity_id IN(` + rbTermsAncestry + `) AND t.vocab=1 AND COALESCE(s.label_override,t.label) LIKE 'Dra%')`, nil}
		cases := []struct {
			query string
			want  rbClause
		}{
			{`{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}`, drama},
			{`{"variant":"group","not":{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}}`, rbTermsNot(drama)},
			{`{"variant":"predicate","field":"genre","operator":"not-equals","value":"Drama"}`, rbTermsNot(drama)},
			{`{"variant":"predicate","field":"genre","operator":"is-present","value":null}`, genrePresent},
			{`{"variant":"predicate","field":"genre","operator":"is-missing","value":null}`, rbTermsNot(genrePresent)},
			{`{"variant":"predicate","field":"genre","operator":"starts-with","value":"Dra"}`, genrePrefix},
			{`{"variant":"predicate","field":"tag","operator":"equals","value":"NOIR"}`, noir},
			{`{"variant":"predicate","field":"tag","operator":"not-equals","value":"Noir"}`, rbTermsNot(noir)},
			{`{"variant":"predicate","field":"label","operator":"equals","value":"Blocked"}`, rbTermsAttributeClause("label", "Blocked")},
			{`{"variant":"predicate","field":"contentRating","operator":"equals","value":"pg-13"}`, rbTermsAttributeClause("contentRating", "pg-13")},
			{`{"variant":"predicate","field":"studio","operator":"contains","value":"studio"}`, rbTermsAttributeClause("studio", "Studio A")},
			{`{"variant":"predicate","field":"network","operator":"equals","value":"Net X"}`, rbTermsAttributeClause("network", "Net X")},
			{`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}`, alice},
			{`{"variant":"predicate","field":"actor","operator":"not-equals","value":"Alice Actor"}`, rbTermsNot(alice)},
			{`{"variant":"predicate","field":"actor","operator":"is-present","value":null}`, rbTermsCreditClause("actor", "")},
			{`{"variant":"predicate","field":"director","operator":"equals","value":"Bob Director"}`, rbTermsCreditClause("director", "Bob Director")},
			{`{"variant":"predicate","field":"writer","operator":"equals","value":"Carol Writer"}`, rbTermsCreditClause("writer", "Carol Writer")},
			{`{"variant":"predicate","field":"person","operator":"equals","value":"person-alice"}`, rbTermsPersonClause("person", "person-alice")},
			{`{"variant":"predicate","field":"cast","operator":"equals","value":"person-alice"}`, rbTermsPersonClause("cast", "person-alice")},
			{`{"variant":"predicate","field":"crew","operator":"equals","value":"person-bob"}`, rbTermsPersonClause("crew", "person-bob")},
			{`{"variant":"predicate","field":"person","operator":"not-in","value":["person-alice"]}`, rbTermsNot(rbTermsPersonClause("person", "person-alice"))},
			{`{"variant":"group","all":[{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"},{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}]}`, rbTermsAnd(drama, alice)},
			{`{"variant":"group","any":[{"variant":"predicate","field":"genre","operator":"equals","value":"Comedy"},{"variant":"predicate","field":"actor","operator":"equals","value":"Bob Director"}]}`, rbTermsOr(comedy, bob)},
		}
		for _, tc := range cases {
			for _, keep := range []bool{false, true} {
				for _, desc := range []bool{false, true} {
					in := TermsFilterInput{View: f.input, Query: json.RawMessage(tc.query)}
					in.View.Limit = 2
					in.View.KeepArticles, in.View.Descending = keep, desc
					got := rbTermsTraverse(t, f, in)
					want := rbTermsOracle(t, f, in.View, tc.want.where, tc.want.args...)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("native traversal differs %s keep=%v desc=%v: got %v want %v", tc.query, keep, desc, got, want)
					}
				}
			}
		}
	})
}

func TestRBFilterTermsEpisodeAncestryAndDuplicateReasons(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsFixture(t)
		in := TermsFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}`)}
		in.View.Kind = 4
		in.View.Limit = 10
		page, _, err := rbTermsRead(t, f, in)
		if err != nil || !page.Exhausted || len(page.Items) != 2 {
			t.Fatal("episodes did not inherit their show's genre", page, err)
		}
		// Both episodes are members; duplicate term sources on one entity and a
		// duplicate credit never duplicate a page row or change truth.
		in.View.Kind = 1
		in.View.Limit = 10
		page, _, err = rbTermsRead(t, f, in)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if !reflect.DeepEqual(ids, []int64{1, 3}) {
			t.Fatal("duplicate reasons changed membership", ids)
		}
		in.Query = json.RawMessage(`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}`)
		page, _, err = rbTermsRead(t, f, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = nil
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if !reflect.DeepEqual(ids, []int64{1, 2}) {
			t.Fatal("duplicate credit changed membership", ids)
		}
	})
}

func TestRBFilterTermsRenamedRetiredAndStaleImages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsFixture(t)
		// The credited name is the credit row's value, not the current person name.
		for query, want := range map[string][]int64{
			`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Actor"}`:   {1, 2},
			`{"variant":"predicate","field":"actor","operator":"equals","value":"Alice Renamed"}`: nil,
			`{"variant":"predicate","field":"person","operator":"equals","value":"person-alice"}`: {1, 2},
			`{"variant":"predicate","field":"person","operator":"equals","value":"Alice Actor"}`:  nil,
		} {
			in := TermsFilterInput{View: f.input, Query: json.RawMessage(query)}
			in.View.Limit = 10
			page, _, err := rbTermsRead(t, f, in)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			if !reflect.DeepEqual(ids, want) {
				t.Fatalf("renamed person semantics %s: got %v want %v", query, ids, want)
			}
		}
		// An unresolved credit still matches by credited name but never by token.
		in := TermsFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"actor","operator":"equals","value":"Ghost Actor"}`)}
		in.View.Limit = 10
		page, _, err := rbTermsRead(t, f, in)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != 3 {
			t.Fatal("unresolved credit lost its credited name", page, err)
		}
		in.Query = json.RawMessage(`{"variant":"predicate","field":"person","operator":"equals","value":"person-ghost"}`)
		page, _, err = rbTermsRead(t, f, in)
		if err != nil || len(page.Items) != 0 || !page.Exhausted {
			t.Fatal("unresolved person token leaked", page, err)
		}
		// Stale and unsealed credit images never supply facts; retired items
		// never reach admission even when an image exists.
		for query := range map[string]bool{
			`{"variant":"predicate","field":"actor","operator":"equals","value":"Stale Actor"}`:    true,
			`{"variant":"predicate","field":"actor","operator":"equals","value":"Unsealed Actor"}`: true,
		} {
			in := TermsFilterInput{View: f.input, Query: json.RawMessage(query)}
			in.View.Limit = 10
			page, _, err = rbTermsRead(t, f, in)
			if err != nil || len(page.Items) != 0 || !page.Exhausted {
				t.Fatalf("stale credit image supplied facts: %s %v %v", query, page, err)
			}
		}
	})
}

func TestRBFilterTermsLabelPolicyDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsFixture(t)
		f.exec(t, `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('p','["blocked"]')`)
		in := TermsFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"tag","operator":"equals","value":"Noir"}`)}
		page, cost, err := rbTermsRead(t, f, in)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, TermsFilterPage{}) || cost.LookupStatements != 2 || cost.PublicationStatements != 0 {
			t.Fatal("blocked label policy escaped before business SQL", page, cost, err)
		}
	})
}

func TestRBFilterTermsFencesAndRefusals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbTermsFixture(t)
		in := TermsFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"genre","operator":"equals","value":"Drama"}`)}
		in.View.Limit = 1
		first, _, err := rbTermsRead(t, f, in)
		if err != nil || len(first.Items) != 1 || first.After == nil {
			t.Fatal(first, err)
		}
		in.After = first.After
		in.Query = json.RawMessage(`{"variant":"predicate","field":"genre","operator":"equals","value":"Comedy"}`)
		page, _, err := rbTermsRead(t, f, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, TermsFilterPage{}) {
			t.Fatal("cursor accepted another query", page, err)
		}
		in.After = nil
		in.Query = json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"title","operator":"equals","value":"Alpha"}}`)
		page, _, err = rbTermsRead(t, f, in)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, TermsFilterPage{}) {
			t.Fatal("unsupported NOT became permission", page, err)
		}
		in.Query = json.RawMessage(`{"variant":"predicate","field":"person","operator":"equals","value":"person-alice"}`)
		f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, "UPDATE authorization_session_families SET revoked=1") }}
		page, _, err = rbTermsRead(t, f, in)
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, TermsFilterPage{}) {
			t.Fatal("revoked page escaped", page, err)
		}
		f.reader.snapshots = f.snapshots
		_, request, _ := liveRequests("terms-bypass")
		page, _, err = f.reader.TermsFilterWindow(t.Context(), request, time.Second, BrowseOutput{}, in)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, TermsFilterPage{}) {
			t.Fatal("LIVE bypass", page, err)
		}
		// A present-attribute filter over a page with no tag rows is an exact
		// empty page, not a sampled answer.
		f2 := rbTermsFixture(t)
		in = TermsFilterInput{View: f2.input, Query: json.RawMessage(`{"variant":"predicate","field":"tag","operator":"is-present","value":null}`)}
		in.View.Kind = 2
		page, _, err = rbTermsRead(t, f2, in)
		if err != nil || len(page.Items) != 0 || !page.Exhausted {
			t.Fatal("attribute presence refused a small page", page, err)
		}
	})
}

func TestRBFilterTermsCapture(t *testing.T) {
	f := newRBFilterTermsSource(t)
	defs := RBFilterTermsDefinitions()
	if len(defs) != 36 {
		t.Fatal("source/byte inventory changed", len(defs))
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, rbTermsFixtureHash(t))
	}
}
