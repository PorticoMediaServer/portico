package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func rsArtInput(f *browseFixture, id, kind int64, role string, page bool) ArtworkInput {
	v := f.input
	v.Kind = kind
	return ArtworkInput{View: v, Item: id, Role: role, PageSlot: page}
}
func TestRSArtInheritanceAndPublicRoles(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	tests := []struct {
		id, kind int64
		role     string
		page     bool
		owner    int64
		selected string
		n        int
	}{
		{1104, 4, "poster", true, 1103, "poster", 2}, {1104, 4, "backdrop", true, 1102, "backdrop", 3}, {1104, 4, "logo", true, 1102, "logo", 4}, {1104, 4, "still", true, 1104, "still", 6},
		{1103, 3, "backdrop", true, 1102, "backdrop", 3}, {1104, 4, "logo", false, 0, "", 0},
		{1105, 5, "poster", true, 1105, "portrait", 7}, {1106, 6, "poster", true, 1106, "cover", 8},
		{1107, 7, "poster", true, 1107, "cover", 9}, {1107, 7, "poster", false, 1106, "cover", 8},
		{1107, 7, "cover", false, 1107, "cover", 9}, {1109, 9, "poster", true, 1108, "cover", 10}, {1110, 10, "poster", true, 1110, "poster", 11},
		{1101, 1, "poster", true, 0, "", 0}, {1106, 6, "backdrop", true, 0, "", 0},
		{1102, 2, "backdrop", true, 1102, "backdrop", 3}, {1103, 3, "poster", true, 1103, "poster", 2}, {1108, 8, "poster", true, 1108, "cover", 10},
	}
	for _, tc := range tests {
		d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, tc.id, tc.kind, tc.role, tc.page))
		if err != nil || d.Present != (tc.owner != 0) || d.Owner != tc.owner || d.Role != tc.selected || (d.Present && (d.Digest != rsArtDigest(tc.n) || d.Small.Bytes != 1234 || d.Large.Width != 600 || d.OwnerPublicID[0] != 0x80)) {
			t.Fatalf("%+v: %+v %v", tc, d, err)
		}
		if cost.LookupStatements > 20 || cost.PublicationStatements < 6 || cost.PublicationStatements > 7 || len(cost.LookupWindows) != 4 {
			t.Fatalf("unbounded statement/window work %+v", cost)
		}
	}
	// Movie/item art routes retain their own cover alias.
	rsArtSelect(t, f, 1101, "cover", "matched", 14)
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1101, 1, "poster", false))
	if err != nil || d.Owner != 1101 || d.Role != "cover" || d.Digest != rsArtDigest(14) {
		t.Fatal("movie cover alias", d, err)
	}
	// Missing episode still stays empty, even with a selected show still.
	f.exec(t, `DELETE FROM artwork_selections WHERE entity_id=1104 AND role='still'`)
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "still", true))
	if err != nil || d.Present {
		t.Fatal("inherited a still", d, err)
	}
	// The show is the next reference after withdrawing the season poster.
	rsArtSelect(t, f, 1102, "poster", "matched", 12)
	f.exec(t, `DELETE FROM artwork_selections WHERE entity_id=1103 AND role='poster'`)
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, 1104, 4, "poster", true))
	if err != nil || d.Owner != 1102 || d.Digest != rsArtDigest(12) {
		t.Fatal("show fallback", d, err)
	}
	// Artist art is not an album fallback; collection membership is not an art source.
	f.exec(t, `DELETE FROM artwork_selections WHERE entity_id IN(1106,1110)`)
	for _, tc := range []struct{ id, kind int64 }{{1106, 6}, {1110, 10}} {
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, rsArtInput(f, tc.id, tc.kind, "poster", true))
		if err != nil || d.Present {
			t.Fatal("invented child/artist fallback", d, err)
		}
	}
}
func TestRSArtLockedAndReadySourceFences(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	in := rsArtInput(f, 1104, 4, "poster", true)
	f.exec(t, `UPDATE artwork_blobs SET status='deleting',deletion_token=zeroblob(16) WHERE digest=?`, rsArtDigest(2))
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrNotReady) || !reflect.DeepEqual(d, ArtworkFact{}) {
		t.Fatal("revived matched art over unavailable locked choice", d, err)
	}
	f.exec(t, `DELETE FROM artwork_selections WHERE entity_id=1103 AND choice='locked'`)
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Digest != rsArtDigest(1) || d.Choice != "matched" {
		t.Fatal("matched fallback after explicit unlock/removal", d, err)
	}
	// A selected source with stale/missing inventory evidence must not be served.
	f.exec(t, `UPDATE artwork_selections SET source_object_id='missing-object',source_object_revision='old' WHERE entity_id=1103`)
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrNotReady) || d.Present {
		t.Fatal("unfenced local source", d, err)
	}
}
func TestRSArtVisibilityAndPublication(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	in := rsArtInput(f, 1104, 4, "poster", true)
	r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
		f.exec(t, `UPDATE artwork_selections SET digest=? WHERE entity_id=1103 AND choice='locked'`, rsArtDigest(3))
	}}
	d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || cost.Attempts != 2 || d.Digest != rsArtDigest(3) {
		t.Fatal("selected-source first/final equality", d, cost.Attempts, err)
	}
	r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, ArtworkFact{}) {
		t.Fatal("revoked source escaped", d, err)
	}
}
func TestRSArtDenialsAndAncestry(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	in := rsArtInput(f, 1104, 4, "poster", true)
	// Retired season does not supply art, even though its selections remain.
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1103`)
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Present {
		t.Fatal("retired ancestor", d, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=0 WHERE id=1103`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=1104`)
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || d.Present {
		t.Fatal("ancestor art authorized hidden episode", d, err)
	}
	in.View.Kind = 3
	in.Item = 1103
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || d.Present {
		t.Fatal("default policy manufactured container eligibility", d, err)
	}
}
func TestRSArtActualBytesAndLIVE(t *testing.T) {
	f, r := newRSArtFixture(t, true)
	in := rsArtInput(f, 1104, 4, "poster", true)
	q, request, output := liveRequests("rs-art")
	d, _, err := r.Artwork(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || d.Present {
		t.Fatal("unadmitted artwork", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		d, cost, err := r.Artwork(ctx, request, time.Second, out, in)
		// 64*3 digest bytes + 16 owner-ID bytes + role/choice + MIME per slot.
		want := int64(64*3 + 16 + len("poster") + len("locked") + 2*len("image/jpeg") + 2*len("ready") + len("accepted-fence") + len("movies"))
		window := cost.LookupWindows[2]
		if err != nil || !d.Present || window.NativeReservedBytes <= want || window.NativeReservedBytes >= f.snapshots.NativeRowReservation() || window.GoReservedBytes != out.Request.Need[governor.MemoryBytes]/2 || f.authority.ForegroundUsage().Live != 1 {
			t.Fatalf("actual bytes/LIVE %+v %+v %v", d, cost, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d, _, err = r.Artwork(ctx, request, time.Second, BrowseOutput{}, in)
	if err == nil || d.Present {
		t.Fatal("cancelled output", err)
	}
	result, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2}, func(scope *governor.ReadScope) error {
		_, _, err := queryReadScope(scope, "rs.art.point", int64(1103), "poster")
		return err
	})
	if !errors.Is(err, governor.ErrSnapshotBudget) || result.Refusal != nil {
		t.Fatal("tiny forecast decoded art", result, err)
	}
}
func TestRSArtSQLMutants(t *testing.T) {
	f := newRSArtSource(t)
	digest := func(sql string) string {
		rows, err := f.conn.QueryContext(t.Context(), sql, int64(1103), "poster")
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
			t.Fatal("lost point")
		}
		if err = rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		return v[4].(string)
	}
	if digest(rsArtPointSQL) != rsArtDigest(2) {
		t.Fatal("positive locked control")
	}
	matched := strings.Replace(rsArtPointSQL, "ORDER BY a.choice LIMIT 1", "ORDER BY a.choice DESC LIMIT 1", 1)
	if matched == rsArtPointSQL || digest(matched) != rsArtDigest(1) {
		t.Fatal("real matched-over-locked SQL mutant survived")
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1103`)
	season := func(sql string) any {
		rows, err := f.conn.QueryContext(t.Context(), sql, rsArtSourceArgs(1104, 1, 4)...)
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
			t.Fatal("lost root")
		}
		if err = rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		return v[30]
	}
	if season(rsArtSourceSQL) != nil {
		t.Fatal("positive retired ancestor control")
	}
	retired := strings.Replace(rsArtSourceSQL, "AND se.retired=0", "AND 1=1", 1)
	if retired == rsArtSourceSQL || season(retired) != int64(1103) {
		t.Fatal("real ancestor-visibility SQL mutant survived")
	}
}

