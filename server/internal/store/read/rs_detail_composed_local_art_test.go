package read

import (
	"testing"
	"time"
)

// A composed detail whose selected artwork came from a local source object must
// pass final provenance verification. The rs.art.point row carries 35 columns
// (P01 added the artwork_sources.retiring column at index 34) while
// rs.art.verify_local binds exactly the first 34, so the composed reader must
// slice the same way the merged artwork reader does.
func TestRSDetailComposedLocalSourceArt(t *testing.T) {
	f, r := newRSDetailComposedFixture(t, false)
	f.exec(t, `UPDATE artwork_selections SET source_object_id='art-object',source_object_revision='r1' WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='locked'`)
	in := RSDetailComposedInput{View: f.input, Item: 1103}
	in.View.Kind = 3
	d, _, err := r.detail(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Art.State != RSDetailFactPresent || d.Art.Fact == nil || d.Art.Fact.Role != "poster" || d.Art.Fact.Digest != rsArtDigest(2) {
		t.Fatalf("local source art %+v %v", d.Art, err)
	}
}
