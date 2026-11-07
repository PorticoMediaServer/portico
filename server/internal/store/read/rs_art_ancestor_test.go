package read

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestRSArtFreshAncestorOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		in := rsArtInput(f, 1104, 4, "poster", true)
		before, _, e := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil || !before.Present || before.Owner != 1103 || before.Digest != rsArtDigest(2) {
			t.Fatal("missing live season control", before, e)
		}
		r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
			// Keep every artwork selection, revision and digest unchanged. Only the
			// inherited owner's current liveness changes after payload copy.
			f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1103`)
		}}
		after, cost, e := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if e != nil || cost.Attempts != 2 || after.Present || after.Owner != 0 || after.Digest != "" {
			t.Fatal("retired ancestor poster escaped fresh fence", after, cost.Attempts, e)
		}
		var digest string
		if e = f.conn.QueryRowContext(t.Context(), `SELECT digest FROM artwork_selections WHERE entity_id=1103 AND role='poster' AND choice='locked'`).Scan(&digest); e != nil || digest != before.Digest {
			t.Fatal("ancestor control changed selected choice", digest, e)
		}
	})
}
