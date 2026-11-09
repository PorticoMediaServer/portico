package read

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ipHistorySeed adds one owner with an explicit locked field, twelve typed
// history entries (with before/after values) and one person owner whose only
// visible credit is a live movie in the admitted library. History ids are
// sequential from 1 because this fixture is fresh.
var ipHistorySeed = []string{
	`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2001,x'000000000000000000000000000007d1',1,2),(2003,x'000000000000000000000000000007d3',1,2),(2005,x'000000000000000000000000000007d5',1,1)`,
	`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2001,x'000000000000000000000000000007d1',1,2,'IP Show','ip show','IP Show','ip show','IP Show',2001,zeroblob(16),zeroblob(16)),(2003,x'000000000000000000000000000007d3',1,2,'History Show','history show','History Show','history show','History Show',2003,zeroblob(16),zeroblob(16)),(2005,x'000000000000000000000000000007d5',1,1,'Person Film','person film','Person Film','person film','Person Film',2005,zeroblob(16),zeroblob(16))`,
	`INSERT INTO metadata_edit_owners(entity_id,kind,revision,command_incarnation) VALUES(2001,'show',2,zeroblob(16))`,
	`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('show',2001,'description','Owner text','Automatic text',1,'owner','2026-11-01T00:00:00Z')`,
	`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(1,'person-token','person-key','Person One','Person One',zeroblob(16)),(2,'orphan-token','orphan-key','Person Two','Person Two',zeroblob(16))`,
	`INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT 2005,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=2005`,
	`INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(2005,2005,0,1,1,1,'Person One','fixture','cast',0)`,
	`UPDATE catalog_entities SET credit_image_id=2005 WHERE id=2005`,
	`INSERT INTO metadata_edit_owners(person_id,kind,revision,command_incarnation) VALUES(1,'person',1,zeroblob(16))`,
	`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('person',1,'biography','Person biography','Automatic biography',0,'owner','2026-11-01T00:00:00Z')`,
}

func ipHistorySource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	for _, q := range ipHistorySeed {
		f.exec(t, q)
	}
	for i := 0; i < 12; i++ {
		f.exec(t, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2001,'1','2','edit','owner','2026-11-01T00:00:00Z','{}','{}')`)
	}
	f.exec(t, `INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) SELECT id,'description','Old',1,'Old automatic',1,0,'New',1,'New automatic',1,1 FROM metadata_owner_history WHERE kind='show' AND entity_id=2001`)
	// A history-only subject with no owner row proves history is independent.
	for i := 0; i < 2; i++ {
		f.exec(t, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2003,'1','1','automatic','provider','2026-11-01T00:00:00Z','{}','{}')`)
	}
	return f
}

func ipHistoryFixtureOn(t *testing.T, source *browseFixture) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, source, append(browseDefinitions(), append(IPHistoryHarnessDefinitions(), IPSourceHarnessDefinitions()...)...))
	return f, &ForegroundReader{snapshots: f.snapshots, pool: f.snapshots}
}

func ipHistoryFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	return ipHistoryFixtureOn(t, ipHistorySource(t))
}

func ipHistoryInput(f *browseFixture) IPHistoryInput {
	v := f.input
	v.Kind = 2
	return IPHistoryInput{View: v, Kind: "show", Entity: 2001}
}

