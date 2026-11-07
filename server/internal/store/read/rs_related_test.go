package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func seedRSRelated(t *testing.T, f *browseFixture) {
	t.Helper()
	f.exec(t, `UPDATE catalog_entities SET retired=0,admission_own_age=10,community_rating_key=7,year=2000 WHERE id<=12`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(10,'collection',0,1,1,1,0)`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1)`)
	for _, v := range []struct{ id, kind int64 }{{2100, 7}, {2101, 9}} {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(v.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, v.id, public, v.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,album_id,book_id) SELECT id,public_id,library_id,kind,'Part','part','p','part','p',0,public_id,public_id,1,1 FROM catalog_subjects WHERE id=?`, v.id)
	}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2000,x'd0070000000000000000000000000000',1,10)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'Collection','collection','c','collection','c',0,public_id,public_id FROM catalog_subjects WHERE id=2000`)
	f.exec(t, `INSERT INTO catalog_terms(id,vocab,key,label) VALUES(1,1,'genre','Genre'),(2,8,'keyword','Keyword')`)
	f.exec(t, `INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(1,1),(1,2),(2,1),(2,2),(3,1),(4,1),(5,1),(6,1),(7,1),(8,1),(9,1),(10,1)`)
	// Local, NFO and owner sources share normalized identity without a provider ID.
	f.exec(t, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,source_name) VALUES(1,1,'nfo','g','Genre'),(2,1,'owner','g','Genre')`)
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(1,'p1','local:1','Person','person',zeroblob(16))`)
	f.exec(t, `INSERT INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting')`)
	for id := int64(1); id <= 3; id++ {
		f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT ?,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=?`, id, id)
		for ord := int64(0); ord < 3; ord++ {
			var person any
			providerID := ""
			name := "Owner name"
			if ord == 0 {
				person = int64(1)
				name = "Person"
			}
			if ord == 1 {
				providerID = "provider-person"
				name = "Provider person"
			}
			f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,provider_person_id,source_ordinal) VALUES(?,?,?,?,1,2,?,'nfo',?,?,?)`, id, id, ord, person, name, fmt.Sprint(ord), providerID, ord)
		}
	}
	f.exec(t, `UPDATE catalog_entities SET credit_image_id=id WHERE id IN(1,2)`)
	// Image3 remains off-head, and must not add three matching people to item3.
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(2000,1,'a'),(2000,2,'b'),(2000,4,'c')`)
	f.exec(t, `UPDATE catalog_entities SET parent_id=1,show_id=1,artist_id=1,album_id=1,book_id=1 WHERE id=11`)
	f.exec(t, `UPDATE catalog_entities SET local_key='local-seed' WHERE id IN(1,10)`)
	f.exec(t, `INSERT INTO metadata_entity_external_ids(entity_id,provider,subject_kind,provider_id) VALUES(1,'tmdb','movie','seed'),(8,'tmdb','movie','seed')`)
	f.exec(t, `UPDATE catalog_entities SET community_rating_key=8 WHERE id=3`)
	f.exec(t, `UPDATE catalog_entities SET community_rating_key=9 WHERE id=4`)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=7`)
	for id := int64(1); id <= 12; id++ {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1,1,'mkv','','',1,1,1,1)`, id, fmt.Sprint(id), fmt.Sprintf("/media/%d", id))
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(?,?,1)`, id, id)
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=6`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating) SELECT 'p',public_id,id,library_id,2 FROM catalog_entities WHERE id=5`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating) SELECT 'q',public_id,id,library_id,1 FROM catalog_entities WHERE id=2`)
}
func rsRelatedHash(t *testing.T) string {
	data, err := os.ReadFile("rs_related_test.go")
	if err != nil {
		t.Fatal(err)
	}
	a, b := strings.Index(string(data), "func seedRSRelated("), strings.Index(string(data), "func rsRelatedHash(")
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[a:b]...)))
}
func rsRelatedFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	f := newBrowseSource(t)
	seedRSRelated(t, f)
	gc := readfixture.LiveConfig(time.Now)
	gc.Preparation = &governor.PreparationProfile{MaxActive: 2, MaxBytes: 32 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 32 << 20}
	f = composeReadFixture(t, f, append(browseDefinitions(), RSRelatedDefinitions()...), gc, false, rsRelatedHash(t))
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func rsBegin(t *testing.T, f *browseFixture, in RelatedInput, similar bool) *RelatedWork {
	t.Helper()
	w, refusal, err := BeginRelatedWork(t.Context(), f.authority, 1, governor.JobKey{Domain: 79, ID: "related-job"}, time.Now().Add(12*time.Second), in, similar)
	if err != nil {
		t.Fatal(err, refusal)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}
func rsPoll(t *testing.T, f *browseFixture, r *ForegroundReader, in RelatedInput, w *RelatedWork, similar bool) (RelatedPage, BrowseReadResult, error) {
	t.Helper()
	live, request, output := liveRequests("related-poll")
	var page RelatedPage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var err error
		if similar {
			page, cost, err = r.Similar(ctx, request, time.Second, out, in, w)
		} else {
			page, cost, err = r.Related(ctx, request, time.Second, out, in, w)
		}
		if err == nil {
			assertLiveUsageWithPreparation(t, f)
			if page.Pending && len(page.Rows) != 0 {
				t.Fatal("pending leaked ranked prefix")
			}
		}
		return err
	})
	return page, cost, err
}
func assertLiveUsageWithPreparation(t *testing.T, f *browseFixture) {
	t.Helper()
	if u := f.authority.ForegroundUsage(); u.Live != 1 {
		t.Fatal("consumer lost LIVE", u)
	}
	if m := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]; m < rsRelatedBytes+(8<<20) {
		t.Fatal("unfunded retained state", m)
	}
}
func rsFinish(t *testing.T, f *browseFixture, r *ForegroundReader, in RelatedInput, w *RelatedWork, similar bool) RelatedPage {
	t.Helper()
	for n := 0; n < 100; n++ {
		p, _, err := rsPoll(t, f, r, in, w, similar)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Pending {
			return p
		}
	}
	t.Fatal("preparation failed to progress")
	return RelatedPage{}
}
func TestRSRelatedCompleteRankingAndLocalIdentity(t *testing.T) {
	f, r := rsRelatedFixture(t)
	in := RelatedInput{View: f.input, Item: 1, Windows: 2}
	in.View.Limit = 2
	w := rsBegin(t, f, in, true)
	first, _, err := rsPoll(t, f, r, in, w, true)
	if err != nil || !first.Pending || len(first.Rows) != 0 {
		t.Fatal("partial success", first, err)
	}
	p := rsFinish(t, f, r, in, w, true)
	if len(p.Rows) != 1 || p.Rows[0].Total != 4 || p.Rows[0].Exhausted || p.Rows[0].Next == nil {
		t.Fatalf("exact selected set %+v", p)
	}
	row := p.Rows[0]
	var got []int64
	for _, e := range row.Entries {
		got = append(got, e.ID)
		if e.Title == "" || e.Shared != len(e.Reasons) {
			t.Fatal("missing hydration/explanation", e)
		}
	}
	if !slices.Equal(got, []int64{2, 4}) {
		t.Fatal("distinct facets/quality/stable rank", got)
	}
	// Item2 has two terms, three current local/provider/name credits and collection.
	if row.Entries[0].Shared != 6 {
		t.Fatal("lost current local credit", row.Entries[0])
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	in.Row = &row.Key
	in.After = row.Next
	in.View.Limit = 40
	w = rsBegin(t, f, in, true)
	p = rsFinish(t, f, r, in, w, true)
	got = nil
	for _, e := range p.Rows[0].Entries {
		got = append(got, e.ID)
	}
	if !slices.Equal(got, []int64{3, 9}) || !p.Rows[0].Exhausted || p.Rows[0].Next != nil {
		t.Fatal("continuation skipped or repeated", got, p)
	}
}
func TestRSRelatedMatrixAndCurrentFence(t *testing.T) {
	f, r := rsRelatedFixture(t)
	in := RelatedInput{View: f.input, Item: 1, Windows: 16}
	in.View.Limit = 40
	w := rsBegin(t, f, in, false)
	p := rsFinish(t, f, r, in, w, false)
	if len(p.Rows) != 5 || p.Rows[0].Key.Kind != 0 {
		t.Fatalf("matrix %+v", p)
	}
	for _, row := range p.Rows {
		if row.Total != len(row.Entries) || !row.Exhausted {
			t.Fatal("incomplete matrix")
		}
	}
	for _, row := range p.Rows {
		if row.Key.Kind == 1 && row.Key.ID == 1 {
			if row.Entries[0].ID != 9 {
				t.Fatal("nearest-year descending tie", row)
			}
		}
		if row.Key.Kind == 3 {
			var got []int64
			for _, e := range row.Entries {
				got = append(got, e.ID)
			}
			if !slices.Equal(got, []int64{2, 4}) {
				t.Fatal("collection year/title order", got)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	in.Windows = 1
	w = rsBegin(t, f, in, false)
	p, _, err := rsPoll(t, f, r, in, w, false)
	if err != nil || !p.Pending {
		t.Fatal(err, p)
	}
	f.exec(t, `UPDATE viewer_revisions SET revision=revision+1 WHERE profile_id='p'`)
	// The shared revision row may be absent until a personal command creates it;
	// a direct source revision change must independently invalidate this work.
	f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=1`)
	p, _, err = rsPoll(t, f, r, in, w, false)
	if !errorsIsRelatedStale(err) || !reflect.DeepEqual(p, RelatedPage{}) {
		t.Fatal("stale prefix escaped", err, p)
	}
}
func errorsIsRelatedStale(err error) bool {
	return err != nil && (strings.Contains(err.Error(), ErrStale.Error()) || strings.Contains(err.Error(), governor.ErrPreparationStale.Error()))
}

