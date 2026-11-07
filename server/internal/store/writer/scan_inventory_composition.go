package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Trusted startup only. The caller must supply W's complete reviewed physical
// recipe and actual Capture receipt; there is no default, worker budget or
// public enable. No product startup currently calls this composition. Until it
// does, the existing original-history path keeps refusing every fresh effect.
func composeInventoryDriver(ctx context.Context, d *ScanDriver, plan effectivePlan) (err error) {
	var driverLocked, runtimeLocked, executorLocked, installed bool
	var r *effectiveRuntime
	var oldRegistry *stmt.Registry
	var oldRetention *scanOwnerRetention
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.inventoryDriverPlan = nil
			r.registry, d.retention = oldRegistry, oldRetention
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
		if driverLocked {
			d.mu.Unlock()
		}
	}()
	if ctx == nil {
		return ErrInvalid
	}
	if err = d.current(); err != nil {
		return err
	}
	if !d.mu.TryLock() {
		return ErrBusy
	}
	driverLocked = true
	r = d.writer.effective
	if !r.mu.TryLock() {
		return ErrBusy
	}
	runtimeLocked = true
	if d.preparation != nil || d.operation != nil || d.work != nil || r.inventoryDriverPlan != nil {
		return ErrBusy
	}
	if r.registry == nil || r.inventoryPlan == nil || r.inventoryPreparationPlan == nil || r.issuerPlans[0] == nil || r.issuerPlans[1] == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if err = d.retention.check(r.executor, r.registry); err != nil {
		return err
	}
	plan, err = freezeInventoryDriverPlan(r.executor, plan)
	if err != nil {
		return err
	}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(inventoryDriverCommandDefinitions()))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, def := range inventoryDriverCommandDefinitions() {
		found := false
		for _, old := range all {
			if old.ID == def.ID {
				if !reflect.DeepEqual(old, def) {
					return invalid("inventory_driver_definition")
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, def)
		}
	}
	receipts := []stmt.Receipt{r.plan.registration, plan.registration, r.inventoryPlan.registration, r.inventoryPreparationPlan.registration}
	for _, p := range r.lifecyclePlans {
		if p != nil {
			receipts = append(receipts, p.registration)
		}
	}
	for _, p := range r.issuerPlans {
		if p != nil {
			receipts = append(receipts, p.registration)
		}
	}
	registry, err := stmt.New(all, receipts...)
	if err != nil {
		return err
	}
	retention, err := scanOwnerRetentionFor(r.executor, registry)
	if err != nil {
		return err
	}
	if err = errors.Join(d.current(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, oldRetention = r.registry, d.retention
	r.registry, d.retention = registry, retention
	r.inventoryDriverPlan = &plan
	installed = true
	return nil
}

// The installed whole-driver plan must be the actual plan of THIS frame.
// A history/preparation subset or another registry cannot authorize effects.
func checkInventoryDriverTurn(ctx context.Context, turn *commandTurn, in *inventoryInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || in == nil || in.issuer == nil || in.issuer.driver == nil || in.issuer.driver.writer == nil || in.issuer.driver.writer.effective == nil {
		return ErrInvalid
	}
	r := in.issuer.driver.writer.effective
	if r.inventoryDriverPlan == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if turn.registry != r.registry || turn.transaction.writer != r.executor || turn.transaction.workLimit != r.inventoryDriverPlan.recipe.limit {
		return ErrInvalid
	}
	keeper := r.executor.keeper.Load()
	if keeper == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err = keeper.Check(); err != nil {
		return err
	}
	for _, def := range inventoryDriverCommandDefinitions() {
		if _, ok := turn.steps[def.ID]; !ok {
			return invalid("inventory_driver_step")
		}
	}
	return turn.checkLifetimes(ctx)
}