func TestIPHistoryOwnerAndFields(t *testing.T) {
	f, r := ipHistoryFixture(t)
	page, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f))
	if err != nil {
		t.Fatal(err)
	}
	if !page.Owner.Present || page.Owner.Revision != 2 || page.Owner.Retired || page.Kind != "show" || page.Entity != 2001 {
		t.Fatalf("owner %+v", page.Owner)
	}
	if len(page.Fields) != 1 || page.Fields[0].Field != "description" || page.Fields[0].Value != "Owner text" || page.Fields[0].Automatic != "Automatic text" || !page.Fields[0].Locked || !page.Fields[0].ValuePresent {
		t.Fatalf("fields %+v", page.Fields)
	}
	if len(page.Entries) != ipHistoryPageSize || page.Exhausted || page.Next == nil || page.RawRows != ipHistoryPageSize+1 {
		t.Fatalf("first page entries=%d exhausted=%v raw=%d", len(page.Entries), page.Exhausted, page.RawRows)
	}
	for i, entry := range page.Entries {
		if entry.BaseRevision != "1" || entry.Revision != "2" || entry.Actor != "owner" || entry.ObservedAt == "" {
			t.Fatalf("entry %d %+v", i, entry)
		}
		if len(entry.Fields) != 1 || entry.Fields[0].Field != "description" || entry.Fields[0].BeforeValue != "Old" || entry.Fields[0].AfterValue != "New" || !entry.Fields[0].AfterLocked || !entry.Fields[0].BeforePresent {
			t.Fatalf("entry %d fields %+v", i, entry.Fields)
		}
		if i > 0 && page.Entries[i-1].ID <= entry.ID {
			t.Fatal("history ids are not strictly descending", page.Entries)
		}
	}
	in := ipHistoryInput(f)
	in.After = page.Next
	next, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Entries) != 4 || !next.Exhausted || next.Next != nil || next.RawRows != 4 {
		t.Fatalf("second page entries=%d exhausted=%v raw=%d", len(next.Entries), next.Exhausted, next.RawRows)
	}
	if next.Entries[0].ID >= page.Entries[len(page.Entries)-1].ID {
		t.Fatal("keyset page 2 overlaps page 1", next.Entries[0].ID)
	}
}

// A row inserted after page 1 is above the raw keyset and cannot reorder or
// duplicate page 2; both pages are an exact prefix of the same descending stream.
func TestIPHistoryStableRawKeyset(t *testing.T) {
	f, r := ipHistoryFixture(t)
	first, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f))
	if err != nil || first.Next == nil {
		t.Fatal("first page", err)
	}
	f.exec(t, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2001,'2','3','edit','owner','2026-11-02T00:00:00Z','{}','{}')`)
	in := ipHistoryInput(f)
	in.After = first.Next
	second, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(second.Entries) != 4 || !second.Exhausted {
		t.Fatalf("second page %+v err=%v", second, err)
	}
	for _, entry := range second.Entries {
		if entry.ID >= first.Next.id {
			t.Fatal("newer row entered the page", entry.ID)
		}
	}
}

// The cursor pins the owner/source witness: an owner change between pages is
// terminal stale, never silently mixed.
func TestIPHistoryCursorStale(t *testing.T) {
	f, r := ipHistoryFixture(t)
	first, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f))
	if err != nil || first.Next == nil {
		t.Fatal("first page", err)
	}
	f.exec(t, `UPDATE metadata_edit_owners SET revision=3 WHERE entity_id=2001`)
	in := ipHistoryInput(f)
	in.After = first.Next
	if _, _, err = r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrStale) {
		t.Fatal("owner drift", err)
	}
}

func TestIPHistoryPersonOwner(t *testing.T) {
	f, r := ipHistoryFixture(t)
	v := f.input
	v.Kind = 1
	page, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, IPHistoryInput{View: v, Kind: "person", Entity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Owner.Present || page.Owner.Revision != 1 || len(page.Fields) != 1 || page.Fields[0].Field != "biography" || page.Fields[0].Locked {
		t.Fatalf("person owner %+v fields %+v", page.Owner, page.Fields)
	}
	if len(page.Entries) != 0 || !page.Exhausted {
		t.Fatalf("person history %+v", page.Entries)
	}
}

// History outlives the automatic owner state: no owner row is not an empty read.
func TestIPHistoryWithoutOwner(t *testing.T) {
	f, r := ipHistoryFixture(t)
	in := ipHistoryInput(f)
	in.Entity = 2003
	page, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal(err)
	}
	if page.Owner.Present || len(page.Fields) != 0 || len(page.Entries) != 2 || !page.Exhausted {
		t.Fatalf("history without owner %+v", page)
	}
}

func TestIPHistoryOwnerFieldsBound(t *testing.T) {
	f, r := ipHistoryFixture(t)
	for i := 0; i < ipOwnerMaxFields; i++ {
		f.exec(t, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('show',2001,?,'v','a',0,'owner','2026-11-01T00:00:00Z')`, fmt.Sprintf("field-%02d", i))
	}
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f)); !errors.Is(err, ErrUnqualified) {
		t.Fatal("over-bound owner fields", err)
	}
}

