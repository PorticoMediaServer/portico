package writer

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A Basic original is funded in an earlier turn, so its smoke ceiling is later
// than the deadline the driver already recorded. bindFrame refuses any frame
// whose deadline is after the preparation's, so dispatch must clip to it.
func TestCommandDeadlineClipsToPreparedOriginal(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	ceiling := now.Add(MaxCommandSafetyHold)
	issued := now.Add(-time.Minute)
	later := now.Add(time.Hour)
	work := func(deadline time.Time) *scanDriverWork {
		return &scanDriverWork{preparation: &scanPreparation{expected: governor.Request{Deadline: deadline}}}
	}
	issuer := func(deadline time.Time) *inventoryInvocation {
		return &inventoryInvocation{issuer: &scanIssuerInvocation{deadline: deadline}}
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		scan *inventoryInvocation
		work *scanDriverWork
		want time.Time
	}{
		{"safety_ceiling", context.Background(), nil, nil, ceiling},
		{"issuer_deadline", context.Background(), issuer(issued), nil, issued},
		{"prepared_original", context.Background(), nil, work(issued), issued},
		{"prepared_original_beats_issuer", context.Background(), issuer(now.Add(time.Second)), work(issued), issued},
		{"later_preparation_ignored", context.Background(), issuer(issued), work(later), issued},
		{"zero_preparation_ignored", context.Background(), nil, work(time.Time{}), ceiling},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dispatchDeadline(tc.ctx, now, tc.scan, tc.work)
			if got != tc.want {
				t.Fatalf("deadline %v, want %v", got, tc.want)
			}
			// Same predicate the driver applies to the owned frame.
			if tc.work != nil && tc.work.preparation.expected.Deadline != (time.Time{}) && got.After(tc.work.preparation.expected.Deadline) {
				t.Fatal("frame deadline outlives the prepared original")
			}
		})
	}
	caller, cancel := context.WithDeadline(context.Background(), issued)
	defer cancel()
	if got := dispatchDeadline(caller, now, nil, work(now.Add(time.Second))); got != issued {
		t.Fatalf("caller deadline %v, want %v", got, issued)
	}
}
