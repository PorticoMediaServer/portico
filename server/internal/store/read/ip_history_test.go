package read

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ipHistorySeed adds one owner with an explicit locked field, twelve typed
// history entries (with before/after values) and one person owner. History ids
// are sequential from 1 because this fixture is fresh.
var ipHistorySeed = []string{
	`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2001,x'000000000000000000000000000007d1',1,2),(2003,x'000000000000000000000000000007d3',1,2)`,
	`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2001,x'000000000000000000000000000007d1',1,2,'IP Show','ip show','IP Show','ip show','IP Show',2001,zeroblob(16),zeroblob(16)),(2003,x'000000000000000000000000000007d3',1,2,'History Show','history show','History Show','history show','History Show',2003,zeroblob(16),zeroblob(16))`,
	`INSERT INTO metadata_edit_owners(entity_id,kind,revision,command_incarnation) VALUES(2001,'show',2,zeroblob(16))`,
	`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('show',2001,'description','Owner text','Automatic text',1,'owner','2026-11-01T00:00:00Z')`,
	`INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(1,'person-token','person-key','Person One','Person One',zeroblob(16))`,
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

func ipHistoryFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeBrowseFixture(t, ipHistorySource(t), append(browseDefinitions(), append(IPHistoryHarnessDefinitions(), IPSourceHarnessDefinitions()...)...))
	return f, &ForegroundReader{snapshots: f.snapshots, pool: f.snapshots}
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

// The owner point requires a live entity in the admitted library; a retired or
// foreign subject is denied, not reported as an empty owner.
func TestIPHistoryScopeDenied(t *testing.T) {
	f, r := ipHistoryFixture(t)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2001`)
	if _, _, err := r.ipHistory(t.Context(), foregroundRequest(), time.Second, f.output, ipHistoryInput(f)); !errors.Is(err, ErrDenied) {
		t.Fatal("retired subject", err)
	}
}
