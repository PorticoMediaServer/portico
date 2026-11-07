package writer

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// W-owned component fixture: real SAME governor/binder/BEGIN/retirement path,
// synthetic complete work/memory figures, NOT production or physical proof.
// The caller supplies the exact fully Captured native registry before BEGIN.
// No public Writer, disclosure capability, second writer or adapter is minted.
func inventoryMemoryFixtureTurn(t *testing.T, w *executor, registry *stmt.Registry, input *inventoryAcceptCommand, budget inventoryMemoryBudget) (*commandTurn, *governor.Authority) {
	t.Helper()
	f := newInventoryMemoryFixture(t, w, registry, input, budget)
	turn, err := f.begin(t, input)
	if err != nil {
		t.Fatal(err)
	}
	return turn, f.authority
}

// The ONE fixture builder owns the unchanged synthetic recipe and grant path.
// A progressive test retains this object, not a copied recipe or grant builder.
type inventoryMemoryFixture struct {
	executor    *executor
	registry    *stmt.Registry
	governor    governor.Governor
	authority   *governor.Authority
	wal         *governor.WALGate
	request     governor.Request
	recipe      turnRecipe
	budget      inventoryMemoryBudget
	path        string
	maintenance bool
	steps       []statementStep
}

func newInventoryMemoryFixture(t *testing.T, w *executor, registry *stmt.Registry, input *inventoryAcceptCommand, budget inventoryMemoryBudget, foreground ...governor.Config) *inventoryMemoryFixture {
	t.Helper()
	if len(foreground) > 1 {
		t.Fatal("multiple foreground fixture configurations")
	}
	var fg *governor.Config
	if len(foreground) == 1 {
		fg = &foreground[0]
	}
	return newInventoryMemoryFixtureComposition(t, w, registry, input, budget, fg)
}