func TestRSArtLocalProvenance(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	f.exec(t, `INSERT OR IGNORE INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('art-source','movies','/media','/media','root-a')`)
	f.exec(t, `INSERT OR IGNORE INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('art-object','art-source','asset','root-a','cover.jpg','r1','{}',1000,1)`)
	f.exec(t, `UPDATE artwork_selections SET source_object_id='art-object',source_object_revision='r1' WHERE entity_id=1103`)
	in := rsArtInput(f, 1104, 4, "poster", true)
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || !d.Present {
		t.Fatal("current object provenance", d, err)
	}
	for _, change := range []string{`UPDATE inventory_objects SET revision='r2' WHERE id='art-object'`, `UPDATE inventory_objects SET retired=1 WHERE id='art-object'`, `UPDATE library_sources SET incarnation='root-b' WHERE id='art-source'`, `UPDATE catalog_assets SET modified_ns=2 WHERE token='asset'`, `UPDATE library_sources SET library_id='other' WHERE id='art-source'`} {
		f.exec(t, change)
		d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if !errors.Is(err, ErrNotReady) || d.Present {
			t.Fatal("stale provenance", change, d, err)
		}
		f.exec(t, `UPDATE inventory_objects SET revision='r1',retired=0 WHERE id='art-object'`)
		f.exec(t, `UPDATE library_sources SET incarnation='root-a',library_id='movies' WHERE id='art-source'`)
		f.exec(t, `UPDATE catalog_assets SET modified_ns=1 WHERE token='asset'`)
	}
	// Object drift without an art revision still invalidates the private draft.
	r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() { f.exec(t, `UPDATE inventory_objects SET revision='r2' WHERE id='art-object'`) }}
	d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrNotReady) || d.Present || cost.Attempts != 2 {
		t.Fatal("unfenced source drift", d, cost.Attempts, err)
	}
}

