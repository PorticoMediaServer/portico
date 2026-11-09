package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA-10 retirement side. Command 106 acknowledges every retirement receipt the
// permanent removal driver already holds and deletes at most ONE acknowledged
// receipt header per turn, oldest first. Reclaim is admitted only after the
// walk itself is terminal (retirement_state='complete' and phase=5): the exact
// completion proof is what makes every earlier receipt dead history.
//
// ACK alone refunds nothing. receipt_retained_count/bytes decrease only by the
// measured retained_bytes of the header actually deleted, the final unit must
// match the remaining debt exactly, and the driver row with its original
// request, incarnation and reclaim watermark stays permanently.
const iaRetirementReclaimRequestVersion uint16 = 1

type iaRetirementReclaimRequest struct {
	kind, target      string
	libraryID         int64
	libraryKey        string
	resolvedRoot      string
	sourceIncarnation string
	sourceGeneration  int64
	rootIdentity      string
	// latestSequence is the caller's observed driver last_command_sequence. A
	// turn is fresh only one past it, so a stale caller cannot skip or repeat.
	latestSequence int64
}

type iaRetirementReclaimCommand struct {
	identity CommandIdentity
	request  iaRetirementReclaimRequest
	digest   RequestDigest
}

// Private same-turn outcome. Fresh carries this turn's exact typed result and
// digest; replay carries the decoded slot; retired carries neither.
type iaRetirementReclaimOutcome struct {
	decision receiptDecision
	result   iaReclaimResult
	digest   ResultDigest
}

type iaRetirementReclaimOwner struct {
	exists                          bool
	incarnation                     [16]byte
	original                        iaRetirementOriginal
	last, acknowledged, reclaimed   int64
	retainedCount, retainedBytes    int64
	state, objectCursor             string
	phase, entityKind, entityCursor int64
	walkRemoved, walkKept           bool
	due                             sql.NullInt64
	slot                            iaReclaimSlotRow
}

func validateIARetirementReclaimRequest(q iaRetirementReclaimRequest) error {
	if (q.kind != "library" && q.kind != "folder") || !iaReclaimText(q.target, 128) ||
		!iaReclaimText(q.libraryKey, 128) || !iaRetirementNativeRoot(q.resolvedRoot) ||
		q.libraryID <= 0 || q.latestSequence < 1 || q.latestSequence == math.MaxInt64 {
		return ErrInvalid
	}
	if q.kind == "library" {
		if q.target != q.libraryKey || q.sourceIncarnation != "" || q.rootIdentity != "" || q.sourceGeneration != 0 {
			return ErrInvalid
		}
	} else if !iaReclaimText(q.sourceIncarnation, 128) || !iaReclaimText(q.rootIdentity, 256) ||
		q.sourceGeneration <= 0 || q.sourceGeneration == math.MaxInt64 {
		return ErrInvalid
	}
	total := len(q.kind) + len(q.target) + len(q.libraryKey) + len(q.resolvedRoot) + len(q.sourceIncarnation) + len(q.rootIdentity)
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

func newIARetirementReclaimCommand(identity CommandIdentity, request iaRetirementReclaimRequest) (*iaRetirementReclaimCommand, error) {
	if identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) || identity.Sequence < 2 {
		return nil, invalid("ia_retirement_reclaim_identity")
	}
	if err := validateIARetirementReclaimRequest(request); err != nil {
		return nil, err
	}
	if identity.Sequence-request.latestSequence != 1 {
		return nil, invalid("ia_retirement_reclaim_sequence")
	}
	digest, err := canonicalRequestDigest(IARetireReclaim, iaRetirementReclaimRequestVersion, identity, request)
	if err != nil {
		return nil, err
	}
	return &iaRetirementReclaimCommand{identity: identity, request: request, digest: digest}, nil
}

// One governed retirement ACK/reclaim turn or its original replay. Current
// owner authority is established BEFORE the retained driver/slot read; the
// complete saved slot is decoded before any fresh completion check.
func reclaimIARetirement(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, request iaRetirementReclaimRequest) (out iaRetirementReclaimOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IARetireReclaim {
		return out, ErrInvalid
	}
	command, err := newIARetirementReclaimCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, err := loadIARetirementReclaimOwner(ctx, turn, command)
	if err != nil {
		return out, err
	}
	saved, present, err := decodeIAReclaimSlot(owner.slot, iaRetirementReclaimKind, owner.incarnation,
		owner.last, owner.acknowledged, owner.reclaimed)
	if err != nil {
		return out, err
	}
	sequence := command.identity.Sequence
	if present && saved.sequence == sequence {
		if saved.operation != command.identity.OperationID || saved.request != command.digest {
			return out, ErrConflict
		}
		return iaRetirementReclaimOutcome{decision: receiptReplay, result: saved.result, digest: saved.digest}, nil
	}
	if sequence <= owner.acknowledged {
		return iaRetirementReclaimOutcome{decision: receiptRetired}, nil
	}
	if sequence > owner.last+1 {
		return out, ErrConflict
	}
	if request.latestSequence != owner.last || sequence != owner.last+1 {
		return out, ErrResolveRequired
	}
	return applyIARetirementReclaim(ctx, turn, authority, command, owner)
}

