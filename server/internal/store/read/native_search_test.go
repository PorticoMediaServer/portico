package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// This is a read fixture, not producer acceptance or a bank qualification. The
// stored manifests are derived from actual prepared rows, never a fake seal.
func seedNativeSearch(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	p := searchpolicy.Compiled()
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(4,'episode',1,0,1,1,0)`)
	f.exec(t, `UPDATE catalog_subjects SET kind=CASE id WHEN 1 THEN 2 ELSE 4 END WHERE id IN(1,2,3)`)
	f.exec(t, `UPDATE catalog_entities SET kind=CASE id WHEN 1 THEN 2 ELSE 4 END,title=CASE id WHEN 2 THEN 'Arrival' ELSE 'Matrix' END,retired=0,show_id=CASE id WHEN 1 THEN NULL ELSE 1 END WHERE id IN(1,2,3)`)
	f.exec(t, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'v1',?,?,?,'active',0)`, p.RecipeVersion, p.EngineVersion, p.UnicodeFingerprint)
	f.exec(t, `INSERT INTO search_revision(singleton,revision,active_generation_id,input_sequence) VALUES(1,1,1,0)`)
	for _, id := range []int64{1, 2, 3} {
		var title string
		var kind, revision int64
		var inc []byte
		if err := f.conn.QueryRowContext(t.Context(), `SELECT title,kind,revision,metadata_incarnation FROM catalog_entities WHERE id=?`, id).Scan(&title, &kind, &revision, &inc); err != nil {
			t.Fatal(err)
		}
		prepared := search.PrepareTitle(title)
		fragments, err := search.PrepareFragments(title, search.DefaultFragmentBudget())
		if err != nil {
			t.Fatal(err)
		}
		manifestRows := make([]search.ManifestRow, fragments.Len())
		for i := range manifestRows {
			manifestRows[i] = fragments.At(i).ManifestRow(1, int64(i), "revision-1")
		}
		var incarnation [16]byte
		copy(incarnation[:], inc)
		manifest, err := search.PrepareManifest(search.ManifestInput{Subject: search.ManifestEntity, SubjectID: id, LibraryID: 1, Kind: kind, SearchGenerationID: 1, BankKey: "v1", EngineVersion: p.EngineVersion, UnicodeFingerprint: p.UnicodeFingerprint, Recipe: p.RecipeVersion, InputGeneration: 1, SourceRevision: revision, SourceIncarnation: incarnation}, manifestRows)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `INSERT INTO search_publications(id,entity_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title,title_fold,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,1,1,?,1,?,?,?,?,?,?,?)`, id, id, kind, inc, revision, p.RecipeVersion, title, prepared.Fold(), prepared.PrefixTokens(), prepared.InitialTokens())
		f.exec(t, `INSERT INTO search_publish_jobs(entity_id,search_generation_id,published_generation,published_document_id,input_generation,source_incarnation,source_revision,recipe_version,phase) VALUES(?,1,1,?,1,?,?,?,'idle')`, id, id, inc, revision, p.RecipeVersion)
		for i, row := range manifestRows {
			fragment := id*100 + int64(i)
			f.exec(t, `INSERT INTO search_fragments(id,owner_entity,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,?,1,?,?,?,?,?,1,1,?)`, fragment, id, row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized, id)
			f.exec(t, `INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, fragment, row.Prefix)
			f.exec(t, `INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, fragment, row.Lexical)
			f.exec(t, `INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, fragment, row.Normalized)
		}
		seed, digest := manifest.Seed(), manifest.Digest()
		f.exec(t, `INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,verified_fragments,verified_bytes,chain,sealed) VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, id, search.ManifestAlgorithm, seed[:], digest[:], manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), digest[:])
		f.exec(t, `INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, id, prepared.PrefixTokens(), prepared.InitialTokens())
	}
	// A raw People posting is intentionally NOT accepted without its own birth.
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name) VALUES(1,'person','person','Keanu','keanu')`)
	f.exec(t, `INSERT INTO catalog_people_names(rowid,name) VALUES(1,'Keanu')`)
	f.exec(t, "COMMIT")
}
func nativeFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("native_search_test.go")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data...)))
}
func newNativeSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	seedNativeSearch(t, f)
	return f
}
func newNativeFixture(t *testing.T) (*browseFixture, *NativeFullSearchReader) {
	t.Helper()
	f := newNativeSource(t)
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, nativeFixtureHash(t))
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	return f, r
}
func nativeRead(t *testing.T, f *browseFixture, r *NativeFullSearchReader, text string, after *FullSearchCursor) (FullSearchPage, *BrowseReadResult, error) {
	t.Helper()
	q, err := search.PlanQuery(text, false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("native")
	request.Budget.RawRows = 4096
	var page FullSearchPage
	var cost *BrowseReadResult
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, After: after}, q)
		return err
	})
	return page, cost, err
}
func TestNativeSearchCurrentAllKindsAndMixedOwners(t *testing.T) {
	f, r := newNativeFixture(t)
	for _, v := range []struct {
		query string
		ids   []int64
		tiers []uint8
	}{{"matrix", []int64{3, 1, 2}, []uint8{0, 0, 5}}, {"matrix arrival", []int64{2}, []uint8{5}}} {
		var cursor *FullSearchCursor
		done := false
		for n := 0; n < 100; n++ {
			page, cost, err := nativeRead(t, f, r, v.query, cursor)
			if err != nil {
				t.Fatalf("%s step%d: %v cost%+v", v.query, n, err, cost)
			}
			if cost == nil || len(cost.LookupWindows) != 1 || len(cost.PublicationWindows) != 1 || cost.PublicationStatements != 7 {
				t.Fatal("whole operation evidence", cost)
			}
			if page.Disposition == SearchPending {
				if len(page.Hits) != 0 || page.Next == nil || page.Exhausted {
					t.Fatal("partial match escaped")
				}
				cursor = page.Next
				continue
			}
			var ids []int64
			var tiers []uint8
			for _, hit := range page.Hits {
				ids = append(ids, hit.ID)
				tiers = append(tiers, hit.Tier)
				if hit.Kind <= 0 {
					t.Fatal("missing entity kind")
				}
			}
			if !reflect.DeepEqual(ids, v.ids) || !reflect.DeepEqual(tiers, v.tiers) || !page.Exhausted {
				t.Fatalf("oracle %s: %+v", v.query, page)
			}
			done = true
			break
		}
		if !done {
			t.Fatal("bounded continuation did not finish", v.query)
		}
	}
}
func TestNativeSearchRefusesUnsupportedAndStaleProofs(t *testing.T) {
	f, r := newNativeFixture(t)
	page, _, err := nativeRead(t, f, r, "keanu", nil)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(page, FullSearchPage{}) {
		t.Fatal("People proof silently dropped", page, err)
	}
	page, _, err = nativeRead(t, f, r, "matrix", nil)
	if err != nil || page.Next == nil {
		t.Fatal("pending setup", err)
	}
	after := page.Next
	f.exec(t, `UPDATE search_revision SET revision=revision+1 WHERE singleton=1`)
	page, _, err = nativeRead(t, f, r, "matrix", after)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, FullSearchPage{}) {
		t.Fatal("mixed bank continuation", page, err)
	}
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	page, _, err = nativeRead(t, f, r, "matrix", nil)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, FullSearchPage{}) {
		t.Fatal("revoked source disclosed", page, err)
	}
}
func TestNativeSearchPlans(t *testing.T) {
	f := newNativeSource(t)
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, NativeFullSearchDefinitions(), stats, nativeFixtureHash(t))
		captures = append(captures, capture{string(stats), g})
	}
	data, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err = z.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if root := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); root != "" {
		if err = os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, "native-full-search-plans.json.gz"), compressed.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open("testdata/native-full-search-plans.json.gz")
	if err != nil {
		t.Fatal("owned native Capture baseline required", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	want, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(want, data); err != nil {
		t.Fatal(err)
	}
}
