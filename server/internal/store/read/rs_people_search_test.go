package read

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestRSPeopleSearchNativeSummariesAndDelivery(t *testing.T) {
	for _, mode := range []string{"ready", "person-art-during-encode", "revoke-during-encode", "consumer-error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := readfixture.LiveConfig(time.Now)
				cfg.Preparation = &governor.PreparationProfile{MaxActive: 1, MaxBytes: 4 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
				defs := append(NativeFullSearchDefinitions(), PersonCreditsDefinitions()...)
				defs = append(defs, RSPeoplePageDefinitions()...)
				f := composeReadFixture(t, newNativeSource(t), defs, cfg, false, nativeFixtureHash(t))
				r, e := NewRSPeopleSearchReader(f.snapshots, f.authority)
				if e != nil {
					t.Fatal(e)
				}
				query, e := search.PlanQuery("keanu", true)
				if e != nil {
					t.Fatal(e)
				}
				view := f.input
				view.Kind = 4
				p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, RSPeopleSearchInput{Views: []BrowseInput{view}, Query: query, Order: "relevance"})
				if e != nil || ref != nil {
					t.Fatal(e, ref)
				}
				defer p.Close()
				if p.work.lease != p.search.lease {
					t.Fatal("second preparation lease")
				}
				native := r.search.read
				calls := 0
				r.search.read = func(ctx context.Context, q governor.Request, b time.Duration, out BrowseOutput, in FullSearchInput, query search.Query) (FullSearchPage, *BrowseReadResult, error) {
					calls++
					return native(ctx, q, b, out, in, query)
				}
				encoded, consumed := 0, 0
				consumerErr := errors.New("consumer stopped")
				done := false
				for poll := 0; poll < 100; poll++ {
					fg, req, out := liveRequests("people-search")
					req.Budget.RawRows = 4096
					before := calls
					_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
						var err error
						done, _, err = r.Publish(ctx, req, time.Second, out, p, func(ctx context.Context, page RSPeopleSearchPage) ([]byte, error) {
							encoded++
							if !page.Complete || page.Pending || page.CountStatus != SearchCountUnknown || len(page.People) != 1 || page.People[0].ID != 1 || page.People[0].Name != "Keanu" || page.People[0].CreditCount != 1 || len(page.People[0].Roles) != 1 || page.People[0].Roles[0] != "Acting" || page.Next != nil {
								t.Fatal(page)
							}
							if mode == "person-art-during-encode" {
								f.exec(t, `UPDATE catalog_people SET artwork_revision=artwork_revision+1 WHERE id=1`)
							}
							if mode == "revoke-during-encode" {
								f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
							}
							return []byte("owned people summary"), ctx.Err()
						}, func(ctx context.Context, b []byte) error {
							consumed++
							if p.search.closed || p.work == nil || f.authority.ForegroundUsage().Live != 1 {
								t.Fatal("lost physical custody")
							}
							if mode == "consumer-error" {
								return consumerErr
							}
							return ctx.Err()
						})
						return err
					})
					if e != nil {
						t.Logf("poll=%d selected=%t captured=%t current=%d entries=%d calls=%d", poll, p.selected, p.work != nil && p.work.captured, func() int64 {
							if p.work != nil {
								return p.work.current.person
							}
							return -1
						}(), len(p.entries), calls)
					}
					if calls-before != 1 {
						t.Fatal("native calls in poll", calls-before)
					}
					if done || e != nil {
						break
					}
				}
				if encoded != 1 || !p.search.closed || p.work != nil {
					t.Fatal("delivery did not finish", encoded, e)
				}
				switch mode {
				case "ready":
					if e != nil || !done || consumed != 1 {
						t.Fatal(done, consumed, e)
					}
				case "person-art-during-encode":
					if !errors.Is(e, ErrStale) || consumed != 0 {
						t.Fatal("stale summary escaped", consumed, e)
					}
				case "revoke-during-encode":
					if !errors.Is(e, ErrDenied) || consumed != 0 {
						t.Fatal("revoked summary escaped", consumed, e)
					}
				case "consumer-error":
					if !errors.Is(e, consumerErr) || consumed != 1 {
						t.Fatal(consumed, e)
					}
				}
				assertLiveUsage(t, f, 0, 0)
			})
		})
	}
}
