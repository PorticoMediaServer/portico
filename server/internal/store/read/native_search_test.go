package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	profile := searchpolicy.Compiled()
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(4,'episode',1,0,1,1,0)`)
	f.exec(t, `UPDATE catalog_subjects SET kind=CASE id WHEN 1 THEN 2 ELSE 4 END WHERE id IN(1,2,3)`)
	f.exec(t, `UPDATE catalog_entities SET kind=CASE id WHEN 1 THEN 2 ELSE 4 END,title=CASE id WHEN 2 THEN 'Arrival' ELSE 'Matrix' END,retired=0,show_id=CASE id WHEN 1 THEN NULL ELSE 1 END,numbering=CASE id WHEN 1 THEN NULL ELSE 'seasonal' END,episode_number=CASE id WHEN 1 THEN NULL ELSE id END WHERE id IN(1,2,3)`)
	f.exec(t, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms,command_incarnation) VALUES(1,'v1',?,?,?,'active',0,zeroblob(16))`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint)
	f.exec(t, `INSERT INTO search_revision(singleton,revision,active_generation_id,input_sequence) VALUES(1,1,1,0)`)
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(1,'person','person','Keanu','keanu',zeroblob(16),x'11111111111111111111111111111111')`)
	f.exec(t, `INSERT INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting')`)
	f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT 1,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=2`)
	f.exec(t, `UPDATE catalog_entities SET credit_image_id=1 WHERE id=2`)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1,2,0,1,1,2,'Original appearance','test','credit',0)`)
	nativeFixtureImage(t, f, search.ManifestEntity, 1, 1, []nativeFixtureField{{1, "Matrix"}, {4, "Alternate source"}})
	nativeFixtureImage(t, f, search.ManifestEntity, 2, 2, []nativeFixtureField{{1, "Arrival"}, {6, "Original appearance"}})
	nativeFixtureImage(t, f, search.ManifestEntity, 3, 3, []nativeFixtureField{{1, "Matrix"}})
	nativeFixtureImage(t, f, search.ManifestPerson, 1, 1001, []nativeFixtureField{{1, "Keanu"}})
	vector := nativeSiblingVector(t)
	f.exec(t, `UPDATE catalog_entities SET title='Independent own',retired=0 WHERE id=4`)
	nativeFixtureImage(t, f, search.ManifestEntity, 4, 4, []nativeFixtureField{{1, "Independent own"}, {4, vector.Source}})

	seedNativeAutomatic(t, f)
	seedNativeAlbums(t, f)
	f.exec(t, "COMMIT")
}

type nativeFixtureField struct {
	field uint16
	value string
}

