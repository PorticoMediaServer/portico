package read

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// A locked selection with a NULL digest is the adopted E empty choice. It must
// consume its role at precedence time instead of being asserted as a string or
// letting the matched row behind it supply an image.
func TestForegroundLockedEmptySelectionOmitsRole(t *testing.T) {
	f, r := newForegroundFixture(t)
	in := TitleInput{View: f.input, Item: 1, Asset: 1}
	d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Images) != 1 || d.Images[0].Role != "poster" || d.Images[0].Provider != "owner" {
		t.Fatalf("nonempty locked control %+v %v", d.Images, e)
	}
	// Keep the matched poster behind the locked row to prove precedence, not
	// row removal, is what blocks it.
	f.exec(t, `UPDATE artwork_selections SET digest=NULL,provider='',image_id='',origin='',attribution='' WHERE kind='entity' AND entity_id=1 AND role='poster' AND choice='locked'`)
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Images) != 0 {
		t.Fatalf("locked-empty revived matched art %+v %v", d.Images, e)
	}
	var matched int
	if e = f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=1 AND role='poster' AND choice='matched'`).Scan(&matched); e != nil || matched != 1 {
		t.Fatalf("matched fixture row missing after empty choice: %d %v", matched, e)
	}
	// A different role after the consumed role is still served in PK order.
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',1,'logo','matched',printf('%064d',1),'provider','logo','provider','','f','test','2026-11-06T00:00:00Z')`)
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Images) != 1 || d.Images[0].Role != "logo" || d.Images[0].Provider != "provider" {
		t.Fatalf("later role lost after empty choice %+v %v", d.Images, e)
	}
	// Actual removal of the empty choice restores the matched row.
	f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1 AND role='poster' AND choice='locked'`)
	d, _, e = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if e != nil || len(d.Images) != 2 || d.Images[0].Role != "logo" || d.Images[1].Role != "poster" || d.Images[1].Provider != "provider" {
		t.Fatalf("matched fallback after removal %+v %v", d.Images, e)
	}
}

// The raw 128-role/129-row refusal is unchanged: extra roles behind a
// locked-empty precedence row still refuse the whole detail.
func TestForegroundLockedEmptySelectionKeepsRawBounds(t *testing.T) {
	f, r := newForegroundFixture(t)
	f.exec(t, `UPDATE artwork_selections SET digest=NULL,provider='',image_id='',origin='',attribution='' WHERE kind='entity' AND entity_id=1 AND role='poster' AND choice='locked'`)
	f.exec(t, "BEGIN")
	for n := 0; n < 128; n++ {
		f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',1,?,'matched',printf('%064d',1),'provider','image','provider','','f','test','2026-11-06T00:00:00Z')`, fmt.Sprintf("bound%03d", n))
	}
	f.exec(t, "COMMIT")
	d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatalf("raw role bound escaped %+v %v", d, e)
	}
}

// A LIVE revocation after the business windows still denies a detail whose
// selection set includes a locked-empty row; no partial images escape.
func TestForegroundLockedEmptySelectionPublication(t *testing.T) {
	f, r := newForegroundFixture(t)
	f.exec(t, `UPDATE artwork_selections SET digest=NULL,provider='',image_id='',origin='',attribution='' WHERE kind='entity' AND entity_id=1 AND role='poster' AND choice='locked'`)
	r.snapshots = &foregroundBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 2 {
			f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
		}
	}}
	d, _, e := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, TitleInput{View: f.input, Item: 1, Asset: 1})
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(d, TitleDetail{}) {
		t.Fatalf("revoked locked-empty detail escaped %+v %v", d, e)
	}
}
