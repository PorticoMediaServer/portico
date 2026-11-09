package read

import (
	"testing"
	"time"
)

// The composed art slot treats a locked NULL digest as proven absence: it is
// not pending work, the matched alternative behind it never revives the role,
// and final verification uses the empty selection witness. The non-empty
// control still publishes the selected artwork as present.
func TestRSDetailComposedArtSlotLockedEmpty(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	in := RSDetailComposedInput{View: f.input, Item: 1103}
	in.View.Kind = 3
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Art.State != RSDetailFactPresent || d.Art.Fact == nil || d.Art.Fact.Digest != rsArtDigest(2) {
		t.Fatalf("nonempty season poster control %+v %v", d.Art, err)
	}
	f.exec(t, `UPDATE artwork_selections SET digest=NULL,provider='',image_id='',origin='',attribution='',source_object_id=NULL,source_object_revision=NULL WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='locked'`)
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Art.State != RSDetailFactAbsent || d.Art.Fact != nil {
		t.Fatalf("locked-empty season poster reported art %+v %v", d.Art, err)
	}
	if len(d.Images) != 0 {
		t.Fatalf("matched poster revived in images %+v", d.Images)
	}
	var matched int
	if err = f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='matched'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatalf("matched poster row missing: %d %v", matched, err)
	}
}

// A locked selection with a NULL digest is the adopted E empty choice. On a
// composed detail it must consume its role at precedence time instead of being
// asserted as a string: no panic escapes, the role is omitted, and the matched
// alternative behind it never revives it. Other roles are still served.
func TestRSDetailComposedLockedEmptySelectionOmitsRole(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	in := RSDetailComposedInput{View: f.input, Item: 1101}
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(d.Images) != 1 || d.Images[0].Role != "poster" || d.Images[0].Provider != "fixture" || d.Images[0].Digest != rsArtDigest(12) {
		t.Fatalf("nonempty locked control %+v %v", d.Images, err)
	}
	// Keep the matched poster behind the empty locked row to prove precedence,
	// not row removal, is what consumes the role.
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',1101,'poster','locked',NULL,'','','','','accepted-fence','fixture','now')`)
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(d.Images) != 0 {
		t.Fatalf("locked-empty poster revived matched art %+v %v", d.Images, err)
	}
	var matched int
	if err = f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE kind='entity' AND entity_id=1101 AND role='poster' AND choice='matched'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatalf("matched fixture row missing after empty choice: %d %v", matched, err)
	}
	// A different role after the consumed role is still served in PK order.
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',1101,'logo','matched',printf('%064d',1),'provider','logo','provider','','f','test','2026-11-06T00:00:00Z')`)
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(d.Images) != 1 || d.Images[0].Role != "logo" || d.Images[0].Provider != "provider" {
		t.Fatalf("later role lost after empty choice %+v %v", d.Images, err)
	}
	// Actual removal of the empty choice restores the matched poster.
	f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1101 AND role='poster' AND choice='locked'`)
	d, _, err = r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(d.Images) != 2 || d.Images[0].Role != "logo" || d.Images[1].Role != "poster" || d.Images[1].Digest != rsArtDigest(12) {
		t.Fatalf("matched fallback after removal %+v %v", d.Images, err)
	}
}
