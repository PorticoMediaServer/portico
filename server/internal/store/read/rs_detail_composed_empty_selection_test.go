package read

import (
	"testing"
	"time"
)

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