func newInventoryMemoryFixtureComposition(t *testing.T, w *executor, registry *stmt.Registry, input *inventoryAcceptCommand, budget inventoryMemoryBudget, foregroundConfig *governor.Config, maintenance ...governedFixtureMaintenance) *inventoryMemoryFixture {
	t.Helper()
	if input == nil || !input.valid || registry == nil {
		t.Fatal("missing component input/registry")
	}
	total := int64(65536) // existing unconfigured component profile, never fresh authority
	if budget != (inventoryMemoryBudget{}) {
		if budget.preparationBytes > math.MaxInt64 || budget.reservedBytes > math.MaxInt64-budget.preparationBytes {
			t.Fatal("overflow test profile")
		}
		total = int64(budget.preparationBytes + budget.reservedBytes)
	}
	if err := validateInventoryMemory(budget, total); err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	now := w.now
	if len(maintenance) > 1 {
		t.Fatal("multiple maintenance fixture configurations")
	}
	var cp *governedFixtureMaintenance
	if len(maintenance) == 1 {
		cp = &maintenance[0]
	}
	g, a, wal, request := governedFixtureComposition(t, now, governor.Amounts{}, foregroundConfig, cp, governedFixtureProfile{recipe, total})
	request.Key.ID = input.request.scope.runID
	request.Footprint = []governor.Scope{{Library: input.request.scope.libraryID, Domain: uint16(InventoryAccept)}}
	var seq int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, entry := range registry.Manifest() {
		steps = append(steps, statementStep{entry.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	return &inventoryMemoryFixture{executor: w, registry: registry, governor: g, authority: a, wal: wal, request: request, recipe: recipe, budget: budget, path: path, maintenance: cp != nil, steps: steps}
}

func (f *inventoryMemoryFixture) begin(t *testing.T, input *inventoryAcceptCommand) (*commandTurn, error) {
	t.Helper()
	if input == nil || !input.valid || input.request.scope.runID != f.request.Key.ID ||
		input.request.scope.libraryID != f.request.Footprint[0].Library {
		return nil, ErrInvalid
	}
	if err := refreshWriterWAL(f.path, f.wal, f.executor.now); err != nil {
		return nil, err
	}
	request := f.request
	request.Deadline = f.executor.now().Add(time.Second)
	// Actual current gate metadata; required by merged concurrent keeper.
	if f.maintenance {
		var riskErr error
		request.RestartRisk, riskErr = f.wal.RestartRisk(f.executor.now())
		if riskErr != nil {
			return nil, riskErr
		}
	}
	decision := f.governor.Try(request)
	if decision.Grant == nil {
		return nil, effectiveAdmissionRefusal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(InventoryAccept, f.recipe.limit, f.governor, f.authority, f.wal, decision.Grant, request)
	if err != nil {
		return nil, errors.Join(err, f.authority.Retired(decision.Grant))
	}
	custody.inventoryMemory = f.budget
	tx, err := f.executor.begin(t.Context(), InventoryAccept, f.recipe, custody)
	if err != nil {
		// Only ErrBusy precedes frame custody. Other failures belong to its
		// terminal path; never refund unknown/fenced native holdings here.
		if errors.Is(err, ErrBusy) {
			err = errors.Join(err, custody.retired())
		}
		return nil, err
	}
	t.Cleanup(func() { tx.rollback(nil) })
	turn, err := newCommandTurn(tx, f.registry, f.steps)
	if err != nil {
		_, rollbackErr := tx.rollback(err)
		return nil, errors.Join(err, rollbackErr)
	}
	return turn, nil
}

func memoryFixtureInput(t *testing.T) *inventoryAcceptCommand {
	t.Helper()
	request, identity := inventoryRequestFixture()
	input, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestInventoryMemorySameGrantCoverage(t *testing.T) {
	w, _, registry := inventoryLookupFixture(t)
	turn, a := inventoryMemoryFixtureTurn(t, w, registry, memoryFixtureInput(t), inventoryMemoryBudget{32768, 32768})
	before := a.Usage()
	for _, bytes := range []uint64{1, 32767, 32768, 32768} {
		if err := turn.requireInventoryMemory(t.Context(), bytes); err != nil {
			t.Fatal(bytes, err)
		}
	}
	for _, bytes := range []uint64{32769, 65536, math.MaxUint64} {
		var refused *Refusal
		if err := turn.requireInventoryMemory(t.Context(), bytes); !errors.As(err, &refused) || refused.Code != RefusalCapacity {
			t.Fatal("borrowed reserve/overflow", bytes, err)
		}
	}
	if err := turn.requireInventoryMemory(t.Context(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if a.Usage() != before {
		t.Fatal("coverage check resized/refunded grant")
	}
	c := turn.transaction.custody.(*governedTurn)
	c.g.Cancel(c.grant)
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("canceled grant admitted", err)
	}
	if a.Usage() != before {
		t.Fatal("cancel refunded physical memory")
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("physical memory holding leaked")
	}
}

func TestInventoryMemoryRequiresRealConfiguredCustody(t *testing.T) {
	w, _, registry := inventoryLookupFixture(t)
	// Capture plus plain synthetic custody cannot authorize a single byte.
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := newCommandTurn(tx, registry, []statementStep{{scanReceiptOwner, Work{1, 1, 1, 1, time.Microsecond}}})
	if err != nil {
		t.Fatal(err)
	}
	var refused *Refusal
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
		t.Fatal("Capture became memory authority", err)
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
	real, a := inventoryMemoryFixtureTurn(t, w, registry, memoryFixtureInput(t), inventoryMemoryBudget{})
	if err := real.requireInventoryMemory(t.Context(), 1); !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
		t.Fatal("unconfigured memory unlimited", err)
	}
	if a.Usage()[governor.ScanMinimum][governor.MemoryBytes] == 0 {
		t.Fatal("fixture did not hold real memory")
	}
}

func TestInventoryMemoryExactPartitionAndBinding(t *testing.T) {
	for _, budget := range []inventoryMemoryBudget{{1, 0}, {0, 1}, {65536, 1}, {1, 65536}, {math.MaxUint64, 1}, {1, math.MaxUint64}} {
		if err := validateInventoryMemory(budget, 65536); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid partition", budget, err)
		}
	}
	w, _, registry := inventoryLookupFixture(t)
	turn, _ := inventoryMemoryFixtureTurn(t, w, registry, memoryFixtureInput(t), inventoryMemoryBudget{32768, 32768})
	c := turn.transaction.custody.(*governedTurn)
	original := c.inventoryMemory
	c.inventoryMemory = inventoryMemoryBudget{65536, 1}
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid installed partition", err)
	}
	c.inventoryMemory = original
	c.expected.Need[governor.MemoryBytes]++
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("foreign amount laundered", err)
	}
	c.expected.Need[governor.MemoryBytes]--
	turn.transaction.descriptor.ID = BasicAccept
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong purpose", err)
	}
	turn.transaction.descriptor.ID = InventoryAccept
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := turn.requireInventoryMemory(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	turn.transaction.cancel()
	if err := turn.requireInventoryMemory(t.Context(), 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func assertPartitionPolicyError(t *testing.T, err error, field string) {
	t.Helper()
	var policy *PolicyError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &policy) || policy.Field != field || policy.Limit != 0 || policy.Cause != ErrInvalid ||
		err.Error() != field+": "+ErrInvalid.Error()+" (limit 0)" {
		t.Fatalf("changed typed partition error: %#v / %v; want Field=%q Limit=0 Cause=ErrInvalid", policy, err, field)
	}
}

func TestInventoryMemoryPreservesTypedPartitionError(t *testing.T) {
	bad := inventoryMemoryBudget{65536, 1}
	assertPartitionPolicyError(t, validateInventoryMemory(bad, 65536), "inventory_memory_partition")
	w, _, registry := inventoryLookupFixture(t)
	turn, _ := inventoryMemoryFixtureTurn(t, w, registry, memoryFixtureInput(t), inventoryMemoryBudget{32768, 32768})
	turn.transaction.custody.(*governedTurn).inventoryMemory = bad
	assertPartitionPolicyError(t, turn.requireInventoryMemory(t.Context(), 1), "inventory_memory_partition")
}

func TestInventoryMemoryProfileValidatedBeforeAdmission(t *testing.T) {
	w, _, _, _ := inventoryFacadeFixture(t)
	r := w.effective
	for _, budget := range []inventoryMemoryBudget{{1, 0}, {1, 1}, {math.MaxUint64, 1}} {
		profile := *r.inventoryPlan
		profile.inventoryMemory = budget
		if candidate, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, r.plan, profile); !errors.Is(err, ErrInvalid) || candidate != nil {
			t.Fatal("invalid frozen profile", budget, err)
		}
	}
	profile := *r.inventoryPlan
	profile.inventoryMemory = inventoryMemoryBudget{64 << 20, 64 << 20}
	bound, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, r.plan, profile)
	if err != nil {
		t.Fatal(err)
	}
	profile.inventoryMemory.preparationBytes--
	if bound.inventoryPlan.inventoryMemory.preparationBytes != 64<<20 {
		t.Fatal("mutable partition retained")
	}
	effective := r.plan
	effective.inventoryMemory = inventoryMemoryBudget{64 << 20, 64 << 20}
	if _, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, effective); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong-purpose partition", err)
	}
}

func TestInventoryMemoryLateCancellation(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "protected"}[protected], func(t *testing.T) {
			w, _, registry := inventoryLookupFixture(t)
			now := w.now
			var armed bool
			var cancelCall context.CancelFunc
			w.now = func() time.Time {
				if armed {
					cancelCall()
				}
				return now()
			}
			turn, a := inventoryMemoryFixtureTurn(t, w, registry, memoryFixtureInput(t), inventoryMemoryBudget{32768, 32768})
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelCall = cancel
			if protected {
				cancelCall = turn.transaction.cancel
			}
			armed = true
			err := turn.requireInventoryMemory(caller, 1)
			w.now = now
			if !errors.Is(err, context.Canceled) {
				t.Fatal("late cancellation became memory proof", err)
			}
			if a.Usage()[governor.ScanMinimum][governor.MemoryBytes] == 0 {
				t.Fatal("cancellation refunded physical holding")
			}
		})
	}
}
