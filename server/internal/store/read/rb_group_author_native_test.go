package read

import (
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBGroupAuthorNative(t *testing.T) { synctest.Test(t, testRBGroupAuthorNative) }
func testRBGroupAuthorNative(t *testing.T) {
	source := rbGroupSource(t)
	source.exec(t, `UPDATE catalog_entities SET series='Saga',series_position='2' WHERE id=2006`)
	for n := 0; n < 3; n++ {
		id := int64(2013 + n)
		part := int64(6010 + n)
		series, position := "Saga", ""
		if n == 1 {
			position = "0"
		}
		if n == 2 {
			series = ""
		}
		for _, v := range []struct{ id, kind int64 }{{id, 8}, {part, 9}} {
			source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,unhex(?),1,?)`, v.id, fmt.Sprintf("%032x", v.id), v.kind)
		}
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,author,series,series_position) VALUES(?,unhex(?),1,8,'A','a','a','a','a',0,zeroblob(16),zeroblob(16),'Author',?,?)`, id, fmt.Sprintf("%032x", id), series, position)
		source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,book_id) VALUES(?,unhex(?),1,9,'Part','p','p','p','p',0,zeroblob(16),zeroblob(16),?)`, part, fmt.Sprintf("%032x", part), id)
	}
	defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
	f := composeBrowseFixture(t, source, defs)
	view := f.input
	view.Kind = 9
	p, _, e := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if e != nil {
		t.Fatal(e)
	}
	want := append([]int64{6010, 6011}, rbGroupExpected(RBGroupBookParts)...)
	want = append(want, 6012)
	var got []int64
	for _, v := range p.Items {
		got = append(got, v.ID)
	}
	if !reflect.DeepEqual(got, want) || p.RawRows != 87 {
		t.Fatal(got, want, p.RawRows)
	}
	view.Descending = true
	reverse, _, err := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range reverse.Items {
		if v.ID != want[len(want)-1-i] {
			t.Fatal("native descending order")
		}
	}
	view.Descending = false
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	p, _, e = f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, RBAuthorScope{}) {
		t.Fatal("publication leak", e)
	}
	f.reader.snapshots = f.snapshots
	f.exec(t, `UPDATE authorization_session_families SET revoked=0`)
	f.exec(t, "BEGIN")
	for n := 0; n < 177; n++ {
		id := int64(10000 + n)
		public := fmt.Sprintf("%032x", id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,unhex(?),1,9)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,book_id,retired) VALUES(?,unhex(?),1,9,'Hidden','h','h','h','h',0,zeroblob(16),zeroblob(16),2006,1)`, id, public)
	}
	f.exec(t, "COMMIT")
	p, _, e = f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(p, RBAuthorScope{}) {
		t.Fatal("rejected raw tail escaped cap", e)
	}

}

var _ []stmt.Definition = RBAuthorDefinitions()
