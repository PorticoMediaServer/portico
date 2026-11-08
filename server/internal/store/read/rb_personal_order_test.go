package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_PERSONAL_ORDER_JOURNAL
func newRBPersonalOrderSource(t *testing.T) *browseFixture {
	f := newBrowseSource(t)
	f.exec(t, `DELETE FROM catalog_entities WHERE id>12`)
	f.exec(t, `UPDATE catalog_entities SET sort_key=CASE id WHEN 1 THEN 'z' WHEN 2 THEN 'A' WHEN 3 THEN 'a' WHEN 4 THEN 'é' WHEN 5 THEN 'É' WHEN 6 THEN 'b' WHEN 7 THEN 'B' WHEN 8 THEN 'c' WHEN 9 THEN 'd' WHEN 10 THEN 'e' WHEN 11 THEN 'f' ELSE 'g' END`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at)
SELECT 'p',public_id,id,library_id,CASE id WHEN 1 THEN 0 ELSE 7 END,CASE id WHEN 1 THEN 9223372036854775806 ELSE 9 END,CASE id WHEN 1 THEN '2026-01-01T00:00:00Z' ELSE '2025-01-01T00:00:00Z' END FROM catalog_entities WHERE id IN(1,2,4)`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at) SELECT 'q',public_id,id,library_id,10,100,'2030-01-01T00:00:00Z' FROM catalog_entities WHERE id=5`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at) SELECT 'p',public_id,id,library_id,10,9223372036854775807,'2031-01-01T00:00:00Z' FROM catalog_entities WHERE id=9`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at) SELECT 'p',public_id,id,library_id,NULL,0,'' FROM catalog_entities WHERE id=11`)
	return f
}

// END_RB_PERSONAL_ORDER_JOURNAL

func rbPersonalOrderFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_personal_order_test.go")
	if e != nil {
		t.Fatal(e)
	}
	a := strings.Index(string(data), "// BEGIN_RB_PERSONAL_ORDER_JOURNAL\n")
	b := strings.Index(string(data), "// END_RB_PERSONAL_ORDER_JOURNAL\n")
	if a < 0 || b <= a {
		t.Fatal("personal source journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[a:b]...)))
}
func rbPersonalOrderFixture(t *testing.T, live bool) *browseFixture {
	t.Helper()
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	cfg.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	if live {
		cfg = readfixture.LiveConfig(time.Now)
	}
	return composeReadFixture(t, newRBPersonalOrderSource(t), append(browseDefinitions(), RBPersonalOrderDefinitions()...), cfg, !live, rbPersonalOrderFixtureHash(t))
}

func TestRBPersonalOrderCompleteMissingAndValuedRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalOrderFixture(t, false)
		for _, order := range []PersonalOrder{PersonalRatingOrder, PersonalPlayedOrder, PersonalPlayCountOrder} {
			for _, desc := range []bool{false, true} {
				in := PersonalOrderInput{View: f.input, Order: order}
				in.View.Limit = 3
				in.View.Descending = desc
				var got []int64
				for n := 0; ; n++ {
					if n > 8 {
						t.Fatal("cursor stalled")
					}
					page, _, e := f.reader.personalOrderExact(t.Context(), readfixture.ReadRequest(), time.Second, f.output, in)
					if e != nil || page.Total != 11 || page.RawRows != 12 {
						t.Fatal(order, desc, page, e)
					}
					for _, v := range page.Items {
						got = append(got, v.ID)
					}
					if page.Exhausted {
						if page.After != nil {
							t.Fatal("exhausted cursor")
						}
						break
					}
					if page.After == nil {
						t.Fatal("lost exact continuation")
					}
					in.After = page.After
				}
				var want []int64
				if order == PersonalRatingOrder {
					want = []int64{3, 6, 7, 8, 10, 11, 12, 1, 5, 2, 4}
					if desc {
						want = []int64{4, 2, 3, 6, 7, 8, 10, 11, 12, 1, 5}
					}
				} else {
					want = []int64{3, 6, 7, 8, 10, 11, 12, 5, 2, 4, 1}
					if desc {
						want = []int64{1, 4, 2, 3, 6, 7, 8, 10, 11, 12, 5}
					}
				}
				if !slices.Equal(got, want) {
					t.Fatal("profile/public-identity/missing-title/value/int64 ordering differs", order, desc, got, want)
				}
			}
		}
	})
}

func TestRBPersonalOrderLargeRawSetAndPublicationRefusals(t *testing.T) {
	for _, mode := range []string{"large-hidden", "revoke", "personal-revision"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := rbPersonalOrderFixture(t, false)
				in := PersonalOrderInput{View: f.input, Order: PersonalPlayCountOrder}
				want := ErrStale
				if mode == "large-hidden" {
					f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired) SELECT id,public_id,library_id,kind,'Hidden','h','h','h','h',0,zeroblob(16),zeroblob(16),1 FROM catalog_subjects WHERE id BETWEEN 13 AND 270`)
					want = ErrNotReady
				} else {
					f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
						if mode == "revoke" {
							f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
						} else {
							f.exec(t, `UPDATE personal_items SET play_count=0 WHERE profile_id='p' AND item_id=1`)
							f.exec(t, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','movies',2) ON CONFLICT(profile_id,library_id) DO UPDATE SET revision=revision+1`)
						}
					}}
					if mode == "revoke" {
						want = ErrDenied
					}
				}
				page, _, e := f.reader.personalOrderExact(t.Context(), readfixture.ReadRequest(), time.Second, f.output, in)
				if !errors.Is(e, want) || !reflect.DeepEqual(page, PersonalOrderPage{}) {
					t.Fatal("incomplete or invalid page escaped", mode, page, e)
				}
			})
		})
	}
}

func TestRBPersonalOrderPublicLIVEAndCursorBinding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalOrderFixture(t, true)
		in := PersonalOrderInput{View: f.input, Order: PersonalRatingOrder}
		in.View.Limit = 3
		q, request, output := liveRequests("personal-exact")
		if _, _, e := f.reader.PersonalOrderExact(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(e, ErrInput) {
			t.Fatal("LIVE bypass", e)
		}
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			page, _, e := f.reader.PersonalOrderExact(ctx, request, time.Second, out, in)
			if e != nil {
				return e
			}
			if len(page.Items) != 3 || page.After == nil || page.Exhausted {
				t.Fatal(page)
			}
			assertLiveUsage(t, f, 1, 0)
			in.After = page.After
			in.Order = PersonalPlayCountOrder
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		assertLiveUsage(t, f, 0, 0)
		q, request, output = liveRequests("personal-other-sort")
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			bad, _, e := f.reader.PersonalOrderExact(ctx, request, time.Second, out, in)
			if !errors.Is(e, ErrStale) || !reflect.DeepEqual(bad, PersonalOrderPage{}) {
				t.Fatal("cross-sort cursor", bad, e)
			}
			return e
		})
		if !errors.Is(e, ErrStale) {
			t.Fatal(e)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func TestRBPersonalOrderRejectsMismatchedNativeIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalOrderFixture(t, false)
		_, e := f.conn.ExecContext(t.Context(), `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating) SELECT 'p',zeroblob(16),id,library_id,10 FROM catalog_entities WHERE id=10`)
		if e == nil {
			t.Fatal("native personal identity FK accepted a mismatched retained subject")
		}
	})
}
