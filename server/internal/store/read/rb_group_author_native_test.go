package read

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"testing"
	"time"
)

func TestRBGroupAuthorNative(t *testing.T) {
	source := rbGroupSource(t)
	source.exec(t, `UPDATE catalog_entities SET series='Saga',series_position='2' WHERE id=2006`)
	defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBAuthorDefinitions()...)
	f := composeBrowseFixture(t, source, defs)
	view := f.input
	view.Kind = 9
	p, _, e := f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if e != nil {
		t.Fatal(e)
	}
	want := rbGroupExpected(RBGroupBookParts)
	var got []int64
	for _, v := range p.Items {
		got = append(got, v.ID)
	}
	if !reflect.DeepEqual(got, want) || p.RawRows != 80 {
		t.Fatal(got, want, p.RawRows)
	}
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	p, _, e = f.reader.rbAuthorExact(t.Context(), rbGroupRequest(), time.Second, f.output, view, 2005)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, RBAuthorScope{}) {
		t.Fatal("publication leak", e)
	}
}

var _ []stmt.Definition = RBAuthorDefinitions()