func nativeFixtureImage(t *testing.T, f *browseFixture, subject search.ManifestSubject, id, publication int64, fields []nativeFixtureField) {
	t.Helper()
	profile := searchpolicy.Compiled()
	var title string
	var kind, library, revision int64
	var inc []byte
	if subject == search.ManifestEntity {
		if err := f.conn.QueryRowContext(t.Context(), `SELECT title,kind,library_id,revision,metadata_incarnation FROM catalog_entities WHERE id=?`, id).Scan(&title, &kind, &library, &revision, &inc); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := f.conn.QueryRowContext(t.Context(), `SELECT name,revision,search_incarnation FROM catalog_people WHERE id=?`, id).Scan(&title, &revision, &inc); err != nil {
			t.Fatal(err)
		}
	}
	var rows []search.ManifestRow
	for _, field := range fields {
		fragments, err := search.PrepareFragments(field.value, search.DefaultFragmentBudget())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < fragments.Len(); i++ {
			rows = append(rows, fragments.At(i).ManifestRow(field.field, int64(i), fmt.Sprintf("revision-field-%d", field.field)))
		}
	}
	var incarnation [16]byte
	copy(incarnation[:], inc)
	manifest, err := search.PrepareManifest(search.ManifestInput{Subject: subject, SubjectID: id, LibraryID: library, Kind: kind, SearchGenerationID: 1, BankKey: "v1", EngineVersion: profile.EngineVersion, UnicodeFingerprint: profile.UnicodeFingerprint, Recipe: profile.RecipeVersion, InputGeneration: 1, SourceRevision: revision, SourceIncarnation: incarnation}, rows)
	if err != nil {
		t.Fatal(err)
	}
	prepared := search.PrepareTitle(title)
	personTokens := ""
	if subject == search.ManifestPerson {
		personTokens = search.PreparePersonName(title)
	}
	var entity, person, lib, k any
	if subject == search.ManifestEntity {
		entity, lib, k = id, library, kind
	} else {
		person = id
	}
	f.exec(t, `INSERT INTO search_publications(id,entity_id,person_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title,title_fold,title_prefix_tokens,initial_prefix_tokens,person_name_tokens) VALUES(?,?,?,1,?,?,1,?,?,?,?,?,?,?,?)`, publication, entity, person, lib, k, inc, revision, profile.RecipeVersion, title, prepared.Fold(), prepared.PrefixTokens(), prepared.InitialTokens(), personTokens)
	// Birth values are explicit fixture inputs so ANALYZE samples are repeatable.
	var jobBirth [16]byte
	binary.BigEndian.PutUint64(jobBirth[8:], uint64(publication))
	f.exec(t, `INSERT INTO search_publish_jobs(entity_id,person_id,search_generation_id,published_generation,published_document_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation) VALUES(?,?,1,1,?,1,?,?,?,'idle',?)`, entity, person, publication, inc, revision, profile.RecipeVersion, jobBirth[:])
	for i, row := range rows {
		fragment := (publication-1)*1000 + int64(i) + 1
		f.exec(t, `INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,?,?,?,?,?,?,?,?,1,1,?)`, fragment, entity, person, row.Field, row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized, publication)
		f.exec(t, `INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, fragment, row.Prefix)
		f.exec(t, `INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, fragment, row.Lexical)
		f.exec(t, `INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, fragment, row.Normalized)
	}
	seed, digest := manifest.Seed(), manifest.Digest()
	f.exec(t, `INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,verified_fragments,verified_bytes,chain,sealed) VALUES(?,?,?,?,?,?,?,?,?,?,?,1)`, publication, search.ManifestAlgorithm, seed[:], digest[:], manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), digest[:])
	if subject == search.ManifestEntity {
		f.exec(t, `INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, id, prepared.PrefixTokens(), prepared.InitialTokens())
	} else {
		f.exec(t, `INSERT INTO catalog_people_names(rowid,name) VALUES(?,?)`, id, search.PreparePersonName(title))
	}
}