func TestRSArtReadingUsesSharedEligibility(t *testing.T) {
	f := newRSArtSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	// Only a test recipe is available in this dependency base. This tests
	// composition, not the parent's real accepted-publication eligibility proof.
	f.exec(t, `BEGIN`)
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	f.exec(t, `UPDATE catalog_subjects SET kind=20 WHERE id=1101`)
	f.exec(t, `UPDATE catalog_entities SET kind=20 WHERE id=1101`)
	f.exec(t, `COMMIT`)
	rsArtSelect(t, f, 1101, "cover", "matched", 13)
	f = composeReadFixture(t, f, append(HarnessDefinitions(), ArtworkDefinitions()...), readfixture.LiveConfig(time.Now), true, rsArtHash(t))
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	in := rsArtInput(f, 1101, 20, "poster", true)
	d, _, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || d.Present {
		t.Fatal("default policy admitted Reading", d, err)
	}
	in.View.policy = readPolicyBinding{recipe: rsTestReadingPolicy, subject: 1101}
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.Owner != 1101 || d.Role != "cover" || d.Digest != rsArtDigest(13) {
		t.Fatal("compiled Reading policy composition", d, err)
	}
	f.exec(t, `BEGIN`)
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	f.exec(t, `UPDATE catalog_subjects SET kind=21 WHERE id=1101`)
	f.exec(t, `UPDATE catalog_entities SET kind=21 WHERE id=1101`)
	f.exec(t, `COMMIT`)
	in.View.Kind = 21
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || d.OwnerKind != 21 || d.Digest != rsArtDigest(13) {
		t.Fatal("comic policy composition", d, err)
	}
	in.View.policy.subject = 1102
	d, _, err = r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrInput) || d.Present {
		t.Fatal("different subject supplied eligibility", d, err)
	}
}

// Interpose after the exact-byte payload, including its native row cleanup.
// It delegates all forecast/admission/custody to the existing snapshot owner.
type rsArtBoundarySnapshots struct {
	*governor.Snapshots
	afterPayload func()
}

func (s *rsArtBoundarySnapshots) ReadForecasted(ctx context.Context, request governor.Request, budget time.Duration, forecast governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return s.Snapshots.ReadForecasted(ctx, request, budget, forecast, func(scope *governor.ReadScope) error {
		err := read(scope)
		if err == nil && s.afterPayload != nil {
			f := s.afterPayload
			s.afterPayload = nil
			f()
		}
		return err
	})
}

func TestRSArtCancellationAndHierarchyDrift(t *testing.T) {
	f, r := newRSArtFixture(t, false)
	in := rsArtInput(f, 1104, 4, "poster", true)
	r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: func() {
		f.exec(t, `UPDATE catalog_entities SET artwork_revision=artwork_revision+1 WHERE id=1103`)
		f.exec(t, `UPDATE artwork_selections SET digest=? WHERE entity_id=1103 AND choice='locked'`, rsArtDigest(3))
	}}
	d, cost, err := r.artwork(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || cost.Attempts != 2 || d.Digest != rsArtDigest(3) {
		t.Fatal("complete retry after source-head drift", d, cost.Attempts, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.snapshots = &rsArtBoundarySnapshots{Snapshots: f.snapshots, afterPayload: cancel}
	d, _, err = r.artwork(ctx, foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(d, ArtworkFact{}) {
		t.Fatal("cancelled private draft escaped", d, err)
	}
}
