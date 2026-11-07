package writer

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Closed component profiles only. Domains cannot supply a replacement recipe,
// construct a second grant or treat these synthetic figures as qualification.
type searchResourceFixtureProfile uint8

const searchImageStatementFixture searchResourceFixtureProfile = 1

// Actual SAME governor/binder/frame; synthetic component resources, no public
// writer/capability/production recipe or native statement registration supplied.
// SQL controls must complete their real Capture BEFORE calling this helper,
// then construct newCommandTurn on the returned SAME transaction.
func searchResourcesFixture(t *testing.T, w *executor, path string, budget searchImageMemoryBudget, profiles ...searchResourceFixtureProfile) (*commandTurn, *governor.Authority) {
	t.Helper()
	recipe, library := testRecipe(), int64(1)
	if len(profiles) > 1 {
		t.Fatal("multiple search fixture profiles")
	}
	if len(profiles) == 1 {
		if profiles[0] != searchImageStatementFixture {
			t.Fatal("unknown search fixture profile")
		}
		// Eighteen closed image/receipt statement reservations, each
		// {16 rows,2000 VM,16 pages,65536 WAL,100us}, plus the complete
		// setup and terminal reserves below. NOT a measured native profile.
		recipe = turnRecipe{
			limit:    Work{320, 37000, 320, 1310720, 25 * time.Millisecond},
			setup:    Work{16, 500, 16, 65536, time.Millisecond},
			terminal: Work{16, 500, 16, 65536, time.Millisecond},
		}
		library = 3 // actual image-BEGIN native fixture library
	}
	total := int64(65536)
	if budget != (searchImageMemoryBudget{}) {
		if budget.preparationBytes > math.MaxInt64 || budget.reservedBytes > math.MaxInt64-budget.preparationBytes {
			t.Fatal("overflow fixture partition")
		}
		total = int64(budget.preparationBytes + budget.reservedBytes)
	}
	if err := validateSearchImageMemory(budget, total); err != nil {
		t.Fatal(err)
	}
	g, a, wal, request := governedFixture(t, func() time.Time { return w.now() }, governedFixtureProfile{recipe, total})
	request.Key = governor.JobKey{Domain: uint16(SearchImageStage), ID: "component/publisher"}
	request.Class = governor.Background
	request.Footprint = []governor.Scope{{Library: library, Domain: uint16(SearchImageStage)}}
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	c, err := bindGovernedTurn(SearchImageStage, recipe.limit, g, a, wal, decision.Grant, request)
	if err != nil {
		_ = a.Retired(decision.Grant)
		t.Fatal(err)
	}
	c.searchImageMemory = budget
	tx, err := w.begin(t.Context(), SearchImageStage, recipe, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	return &commandTurn{transaction: tx}, a
}

func TestSearchImageStatementFixtureReservesWholeTurn(t *testing.T) {
	w, path, _ := testExecutor(t)
	turn, a := searchResourcesFixture(t, w, path, searchImageMemoryBudget{4 << 20, 4 << 20}, searchImageStatementFixture)
	c := turn.transaction.custody.(*governedTurn)
	wantScope := governor.Scope{Library: 3, Domain: uint16(SearchImageStage)}
	if c.expected.Class != governor.Background || len(c.expected.Footprint) != 1 || c.expected.Footprint[0] != wantScope {
		t.Fatal("statement profile lost its exact background scope", c.expected)
	}
	before := a.Usage()
	// Prospective ledger controls only: no accepting SQL or public issuer.
	step := Work{16, 2000, 16, 65536, 100 * time.Microsecond}
	for n := 0; n < 18; n++ {
		if err := turn.transaction.before(step); err != nil {
			t.Fatal("complete finite inventory refused", n, err)
		}
	}
	if err := turn.transaction.before(step); !errors.Is(err, ErrQuantum) {
		t.Fatal("extra statement borrowed terminal reserve", err)
	}
	if err := turn.requireSearchImageMemory(t.Context(), 4<<20); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != before {
		t.Fatal("prospective work refunded the physical grant")
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("physical return did not retire the grant")
	}
}

func TestSearchImageMemorySameGrantPartitionAndPurpose(t *testing.T) {
	w, path, _ := testExecutor(t)
	turn, a := searchResourcesFixture(t, w, path, searchImageMemoryBudget{32768, 32768})
	before := a.Usage()
	for _, n := range []uint64{1, 32767, 32768, 32768} {
		if err := turn.requireSearchImageMemory(t.Context(), n); err != nil {
			t.Fatal(n, err)
		}
	}
	for _, n := range []uint64{32769, 65536, math.MaxUint64} {
		var refused *Refusal
		if err := turn.requireSearchImageMemory(t.Context(), n); !errors.As(err, &refused) || refused.Code != RefusalCapacity {
			t.Fatal("borrowed reserve/overflow", n, err)
		}
	}
	if err := turn.requireSearchImageMemory(t.Context(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("publisher used Inventory allowance", err)
	}
	c := turn.transaction.custody.(*governedTurn)
	budget := c.searchImageMemory
	c.searchImageMemory = searchImageMemoryBudget{}
	c.inventoryMemory = inventoryMemoryBudget{32768, 32768}
	var refused *Refusal
	if err := turn.requireSearchImageMemory(t.Context(), 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
		t.Fatal("Inventory partition became publisher authority", err)
	}
	c.inventoryMemory = inventoryMemoryBudget{}
	c.searchImageMemory = searchImageMemoryBudget{65536, 1}
	assertPartitionPolicyError(t, turn.requireSearchImageMemory(t.Context(), 1), "search_image_memory_partition")
	c.searchImageMemory = budget
	c.expected.Need[governor.MemoryBytes]++
	if err := turn.requireSearchImageMemory(t.Context(), 1); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("foreign expected grant", err)
	}
	c.expected.Need[governor.MemoryBytes]--
	if a.Usage() != before {
		t.Fatal("coverage check resized or refunded memory")
	}
	c.g.Cancel(c.grant)
	if err := turn.requireSearchImageMemory(t.Context(), 1); !errors.Is(err, governor.ErrGrant) || a.Usage() != before {
		t.Fatal("cancel granted/refunded memory", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("memory holding outlived physical return")
	}
}

func TestSearchImageMemoryConfigurationAndLifetime(t *testing.T) {
	for _, b := range []searchImageMemoryBudget{{1, 0}, {0, 1}, {65536, 1}, {1, 65536}, {math.MaxUint64, 1}, {1, math.MaxUint64}} {
		if err := validateSearchImageMemory(b, 65536); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid partition", b, err)
		}
	}
	w, path, _ := testExecutor(t)
	tx, err := w.begin(t.Context(), SearchImageStage, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var refused *Refusal
	if err := (&commandTurn{transaction: tx}).requireSearchImageMemory(t.Context(), 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
		t.Fatal("plain custody became memory authority", err)
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"zero", "caller", "protected", "late_caller", "late_protected"} {
		t.Run(mode, func(t *testing.T) {
			budget := searchImageMemoryBudget{32768, 32768}
			if mode == "zero" {
				budget = searchImageMemoryBudget{}
			}
			turn, a := searchResourcesFixture(t, w, path, budget)
			defer turn.transaction.rollback(nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			oldNow := w.now
			defer func() { w.now = oldNow }()
			switch mode {
			case "caller":
				cancel()
			case "protected":
				turn.transaction.cancel()
			case "late_caller":
				w.now = func() time.Time { cancel(); return oldNow() }
			case "late_protected":
				w.now = func() time.Time { turn.transaction.cancel(); return oldNow() }
			}
			err := turn.requireSearchImageMemory(ctx, 1)
			if mode == "zero" {
				if !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
					t.Fatal("zero profile became unlimited", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled coverage became success", err)
			}
			if a.Usage()[governor.Background][governor.MemoryBytes] == 0 {
				t.Fatal("cancellation refunded before physical return")
			}
		})
	}
}

func TestSearchImageMemoryProfileRejectsForeignPartitions(t *testing.T) {
	for _, c := range []struct {
		purpose CommandID
		plan    effectivePlan
	}{
		{EffectiveStage, effectivePlan{searchImageMemory: searchImageMemoryBudget{1, 1}}},
		{InventoryAccept, effectivePlan{searchImageMemory: searchImageMemoryBudget{1, 1}}},
		{SearchImageStage, effectivePlan{inventoryMemory: inventoryMemoryBudget{1, 1}}},
		{SearchImageStage, effectivePlan{searchImageMemory: searchImageMemoryBudget{1, 0}}},
	} {
		c.plan.need[governor.MemoryBytes] = 2
		if _, err := freezeCommandPlan(nil, c.plan, nil, c.purpose); !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign/malformed partition reached registration", c.purpose, err)
		}
	}
}

func TestSearchImageTimeLeaseAndTerminal(t *testing.T) {
	w, _, clock := testExecutor(t)
	for _, mode := range []string{"live", "equal_deadline", "expired", "short_lease", "backward", "caller", "protected", "late_caller", "late_protected", "wrong_purpose", "zero_lease"} {
		t.Run(mode, func(t *testing.T) {
			base := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			*clock = base
			purpose := SearchImageStage
			if mode == "wrong_purpose" {
				purpose = InventoryAccept
			}
			tx, err := w.begin(t.Context(), purpose, testRecipe(), &testCustody{})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			turn := &commandTurn{transaction: tx}
			expires := base.Add(time.Minute).UnixMilli()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			oldNow := w.now
			// Restore forward component time before unconditional retirement;
			// a deliberate backward sample must not poison later table cases.
			defer func() { w.now = oldNow; *clock = base.Add(2 * time.Millisecond) }()
			var want error
			switch mode {
			case "equal_deadline":
				expires = base.Add(MaxCommandSafetyHold).UnixMilli()
			case "expired":
				expires = base.UnixMilli()
				want = ErrConflict
			case "short_lease":
				expires = base.Add(MaxCommandSafetyHold - time.Millisecond).UnixMilli()
				want = ErrConflict
			case "backward":
				*clock = base.Add(-time.Nanosecond)
				want = ErrQuantum
			case "caller":
				cancel()
				want = context.Canceled
			case "protected":
				tx.cancel()
				want = context.Canceled
			case "late_caller":
				w.now = func() time.Time { cancel(); return base }
				want = context.Canceled
			case "late_protected":
				w.now = func() time.Time { tx.cancel(); return base }
				want = context.Canceled
			case "wrong_purpose":
				want = ErrInvalid
			case "zero_lease":
				expires = 0
				want = ErrInvalid
			}
			value, err := turn.searchImageTimeMS(ctx, expires)
			w.now = oldNow
			if want != nil {
				if !errors.Is(err, want) || value != 0 {
					t.Fatal("invalid timestamp escaped", value, err, want)
				}
				return
			}
			if err != nil || value != base.UnixMilli() {
				t.Fatal(value, err)
			}
			input := &searchImageStageCommand{valid: true, request: searchImageStageRequest{owner: searchImageBeginOwner{leaseExpiresMS: expires}}}
			*clock = base.Add(time.Millisecond)
			if err := checkSearchImageTerminal(ctx, turn, input); err != nil || value != base.UnixMilli() {
				t.Fatal("terminal replaced timestamp", value, err)
			}
			*clock = base.Add(500 * time.Microsecond)
			if err := tx.before(Work{1, 1, 1, 1, time.Microsecond}); !errors.Is(err, ErrQuantum) {
				t.Fatal("publisher helper did not raise statement clock floor", err)
			}
		})
	}
}
