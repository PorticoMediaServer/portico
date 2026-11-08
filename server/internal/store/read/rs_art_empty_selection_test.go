package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// An explicit locked choice without a digest is successful absence. It has no
// provider origin, no source object and no renditions to wait for.
func rsArtSelectEmpty(t *testing.T, f *browseFixture, id int64, role string) {
	t.Helper()
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',?,?,'locked',NULL,'','','','','accepted-fence','fixture','now')`, id, role)
}
func rsArtLockEmpty(t *testing.T, f *browseFixture, id int64, role string) {
	t.Helper()
	f.exec(t, `UPDATE artwork_selections SET digest=NULL,provider='',image_id='',origin='',attribution='' WHERE kind='entity' AND entity_id=? AND role=? AND choice='locked'`, id, role)
}

// A locked NULL digest is a decision, not pending work: it stops matched,
// alias and ancestor fallback and never panics or reports ErrNotReady merely
// because its source/rendition joins are NULL.
func TestRSArtLockedEmptyBlocksFallback(t *testing.T) {
	t.Run("child blocks matched and ancestor", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtSelect(t, f, 1102, "poster", "matched", 12)
		rsArtSelectEmpty(t, f, 1104, "poster")
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Present || cost.Attempts != 1 || len(cost.LookupWindows) != 4 {
			t.Fatal("explicit child empty selection", d, cost.Attempts, err)
		}
		f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1104 AND role='poster'`)
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Owner != 1103 || d.Digest != rsArtDigest(2) || d.Choice != "locked" {
			t.Fatal("matched fallback after actual removal", d, err)
		}
	})
	t.Run("empty ancestor blocks further inheritance", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtSelect(t, f, 1102, "poster", "matched", 12)
		rsArtLockEmpty(t, f, 1103, "poster")
		d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Present {
			t.Fatal("empty season inherited show poster", d, err)
		}
		f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='locked'`)
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Owner != 1103 || d.Digest != rsArtDigest(1) {
			t.Fatal("matched child after unlock", d, err)
		}
		f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='matched'`)
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Owner != 1102 || d.Digest != rsArtDigest(12) {
			t.Fatal("ancestor poster after actual removal", d, err)
		}
	})
	t.Run("empty blocks cover alias", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtSelect(t, f, 1101, "cover", "matched", 14)
		rsArtSelectEmpty(t, f, 1101, "poster")
		d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1101, 1, "poster", false))
		if err != nil || d.Present {
			t.Fatal("empty poster revived cover alias", d, err)
		}
		f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1101 AND role='poster' AND choice='locked'`)
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1101, 1, "poster", false))
		if err != nil || d.Owner != 1101 || d.Digest != rsArtDigest(14) {
			t.Fatal("cover alias after actual removal", d, err)
		}
	})
}

// The empty selection is proved at the final boundary by its own WITHOUT ROWID
// key, not by a digest comparison that NULL could never satisfy.
func TestRSArtLockedEmptyFinalSourceEquality(t *testing.T) {
	t.Run("unchanged empty survives final verify", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtLockEmpty(t, f, 1103, "poster")
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || d.Present || cost.Attempts != 1 || len(cost.LookupWindows) != 4 || cost.PublicationStatements == 0 {
			t.Fatal("explicit empty result re-verified", d, cost.Attempts, err)
		}
	})
	t.Run("empty to nonempty after payload retries safely", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtLockEmpty(t, f, 1103, "poster")
		r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
			f.exec(t, `UPDATE artwork_selections SET digest=? WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='locked'`, rsArtDigest(3))
		}}
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || cost.Attempts != 2 || !d.Present || d.Owner != 1103 || d.Digest != rsArtDigest(3) {
			t.Fatal("empty selection changed to nonempty", d, cost.Attempts, err)
		}
	})
	t.Run("removal after payload retries to matched", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtLockEmpty(t, f, 1103, "poster")
		r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
			f.exec(t, `DELETE FROM artwork_selections WHERE kind='entity' AND entity_id=1103 AND role='poster' AND choice='locked'`)
		}}
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || cost.Attempts != 2 || d.Owner != 1103 || d.Digest != rsArtDigest(1) {
			t.Fatal("empty selection removed after payload", d, cost.Attempts, err)
		}
	})
	t.Run("revision drift on unchanged empty retries", func(t *testing.T) {
		f, r := newRSArtFixture(t, false)
		rsArtLockEmpty(t, f, 1103, "poster")
		r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
			f.exec(t, `UPDATE catalog_entities SET artwork_revision=artwork_revision+1 WHERE id=1103`)
		}}
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
		if err != nil || cost.Attempts != 2 || d.Present {
			t.Fatal("empty selection revision drift", d, cost.Attempts, err)
		}
	})
}

