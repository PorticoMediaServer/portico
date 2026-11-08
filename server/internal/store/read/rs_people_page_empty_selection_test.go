package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const rsPageLockedEmptyPortrait = `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('person',1,'portrait','locked',NULL,'','','','','f','test','2026-11-06T00:00:00Z')`

// A locked portrait with a NULL digest is a present blocking choice. The page
// must succeed with no portrait and must not fall back to the matched row.
func TestRSPeoplePageLockedEmptyPortraitBlocksMatched(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	out := finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{}))
	if out.Portrait == nil || out.Portrait.MIME != "image/png" {
		t.Fatalf("nonempty matched portrait control %+v", out.Portrait)
	}
	f.exec(t, rsPageLockedEmptyPortrait)
	// The empty choice is still a row, so the preflight sees locked first.
	rows, err := f.conn.QueryContext(t.Context(), rsPagePortraitOctetsSQL, int64(1), "locked")
	if err != nil || !rows.Next() {
		t.Fatalf("locked-empty octet row missing %v", err)
	}
	rows.Close()
	out = finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{}))
	if out.Portrait != nil || !out.Complete || out.Person == nil {
		t.Fatalf("locked-empty produced a portrait or stale page %+v", out)
	}
	var matched int
	if err = f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE kind='person' AND entity_id=1 AND role='portrait' AND choice='matched'`).Scan(&matched); err != nil || matched != 1 {
		t.Fatalf("matched row missing behind empty choice: %d %v", matched, err)
	}
	// Actual removal of the locked choice restores matched portrait art.
	f.exec(t, `DELETE FROM artwork_selections WHERE kind='person' AND entity_id=1 AND role='portrait' AND choice='locked'`)
	out = finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{}))
	if out.Portrait == nil || out.Portrait.MIME != "image/png" || out.Portrait.Width != 1 || out.Portrait.Height != 1 {
		t.Fatalf("matched portrait after removal %+v", out.Portrait)
	}
}

// The adopted exact-byte point keeps its shape: the locked-empty choice owns no
// blob bytes while the matched source has digest/blob/mime octets. The payload
// statement passes exact-byte gating with a zero-byte point.
func TestRSPeoplePagePortraitLockedEmptyExactBytes(t *testing.T) {
	f, _ := newRSPeoplePageFixture(t, false)
	rows, err := f.conn.QueryContext(t.Context(), rsPagePortraitOctetsSQL, int64(1), "matched")
	if err != nil {
		t.Fatal(err)
	}
	var matched []any
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	matched = make([]any, len(cols))
	values := make([]any, len(cols))
	for i := range values {
		matched[i] = &values[i]
	}
	if !rows.Next() {
		t.Fatal("missing matched octet row")
	}
	if err = rows.Scan(matched...); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	want := int64(4*64 + len("image/png"))
	if got, e := rsPointBytes(values); e != nil || got != want {
		t.Fatalf("matched portrait octets %d want %d (%v)", got, want, e)
	}
	f.exec(t, rsPageLockedEmptyPortrait)
	rows, err = f.conn.QueryContext(t.Context(), rsPagePortraitOctetsSQL, int64(1), "locked")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing locked-empty octet row")
	}
	for i := range values {
		values[i] = nil
		matched[i] = &values[i]
	}
	if err = rows.Scan(matched...); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if got, e := rsPointBytes(values); e != nil || got != 0 {
		t.Fatalf("locked-empty portrait octets %d (%v)", got, e)
	}
	// One reserved native byte is enough for the empty choice: the exact point
	// is zero and no blob payload is copied.
	if _, err = f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *governor.ReadScope) error {
		rows, calls, e := queryReadScope(scope, "rs.people.page.portrait", int64(1), "locked")
		if e != nil || calls != 2 || len(rows) != 1 || rows[0][0] != nil || rows[0][1] != nil {
			return errors.Join(ErrStale, e)
		}
		return nil
	}); err != nil {
		t.Fatalf("locked-empty exact-byte point %v", err)
	}
}

// A nonempty portrait selection whose source row is unavailable/retiring stays
// distinct from locked-empty: it refuses as stale instead of pretending the
// person has no portrait or inventing a fallback.
func TestRSPeoplePagePortraitUnavailableSourceStaysStale(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	f.exec(t, `PRAGMA foreign_keys=OFF`)
	f.exec(t, `DELETE FROM artwork_sources WHERE digest=printf('%064d',1)`)
	f.exec(t, `PRAGMA foreign_keys=ON`)
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	var err error
	var out RSPersonPage
	for n := 0; n < 30; n++ {
		out, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
		if err != nil || out.Complete {
			break
		}
	}
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(out, RSPersonPage{}) {
		t.Fatalf("unavailable nonempty portrait %+v %v", out, err)
	}
}

// A LIVE revocation still denies the whole page when the locked-empty portrait
// choice was consumed; no empty or partial page escapes as success.
func TestRSPeoplePageLockedEmptyPortraitLIVE(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, true)
	f.exec(t, rsPageLockedEmptyPortrait)
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	q, request, output := liveRequests("person-empty-portrait-revoke")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		v, _, e := r.Page(ctx, request, time.Second, out, p)
		if !reflect.DeepEqual(v, RSPersonPage{}) || !errors.Is(e, ErrDenied) {
			t.Fatal("locked-empty page escaped revocation", v, e)
		}
		return e
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}

// Marking an existing source for retirement must not make its selected bytes
// available to a newly prepared page; the digest still blocks matched fallback.
func TestRSPeoplePagePortraitRetiringSourceStaysStale(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	before := finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{}))
	if before.Portrait == nil {
		t.Fatal("ready portrait control missing")
	}
	f.exec(t, `UPDATE artwork_sources SET retiring=1,retirement_token=zeroblob(16) WHERE digest=printf('%064d',1)`)
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	var err error
	var out RSPersonPage
	for n := 0; n < 30; n++ {
		out, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
		if err != nil || out.Complete {
			break
		}
	}
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(out, RSPersonPage{}) {
		t.Fatalf("retiring nonempty portrait escaped: %+v %v", out, err)
	}
	f.exec(t, `UPDATE artwork_sources SET retiring=0,retirement_token=NULL WHERE digest=printf('%064d',1)`)
	after := finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{}))
	if after.Portrait == nil || !reflect.DeepEqual(before.Portrait, after.Portrait) {
		t.Fatalf("ready portrait after exact reset: before=%+v after=%+v", before.Portrait, after.Portrait)
	}
}