func TestIPHistoryCancelledLifetime(t *testing.T) {
	f, r := ipHistoryFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if page, _, err := r.ipHistory(ctx, foregroundRequest(), time.Second, f.output, ipHistoryInput(f)); err == nil || page.Entries != nil || page.Owner.Present {
		t.Fatal("cancelled history read", page, err)
	}
}

func TestIPHistoryRefusals(t *testing.T) {
	var nilReader *ForegroundReader
	if _, _, err := nilReader.IPHistory(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, IPHistoryInput{}); !errors.Is(err, ErrInput) {
		t.Fatal("nil reader", err)
	}
	r := &ForegroundReader{}
	for name, in := range map[string]IPHistoryInput{
		"zero entity":  {View: BrowseInput{Library: 1, Kind: 2}, Kind: "show"},
		"zero library": {View: BrowseInput{Kind: 2}, Kind: "show", Entity: 1},
		"bad kind":     {View: BrowseInput{Library: 1, Kind: 2}, Kind: "episode", Entity: 1},
		"descending":   {View: BrowseInput{Library: 1, Kind: 2, Descending: true}, Kind: "show", Entity: 1},
		"keep":         {View: BrowseInput{Library: 1, Kind: 2, KeepArticles: true}, Kind: "show", Entity: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := r.ipHistory(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
		})
	}
}

// The owner point requires a live entity in the admitted library; a retired,
// foreign or wrong-kind subject is denied, not reported as an empty owner.
func TestIPHistoryScopeDenied(t *testing.T) {
	f, r := ipHistoryFixture(t)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2001`)
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f)); !errors.Is(err, ErrDenied) {
		t.Fatal("retired subject", err)
	}
	in := ipHistoryInput(f)
	in.View.Library = 2
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrDenied) {
		t.Fatal("foreign library", err)
	}
	kind := ipHistoryInput(f)
	kind.View.Kind = 1
	kind.Kind = "item"
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, kind); !errors.Is(err, ErrDenied) {
		t.Fatal("wrong kind", err)
	}
}

// metadata_owner_history.id is one global AUTOINCREMENT, so an owner's page
// spans ids that include other owners' edits. A, B-with-fields, A must return
// A's page with A's own fields and never touch B's rows.
func TestIPHistoryInterleavedOwnerFields(t *testing.T) {
	source := newForegroundSource(t)
	for _, q := range []string{
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2101,x'00000000000000000000000000000835',1,2),(2102,x'00000000000000000000000000000836',1,2)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2101,x'00000000000000000000000000000835',1,2,'Owner A','owner a','Owner A','owner a','Owner A',2101,zeroblob(16),zeroblob(16)),(2102,x'00000000000000000000000000000836',1,2,'Owner B','owner b','Owner B','owner b','Owner B',2102,zeroblob(16),zeroblob(16))`,
		`INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2101,'1','2','edit','a','2026-11-01T00:00:00Z','{}','{}')`,
		`INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2102,'1','2','edit','b','2026-11-01T00:00:00Z','{}','{}')`,
		`INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('show',2101,'2','3','edit','a','2026-11-02T00:00:00Z','{}','{}')`,
		`INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) SELECT id,'title','Before',1,'',0,0,'After',1,'',0,0 FROM metadata_owner_history WHERE kind='show' AND entity_id=2101`,
		`INSERT INTO metadata_owner_history_fields(history_id,field,before_value,before_value_present,before_automatic,before_automatic_present,before_locked,after_value,after_value_present,after_automatic,after_automatic_present,after_locked) SELECT id,'description','Before',1,'',0,0,'After',1,'',0,0 FROM metadata_owner_history WHERE kind='show' AND entity_id=2102`,
	} {
		source.exec(t, q)
	}
	f, r := ipHistoryFixtureOn(t, source)
	in := ipHistoryInput(f)
	in.Entity = 2101
	page, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil {
		t.Fatal("interleaved owner history", err)
	}
	if len(page.Entries) != 2 || !page.Exhausted || page.Entries[0].ID != 3 || page.Entries[1].ID != 1 {
		t.Fatalf("owner A page %+v", page.Entries)
	}
	for _, entry := range page.Entries {
		if len(entry.Fields) != 1 || entry.Fields[0].Field != "title" {
			t.Fatalf("owner A field leak %+v", entry.Fields)
		}
	}
	other := in
	other.Entity = 2102
	otherPage, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, other)
	if err != nil || len(otherPage.Entries) != 1 || len(otherPage.Entries[0].Fields) != 1 || otherPage.Entries[0].Fields[0].Field != "description" {
		t.Fatalf("owner B page %+v err=%v", otherPage.Entries, err)
	}
}

