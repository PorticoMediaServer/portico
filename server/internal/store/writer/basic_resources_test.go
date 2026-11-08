package writer

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Component controls only: W's existing synthetic profile, real exact-purpose
// grant, SAME native frame and physical rollback. No Basic recipe is qualified
// or enabled, and no statements need a registry/Capture for these helper calls.
func basicResourceTestTurn(t *testing.T, w *executor, path string, id CommandID, budget commandMemoryBudget) (*commandTurn, *governor.Authority) {
	t.Helper()
	g, a, wal, request := governedFixture(t, w.now)
	request.Key.Domain = uint16(id)
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	d := g.Try(request)
	if d.Grant == nil {
		t.Fatal(d.Refusal)
	}
	c, err := bindGovernedTurn(id, testRecipe().limit, g, a, wal, d.Grant, request)
	if err != nil {
		t.Fatal(errors.Join(err, a.Retired(d.Grant)))
	}
	c.basicMemory = budget
	tx, err := w.begin(t.Context(), id, testRecipe(), c)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			err = errors.Join(err, c.retired())
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	return &commandTurn{transaction: tx}, a
}

func TestBasicResourcesPurposeAndOriginalLease(t *testing.T) {
	w, path, clock := testExecutor(t)
	for _, id := range []CommandID{BasicStage, BasicSeal, BasicAccept} {
		purpose, err := basicPurposeForCommand(id)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(string(purpose), func(t *testing.T) {
			*clock = clock.Truncate(time.Millisecond)
			turn, _ := basicResourceTestTurn(t, w, path, id, commandMemoryBudget{})
			deadline, ok := turn.transaction.ctx.Deadline()
			if !ok || deadline != turn.transaction.custody.(*governedTurn).expected.Deadline {
				t.Fatal("missing original protected deadline", deadline)
			}
			expires := deadline.UnixMilli()
			accepted, err := turn.basicTimeMS(t.Context(), purpose, expires)
			if err != nil || accepted != clock.UnixMilli() {
				t.Fatal("exact-purpose timestamp refused", accepted, err)
			}
			for _, other := range []BasicPurpose{BasicPurposeStage, BasicPurposeSeal, BasicPurposeAccept, BasicPurpose("foreign")} {
				if other == purpose {
					continue
				}
				if got, err := turn.basicTimeMS(t.Context(), other, expires); got != 0 || !errors.Is(err, ErrInvalid) {
					t.Fatal("foreign purpose released time", other, got, err)
				}
				if err := checkBasicTerminal(t.Context(), turn, other, expires); !errors.Is(err, ErrInvalid) {
					t.Fatal("foreign purpose reached terminal", other, err)
				}
			}
			// A live lease must cover the WHOLE original deadline, not merely
			// this sample or the remaining terminal reservation.
			if got, err := turn.basicTimeMS(t.Context(), purpose, expires-1); got != 0 || !errors.Is(err, ErrConflict) {
				t.Fatal("short original lease released time", got, err)
			}
			*clock = clock.Add(time.Millisecond)
			if err := checkBasicTerminal(t.Context(), turn, purpose, expires); err != nil {
				t.Fatal(err)
			}
			if accepted != turn.transaction.start.UnixMilli() {
				t.Fatal("terminal changed accepted timestamp", accepted)
			}
			*clock = deadline
			// The grant is the original protected deadline here; its exact
			// expiry check precedes the native lease check and must still refuse.
			if err := checkBasicTerminal(t.Context(), turn, purpose, expires); !errors.Is(err, governor.ErrGrant) {
				t.Fatal("expired original grant reached terminal", err)
			}
		})
	}
}

func TestBasicResourcesMemorySameGrantAndRollback(t *testing.T) {
	w, path, clock := testExecutor(t)
	*clock = clock.Truncate(time.Millisecond)
	for _, id := range []CommandID{BasicStage, BasicSeal, BasicAccept} {
		purpose, _ := basicPurposeForCommand(id)
		t.Run(string(purpose), func(t *testing.T) {
			turn, a := basicResourceTestTurn(t, w, path, id, commandMemoryBudget{32768, 32768})
			before := a.Usage()
			if before[governor.ScanMinimum][governor.MemoryBytes] != 65536 {
				t.Fatal("missing real grant holding", before)
			}
			for _, n := range []uint64{1, 32768, 32768} {
				if err := turn.requireBasicMemory(t.Context(), purpose, n); err != nil {
					t.Fatal(n, err)
				}
			}
			for _, n := range []uint64{32769, 65536, math.MaxUint64} {
				var refused *Refusal
				if err := turn.requireBasicMemory(t.Context(), purpose, n); !errors.As(err, &refused) || refused.Code != RefusalCapacity {
					t.Fatal("borrowed native reserve", n, err)
				}
			}
			if err := turn.requireBasicMemory(t.Context(), purpose, 0); !errors.Is(err, ErrInvalid) {
				t.Fatal("zero-byte proof accepted", err)
			}
			other := BasicPurposeStage
			if purpose == other {
				other = BasicPurposeSeal
			}
			if err := turn.requireBasicMemory(t.Context(), other, 1); !errors.Is(err, ErrInvalid) {
				t.Fatal("foreign purpose borrowed memory", err)
			}
			c := turn.transaction.custody.(*governedTurn)
			c.g.Cancel(c.grant)
			if err := turn.requireBasicMemory(t.Context(), purpose, 1); !errors.Is(err, governor.ErrGrant) {
				t.Fatal("cancelled grant authorized memory", err)
			}
			if a.Usage() != before || w.db.Stats().InUse != 1 {
				t.Fatal("proof/cancellation refunded physical custody", a.Usage(), w.db.Stats())
			}
			if result, err := turn.transaction.rollback(nil); err != nil || result.state != rolledBack {
				t.Fatal("native rollback failed", result, err)
			}
			if a.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
				t.Fatal("native rollback failed to release holding", a.Usage(), w.db.Stats())
			}
		})
	}
}

