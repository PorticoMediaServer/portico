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

func TestRSSearchPublishFreshFenceAndJoinedConsumer(t *testing.T) {
	for _, mode := range []string{"ready", "revoke-during-encoding", "source-during-encoding", "consumer-error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := readfixture.LiveConfig(time.Now)
				cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
				f := composeReadFixture(t, newNativeSource(t), NativeFullSearchDefinitions(), cfg, false, nativeFixtureHash(t))
				view := f.input
				view.Kind = 0
				view.Limit = 20
				r, e := NewRSSearchReader(f.authority, f.snapshots)
				if e != nil {
					t.Fatal(e)
				}
				query, e := search.PlanQuery("matrix", true)
				if e != nil {
					t.Fatal(e)
				}
				p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, RSSearchInput{Views: []BrowseInput{view}, Query: query, Group: 4, Full: true, Order: "relevance", Capacity: 64})
				if e != nil || ref != nil {
					t.Fatal(e, ref)
				}
				defer p.Close()
				native := r.read
				calls, encoded, delivered := 0, 0, 0
				r.read = func(ctx context.Context, req governor.Request, b time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
					calls++
					return native(ctx, req, b, out, in, q)
				}
				consumerErr := errors.New("consumer stopped")
				var done bool
				for poll := 0; poll < 100; poll++ {
					fg, req, out := liveRequests("rs-publish")
					req.Budget.RawRows = 4096
					before := calls
					_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
						var e error
						done, _, e = r.Publish(ctx, req, time.Second, out, p, func(ctx context.Context, page RSSearchPage) ([]byte, error) {
							encoded++
							if !page.Complete || len(page.Groups) != 1 || len(page.Groups[0].Hits) != 2 {
								t.Fatal(page)
							}
							switch mode {
							case "revoke-during-encoding":
								f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
							case "source-during-encoding":
								f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
							}
							return []byte("bounded owned response"), ctx.Err()
						}, func(ctx context.Context, b []byte) error {
							delivered++
							if string(b) != "bounded owned response" || p.closed || f.authority.ForegroundUsage().Live != 1 {
								t.Fatal("consumer lost custody")
							}
							if mode == "consumer-error" {
								return consumerErr
							}
							return ctx.Err()
						})
						return e
					})
					if calls-before != 1 {
						t.Fatal("extra native invocation", calls-before)
					}
					if done || e != nil {
						break
					}
				}
				if encoded != 1 || !p.closed {
					t.Fatal("encoding or physical join incomplete", encoded, p.closed, e)
				}
				switch mode {
				case "ready":
					if e != nil || !done || delivered != 1 {
						t.Fatal(done, delivered, e)
					}
				case "consumer-error":
					if !errors.Is(e, consumerErr) || delivered != 1 {
						t.Fatal(delivered, e)
					}
				case "revoke-during-encoding":
					if !errors.Is(e, ErrDenied) || delivered != 0 {
						t.Fatal("encoded revoked payload escaped", delivered, e)
					}
				case "source-during-encoding":
					if !errors.Is(e, ErrStale) || delivered != 0 {
						t.Fatal("encoded stale payload escaped", delivered, e)
					}
				}
				assertLiveUsage(t, f, 0, 0)
			})
		})
	}
}
