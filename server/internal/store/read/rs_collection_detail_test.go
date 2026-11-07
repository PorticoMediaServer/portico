package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRSCollectionDetailAuthoredAndLocal(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "authored", true: "local"}[local], func(t *testing.T) {
			f, r := newRSAutoFixture(t, func(f *browseFixture) {
				if local {
					f.exec(t, `UPDATE catalog_automatic_collections SET provider='nfo',provider_id='local'`)
					f.exec(t, `UPDATE catalog_entities SET automatic_collection_name=' LOCAL ' WHERE id IN(1,2)`)
				} else {
					f.exec(t, `DELETE FROM catalog_automatic_collections`)
					f.exec(t, `DELETE FROM catalog_collection_members`)
					f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1101,4,'a'),(1101,1,'b'),(1101,2,'b')`)
				}
			})
			p := rsBeginCollection(t, f, r)
			rsCompleteCollection(t, f, p)
			d, _, e := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
			if e != nil {
				t.Fatal(e)
			}
			var got []int64
			for _, v := range d.Items {
				got = append(got, v.ID)
			}
			want := []int64{4, 1, 2}
			origin := "owner"
			if local {
				want = []int64{1, 4, 5}
				origin = "automatic"
			}
			if !slices.Equal(got, want) || d.ExactCount != len(want) || d.Origin != origin {
				t.Fatal(d, got, want)
			}
		})
	}
}
func TestRSCollectionDetailPublicationAndLease(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	for _, mode := range []string{"revoke", "identity", "cancel", "foreground"} {
		t.Run(mode, func(t *testing.T) {
			p := rsBeginCollection(t, f, r)
			rsCompleteCollection(t, f, p)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "revoke":
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			case "identity":
				f.exec(t, `UPDATE catalog_automatic_collections SET provider_id='D'`)
			case "cancel":
				cancel()
			case "foreground":
				if e := f.authority.InterruptPreparations(governor.PublicationFootprint{{Library: 1}}); e != nil {
					t.Fatal(e)
				}
			}
			d, _, e := p.detail(ctx, foregroundRequest(), time.Second, f.output, 0, f.input.Now)
			if e == nil || !reflect.DeepEqual(d, CollectionDetail{}) || !p.closed {
				t.Fatal("invalid ready result", d, e)
			}
			f.exec(t, `UPDATE authorization_session_families SET revoked=0`)
			f.exec(t, `UPDATE catalog_automatic_collections SET provider_id='C'`)
		})
	}
}
func TestRSCollectionDetailActualBytesAndDefaultPolicy(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, strings.Repeat("界", 12000))
	p := rsBeginCollection(t, f, r)
	rsCompleteCollection(t, f, p)
	d, _, e := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if e != nil || len(d.Items) != 2 || d.Items[0].Title != strings.Repeat("界", 12000) {
		t.Fatal("actual-byte payload", e)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	p = rsBeginCollection(t, f, r)
	step, _, e := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if !errors.Is(e, ErrUnqualified) || step.Ready || !p.closed {
		t.Fatal("unproved container policy", e)
	}
}

func TestRSCollectionDetailContinuationAndLive(t *testing.T) {
	f := composeReadFixture(t, newRSAutoSource(t), append(browseDefinitions(), AutomaticCollectionDefinitions()...), rsAutoConfig(), false, rsAutoHash(t))
	r, e := NewCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	f.input.Limit = 1
	var after *CollectionDetailCursor
	for page := 0; page < 2; page++ {
		p := rsBeginCollection(t, f, r)
		// Each pending quantum is its own foreground entry; the SAME preparation
		// lease survives consumer return and no owner priority/deadline is renewed.
		for n := 0; n < 100; n++ {
			q, request, output := liveRequests("collection-live")
			var step CollectionAdvance
			_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
				var e error
				step, _, e = p.Advance(ctx, request, time.Second, out)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			if step.Ready {
				break
			}
			if n == 99 {
				t.Fatal("pending forever")
			}
		}
		q, request, output := liveRequests("collection-handoff")
		var d CollectionDetail
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			d, _, e = p.Detail(ctx, request, time.Second, out, after)
			if e == nil && f.authority.ForegroundUsage().Live != 1 {
				t.Fatal("consumer custody lost")
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		if len(d.Items) != 1 || d.Items[0].ID != []int64{1, 4}[page] || d.ExactCount != 2 {
			t.Fatal(d)
		}
		after = d.Next
	}
	if after != nil {
		t.Fatal("terminal continuation")
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRSCollectionPreparationExpiryAndSourceFence(t *testing.T) {
	now := time.Now()
	clock := now
	cfg := rsAutoConfig()
	cfg.Now = func() time.Time { return clock }
	f := composeReadFixture(t, newRSAutoSource(t), append(browseDefinitions(), AutomaticCollectionDefinitions()...), cfg, true, rsAutoHash(t))
	r, e := NewCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	p := rsBeginCollection(t, f, r)
	deadline := p.Deadline()
	_, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if e != nil {
		t.Fatal(e)
	}
	if !p.Deadline().Equal(deadline) {
		t.Fatal("poll renewed deadline")
	}
	clock = now.Add(13 * time.Second)
	step, _, e := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if !errors.Is(e, governor.ErrPreparationExpired) || step.Ready || !p.closed || p.nodes != nil {
		t.Fatal("expired work returned exact result", e)
	}
	clock = now
	p = rsBeginCollection(t, f, r)
	_, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, `UPDATE metadata_provider_policy SET revision=2`)
	step, _, e = p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now)
	if !errors.Is(e, ErrStale) || step.Ready || !p.closed {
		t.Fatal("unversioned policy drift survived", e)
	}
}
