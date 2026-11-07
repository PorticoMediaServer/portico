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
