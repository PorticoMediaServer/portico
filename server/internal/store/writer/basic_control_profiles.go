package writer

import (
	"context"
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The installed profile for one closed Basic purpose, or nil when absent.
func (r *effectiveRuntime) basicPlan(kind CommandID) *effectivePlan {
	switch kind {
	case BasicAccept:
		return r.basicAcceptPlan
	case BasicStage:
		return r.basicStagePlan
	case BasicSeal:
		return r.basicSealPlan
	}
	return nil
}

// Upper-bound uses inside ONE Stage or Seal command: shared history and
// control reads, then exactly one family body. The profile covers every
// operation of its purpose, so a stream turn reserves media points it does not
// run; a media Stage installs at most basicStageMaxRows rows of ONE family, and
// a media Seal reads its rows once. Forecasts are conservative, never short.
func basicControlMaxUses(purpose CommandID, id stmt.ID) int {
	switch id {
	case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey,
		basicReceiptScope, basicReceiptIdentity, basicReceiptHeaderLengths, basicReceiptHeader,
		basicControlRun, basicControlSource, basicControlActive, basicControlObject, basicControlLibrary,
		basicControlPolicy, basicControlJob, basicControlAsset, basicControlRunCAS, basicControlPostState, basicControlReceipt,
		basicStageAsset, basicStageWitness, basicStageFamily:
		return 1
	case basicStageHeader:
		return 2 // input phase check plus the media family check
	}
	switch purpose {
	case BasicStage:
		switch id {
		case basicStageBudget, basicStageBudgetCAS, basicStageStreamInsert, basicStageFamilyCAS:
			return 1
		case basicProbeHeader, basicProbeOldPresence, basicProbeAssetCAS,
			basicShapeLookup, basicShapeInsert, basicShapeRefCAS, basicShapeHeaderCAS,
			basicFreezeLinks, basicFreezeSealed, basicFreezeCAS, basicEntityCAS,
			basicCopyHeader, basicCopyCursorCAS:
			return 1
		case basicProbeAsset:
			return 2 // before-tuple proof plus post-CAS readback
		case basicProbeOldInsert:
			return 6 // one OLD row per changed probe scalar field
		case basicMediaStreamInsert, basicMediaBreadthInsert, basicMediaChapterFactInsert, basicMediaChapterInsert,
			basicMediaSubtitleFactInsert, basicMediaSidecarInsert, basicMediaSubtitleInsert, basicMediaEvidenceInsert:
			return basicStageMaxRows
		default:
			if n := basicCopyFamilyMaxUses(id); n > 0 {
				return n // registered copy family statements, from their own files
			}
		}
	case BasicSeal:
		switch id {
		case basicSealStreamFact, basicSealFamilyCAS,
			basicMediaStreamRows, basicMediaBreadthRows, basicMediaChapterFactRows, basicMediaChapterRows,
			basicMediaSubtitleFactRows, basicMediaSidecarRows, basicMediaSubtitleRows, basicMediaEvidenceRows:
			return 1
		}
	}
	return 0
}

func basicControlCommandDefinitions(purpose CommandID) []stmt.Definition {
	if purpose != BasicStage && purpose != BasicSeal {
		return nil
	}
	groups := [][]stmt.Definition{inventoryDisclosureDefinitions(), basicReceiptDefinitions(), basicControlDefinitions(),
		basicStreamStageDefinitions(), basicMediaDefinitions(), basicProbeDefinitions(), basicShapeDefinitions(),
		basicFreezeDefinitions(), basicCopyDefinitions(), basicCopyFamilyDefinitions()}
	for _, d := range scanReceiptDefinitions() {
		switch d.ID {
		case scanReceiptOwner, scanReceiptAck, scanReceiptReclaimKey: // zero-member replay decode
			groups = append(groups, []stmt.Definition{d})
		}
	}
	var defs []stmt.Definition
	seen := map[stmt.ID]bool{}
	for _, group := range groups {
		for _, d := range group {
			if !seen[d.ID] && basicControlMaxUses(purpose, d.ID) > 0 {
				seen[d.ID] = true
				defs = append(defs, d)
			}
		}
	}
	return defs
}

// Private trusted composition installs both closed control profiles exactly
// once on the existing runtime/registry, following composeBasicAcceptProfile.
func composeBasicControlProfiles(ctx context.Context, r *effectiveRuntime, stage, seal effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.basicStagePlan, r.basicSealPlan = nil, nil
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
	if r.basicStagePlan != nil || r.basicSealPlan != nil {
		return ErrBusy
	}
	stageDefs, sealDefs := basicControlCommandDefinitions(BasicStage), basicControlCommandDefinitions(BasicSeal)
	if stage, err = freezeCommandPlan(r.executor, stage, stageDefs, BasicStage); err != nil {
		return err
	}
	if seal, err = freezeCommandPlan(r.executor, seal, sealDefs, BasicSeal); err != nil {
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
	r.basicStagePlan, r.basicSealPlan = &stage, &seal
	installed = true
	return nil
}