func loadIARetirementReclaimOwner(ctx context.Context, turn *commandTurn, command *iaRetirementReclaimCommand) (out iaRetirementReclaimOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if command == nil {
		return out, ErrInvalid
	}
	s := command.request
	var original, request, incarnation []byte
	var sourceIncarnation, rootIdentity sql.NullString
	var sourceGeneration sql.NullInt64
	var walkRemoved, walkKept int64
	row, err := wc008DraftQuery(ctx, turn, iaRetirementReclaimOwnerPoint, s.kind, s.target)
	if err != nil {
		return out, err
	}
	err = row.Scan(&original, &request, &incarnation,
		&out.original.libraryID, &out.original.libraryKey, &out.original.resolvedRoot,
		&sourceIncarnation, &sourceGeneration, &rootIdentity,
		&out.last, &out.acknowledged, &out.reclaimed, &out.retainedCount, &out.retainedBytes,
		&out.state, &out.phase, &out.entityKind, &out.entityCursor, &out.objectCursor, &walkRemoved, &walkKept,
		&out.due,
		&out.slot.sequence, &out.slot.operation, &out.slot.request, &out.slot.kind, &out.slot.version,
		&out.slot.captured, &out.slot.oldSequence, &out.slot.removedSequence, &out.slot.removedKind,
		&out.slot.removedCount, &out.slot.removedBytes, &out.slot.resultSequence, &out.slot.noNext,
		&out.slot.rows, &out.slot.bytes, &out.slot.appliedMS, &out.slot.resultCount, &out.slot.digest)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(original) != 16 || len(request) != 32 || len(incarnation) != 16 ||
		sourceIncarnation.Valid != (s.kind == "folder") || sourceGeneration.Valid != sourceIncarnation.Valid ||
		rootIdentity.Valid != sourceIncarnation.Valid || (walkRemoved != 0 && walkRemoved != 1) ||
		(walkKept != 0 && walkKept != 1) {
		return out, ErrResolveRequired
	}
	out.original.kind, out.original.target = s.kind, s.target
	out.original.sourceIncarnation, out.original.sourceGeneration, out.original.rootIdentity =
		sourceIncarnation.String, sourceGeneration.Int64, rootIdentity.String
	if out.original.libraryID != s.libraryID || out.original.libraryKey != s.libraryKey ||
		out.original.resolvedRoot != s.resolvedRoot || out.original.sourceIncarnation != s.sourceIncarnation ||
		out.original.sourceGeneration != s.sourceGeneration || out.original.rootIdentity != s.rootIdentity {
		return out, ErrResolveRequired
	}
	// The driver is keyed by kind/target; the reclaimed receipt line is the
	// exact immutable original operation. The write request never supplies it,
	// so it comes from the retained driver only.
	copy(out.original.operationID[:], original)
	copy(out.original.requestDigest[:], request)
	copy(out.incarnation[:], incarnation)
	out.walkRemoved, out.walkKept = walkRemoved == 1, walkKept == 1
	if out.incarnation != command.identity.Incarnation {
		return out, ErrResolveRequired
	}
	// The reclaim watermark is a single monotone sequence floor: at most one
	// unacknowledged receipt, never reclaimed ahead of acknowledgement, and
	// never more remaining receipts than receipt-writing turns.
	if out.last < 1 || out.acknowledged < 0 || out.acknowledged > out.last || out.last-out.acknowledged > 1 ||
		out.reclaimed < 0 || out.reclaimed > out.acknowledged || out.last-out.reclaimed < 0 ||
		out.retainedCount < 0 || out.retainedBytes < 0 || (out.retainedCount == 0) != (out.retainedBytes == 0) ||
		out.retainedCount > out.last-out.reclaimed ||
		(out.state != "retiring" && out.state != "complete") || out.phase < 0 || out.phase > 5 ||
		out.due.Valid && out.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	out.exists = true
	return out, nil
}

