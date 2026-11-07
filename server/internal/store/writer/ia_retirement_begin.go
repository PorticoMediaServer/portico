package writer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var errIARetirementConfirmation = errors.New("the confirmation does not match")

// BEGIN has no pre-existing worker lease. It uses the same owned transaction
// clock, high-water and protected deadline as W's unleased InventoryCreate.
// The existing removal lease is claimed later; no new lease is introduced.
func (turn *commandTurn) iaRetirementBeginTime(ctx context.Context) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if (tx.descriptor.ID != IARetireLibrary && tx.descriptor.ID != IARetireSource) || tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	defer tx.guardPanic()
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	now := tx.writer.now()
	value = now.UnixMilli()
	if value < 0 || !time.UnixMilli(value).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return value, nil
}

// Private fresh effect adapter, not a public entry or birth/owner capability.
// The owning W issuer must first prove CURRENT conjunctive owner authority,
// exact original history miss, original target absence, deny/pin ordering and
// admit the complete request/result/statement bytes in its one grant. This
// adapter cannot issue those proofs, select a profile, commit, or return Applied.
// No provider/filesystem work and no whole-library fan-out occurs here.
func applyIARetirementBegin(ctx context.Context, turn *commandTurn, input *iaRetirementCommand) (value iaRetirementResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if input == nil {
		return value, ErrInvalid
	}
	if err = input.checkOriginal(); err != nil {
		return value, err
	}
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != input.purpose {
		return value, ErrInvalid
	}
	for _, id := range []stmt.ID{iaRetirementLibraryFence, iaRetirementSourceFence, iaRetirementConfigurationCAS, iaRetirementConfirmationPoint, iaRetirementLiveLibraryPoint, iaRetirementDriverInsert, iaRetirementHeaderInsert} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("ia_retirement_begin_steps")
		}
	}
	r, s := input.request, input.request.scope
	var one int64
	err = iaRetirementPoint(ctx, turn, iaRetirementLiveLibraryPoint, []any{s.libraryID, s.libraryKey}, &one)
	if err != nil || one != 1 {
		return value, errors.Join(ErrConflict, err)
	}
	if input.purpose == IARetireLibrary {
		if strings.TrimSpace(r.confirmation) != r.libraryName {
			return value, errIARetirementConfirmation
		}
		err = iaRetirementPoint(ctx, turn, iaRetirementConfirmationPoint, []any{s.libraryKey, r.libraryName, s.resolvedRoot, s.libraryRevision}, &one)
		if err != nil || one != 1 {
			return value, errors.Join(ErrConflict, err)
		}
	}
	applied, err := turn.iaRetirementBeginTime(ctx)
	if err != nil {
		return value, err
	}
	o := iaRetirementOriginal{kind: "library", target: s.libraryKey, libraryID: s.libraryID, libraryKey: s.libraryKey, resolvedRoot: s.resolvedRoot,
		operationID: input.identity.OperationID, requestDigest: input.digest}
	var sourceInc, sourceGeneration, rootIdentity any
	if input.purpose == IARetireSource {
		o.kind, o.target, o.sourceIncarnation, o.sourceGeneration, o.rootIdentity = "folder", s.sourceID, s.sourceIncarnation, s.sourceGeneration, s.rootIdentity
		sourceInc, sourceGeneration, rootIdentity = s.sourceIncarnation, s.sourceGeneration, s.rootIdentity
	}
	value = iaRetirementResult{original: o, acceptedRevision: 1, appliedMS: applied, state: "retiring"}
	var variant any = iaRetireLibraryResult{value}
	if input.purpose == IARetireSource {
		variant = iaRetireSourceResult{value}
	}
	digest, err := canonicalResultDigest(input.identity, input.digest, variant)
	if err != nil {
		return value, err
	}
	retained, err := iaRetirementRetainedBytes(input.purpose, value)
	if err != nil {
		return value, err
	}
	// Revision and original root/generation fences share the same transaction
	// with driver/receipt creation. A later insert/unique/FK failure rolls back
	// every fence; callers must never salvage a failed frame's partial effects.
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementConfigurationCAS, s.libraryKey, s.libraryRevision); err != nil {
		return value, err
	}
	if input.purpose == IARetireLibrary {
		err = iaRetirementOneEffect(ctx, turn, iaRetirementLibraryFence, s.libraryID, s.libraryKey, s.resolvedRoot)
	} else {
		err = iaRetirementOneEffect(ctx, turn, iaRetirementSourceFence, s.sourceID, s.libraryKey, s.sourceIncarnation, s.sourceGeneration, s.resolvedRoot, s.rootIdentity)
	}
	if err != nil {
		return value, err
	}
	err = iaRetirementOneEffect(ctx, turn, iaRetirementDriverInsert, o.kind, o.target, o.operationID[:], o.requestDigest[:], input.identity.Incarnation[:], o.libraryID, o.libraryKey, o.resolvedRoot, sourceInc, sourceGeneration, rootIdentity, int64(1), int64(1), retained, applied)
	if err != nil {
		return value, err
	}
	name, _ := iaRetirementKindName(input.purpose)
	err = iaRetirementOneEffect(ctx, turn, iaRetirementHeaderInsert, o.operationID[:], input.identity.Incarnation[:], input.identity.Sequence, input.identity.OperationID[:], input.digest[:], name, int64(1), value.acceptedRevision, applied, o.kind, o.target, value.phase, value.entityKind, value.entityCursor, value.objectCursor, int64(0), int64(0), value.state, int64(0), digest[:], retained)
	if err != nil {
		return value, err
	}
	return value, nil
}