func TestRSRelatedDenialAndLeaseInvalidation(t *testing.T) {
	for _, which := range []string{"denial", "source", "lease"} {
		t.Run(which, func(t *testing.T) {
			f, r := rsRelatedFixture(t)
			in := RelatedInput{View: f.input, Item: 1, Windows: 1}
			w := rsBegin(t, f, in, false)
			if which == "lease" {
				if err := f.authority.InterruptPreparations(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
					if which == "denial" {
						f.exec(t, `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`)
					} else {
						f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=1`)
					}
				}}
			}
			p, _, err := rsPoll(t, f, r, in, w, false)
			if err == nil || !reflect.DeepEqual(p, RelatedPage{}) {
				t.Fatal("unsuccessful publication leaked", p, err)
			}
			if which == "denial" && !strings.Contains(err.Error(), ErrDenied.Error()) {
				t.Fatal("denial precedence", err)
			}
		})
	}
}
func TestRSRelatedActualBytesAndNoProviderRequirement(t *testing.T) {
	f, r := rsRelatedFixture(t)
	f.exec(t, `UPDATE catalog_credits SET provider_person_id=''`)
	title := "é\x00界" + strings.Repeat("界", 33)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=2`, title)
	in := RelatedInput{View: f.input, Item: 1, Windows: 16}
	w := rsBegin(t, f, in, true)
	exact := int64(32 + len(title))
	found := false
	var p RelatedPage
	for n := 0; n < 100; n++ {
		page, cost, err := rsPoll(t, f, r, in, w, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, window := range cost.LookupWindows {
			// The governor now includes its fixed row-decode overhead in the
			// native reservation, alongside the exact variable-row octets.
			if window.NativeReservedBytes == exact+64+64*int64(browseReaderConfig().ReadLimits.Columns) {
				found = true
			}
		}
		if !page.Pending {
			p = page
			break
		}
	}
	if p.Pending || len(p.Rows) != 1 || len(p.Rows[0].Entries) == 0 || p.Rows[0].Entries[0].Title != title || !found {
		t.Fatal("actual octets/local identity", p, found, exact)
	}
}
func TestRSRelatedRawTailAndCapacityRefusal(t *testing.T) {
	f, r := rsRelatedFixture(t)
	f.exec(t, `INSERT INTO catalog_terms(id,vocab,key,label) VALUES(3,1,'broad','Broad')`)
	f.exec(t, `INSERT INTO catalog_entity_terms(entity_id,term_id) SELECT id,3 FROM catalog_entities WHERE id=1 OR id BETWEEN 20 AND 90`)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id BETWEEN 20 AND 89`)
	// Item90 lies beyond a full raw reverse window and has available media.
	f.exec(t, `UPDATE catalog_entities SET retired=0,admission_own_age=10 WHERE id=90`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(90,12,1)`)
	in := RelatedInput{View: f.input, Item: 1, Windows: 16}
	in.View.Limit = 40
	w := rsBegin(t, f, in, true)
	p := rsFinish(t, f, r, in, w, true)
	var ids []int64
	for _, e := range p.Rows[0].Entries {
		ids = append(ids, e.ID)
	}
	if !slices.Contains(ids, 90) {
		t.Fatal("source prefix became success", ids)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// A completely full external-ID comparison with no match has no exact
	// exhaustion witness; it must refuse, rather than guess non-equivalence.
	for n := 0; n < 64; n++ {
		f.exec(t, `INSERT INTO metadata_entity_external_ids(entity_id,provider,subject_kind,provider_id) VALUES(1,'local','movie',?)`, fmt.Sprint(n))
	}
	w = rsBegin(t, f, in, true)
	for n := 0; n < 100; n++ {
		p, _, err := rsPoll(t, f, r, in, w, true)
		if err != nil {
			if !strings.Contains(err.Error(), ErrRelatedCapacity.Error()) || !reflect.DeepEqual(p, RelatedPage{}) {
				t.Fatal("wrong refusal", p, err)
			}
			return
		}
		if !p.Pending {
			t.Fatal("truncated identity success", p)
		}
	}
	t.Fatal("no capacity outcome")
}

func TestRSRelatedTypedAvailabilityBeyondRawWindow(t *testing.T) {
	for _, recipe := range []struct {
		kind, leaf, seedChild int64
		column                string
	}{{6, 7, 2100, "album_id"}, {8, 9, 2101, "book_id"}} {
		t.Run(recipe.column, func(t *testing.T) {
			f, r := rsRelatedFixture(t)
			f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1)`)
			f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2300,randomblob(16),1,5)`)
			f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'Artist','artist','a','artist','a',0,randomblob(16),randomblob(16) FROM catalog_subjects WHERE id=2300`)
			for _, id := range []int64{3000, 3001} {
				f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,randomblob(16),1,?)`, id, recipe.kind)
				f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) SELECT id,public_id,library_id,kind,'Work','work','w','work','w',0,randomblob(16),randomblob(16),2300 FROM catalog_subjects WHERE id=?`, id)
				f.exec(t, `INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(?,1)`, id)
			}
			// These typed children deliberately have no generic parent_id edge.
			f.exec(t, "UPDATE catalog_entities SET "+recipe.column+"=3000 WHERE id=?", recipe.seedChild)
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(?,11,1)`, recipe.seedChild)
			for id := int64(3100); id <= 3165; id++ {
				f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,randomblob(16),1,?)`, id, recipe.leaf)
				f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,`+recipe.column+`) SELECT id,public_id,library_id,kind,'Part','part','p','part','p',0,randomblob(16),randomblob(16),3001 FROM catalog_subjects WHERE id=?`, id)
			}
			f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(3165,12,1)`)
			in := RelatedInput{View: f.input, Item: 3000, Windows: 64}
			in.View.Kind = recipe.kind
			w := rsBegin(t, f, in, true)
			p := rsFinish(t, f, r, in, w, true)
			if len(p.Rows) != 1 || p.Rows[0].Total != 1 || len(p.Rows[0].Entries) != 1 || p.Rows[0].Entries[0].ID != 3001 {
				t.Fatal("typed source tail lost", p)
			}
		})
	}
}

func TestRSRelatedMatrixCountsOnlyNonemptyRows(t *testing.T) {
	row := make([]any, 33)
	row[0] = int64(2)
	row[1] = make([]byte, 16)
	row[3] = int64(1)
	row[5] = int64(1)
	row[8] = int64(2000)
	row[9] = float64(7)
	c := &relatedCandidate{entity: relatedEntity{row}, visible: true, bits: [4]uint64{4}}
	s := relatedState{roots: []relatedEntity{{row}}, features: []relatedFeature{{key: RelatedKey{Kind: 1, ID: 1}}, {key: RelatedKey{Kind: 1, ID: 2}}, {key: RelatedKey{Kind: 1, ID: 3}}}, candidates: map[int64]*relatedCandidate{2: c}, cards: map[int64]string{2: "Title"}}
	rows, err := relatedRows(&s, RelatedInput{View: BrowseInput{Limit: 1}, Item: 1}, false)
	if err != nil || len(rows) != 2 || rows[1].Key.ID != 3 || len(rows[1].Entries) != 1 {
		t.Fatal("empty source rows consumed presentation slots", rows, err)
	}
}