func TestBasicResourcesRequireInstalledCustodyAndClosedPlans(t *testing.T) {
	w, path, _ := testExecutor(t)
	t.Run("uninstalled", func(t *testing.T) {
		turn, _ := basicResourceTestTurn(t, w, path, BasicAccept, commandMemoryBudget{})
		var refused *Refusal
		if err := turn.requireBasicMemory(t.Context(), BasicPurposeAccept, 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
			t.Fatal("uninstalled partition authorized memory", err)
		}
	})
	t.Run("plain_test_custody", func(t *testing.T) {
		tx, err := w.begin(t.Context(), BasicAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.rollback(nil)
		turn := &commandTurn{transaction: tx}
		var refused *Refusal
		if err := turn.requireBasicMemory(t.Context(), BasicPurposeAccept, 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
			t.Fatal("plain custody authorized memory", err)
		}
		if got, err := turn.basicTimeMS(t.Context(), BasicPurposeAccept, tx.start.Add(time.Second).UnixMilli()); got != 0 || !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
			t.Fatal("plain custody released time", got, err)
		}
	})
	plan := effectivePlan{basicMemory: commandMemoryBudget{32768, 32768}}
	if _, err := freezeCommandPlan(w, plan, nil, InventoryAccept); err == nil {
		t.Fatal("foreign plan carried Basic memory")
	} else {
		assertPartitionPolicyError(t, err, "memory_purpose")
	}
	// All three Basic purposes open only through their private complete-profile
	// composers. This bare partition has no definitions, steps or matching need
	// and still refuses before admission.
	for _, id := range []CommandID{BasicStage, BasicSeal, BasicAccept} {
		if _, err := freezeCommandPlan(w, plan, nil, id); err == nil {
			t.Fatal("incomplete Basic recipe enabled", id)
		} else {
			assertPartitionPolicyError(t, err, "basic_memory_partition")
		}
	}
}

func TestBasicResourcesClockAndLateCancellation(t *testing.T) {
	w, path, clock := testExecutor(t)
	for _, name := range []string{"high_water", "grant_before_clock", "grant_on_clock", "caller_on_clock"} {
		t.Run(name, func(t *testing.T) {
			*clock = clock.Truncate(time.Millisecond)
			turn, _ := basicResourceTestTurn(t, w, path, BasicSeal, commandMemoryBudget{32768, 32768})
			c := turn.transaction.custody.(*governedTurn)
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			originalNow := w.now
			defer func() { w.now = originalNow }()
			expires := turn.transaction.start.Add(time.Second).UnixMilli()
			want := governor.ErrGrant
			calls := 0
			switch name {
			case "high_water":
				*clock = clock.Add(2 * time.Millisecond)
				if _, err := turn.basicTimeMS(caller, BasicPurposeSeal, expires); err != nil {
					t.Fatal(err)
				}
				*clock = clock.Add(-time.Millisecond)
				want = ErrQuantum
			case "grant_before_clock":
				c.g.Cancel(c.grant)
				w.now = func() time.Time { calls++; return *clock }
			case "grant_on_clock":
				w.now = func() time.Time { c.g.Cancel(c.grant); return *clock }
			case "caller_on_clock":
				w.now = func() time.Time { cancel(); return *clock }
				want = context.Canceled
			}
			if got, err := turn.basicTimeMS(caller, BasicPurposeSeal, expires); got != 0 || !errors.Is(err, want) {
				t.Fatal("failed proof released timestamp", got, err, want)
			}
			if name == "grant_before_clock" && calls != 0 {
				t.Fatal("cancelled grant reached clock", calls)
			}
			// Keep cleanup at the established high-water after the deliberate
			// regression; never rebase the frame's own clock backwards.
			if name == "high_water" {
				*clock = clock.Add(time.Millisecond)
			}
		})
	}
}
