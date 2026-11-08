package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private trusted composition installs both closed purpose profiles exactly
// once on the existing runtime/registry. There are no worker Work values,
// fallback numbers, new constructors, public minters or alternate executors.
func composeBasicAcceptProfile(ctx context.Context, r *effectiveRuntime, accept effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.basicAcceptPlan = nil
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
	if r.basicAcceptPlan != nil {
		return ErrBusy
	}
	acceptDefs := basicFinalDefinitions(accept.basicAcceptBounds)
	accept, err = freezeCommandPlan(r.executor, accept, acceptDefs, BasicAccept)
	if err != nil {
		return err
	}
	groups := [][]stmt.Definition{acceptDefs}
	receipts := []stmt.Receipt{accept.registration}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(acceptDefs))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, defs := range groups {
		for _, def := range defs {
			found := false
			for _, old := range all {
				if old.ID == def.ID {
					if !reflect.DeepEqual(old, def) {
						return invalid("basic_command_definition")
					}
					found = true
					break
				}
			}
			if !found {
				all = append(all, def)
			}
		}
	}
	// The new full-set native Capture must cover ALL existing definitions as
	// well as both new purposes. An old unrelated receipt cannot fill a hole.
	registry, err := stmt.New(all, receipts...)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, r.registry = r.registry, registry
	r.basicAcceptPlan = &accept
	installed = true
	return nil
}