// Fresh ACK/reclaim turn: ACK every previously written receipt, delete exactly
// one measured header (or none once the debt is exhausted), and rewrite the
// fixed slot with the remaining debt. Every effect rolls back together.
func applyIARetirementReclaim(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaRetirementReclaimCommand, owner iaRetirementReclaimOwner) (out iaRetirementReclaimOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if authority == nil || command == nil || !owner.exists || turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	// The completion proof is required only on the fresh path: a saved slot is
	// decoded without consulting any later live state.
	if owner.state != "complete" || owner.phase != iaRetirementStepComplete {
		return out, ErrResolveRequired
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	appliedMS, err := iaReclaimTime(ctx, turn)
	if err != nil {
		return out, err
	}
	value := iaReclaimResult{kind: iaRetirementReclaimKind, version: 1, captured: owner.last, oldSequence: owner.reclaimed,
		removedKind: "none", resultSequence: owner.reclaimed, appliedMS: appliedMS, resultCount: 1}
	rows, bytes := owner.retainedCount, owner.retainedBytes
	if rows == 0 {
		// Nothing retained: verify the empty family truthfully and refund zero.
		empty, err := iaRetirementReclaimReceipt(ctx, turn, command, owner, owner.reclaimed)
		if err != nil {
			return out, err
		}
		if empty.exists {
			return out, ErrResolveRequired
		}
		value.noNext, value.rows, value.bytes = true, 0, 0
	} else {
		next, err := iaRetirementReclaimReceipt(ctx, turn, command, owner, owner.reclaimed)
		if err != nil {
			return out, err
		}
		if !next.exists || next.sequence != owner.reclaimed+1 || next.bytes <= 0 || next.bytes > bytes ||
			rows == 1 && next.bytes != bytes {
			return out, ErrResolveRequired
		}
		removed, err := iaRetirementReclaimDrop(ctx, turn, command, owner, next.sequence)
		if err != nil {
			return out, err
		}
		if removed != next.bytes {
			return out, ErrResolveRequired
		}
		value.removedSequence = sql.NullInt64{Int64: next.sequence, Valid: true}
		value.removedKind, value.removedCount, value.removedBytes = "header", 1, removed
		value.resultSequence = next.sequence
		value.noNext, value.rows, value.bytes = rows == 1, rows-1, bytes-removed
	}
	if value.rows < 0 || value.bytes < 0 || value.rows > maxCanonicalResultBytes/96 || value.bytes > maxCanonicalResultBytes {
		return out, ErrConflict
	}
	if err = preflightIAReclaimResult(value); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, value)
	if err != nil {
		return out, err
	}
	if err = iaRetirementReclaimWriteSlot(ctx, turn, command, owner, value, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaRetirementReclaimOutcome{decision: receiptFresh, result: value, digest: digest}, nil
}

type iaRetirementReclaimUnit struct {
	exists   bool
	sequence int64
	bytes    int64
}

// The next receipt after the reclaim watermark is one PK keyset row. Its
// retained_bytes is the measured charge this turn may refund.
func iaRetirementReclaimReceipt(ctx context.Context, turn *commandTurn, command *iaRetirementReclaimCommand, owner iaRetirementReclaimOwner, after int64) (out iaRetirementReclaimUnit, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Unit iaRetirementReclaimUnit
		}{out}, err)
		out, err = v.Unit, x
	}()
	if command == nil || !owner.exists || after < 0 {
		return out, ErrInvalid
	}
	s := command.request
	row, err := wc008DraftQuery(ctx, turn, iaRetirementReclaimNextPoint,
		owner.original.operationID[:], owner.incarnation[:], s.kind, s.target, after)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.sequence, &out.bytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	if out.sequence <= after || out.bytes <= 0 {
		return out, ErrResolveRequired
	}
	out.exists = true
	return out, nil
}

func iaRetirementReclaimDrop(ctx context.Context, turn *commandTurn, command *iaRetirementReclaimCommand, owner iaRetirementReclaimOwner, sequence int64) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(turn), bytes, err) }()
	if sequence <= 0 {
		return 0, ErrInvalid
	}
	s := command.request
	row, err := wc008DraftQuery(ctx, turn, iaRetirementReclaimDelete,
		owner.original.operationID[:], owner.incarnation[:], s.kind, s.target, sequence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, basicStageError(err)
	}
	return bytes, nil
}

// One CAS writes the ACK floor, the measured debt, the due edge and the fixed
// slot together. The WHERE carries every driver field this turn read, including
// the whole previous slot, so a concurrent turn can never be overwritten.
func iaRetirementReclaimWriteSlot(ctx context.Context, turn *commandTurn, command *iaRetirementReclaimCommand, owner iaRetirementReclaimOwner, value iaReclaimResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	id := iaRetirementReclaimAdvanceFirst
	if owner.slot.sequence.Valid {
		id = iaRetirementReclaimAdvance
	}
	var due any
	if value.rows > 0 {
		due = value.appliedMS
	}
	args := []any{
		command.identity.Sequence, command.identity.Sequence, value.resultSequence, value.rows, value.bytes, due,
		command.identity.Sequence, command.identity.OperationID[:], command.digest[:], iaRetirementReclaimKind, int64(1),
		value.captured, value.oldSequence, iaReclaimNullable(value.removedSequence), value.removedKind, value.removedCount,
		value.removedBytes, value.resultSequence, flag64(value.noNext), value.rows, value.bytes, value.appliedMS, int64(1),
		digest[:],
		command.request.kind, command.request.target, owner.incarnation[:], owner.last, owner.acknowledged,
		owner.reclaimed, owner.retainedCount, owner.retainedBytes,
	}
	if id == iaRetirementReclaimAdvance {
		args = append(args, owner.slot.operation, owner.slot.sequence.Int64)
	}
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return basicStageError(err)
	}
	if got != command.identity.Sequence {
		return ErrConflict
	}
	return nil
}

func iaRetirementReclaimDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaRetirementReclaimStatements()...)
}