// A person's descriptive history needs a visible live credit in the admitted
// library, applied with RS's person visibility rule.
func TestIPHistoryPersonScope(t *testing.T) {
	person := func(f *browseFixture, id int64) IPHistoryInput {
		v := f.input
		v.Kind = 1
		return IPHistoryInput{View: v, Kind: "person", Entity: id}
	}
	f, r := ipHistoryFixture(t)
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, person(f, 1)); err != nil {
		t.Fatal("visible credit denied", err)
	}
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, person(f, 2)); !errors.Is(err, ErrDenied) {
		t.Fatal("person without a credit", err)
	}
	for name, mutation := range map[string]string{
		"retired work": `UPDATE catalog_entities SET retired=1 WHERE id=2005`,
		"foreign work": `PRAGMA foreign_keys=OFF; UPDATE catalog_entities SET library_id=2 WHERE id=2005; PRAGMA foreign_keys=ON`,
		"unsealed":     `UPDATE catalog_credit_images SET sealed=0 WHERE id=2005`,
		"stale image":  `UPDATE catalog_credit_images SET owner_revision=2 WHERE id=2005`,
	} {
		t.Run(name, func(t *testing.T) {
			f, r := ipHistoryFixture(t)
			for _, q := range strings.Split(mutation, ";") {
				if strings.TrimSpace(q) != "" {
					f.exec(t, q)
				}
			}
			if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, person(f, 1)); !errors.Is(err, ErrDenied) {
				t.Fatal("invisible credit", err)
			}
		})
	}
}

// A restricted profile applies the shared entity age/unrated gate to every IP
// owner/history read, including a person's admitting credit.
func TestIPHistoryRestriction(t *testing.T) {
	// A movie (leaf kind) is the case the catalogue fence cannot decide for a
	// restricted profile, so the read's own age gate is what refuses it.
	item := func(f *browseFixture) IPHistoryInput {
		v := f.input
		v.Kind = 1
		return IPHistoryInput{View: v, Kind: "item", Entity: 2005}
	}
	f, r := ipHistoryFixture(t)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=2005`)
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, item(f)); err != nil {
		t.Fatal("unrestricted profile", err)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, item(f)); !errors.Is(err, ErrDenied) {
		t.Fatal("over-age history", err)
	}
	unrated, ur := ipHistoryFixture(t)
	unrated.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	if _, _, err := ur.ipHistory(t.Context(), foregroundRequest(), time.Second, unrated.output, item(unrated)); !errors.Is(err, ErrDenied) {
		t.Fatal("unrated history", err)
	}
	people, pr := ipHistoryFixture(t)
	people.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=2005`)
	people.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	v := people.input
	v.Kind = 1
	if _, _, err := pr.ipHistory(t.Context(), foregroundRequest(), time.Second, people.output, IPHistoryInput{View: v, Kind: "person", Entity: 1}); !errors.Is(err, ErrDenied) {
		t.Fatal("over-age admitting credit", err)
	}
}

// The cursor pins its exact fence, kind and entity; any mismatch is terminal
// stale rather than a silently different page.
func TestIPHistoryCursorMismatch(t *testing.T) {
	f, r := ipHistoryFixture(t)
	first, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f))
	if err != nil || first.Next == nil {
		t.Fatal("first page", err)
	}
	for name, mutate := range map[string]func(*IPHistoryInput){
		"kind":   func(in *IPHistoryInput) { in.Kind = "album" },
		"entity": func(in *IPHistoryInput) { in.Entity = 2003 },
		"fence": func(in *IPHistoryInput) {
			c := *first.Next
			c.fence.maximumAge = 3
			in.After = &c
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := ipHistoryInput(f)
			in.After = first.Next
			mutate(&in)
			if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, in); !errors.Is(err, ErrStale) {
				t.Fatal("cursor mismatch", err)
			}
		})
	}
}
