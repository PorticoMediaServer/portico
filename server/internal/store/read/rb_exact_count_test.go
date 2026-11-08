package read

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBExactEligibleCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, reader := newRBExactFacetFixture(t)
		f.exec(t, `UPDATE catalog_entities SET year=CASE id WHEN 1 THEN 1799 ELSE 2200 END WHERE id IN(1,2)`)
		job, err := reader.BeginEligibleCount(t.Context(), 1, 16<<20, f.input)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		first, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
		if err != nil || first.Ready || first.RawRows != rbFacetRaw {
			t.Fatal("full raw quantum became an exact count", first, err)
		}
		last, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
		if err != nil || !last.Ready || last.RawRows != 1099-rbFacetRaw {
			t.Fatal("missing raw EOF", last, err)
		}
		if d := f.governor.Try(rbFacetPublicationRequest()); d.Grant != nil {
			t.Fatal("ready count lost its publication lease")
		}
		q, request, output := liveRequests("exact-count")
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			got, _, err := job.Count(ctx, request, time.Second, out, f.input)
			if err != nil {
				return err
			}
			// All eligible entities count, including year=0 and dates outside the
			// facet domain; ID1040 is in another library and every ninth entity is retired.
			var want int64
			for id := int64(1); id <= 1100; id++ {
				if id%9 != 0 && id != 1040 {
					want++
				}
			}
			if got.Count != want {
				t.Fatal("count inherited the year-facet missing rule", got.Count, want)
			}
			assertLiveUsage(t, f, 1, 0)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertLiveUsage(t, f, 0, 0)
	})
}

func TestRBExactEligibleCountEmptyAndDrift(t *testing.T) {
	for _, mode := range []string{"empty", "source-drift"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, reader := newRBExactFacetFixture(t)
				if mode == "empty" {
					f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1`)
				}
				job, err := reader.BeginEligibleCount(t.Context(), 1, 16<<20, f.input)
				if err != nil {
					t.Fatal(err)
				}
				defer job.Close()
				for n := 0; n < 2; n++ {
					step, err := job.Advance(t.Context(), rbFacetReadRequest(), time.Second)
					if err != nil || step.Ready != (n == 1) {
						t.Fatal("eligible emptiness replaced raw EOF", step, err)
					}
				}
				if mode == "source-drift" {
					f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
				}
				q, request, output := liveRequests("exact-count-" + mode)
				_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
					got, _, err := job.Count(ctx, request, time.Second, out, f.input)
					if got != (EligibleCount{}) {
						t.Fatal("empty/stale count exposed a value", got, err)
					}
					if mode == "empty" && err != nil {
						t.Fatal(err)
					}
					if mode == "source-drift" && !errors.Is(err, ErrStale) {
						t.Fatal("source drift accepted", err)
					}
					return err
				})
				if mode == "empty" && err != nil {
					t.Fatal(err)
				}
				if mode == "source-drift" && !errors.Is(err, ErrStale) {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestRBExactEligibleCountDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, reader := newRBExactFacetFixture(t)
		job, err := reader.BeginEligibleCount(t.Context(), 1, 16<<20, f.input)
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		for n := 0; n < 2; n++ {
			if _, err = job.Advance(t.Context(), rbFacetReadRequest(), time.Second); err != nil {
				t.Fatal(err)
			}
		}
		f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
		q, request, output := liveRequests("exact-count-denied")
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			got, _, err := job.Count(ctx, request, time.Second, out, f.input)
			if !errors.Is(err, ErrDenied) || got != (EligibleCount{}) {
				t.Fatal("revoked count escaped", got, err)
			}
			return err
		})
		if !errors.Is(err, ErrDenied) {
			t.Fatal("lost fresh count refusal", err)
		}
	})
}
