package read

import (
	"errors"
	"reflect"
	"testing"
)

func TestRBHomeFixedRetryPinsPrincipal(t *testing.T) {
	for _, mode := range []string{"profile-before-final", "profile-between-attempts", "permission-shrink"} {
		homeFixedCase(t, mode, func(t *testing.T) {
			f, r := homeFixedFixture(t, func(f *browseFixture) {
				retrySecondProfile(t, f)
				f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
				homeFixedEntitySeed(t, f, 2000, 1, nil, nil, nil, "2026-10-01T00:00:00.000000000Z")
				homeFixedAssetSeed(t, f, 2000)
				homeFixedStateSeed(t, f, 2000, 5000, "in_progress")
				f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,resume_unit,activity_at,activity_state,resume_playback_id,favorite) SELECT 'p2',public_id,id,library_id,9000,0,'2026-11-02T12:00:00.000000000Z','in_progress','second-play',1 FROM catalog_entities WHERE id=2000`)
			})
			hook := &savedViewWindowHook{base: f.snapshots}
			hook.hook = func(n int) {
				if mode == "profile-before-final" && n == 2 || mode == "profile-between-attempts" && n == 3 {
					retrySwitchProfile(t, f)
				}
				if mode == "profile-between-attempts" && n == 2 {
					f.exec(t, `UPDATE libraries SET name='Fresh name' WHERE id='movies'`)
				}
				if mode == "permission-shrink" && n == 2 {
					f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id='a'`)
				}
			}
			r.native.snapshots = hook
			page, _, e := homeFixedRead(t, f, r, "continue")
			if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, HomeFixedPage{}) {
				t.Fatal("changed security context published", page, e)
			}
			savedViewSameDeadline(t, hook)
			// Fresh operations still accept the now-current authority. In particular,
			// the p2 case is not rejected merely because its personal tuple is absent.
			r.native.snapshots = f.snapshots
			page, _, e = homeFixedRead(t, f, r, "continue")
			want := int64(9000)
			if mode == "permission-shrink" {
				want = 5000
			}
			if e != nil || !page.Exact || len(page.Items) != 1 || page.Items[0].ResumePosition != want {
				t.Fatal("current principal fixture invalid", page, e)
			}
		})
	}
}
