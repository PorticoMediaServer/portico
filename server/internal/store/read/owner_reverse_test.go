package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordOwnerPlans = flag.Bool("record-owner-plans", false, "record actual owner-reverse Capture goldens")

func seedOwnerRelations(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(1,'person','person','Keanu','keanu',zeroblob(16))`)
	f.exec(t, `UPDATE catalog_credits SET person_id=1 WHERE image_id=1 AND ord IN(0,1,2)`)
	for _, v := range []struct{ image, work int64 }{{2, 1}, {3, 2}} {
		f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation) SELECT ?,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16) FROM catalog_entities WHERE id=?`, v.image, v.work)
		f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,1,1,1,2,'Keanu','test','old',1)`, v.image, v.work)
	}
	// Deliberately include raw, unqualified owner-kind and off-head edges:
	// the reverse pager must charge them before any serving verifier rejects.
	f.exec(t, `UPDATE catalog_entities SET artist_id=1,show_id=1 WHERE id IN(1,2,3)`)
	f.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(1,1),(2,1),(3,1)`)
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1,1,'c'),(1,2,'b'),(1,3,'a')`)
	data, e := os.ReadFile("../search/testdata/read-pairing.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Source string }
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	p, e := search.PrepareFragments(fixture.Source, search.DefaultFragmentBudget())
	if e != nil {
		t.Fatal(e)
	}
	profile := searchpolicy.Compiled()
	f.exec(t, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms,command_incarnation) VALUES(1,'v1',?,?,?,'active',0,zeroblob(16))`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint)
	title := search.PrepareTitle("Title 1")
	f.exec(t, `INSERT INTO search_publications(id,entity_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title,title_fold,title_prefix_tokens,initial_prefix_tokens) SELECT 1,id,1,library_id,kind,1,metadata_incarnation,revision,?,title,?,?,? FROM catalog_entities WHERE id=1`, profile.RecipeVersion, title.Fold(), title.PrefixTokens(), title.InitialTokens())
	for ord := 0; ord < p.Len(); ord++ {
		row := p.At(ord)
		f.exec(t, `INSERT INTO search_fragments(owner_entity,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(1,4,?,'1',?,?,?,1,1,1)`, ord, row.PrefixTokens(), row.LexicalTokens(), row.Normalized())
	}
}

func ownerFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("owner_reverse_test.go")
	if e != nil {
		t.Fatal(e)
	}
	a, b := bytes.Index(data, []byte("func seedOwnerRelations(")), bytes.Index(data, []byte("func ownerFixtureHash("))
	if a < 0 || b <= a {
		t.Fatal("missing owner fixture journal")
	}
	fixture, e := os.ReadFile("../search/testdata/read-pairing.json")
	if e != nil {
		t.Fatal(e)
	}
	journal := append([]byte(foregroundFixtureHash(t)), data[a:b]...)
	journal = append(journal, fixture...)
	return fmt.Sprintf("%x", sha256.Sum256(journal))
}