// A legal legacy R21 accepted-evidence cohort. It supplies no new writer
// receipt authority: these reads intentionally preserve the adopted view.
func seedNativeAutomatic(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(10,'collection',0,1,1,1,0)`)
	f.exec(t, `UPDATE catalog_subjects SET kind=10 WHERE id IN(10,11,12)`)
	f.exec(t, `UPDATE catalog_entities SET kind=10,title=CASE id WHEN 10 THEN 'Shared saga' WHEN 11 THEN 'Provider saga' ELSE 'Authored group' END,collection_name_key=CASE id WHEN 10 THEN 'shared saga' WHEN 11 THEN 'provider saga' ELSE 'authored group' END,retired=0 WHERE id IN(10,11,12)`)
	f.exec(t, `UPDATE catalog_entities SET title=CASE id WHEN 5 THEN 'Local work' WHEN 6 THEN 'Included work' WHEN 7 THEN 'Provider work' ELSE 'Excluded local' END,automatic_collection_name=CASE WHEN id IN(5,8) THEN 'Shared saga' ELSE '' END,metadata_language='en',retired=0 WHERE id IN(5,6,7,8)`)
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'nfo','shared saga',10),(1,'test','other',10),(1,'tmdb','provider',11)`)
	f.exec(t, `INSERT INTO catalog_automatic_member_overrides(collection_id,item_id,present) VALUES(10,6,1),(10,8,0)`)
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(12,6,'a')`)
	f.exec(t, `UPDATE libraries SET metadata_language='en' WHERE id='movies'`)
	f.exec(t, `INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1)`)
	f.exec(t, `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(1,'tmdb',zeroblob(16),1,1,0,'','fixture','fill_missing')`)
	f.exec(t, `INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(1,'tmdb','movie','m1','en','','item',zeroblob(16),1)`)
	f.exec(t, `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed) VALUES('evidence1',1,'fixture','tmdb','movie','m1','en','','item',printf('%064d',1),'2026-11-06T00:00:00Z','fixture',1)`)
	f.exec(t, `INSERT INTO metadata_evidence_collections(evidence_id,provider,evidence_kind,collection_kind,collection_id,name) VALUES('evidence1','tmdb','movie','collection','provider','Provider saga')`)
	f.exec(t, `INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,source_content_revision,command_incarnation) SELECT 'operation1',id,'tmdb','idle','applied',1,'','2026-11-07T00:00:00Z',metadata_incarnation,1,1,1,1,1,1,zeroblob(16),1,0,'fixture','fill_missing',title,'',0,'fixture','fixture','m1',0,0,'fixture','2026-11-06T00:00:00Z',1,zeroblob(16) FROM catalog_entities WHERE id=7`)
	f.exec(t, `INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,selection_revision,generation,decision,observed_at) VALUES('publication1','operation1',7,'tmdb','movie','m1','evidence1',1,1,'accepted','2026-11-06T00:00:00Z')`)
	f.exec(t, `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,accepted_evidence_id,accepted_publication_id,status,observed_at) VALUES(7,'tmdb','primary',zeroblob(16),1,'movie','m1','evidence1','publication1','matched','2026-11-06T00:00:00Z')`)
	f.exec(t, `INSERT INTO metadata_field_origins(subject_kind,provider_id,entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id,evidence_id) VALUES('movie','m1',7,'relations','tmdb','provider','fixture',1.0,'2026-11-06T00:00:00Z','en','',1,'publication1','evidence1')`)
	for _, v := range []struct {
		id    int64
		title string
	}{{5, "Local work"}, {6, "Included work"}, {7, "Provider work"}, {8, "Excluded local"}, {10, "Shared saga"}, {11, "Provider saga"}, {12, "Authored group"}} {
		nativeFixtureImage(t, f, search.ManifestEntity, v.id, v.id, []nativeFixtureField{{1, v.title}})
	}
}

func seedNativeAlbums(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`)
	f.exec(t, `UPDATE catalog_subjects SET kind=CASE id WHEN 20 THEN 6 WHEN 22 THEN 5 ELSE 7 END WHERE id IN(20,21,22,23)`)
	f.exec(t, `UPDATE catalog_entities SET kind=CASE id WHEN 20 THEN 6 WHEN 22 THEN 5 ELSE 7 END,title=CASE id WHEN 20 THEN 'Moonlight collection' WHEN 21 THEN 'First track' WHEN 22 THEN 'One Artist' ELSE 'Later track' END,album_id=CASE WHEN id IN(21,23) THEN 20 ELSE NULL END,artist_id=CASE id WHEN 20 THEN 22 ELSE NULL END,disc_number=CASE id WHEN 21 THEN 1 WHEN 23 THEN 2 ELSE NULL END,track_number=CASE WHEN id IN(21,23) THEN 1 ELSE NULL END,retired=0 WHERE id IN(20,21,22,23)`)
	f.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(21,22)`)
	for _, v := range []struct {
		id    int64
		title string
	}{{20, "Moonlight collection"}, {21, "First track"}, {22, "One Artist"}, {23, "Later track"}} {
		nativeFixtureImage(t, f, search.ManifestEntity, v.id, v.id, []nativeFixtureField{{1, v.title}})
	}
}

type nativePairVector struct {
	Source, Query    string
	PrefixQueryToken string `json:"prefix_query_token"`
}

func nativeSiblingVector(t *testing.T) nativePairVector {
	t.Helper()
	data, err := os.ReadFile("../search/testdata/read-pairing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector nativePairVector
	if err = json.Unmarshal(data, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}
func nativeFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("native_search_test.go")
	if err != nil {
		t.Fatal(err)
	}
	a, b := bytes.Index(data, []byte("func seedNativeSearch(")), bytes.Index(data, []byte("func nativeFixtureHash("))
	if a < 0 || b <= a {
		t.Fatal("missing native seed journal")
	}
	vector, err := os.ReadFile("../search/testdata/read-pairing.json")
	if err != nil {
		t.Fatal(err)
	}
	journal := append([]byte(browsePlanFixtureHash(t)), data[a:b]...)
	journal = append(journal, vector...)
	return fmt.Sprintf("%x", sha256.Sum256(journal))
}

// Scenario fixtures additionally bind their exact source body. The shared
// plan golden remains tied to the unchanged base seed journal only.
func nativeScenarioFixtureHash(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("native_search_test.go")
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("func " + strings.Split(t.Name(), "/")[0] + "(")
	a := bytes.Index(raw, marker)
	if a < 0 {
		t.Fatal("missing native scenario source")
	}
	b := len(raw)
	if next := bytes.Index(raw[a+len(marker):], []byte("\nfunc ")); next >= 0 {
		b = a + len(marker) + next
	}
	journal := append([]byte(nativeFixtureHash(t)), raw[a:b]...)
	return fmt.Sprintf("%x", sha256.Sum256(journal))
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
	}{{"matrix", []int64{3, 1, 2}, []uint8{0, 0, 5}}, {"matrix arrival", []int64{2}, []uint8{5}}, {"keanu", []int64{1, 2}, []uint8{3, 5}}, {"matrix keanu", []int64{2}, []uint8{5}}, {"original appearance", []int64{2}, []uint8{4}}, {"shared saga", []int64{10, 6, 5}, []uint8{0, 5, 5}}, {"provider saga", []int64{11, 7}, []uint8{0, 5}}, {"authored group", []int64{12, 6}, []uint8{0, 5}}, {"moonlight collection", []int64{20, 23, 21}, []uint8{0, 5, 5}}, {"one artist", []int64{22, 23, 21, 20}, []uint8{0, 5, 5, 5}}} {
		var cursor *FullSearchCursor
		done := false
		for n := 0; n < 100; n++ {
			page, cost, err := nativeRead(t, f, r, v.query, cursor)
			if err != nil {
				t.Fatalf("%s step%d: %v cost%+v", v.query, n, err, cost)
			}
			if cost == nil || len(cost.LookupWindows) != 1 || len(cost.PublicationWindows) != 1 || cost.PublicationStatements < 7 || len(page.Hits) > 0 && cost.PublicationStatements <= 7 {
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
				if hit.Subject == search.ManifestEntity && hit.Kind <= 0 {
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

func TestNativeSearchPreviewTheReturnsPartial(t *testing.T) {
	f := newNativeSource(t)
	for id := int64(30); id <= 50; id++ {
		title := fmt.Sprintf("The story %d", id)
		f.exec(t, `UPDATE catalog_entities SET title=?,sort_key=?,retired=0 WHERE id=?`, title, strings.ToLower(title), id)
		nativeFixtureImage(t, f, search.ManifestEntity, id, 1000+id, []nativeFixtureField{{1, title}})
	}
	fixture := nativeScenarioFixtureHash(t)
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, fixture)
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	query, err := search.PlanQuery("the", false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("preview-the")
	request.Budget.RawRows = 4096
	var page FullSearchPage
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, Preview: true, CandidateBudget: 1}, query)
		if e != nil {
			return e
		}
		if page.Disposition != SearchResults || !page.Partial || page.Exhausted || len(page.Hits) == 0 || page.NativeCandidates != 1 || page.CountStatus != SearchCountLowerBound {
			t.Fatal("broad preview waited for a census", page)
		}
		if page.Stamp == nil {
			t.Fatal("partial preview lacks fresh custody")
		}
		kept, e := page.Stamp.RetainHits(page.Hits)
		if e != nil {
			return e
		}
		return page.Stamp.ValidateRetained(ctx, out, kept)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeSearchPerHitPolicyRecheckedBeforePublication(t *testing.T) {
	f := newNativeSource(t)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(32,'native-reading-test',0,0,1,1,0)`)
	f.exec(t, `UPDATE catalog_subjects SET kind=32 WHERE id=60`)
	f.exec(t, `UPDATE catalog_entities SET kind=32,title='The proof',source_revision=1,retired=0 WHERE id=60`)
	nativeFixtureImage(t, f, search.ManifestEntity, 60, 2060, []nativeFixtureField{{1, "The proof"}})
	f.exec(t, "COMMIT")
	fixture := nativeScenarioFixtureHash(t)
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, fixture)
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	r.snapshots = &nativeBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=60`) }}
	f.input.Kind = 0
	f.input.Limit = 20
	query, err := search.PlanQuery("the", false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("per-hit-final")
	request.Budget.RawRows = 4096
	var page FullSearchPage
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, Preview: true, CandidateBudget: 1}, query)
		return e
	})
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, FullSearchPage{}) {
		t.Fatal("per-hit witness drift released output", page, err)
	}
}
func TestNativeSearchRefusesUnsupportedAndStaleProofs(t *testing.T) {
	f, r := newNativeFixture(t)
	page, _, err := nativeRead(t, f, r, "matrix", nil)
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

func TestNativeSearchAcceptedSiblingOracle(t *testing.T) {
	f, r := newNativeFixture(t)
	vector := nativeSiblingVector(t)
	q, err := search.PlanQuery(vector.Query, false)
	if err != nil {
		t.Fatal(err)
	}
	scopeQuery, err := newFullQueryScope(q, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	request := readfixture.ReadRequest()
	request.Budget.RawRows = 4096
	var candidates, same, siblings, logical int
	window, err := f.snapshots.ReadForecasted(t.Context(), request, time.Second, governor.SnapshotForecast{NativeRowBytes: f.snapshots.NativeRowReservation(), MaxQueries: 4096}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
		rows, err := query("full.native.fts.search_fragment_prefix.first", "\""+vector.PrefixQueryToken+"\"*", int64(64))
		if err != nil {
			return err
		}
		for _, row := range rows {
			source, field, err := nativePostingSource(query, scopeQuery, 2, row[0].(int64))
			if err != nil {
				return err
			}
			if source == nil || source.key.SubjectID != 4 || field != 4 {
				continue
			}
			candidates++
			physical, err := query("full.native.field.first", source.key.PublicationID, int64(4), int64(64))
			if err != nil {
				return err
			}
			for _, sibling := range physical {
				if strings.Contains(sibling[4].(string), vector.Query) {
					siblings++
					if sibling[0] == row[0] {
						same++
					}
				}
			}
			proof, _, err := nativeLogicalField(scope.Context(), query, scopeQuery, source, field, 0)
			if err != nil {
				return err
			}
			if proof.matched[0] {
				logical++
			}
		}
		return nil
	})
	if err != nil || window.Refusal != nil {
		t.Fatal(err, window.Refusal)
	}
	if candidates != 1 || same != 0 || siblings != 1 || logical != 1 {
		t.Fatal("actual PB-RD-01", candidates, same, siblings, logical)
	}
	var after *FullSearchCursor
	for n := 0; n < 100; n++ {
		page, _, err := nativeRead(t, f, r, vector.Query, after)
		if err != nil {
			t.Fatal(err)
		}
		if page.Disposition == SearchPending {
			after = page.Next
			continue
		}
		if len(page.Hits) != 1 || page.Hits[0].ID != 4 || page.Hits[0].Tier != 4 || !page.Exhausted {
			t.Fatal("sibling result", page)
		}
		return
	}
	t.Fatal("sibling proof did not complete")
}

type nativeBoundarySnapshots struct {
	*governor.Snapshots
	afterBusiness func()
	fired         bool
}

func (s *nativeBoundarySnapshots) ReadForecasted(ctx context.Context, request governor.Request, budget time.Duration, forecast governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return s.Snapshots.ReadForecasted(ctx, request, budget, forecast, func(scope *governor.ReadScope) error {
		err := read(scope)
		if s.afterBusiness != nil && !s.fired {
			s.fired = true
			s.afterBusiness()
		}
		return err
	})
}
func TestNativeSearchPublicationDenialAndRetirement(t *testing.T) {
	for _, v := range []struct {
		name, sql string
		want      error
		cancel    bool
	}{
		{"revoke-with-cancel", `UPDATE authorization_session_families SET revoked=1`, ErrDenied, true},
		{"retire-library", `UPDATE catalog_libraries SET retired=1 WHERE id=1`, ErrDenied, false},
		{"remove-library", `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`, ErrDenied, false},
	} {
		t.Run(v.name, func(t *testing.T) {
			f, r := newNativeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.snapshots = &nativeBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
				f.exec(t, v.sql)
				if v.cancel {
					cancel()
				}
			}}
			query, err := search.PlanQuery("matrix", false)
			if err != nil {
				t.Fatal(err)
			}
			live, request, output := liveRequests("publication")
			request.Budget.RawRows = 4096
			var page FullSearchPage
			_, err = ConsumeForeground(ctx, f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input}, query)
				return e
			})
			if !errors.Is(err, v.want) || !reflect.DeepEqual(page, FullSearchPage{}) {
				t.Fatal("publication leaked", page, err)
			}
		})
	}
}
func TestNativeSearchReadJoinedStamp(t *testing.T) {
	f, r := newNativeFixture(t)
	prior, _, err := nativeRead(t, f, r, "matrix", nil)
	if err != nil || prior.Stamp == nil {
		t.Fatal("first stamp", err)
	}
	query, err := search.PlanQuery("matrix", false)
	if err != nil {
		t.Fatal(err)
	}
	var after *FullSearchCursor
	for n := 0; n < 100; n++ {
		live, request, output := liveRequests("stamp")
		request.Budget.RawRows = 4096
		var page FullSearchPage
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, After: after, Retained: []*FullSearchReadStamp{prior.Stamp}}, query)
			if e != nil {
				return e
			}
			if page.Stamp == nil {
				t.Fatal("missing final/next-less stamp")
			}
			if e = page.Stamp.ValidateRetained(ctx, out, prior.Stamp); e != nil {
				t.Fatal("read-joined proof", e)
			}
			if e = prior.Stamp.ValidateRetained(ctx, out, page.Stamp); !errors.Is(e, ErrInput) {
				t.Fatal("prior output grant authorized fresh disclosure", e)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if page.Exhausted {
			if page.Next != nil || page.Stamp == nil {
				t.Fatal("exhausted stamp")
			}
			return
		}
		after = page.Next
	}
	t.Fatal("stamp stream did not finish")
}
func TestNativeSearchPeopleEqualIDBirth(t *testing.T) {
	f, r := newNativeFixture(t)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `DELETE FROM catalog_people WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(1,'replacement','replacement','Keanu','keanu',zeroblob(16),x'22222222222222222222222222222222')`)
	f.exec(t, "COMMIT")
	page, _, err := nativeRead(t, f, r, "keanu", nil)
	if err != nil || page.Disposition != SearchPending || len(page.Hits) != 0 || page.Exhausted {
		t.Fatal("old People publication rebased onto equal ID", page, err)
	}
}

func TestNativeSearchUsesCapturedDomainBytePairs(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			f := newNativeSource(t)
			f.exec(t, "BEGIN")
			f.exec(t, "PRAGMA defer_foreign_keys=ON")
			f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(33,'native-byte-test',0,0,1,1,0)`)
			f.exec(t, `UPDATE catalog_subjects SET kind=33 WHERE id=55`)
			f.exec(t, `UPDATE catalog_entities SET kind=33,title='The pair',retired=0 WHERE id=55`)
			nativeFixtureImage(t, f, search.ManifestEntity, 55, 2055, []nativeFixtureField{{1, "The pair"}})
			f.exec(t, "COMMIT")
			defs := NativeFullSearchDefinitions()
			if complete {
				defs = append(defs, TitleFactsDefinitions()...)
			}
			f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false, nativeScenarioFixtureHash(t))
			r, err := NewNativeFullSearchReader(f.snapshots)
			if err != nil {
				t.Fatal(err)
			}
			f.input.Kind = 0
			f.input.Limit = 20
			query, err := search.PlanQuery("the", false)
			if err != nil {
				t.Fatal(err)
			}
			live, request, output := liveRequests("native-byte-pair")
			request.Budget.RawRows = 4096
			var page FullSearchPage
			var cost *BrowseReadResult
			_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, cost, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, Preview: true, CandidateBudget: 1}, query)
				return e
			})
			if !complete {
				if err == nil || !reflect.DeepEqual(page, FullSearchPage{}) {
					t.Fatal("missing domain catalogue released output", page, err)
				}
				return
			}
			if err != nil || len(page.Hits) != 1 || page.Hits[0].ID != 55 {
				t.Fatal("captured byte-pair domain", page, err)
			}
			// Seven common native fences, then item + kind + the two physical
			// octet/payload attempts. The logical pair must not be counted as one.
			if cost == nil || cost.PublicationStatements != 11 {
				t.Fatal("paired attempt evidence", cost)
			}
		})
	}
}

