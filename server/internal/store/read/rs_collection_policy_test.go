package read

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestRSCollectionCompleteRestrictedPolicy(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	// Authored source8 counters intentionally claim empty and the root's own label
	// is hidden. Neither is the inferred R21 membership or its eligibility proof.
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=99,admission_member_count=0,admission_unrated_members=0,admission_min_known_age=NULL WHERE id=1101`)
	for _, c := range []struct {
		name      string
		one, four any
		allow     int
		want      []int64
		denied    bool
	}{
		{"mixed", int64(8), int64(18), 0, []int64{1}, false},
		{"all-hidden", int64(18), int64(18), 0, nil, true},
		{"unrated-blocked", nil, nil, 0, nil, true},
		{"unrated-allowed", nil, nil, 1, []int64{1, 4}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=? WHERE id=1`, c.one)
			f.exec(t, `UPDATE catalog_entities SET admission_own_age=? WHERE id=4`, c.four)
			f.exec(t, `UPDATE profile_restrictions SET allow_unrated=? WHERE profile_id='p'`, c.allow)
			p := rsBeginCollection(t, f, r)
			rsCompleteCollection(t, f, p)
			if !p.nonempty {
				t.Fatal("logical membership confused with visible membership")
			}
			d, _, err := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
			if c.denied {
				if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, CollectionDetail{}) {
					t.Fatal("hidden set escaped", d, err)
				}
			} else {
				var ids []int64
				for _, v := range d.Items {
					ids = append(ids, v.ID)
				}
				if err != nil || !slices.Equal(ids, c.want) || d.ExactCount != len(c.want) {
					t.Fatal(d, err)
				}
			}
		})
	}
	// Complete empty source is a different case from all-hidden nonempty.
	f.exec(t, `DELETE FROM metadata_field_origins WHERE field='relations'`)
	f.exec(t, `UPDATE catalog_automatic_member_overrides SET present=0`)
	p := rsBeginCollection(t, f, r)
	rsCompleteCollection(t, f, p)
	d, _, err := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if err != nil || p.nonempty || d.ExactCount != 0 || !d.Exhausted {
		t.Fatal("genuinely empty detail", d, err)
	}
	// The default browse recipe remains fail-closed even after a closed recipe was used.
	_, _, err = f.reader.window(t.Context(), foregroundRequest(), time.Second, f.output, f.input)
	if !errors.Is(err, ErrUnqualified) {
		t.Fatal("default browse opened", err)
	}
}

func TestRSCollectionPolicyRequiresOwnedCompleteWork(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	p := rsBeginCollection(t, f, r)
	in := p.in.View
	in.policy.recipe = rsCollectionDeliveryPolicy
	_, _, err := f.reader.window(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) {
		t.Fatal("pending delivery accepted", err)
	}
	in.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: 1101}
	if _, err := resolveReadPolicy(in); !errors.Is(err, ErrInput) {
		t.Fatal("nil domain work accepted", err)
	}
	in.policy = readPolicyBinding{state: p}
	if _, err := resolveReadPolicy(in); !errors.Is(err, ErrInput) {
		t.Fatal("default accepts domain work", err)
	}
	rsCompleteCollection(t, f, p)
	// Private work integrity is checked again at delivery, not merely at Ready.
	p.nodes[0].id = 999
	d, _, err := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatal("changed ready tree accepted", d, err)
	}
}

func TestRSCollectionRestrictedFinalSourceAndPolicy(t *testing.T) {
	f, r := newRSAutoFixture(t, nil)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=8 WHERE id IN(1,4)`)
	p := rsBeginCollection(t, f, r)
	rsCompleteCollection(t, f, p)
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE profile_restrictions SET maximum_age=5 WHERE profile_id='p'`) }}
	d, _, err := p.detail(t.Context(), foregroundRequest(), time.Second, f.output, 0, f.input.Now)
	if err == nil || !reflect.DeepEqual(d, CollectionDetail{}) || !p.closed {
		t.Fatal("changed policy released cached values", d, err)
	}
}