func TestOwnerLogicalSiblingReadOracle(t *testing.T) {
	f := newForegroundSource(t)
	seedOwnerRelations(t, f)
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundRequestBytes}
	f = composeReadFixture(t, f, FullSearchDefinitions(), gc, true, ownerFixtureHash(t))
	var candidateRows, sameRow int
	rows, e := f.conn.QueryContext(t.Context(), `SELECT prefix_tokens,normalized FROM search_fragments WHERE publication_id=1 AND field=4 ORDER BY ordinal`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var prefix, norm string
		if e = rows.Scan(&prefix, &norm); e != nil {
			t.Fatal(e)
		}
		if slices.Contains(strings.Fields(prefix), "008f9b0054eb") {
			candidateRows++
			if strings.Contains(norm, "辛哫") {
				sameRow++
			}
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	s := fullScope(t, "辛哫")
	key := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 1, PublicationID: 1, BankID: 1, InputGeneration: 1, Field: 4, Revision: "1"}
	w, e := newLogicalFieldWalk(s, key, "")
	if e != nil {
		t.Fatal(e)
	}
	after := int64(-1)
	windows := 0
	for {
		var done bool
		_, e = f.snapshots.Read(t.Context(), foregroundRequest(), time.Second, func(scope *governor.ReadScope) error {
			var e error
			after, done, e = readLogicalFieldStep(scope.Context(), func(n string, args ...any) ([][]any, error) { return scope.Query(readStatementID(n), args...) }, w, after, 1)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		windows++
		if done {
			break
		}
		if windows > 64 {
			t.Fatal("sibling cursor stuck")
		}
	}
	p, e := w.finish(t.Context(), true)
	if e != nil {
		t.Fatal(e)
	}
	pop := populations(t, s, p, p)
	if candidateRows != 1 || sameRow != 0 || !p.matched[0] || pop[0].count != 1 {
		t.Fatal("PB-RD-01 pairing oracle", candidateRows, sameRow, p.matched, pop)
	}
	// Prefix candidate selection above is prepared-data inspection, NOT native
	// MATCH. Only the sibling walk is actual captured ordinary READ execution.
}

func TestOwnerReversePlans(t *testing.T) {
	f := newForegroundSource(t)
	seedOwnerRelations(t, f)
	defs := FullSearchDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			present := rows.Next()
			e = rows.Close()
			if e != nil || present != (c.Name == "present") {
				t.Fatal(d.ID, c.Name, present, e)
			}
		}
	}
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats, ownerFixtureHash(t))
		if _, e := stmt.New(defs, g.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures = append(captures, capture{string(stats), g})
	}
	actual, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	path := "testdata/owner-reverse-plans.json.gz"
	if *recordOwnerPlans {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, e = z.Write(actual); e != nil {
			t.Fatal(e)
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, buf.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	file, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	z, e := gzip.NewReader(file)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	want, e := io.ReadAll(z)
	if e != nil {
		t.Fatal(e)
	}
	if e = plan.CompareJSON(want, actual); e != nil {
		t.Fatal(e)
	}
}

func TestOwnerReverseKeysetTraversal(t *testing.T) {
	f := newForegroundSource(t)
	seedOwnerRelations(t, f)
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundRequestBytes}
	f = composeReadFixture(t, f, FullSearchDefinitions(), gc, true, ownerFixtureHash(t))
	for _, rel := range []ownerRelation{personWorks, artistAlbums, artistSongs, showChildren, authoredMembers} {
		t.Run(fmt.Sprint(rel), func(t *testing.T) {
			var want []ownerPosition
			if rel == personWorks {
				want = []ownerPosition{{2, 1, 3}, {1, 2, 1}, {1, 1, 2}, {1, 1, 1}, {1, 0, 1}}
			} else {
				want = []ownerPosition{{3, 0, 0}, {2, 0, 0}, {1, 0, 0}}
			}
			for _, limit := range []int{1, 2, 64} {
				var got []ownerPosition
				var cursor *ownerCursor
				for turns := 0; turns < 10; turns++ {
					r := foregroundRequest()
					var page ownerPage
					var calls int
					_, e := f.snapshots.Read(t.Context(), r, time.Second, func(scope *governor.ReadScope) error {
						var err error
						page, err = readOwnerPage(func(n string, args ...any) ([][]any, error) { calls++; return scope.Query(readStatementID(n), args...) }, rel, 1, cursor, limit)
						return err
					})
					if e != nil {
						t.Fatal(e)
					}
					if calls > 3 || len(page.positions) > limit {
						t.Fatal("unbounded raw window", calls, page)
					}
					got = append(got, page.positions...)
					cursor = page.after
					if page.exhausted {
						break
					}
					if turns == 9 {
						t.Fatal("keyset did not finish")
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("wrong raw traversal", limit, got, want)
				}
			}
		})
	}
	// The off-head images2/3 are still present in the raw walk; no LIMIT after
	// filtering or accidental logical-match claim is hidden in this primitive.
	if _, e := readOwnerPage(nil, personWorks, 1, &ownerCursor{relation: personWorks, owner: 2, position: ownerPosition{Work: 1}}, 1); e == nil {
		t.Fatal("foreign-owner cursor")
	}
	if _, e := readOwnerPage(nil, personWorks, 1, nil, 65); e == nil {
		t.Fatal("oversized raw window")
	}
	for _, d := range FullSearchDefinitions() {
		if !slices.Contains([]stmt.Kind{stmt.Read}, d.Kind) {
			t.Fatal("non-read definition")
		}
	}
}