func nativePreviewFixture(t *testing.T) (*browseFixture, *NativeFullSearchReader) {
	t.Helper()
	f := newNativeSource(t)
	for id := int64(30); id <= 50; id++ {
		title := fmt.Sprintf("The story %d", id)
		f.exec(t, `UPDATE catalog_entities SET title=?,sort_key=?,retired=0 WHERE id=?`, title, strings.ToLower(title), id)
		nativeFixtureImage(t, f, search.ManifestEntity, id, 1000+id, []nativeFixtureField{{1, title}})
	}
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, nativeScenarioFixtureHash(t))
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	return f, r
}

func nativePreviewRead(t *testing.T, f *browseFixture, r *NativeFullSearchReader, text string, after *FullSearchCursor, budget int) (FullSearchPage, error) {
	t.Helper()
	query, err := search.PlanQuery(text, false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("preview-" + text)
	request.Budget.RawRows = 4096
	var page FullSearchPage
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, After: after, Preview: true, CandidateBudget: budget}, query)
		return e
	})
	return page, err
}

// A preview cursor holds native posting positions for ONE query; replaying it
// with another query must be refused exactly like the exact path.
func TestNativeSearchPreviewCursorIsBoundToItsQuery(t *testing.T) {
	f, r := nativePreviewFixture(t)
	first, err := nativePreviewRead(t, f, r, "the", nil, 0)
	if err != nil || first.Next == nil || first.Next.native == nil || !first.Next.native.preview {
		t.Fatal("preview setup must leave a continuation", first, err)
	}
	if _, err = nativePreviewRead(t, f, r, "the", first.Next, 0); err != nil {
		t.Fatal("same-query preview continuation", err)
	}
	for _, other := range []string{"story", "the story"} {
		page, err := nativePreviewRead(t, f, r, other, first.Next, 0)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(page, FullSearchPage{}) {
			t.Fatal("preview cursor replayed with another query", other, page, err)
		}
	}
	// The exact path already refuses the same replay; keep both aligned.
	pending, _, err := nativeRead(t, f, r, "matrix", nil)
	if err != nil || pending.Next == nil {
		t.Fatal("exact setup", err)
	}
	if _, _, err = nativeRead(t, f, r, "keanu", pending.Next); !errors.Is(err, ErrStale) {
		t.Fatal("exact cursor replayed with another query", err)
	}
}

