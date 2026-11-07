package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func scanDirectoryTerminalPurpose(k CommandID) bool {
	return k == InventoryDirectoryRefuse || k == InventoryTraversalComplete
}
func scanDirectoryPurpose(k CommandID) bool {
	return k == InventoryDirectoryDiscover || k == InventoryDirectoryEOF || scanDirectoryTerminalPurpose(k)
}

// Same one-time, closed composition as discovery/EOF; no new dispatcher,
// governor, default production budget, public minter or enable is introduced.
func composeScanDirectoryTerminals(ctx context.Context, r *effectiveRuntime, refuse, traversal effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	var oldPlans [4]*effectivePlan
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.directoryPlans = oldPlans
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil || r.registry == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	runtimeLocked = true
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.directoryPlans[0] == nil || r.directoryPlans[1] == nil || r.directoryPlans[2] != nil || r.directoryPlans[3] != nil {
		return ErrBusy
	}
	defs := scanDirectoryTerminalDefinitions()
	refuse, err = freezeCommandPlan(r.executor, refuse, defs, InventoryDirectoryRefuse)
	if err != nil {
		return err
	}
	traversal, err = freezeCommandPlan(r.executor, traversal, defs, InventoryTraversalComplete)
	if err != nil {
		return err
	}
	if refuse.directoryMemory == (commandMemoryBudget{}) || traversal.directoryMemory == (commandMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(defs))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, d := range defs {
		found := false
		for _, prior := range all {
			if prior.ID == d.ID {
				if !reflect.DeepEqual(prior, d) {
					return invalid("directory_terminal_definition")
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	registry, e := stmt.New(all, r.plan.registration, refuse.registration, traversal.registration)
	if e != nil {
		return e
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, oldPlans = r.registry, r.directoryPlans
	r.registry = registry
	r.directoryPlans[2], r.directoryPlans[3] = &refuse, &traversal
	installed = true
	return nil
}
func (r *effectiveRuntime) runScanDirectoryTerminal(ctx context.Context, w *Writer, cap *ScanDisclosure, q ScanReceiptRequest, kind CommandID, input *scanDirectoryTerminalCommand, ack bool) (value scanDirectoryTerminalCommandResult, floor AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{directoryTerminal: value}, ack: floor}, err)
		value, floor, err = v.output.directoryTerminal, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r || !scanDirectoryTerminalPurpose(kind) || ack && input != nil {
		return value, floor, ErrInvalid
	}
	if input != nil && (!input.valid || input.purpose != kind || input.reference() != q) {
		return value, floor, ErrInvalid
	}
	out, floor, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, &inventoryInvocation{disclosure: cap, request: q, directory: kind, directoryTerminalInput: input})
	return out.directoryTerminal, floor, err
}
