package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Fixed scalar preflight workspace only. Saved native payloads are forecast
// from actual same-snapshot byte lengths before copying, under the SAME grant.
const scanLifecycleHistoryBytes = scanByteFixedWorkspace

type scanLifecycleCommandResult struct {
	Outcome ReceiptOutcome
	Value   scanRunLifecycleResult
	Digest  ResultDigest
}

// No fresh effects, birth exception or lease issuance enter this history-only
// provider. Native scope and immutable history use the same protected turn.
func scanLifecycleHistoryCommandDefinitions() []stmt.Definition {
	defs := append(inventoryDisclosureDefinitions(), scanRunLifecycleHistoryDefinitions()...)
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader:
			defs = append(defs, def)
		}
	}
	return append(defs, scanHistoryByteDefinitions()...)
}

// Trusted startup composes both explicit profiles into the SAME registry and
// executor. Complete Capture receipts are mandatory, including definitions
// already registered by the existing runtime. There is no default profile or
// worker budget, and no active frame can overlap this one-time installation.
func composeScanLifecycleHistory(ctx context.Context, r *effectiveRuntime, create, claim effectivePlan) (err error) {
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	return composeScanLifecycleProfiles(ctx, r, create, claim, false)
}

func composeScanLifecycleProfiles(ctx context.Context, r *effectiveRuntime, create, claim effectivePlan, issuer bool) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			if issuer {
				r.issuerPlans = [2]*effectivePlan{}
			} else {
				r.lifecyclePlans = [2]*effectivePlan{}
			}
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil || r.governor == nil || r.authority == nil || r.wal == nil {
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
	if r.registry == nil {
		return ErrInvalid
	}
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if (!issuer && r.lifecyclePlans != ([2]*effectivePlan{})) || (issuer && r.issuerPlans != ([2]*effectivePlan{})) {
		return ErrBusy
	}
	defs := scanLifecycleHistoryCommandDefinitions()
	if issuer {
		defs = scanLifecycleCommandDefinitions()
	}
	create, err = freezeCommandPlan(r.executor, create, defs, InventoryCreate)
	if err != nil {
		return err
	}
	claim, err = freezeCommandPlan(r.executor, claim, defs, InventoryClaim)
	if err != nil {
		return err
	}
	for _, p := range []effectivePlan{create, claim} {
		if p.scanLifecycleMemory.preparationBytes < scanLifecycleHistoryBytes {
			return &Refusal{Code: RefusalCapacity}
		}
	}
	// A shared definition must be byte-identical. New validates all duplicate
	// IDs, so append only the already-matching compiled scope definitions once.
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(defs))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, def := range defs {
		found := false
		for _, old := range all {
			if old.ID == def.ID {
				if !reflect.DeepEqual(old, def) {
					return invalid("scan_lifecycle_history_definition")
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, def)
		}
	}
	registry, err := stmt.New(all, r.plan.registration, create.registration, claim.registration)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, r.registry = r.registry, registry
	if issuer {
		r.issuerPlans = [2]*effectivePlan{&create, &claim}
	} else {
		r.lifecyclePlans = [2]*effectivePlan{&create, &claim}
	}
	installed = true
	return nil
}

func (r *effectiveRuntime) runScanLifecycleHistory(ctx context.Context, w *Writer, cap *ScanDisclosure, q ScanReceiptRequest, purpose CommandID, ack bool) (value scanLifecycleCommandResult, acknowledged AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{lifecycle: value}, ack: acknowledged}, err)
		value, acknowledged, err = v.output.lifecycle, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r || (purpose != InventoryCreate && purpose != InventoryClaim) {
		return value, acknowledged, ErrInvalid
	}
	out, acknowledged, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack,
		&inventoryInvocation{disclosure: cap, request: q, lifecycle: purpose}, nil)
	return out.lifecycle, acknowledged, err
}

func scanLifecycleHistoricalResult(q ScanReceiptRequest, lookup scanLifecycleReceiptLookup) scanLifecycleCommandResult {
	value := scanLifecycleCommandResult{Outcome: ReceiptOutcome{Reference: q.Receipt}}
	switch lookup.decision {
	case receiptFresh:
		value.Outcome.State = Pending
	case receiptRetired:
		value.Outcome.State = ResultRetired
	case receiptReplay:
		value.Outcome.State, value.Outcome.Replayed = Applied, true
		value.Value, value.Digest = lookup.result, lookup.digest
	}
	return value
}