func TestNativeSearchRefusesCandidateBudgetMisuse(t *testing.T) {
	f, r := nativePreviewFixture(t)
	for _, budget := range []int{NativePreviewCandidates + 1, -1} {
		page, err := nativePreviewRead(t, f, r, "the", nil, budget)
		if !errors.Is(err, ErrInput) || !reflect.DeepEqual(page, FullSearchPage{}) {
			t.Fatal("preview candidate budget outside 0..2000 accepted", budget, page, err)
		}
	}
	query, err := search.PlanQuery("the", false)
	if err != nil {
		t.Fatal(err)
	}
	live, request, output := liveRequests("exact-budget")
	request.Budget.RawRows = 4096
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		_, _, e := r.Read(ctx, request, time.Second, out, FullSearchInput{View: f.input, CandidateBudget: 1}, query)
		return e
	})
	if !errors.Is(err, ErrInput) {
		t.Fatal("exact read accepted a preview candidate budget", err)
	}
	// The 2,000 allowance is shared across polls: a continuation that already
	// spent it has nothing left.
	first, err := nativePreviewRead(t, f, r, "the", nil, 0)
	if err != nil || first.Next == nil || first.Next.native == nil {
		t.Fatal("preview setup", err)
	}
	spent := *first.Next
	spent.native = cloneNativeContinuation(first.Next.native)
	spent.native.candidates = NativePreviewCandidates
	if _, err = nativePreviewRead(t, f, r, "the", &spent, 0); !errors.Is(err, ErrInput) {
		t.Fatal("exhausted preview allowance accepted", err)
	}
	spent.native.candidates = NativePreviewCandidates + 1
	if _, err = nativePreviewRead(t, f, r, "the", &spent, 0); !errors.Is(err, ErrStale) {
		t.Fatal("over-budget cursor accepted", err)
	}
}

