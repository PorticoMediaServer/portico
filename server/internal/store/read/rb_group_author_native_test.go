package read

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
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
	f.exec(t, "ANALYZE")
	captureBrowse(t, f, RBAuthorDefinitions(), plan.Analyzed)
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

func TestRBGroupAuthorUnicodePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := rbGroupSource(t)
		source.exec(t, `UPDATE catalog_entities SET title='Émile Zola' WHERE id=2005`)
		source.exec(t, `UPDATE catalog_entities SET author='Émile Zola' WHERE id=2006`)
		defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
		f := composeBrowseFixture(t, source, defs)
		view := f.input
		view.Kind = 9
		got, _, err := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
		if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(got, RBAuthorScope{}) {
			t.Fatal("unproved Unicode author scope claimed complete success", got, err)
		}
	})
}

func TestRBGroupAuthorNativeNonfinite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := rbGroupSource(t)
		source.exec(t, `UPDATE catalog_entities SET series='Saga',series_position='1e999' WHERE id=2006`)
		defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
		f := composeBrowseFixture(t, source, defs)
		view := f.input
		view.Kind = 9
		got, _, err := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
		if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(got, RBAuthorScope{}) {
			t.Fatal("SQLite infinity became an author order", got, err)
		}
	})
}

func TestRBGroupAuthorBounds(t *testing.T) {
	for _, tc := range []struct {
		name         string
		books, parts int
		pending      bool
	}{
		{"combined-256", 1, 255, false},
		{"combined-257", 1, 256, true},
		{"books-16", 16, 80, false},
		{"books-17", 17, 80, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source := rbGroupSource(t)
				source.exec(t, "BEGIN")
				for n := 1; n < tc.books; n++ {
					id := int64(10000 + n)
					public := fmt.Sprintf("%032x", id)
					source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,unhex(?),1,8)`, id, public)
					source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,author) VALUES(?,unhex(?),1,8,'Book','book','b','book','b',0,zeroblob(16),zeroblob(16),'Author')`, id, public)
				}
				for n := 80; n < tc.parts; n++ {
					id := int64(20000 + n)
					public := fmt.Sprintf("%032x", id)
					source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,unhex(?),1,9)`, id, public)
					// Rejected parts still consume the raw complete-set bound.
					source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,book_id,retired) VALUES(?,unhex(?),1,9,'Hidden','h','h','h','h',0,zeroblob(16),zeroblob(16),2006,1)`, id, public)
				}
				source.exec(t, "COMMIT")
				defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
				f := composeBrowseFixture(t, source, defs)
				view := f.input
				view.Kind = 9
				got, _, err := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
				if tc.pending {
					if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(got, RBAuthorScope{}) {
						t.Fatal("incomplete scope escaped", got, err)
					}
				} else if err != nil || got.RawRows != tc.books+tc.parts || len(got.Items) != len(rbGroupExpected(RBGroupBookParts)) {
					t.Fatal("complete bound refused or lost raw rows", got.RawRows, len(got.Items), err)
				}
			})
		})
	}
}

func TestRBGroupAuthorLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
		f := composeReadFixture(t, rbGroupSource(t), defs, readfixture.LiveConfig(time.Now), false)
		view := f.input
		view.Kind = 9
		q, request, output := liveRequests("rb-author")
		_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			got, _, err := f.reader.RBAuthorExact(ctx, request, time.Second, out, view, 2005)
			if err != nil {
				return err
			}
			if len(got.Items) != len(rbGroupExpected(RBGroupBookParts)) {
				t.Fatal("lost complete author scope")
			}
			assertLiveUsage(t, f, 1, 0)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}
