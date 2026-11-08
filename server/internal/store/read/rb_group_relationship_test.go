package read

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRBGroupRelationshipChargesRetainedStrings(t *testing.T) {
	synctest.Test(t, testRBGroupRelationshipChargesRetainedStrings)
}
func testRBGroupRelationshipChargesRetainedStrings(t *testing.T) {
	f := rbGroupFixture(t, false)
	wide := strings.Repeat("a", 8<<10)
	// Each 64-row source quantum fits its own 4MiB Go-copy reservation, while
	// the complete retained prefix exceeds the separately funded output budget.
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) SELECT id+4000,randomblob(16),1,7 FROM catalog_entities WHERE kind=7 AND id<3040`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,album_id) SELECT id,public_id,1,7,'Added','added','a','added','a',0,zeroblob(16),zeroblob(16),2004 FROM catalog_subjects WHERE id>=7000 AND id<7040`)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE kind=7`, wide)
	f.exec(t, `UPDATE catalog_entities SET sort_key=? WHERE id=2004`, wide)
	in := RBRelationshipInput{View: f.input, Sort: RBRelationshipAlbum}
	in.View.Kind = 7
	page, cost, err := f.reader.rbRelationshipExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(err, governor.ErrSnapshotBudget) || !reflect.DeepEqual(page, RBRelationshipPage{}) || len(cost.LookupWindows) < 2 {
		t.Fatal("retained prefix must outgrow custody across separate quanta", page, cost, err)
	}
}

func TestRBGroupRelationshipCompleteOrder(t *testing.T) {
	synctest.Test(t, testRBGroupRelationshipCompleteOrder)
}
func testRBGroupRelationshipCompleteOrder(t *testing.T) {
	f := rbGroupFixture(t, false)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2008,x'08080000000000000000000000000000',1,6)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) VALUES(2008,x'08080000000000000000000000000000',1,6,'Other album','SAME','s','The Same','t',0,zeroblob(16),zeroblob(16),2003)`)
	f.exec(t, `UPDATE catalog_entities SET sort_key='same',sort_key_keep_articles='the same' WHERE id=2004`)
	f.exec(t, `UPDATE catalog_entities SET album_id=2008 WHERE kind=7 AND id%2=0`)
	for _, by := range []RBRelationshipSort{RBRelationshipAlbum, RBRelationshipArtist} {
		for _, desc := range []bool{false, true} {
			for _, keep := range []bool{false, true} {
				in := RBRelationshipInput{View: f.input, Sort: by}
				in.View.Kind = 7
				in.View.Limit = 7
				in.View.Descending = desc
				in.View.KeepArticles = keep
				var got []int64
				for windows := 0; ; windows++ {
					if windows > 20 {
						t.Fatal("cursor stalled")
					}
					p, _, e := f.reader.rbRelationshipExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
					if e != nil {
						t.Fatal(e)
					}
					for _, item := range p.Items {
						got = append(got, item.ID)
					}
					if p.Exhausted {
						break
					}
					if p.Next == nil {
						t.Fatal("missing exact continuation")
					}
					in.After = p.Next
				}
				want := rbGroupExpected(RBGroupCollectionMembers)
				if desc {
					slices.Reverse(want)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("equal-parent-key tie got%v want%v", got, want)
				}
			}
		}
	}
	if rbRelationshipFold("ÉZéa") != "Ézéa" {
		t.Fatal("SQLite ASCII collation changed")
	}
}
func TestRBGroupRelationshipRejectsIncompleteAndPublication(t *testing.T) {
	synctest.Test(t, testRBGroupRelationshipRejectsIncompleteAndPublication)
}
func testRBGroupRelationshipRejectsIncompleteAndPublication(t *testing.T) {
	f := rbGroupFixture(t, false)
	// Expand beyond the complete-set ceiling. Every inserted row is retired:
	// eligibility cannot make this appear to be a small/exhausted raw library.
	f.exec(t, "BEGIN")
	for n := int64(0); n < 177; n++ {
		id := 6000 + n
		public := fmt.Sprintf("%032x", id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,unhex(?),1,7)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,album_id,retired) VALUES(?,unhex(?),1,7,'Overflow','overflow','o','overflow','o',0,zeroblob(16),zeroblob(16),2004,1)`, id, public)
	}
	f.exec(t, "COMMIT")
	in := RBRelationshipInput{View: f.input, Sort: RBRelationshipAlbum}
	in.View.Kind = 7
	p, _, e := f.reader.rbRelationshipExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(p, RBRelationshipPage{}) {
		t.Fatal("bounded top-K escaped", p, e)
	}
	f = rbGroupFixture(t, false)
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE libraries SET data_order_revision=data_order_revision+1 WHERE id='movies'`) }}
	in.View = f.input
	in.View.Kind = 7
	p, _, e = f.reader.rbRelationshipExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, RBRelationshipPage{}) {
		t.Fatal("mixed parent keys published", e)
	}
}
