package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed installation on W's SAME runtime, registry, executor and authority.
// Profiles must carry complete Capture proof and whole-turn forecasts; no
// default production profile or public enable is supplied by this component.
func composeScanDirectories(ctx context.Context, r *effectiveRuntime, discover, eof effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var old *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = old
			r.directoryPlans = [2]*effectivePlan{}
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
	if r.directoryPlans != ([2]*effectivePlan{}) {
		return ErrBusy
	}
	defs := scanDirectoryDefinitions()
	discover, err = freezeCommandPlan(r.executor, discover, defs, InventoryDirectoryDiscover)
	if err != nil {
		return err
	}
	eof, err = freezeCommandPlan(r.executor, eof, defs, InventoryDirectoryEOF)
	if err != nil {
		return err
	}
	if discover.directoryMemory == (commandMemoryBudget{}) || eof.directoryMemory == (commandMemoryBudget{}) {
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
					return invalid("directory_definition")
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	registry, e := stmt.New(all, r.plan.registration, discover.registration, eof.registration)
	if e != nil {
		return e
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	old, r.registry = r.registry, registry
	r.directoryPlans = [2]*effectivePlan{&discover, &eof}
	installed = true
	return nil
}

// Private component entry; trusted ScanDriver issuance/preparation remains a
// separate integration. It exposes no command callback or SQL/resource handle.
func (r *effectiveRuntime) runScanDirectory(ctx context.Context, w *Writer, cap *ScanDisclosure, q ScanReceiptRequest, kind CommandID, input *scanDirectoryCommand, ack bool) (value scanDirectoryCommandResult, floor AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{directory: value}, ack: floor}, err)
		value, floor, err = v.output.directory, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r || (kind != InventoryDirectoryDiscover && kind != InventoryDirectoryEOF) || ack && input != nil {
		return value, floor, ErrInvalid
	}
	if input != nil && (!input.valid || input.purpose != kind || input.reference() != q) {
		return value, floor, ErrInvalid
	}
	out, floor, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, &inventoryInvocation{disclosure: cap, request: q, directory: kind, directoryInput: input})
	return out.directory, floor, err
}
func directoryHistoricalResult(q ScanReceiptRequest, l scanDirectoryLookup) scanDirectoryCommandResult {
	out := scanDirectoryCommandResult{Outcome: ReceiptOutcome{Reference: q.Receipt}}
	switch l.decision {
	case receiptFresh:
		out.Outcome.State = Pending
	case receiptRetired:
		out.Outcome.State = ResultRetired
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = l.result, l.digest
	}
	return out
}
