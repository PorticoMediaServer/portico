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
func composeBasicProfiles(ctx context.Context, r *effectiveRuntime, stage, seal effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.basicPlans = [2]*effectivePlan{}
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
	if r.basicPlans != ([2]*effectivePlan{}) {
		return ErrBusy
	}
	stageDefs, sealDefs := basicCommandDefinitions(BasicStage), basicCommandDefinitions(BasicSeal)
	stage, err = freezeCommandPlan(r.executor, stage, stageDefs, BasicStage)
	if err != nil {
		return err
	}
	seal, err = freezeCommandPlan(r.executor, seal, sealDefs, BasicSeal)
	if err != nil {
		return err
	}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(stageDefs)+len(sealDefs))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, defs := range [][]stmt.Definition{stageDefs, sealDefs} {
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
	registry, err := stmt.New(all, stage.registration, seal.registration)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, r.registry = r.registry, registry
	r.basicPlans = [2]*effectivePlan{&stage, &seal}
	installed = true
	return nil
}