// A nonempty selection whose source is retiring or whose rendition is gone is
// unavailable: it refuses and never revives a smaller matched choice.
func TestRSArtNonemptyUnavailableSourceRefuses(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	in := rsArtInput(f, 1104, 4, "poster", true)
	f.exec(t, `UPDATE artwork_sources SET retiring=1,retirement_token=zeroblob(16) WHERE digest=?`, rsArtDigest(2))
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(d, ArtworkFact{}) {
		t.Fatal("served art from a retiring source", d, err)
	}
	f.exec(t, `UPDATE artwork_sources SET retiring=0,retirement_token=NULL WHERE digest=?`, rsArtDigest(2))
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || !d.Present || d.Digest != rsArtDigest(2) {
		t.Fatal("restored source", d, err)
	}
	f.exec(t, `UPDATE artwork_blobs SET status='deleting',deletion_token=zeroblob(16) WHERE digest=?`, rsArtDigest(2))
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrNotReady) || d.Present {
		t.Fatal("deleting rendition revived matched art", d, err)
	}
	rsArtLockEmpty(t, f, 1103, "poster")
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Present {
		t.Fatal("explicit empty became unavailable", d, err)
	}
}

// The empty decision still owes the ordinary LIVE grant and final authorization.
func TestRSArtLockedEmptyLIVEAuthorization(t *testing.T) {
	f, r := newRSArtFixture(t, true)
	rsArtLockEmpty(t, f, 1103, "poster")
	in := rsArtInput(f, 1104, 4, "poster", true)
	q, request, output := liveRequests("rs-art-empty")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		d, cost, err := r.Artwork(ctx, request, time.Second, out, in)
		if err != nil || d.Present || cost.PublicationStatements == 0 || f.authority.ForegroundUsage().Live != 1 {
			t.Fatalf("LIVE explicit empty %+v %+v %v", d, cost, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	g, gr := newRSArtFixture(t, false)
	rsArtLockEmpty(t, g, 1103, "poster")
	gr.snapshots = &rsArtBoundarySnapshots{Snapshots: g.snapshots, afterPayload: func() {
		g.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	}}
	d, _, err := gr.artwork(t.Context(), foregroundRequest(), time.Second, g.output, rsArtInput(g, 1104, 4, "poster", true))
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, ArtworkFact{}) {
		t.Fatal("revocation escaped through an empty selection", d, err)
	}
}

// The registered NULL-capable witness is exactly the locked key: a NULL digest
// never matches the digest-index verify, and no row shortcut substitutes it.
func TestRSArtLockedEmptyPointAndVerifyWitness(t *testing.T) {
	f, _ := newRSArtFixture(t, false)
	rows, err := f.conn.QueryContext(t.Context(), rsArtPointSQL, int64(1109), "banner")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	v := make([]any, len(cols))
	dst := make([]any, len(v))
	for i := range v {
		dst[i] = &v[i]
	}
	if !rows.Next() {
		t.Fatal("lost explicit empty point")
	}
	if err := rows.Scan(dst...); err != nil {
		t.Fatal(err)
	}
	if len(v) != 35 || v[4] != nil || v[3] != "locked" || v[34] != nil || !rsArtAbsent(v) {
		t.Fatalf("explicit empty point shape %v", v)
	}
	public := []byte{128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9}
	verify := func(revision int64) int {
		r, err := f.conn.QueryContext(t.Context(), rsArtVerifyEmptySQL, int64(1109), int64(9), "banner", public, revision, "accepted-fence")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		n := 0
		for r.Next() {
			n++
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if verify(1) != 1 || verify(2) != 0 {
		t.Fatal("NULL digest witness not exact")
	}
}