func TestNativeSearchRetainedLibrariesShareOneOperation(t *testing.T) {
	f := newNativeSource(t)
	f.exec(t, "BEGIN")
	f.exec(t, "PRAGMA defer_foreign_keys=ON")
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	for _, id := range []int64{60, 1040} {
		f.exec(t, `UPDATE catalog_entities SET title='Sharedproof',retired=0 WHERE id=?`, id)
		nativeFixtureImage(t, f, search.ManifestEntity, id, id, []nativeFixtureField{{1, "Sharedproof"}})
	}
	f.exec(t, "COMMIT")
	f = composeReadFixture(t, f, NativeFullSearchDefinitions(), readfixture.LiveConfig(time.Now), false, nativeScenarioFixtureHash(t))
	r, err := NewNativeFullSearchReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Kind = 0
	f.input.Limit = 20
	query, err := search.PlanQuery("sharedproof", false)
	if err != nil {
		t.Fatal(err)
	}
	read := func(library int64, retained *FullSearchReadStamp) (FullSearchPage, error) {
		view := f.input
		view.Library = library
		in := FullSearchInput{View: view, Preview: true}
		if retained != nil {
			in.Retained = []*FullSearchReadStamp{retained}
		}
		live, request, output := liveRequests("multiple-libraries")
		request.Budget.RawRows = 4096
		var page FullSearchPage
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.Read(ctx, request, time.Second, out, in, query)
			if e == nil && retained != nil {
				e = page.Stamp.ValidateRetained(ctx, out, retained)
			}
			return e
		})
		return page, err
	}
	first, err := read(1, nil)
	if err != nil || len(first.Hits) != 1 || first.Hits[0].ID != 60 {
		t.Fatal("first library", first, err)
	}
	prior, err := first.Stamp.RetainHits(first.Hits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := read(2, prior)
	if err != nil || len(second.Hits) != 1 || second.Hits[0].ID != 1040 {
		t.Fatal("second library", second, err)
	}
	mixed, err := second.Stamp.RetainHits(append(first.Hits, second.Hits...))
	if err != nil || len(mixed.policies) != 2 {
		t.Fatal("mixed issued evidence", err)
	}
	kept, err := mixed.RetainHits(second.Hits)
	if err != nil || len(kept.libraries) != 1 || kept.libraries[0].libraryID != 2 {
		t.Fatal("narrow library evidence", err)
	}
	r.snapshots = &nativeBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_libraries SET retired=1 WHERE id=1`) }}
	denied, err := read(2, mixed)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(denied, FullSearchPage{}) {
		t.Fatal("retained library retired before release", denied, err)
	}
	r.snapshots = f.snapshots
	if _, err = read(2, kept); err != nil {
		t.Fatal("discarded library still validated", err)
	}
}
