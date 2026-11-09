package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA-10 configuration side. Command 105 acknowledges every configuration
// receipt the permanent original owner already holds and then deletes at most
// ONE unit of that owner's acknowledged retained cohort: the next source child
// (lowest ordinal first), or the receipt header once no child remains. The
// child-before-header order is mandatory: catalog_configuration_sources
// references catalog_configuration_receipts with NO ACTION.
//
// ACK alone refunds nothing. receipt_retained_count/bytes decrease only by the
// measured retained_bytes of the row actually deleted in this turn, and the
// original owner row, its request digest and its reclaim watermark remain
// permanently. A turn after the header is gone removes nothing and refunds
// nothing: it only advances the ACK floor and rewrites the fixed slot.
const iaConfigurationReclaimRequestVersion uint16 = 1

type iaConfigurationReclaimRequest struct {
	// The permanent configuration original this turn works on.
	originalOperation [16]byte
	libraryID         int64
	libraryKey        string
	// sourceID is the configure_source target; empty for configure_library.
	sourceID string
	// latestSequence is the caller's observed owner last_command_sequence. A
	// turn is fresh only one past it, so a stale caller cannot skip or repeat.
	latestSequence int64
}

type iaConfigurationReclaimCommand struct {
	identity CommandIdentity
	request  iaConfigurationReclaimRequest
	digest   RequestDigest
}

// Private same-turn outcome. Fresh carries this turn's exact typed result and
// digest; replay carries the decoded slot; retired carries neither.
type iaConfigurationReclaimOutcome struct {
	decision receiptDecision
	result   iaReclaimResult
	digest   ResultDigest
}

type iaConfigurationReclaimOwner struct {
	exists                        bool
	incarnation                   [16]byte
	originalRequest               []byte
	kind                          string
	libraryID                     int64
	libraryKey, sourceID          string
	last, acknowledged, reclaimed int64
	retainedCount, retainedBytes  int64
	due                           sql.NullInt64
	ordinal                       int64
	slot                          iaReclaimSlotRow
}

func validateIAConfigurationReclaimRequest(q iaConfigurationReclaimRequest) error {
	if q.originalOperation == ([16]byte{}) || q.libraryID <= 0 || !iaReclaimText(q.libraryKey, 128) ||
		q.sourceID != "" && !iaReclaimText(q.sourceID, 128) ||
		q.latestSequence < 1 || q.latestSequence == math.MaxInt64 {
		return ErrInvalid
	}
	if len(q.libraryKey)+len(q.sourceID) > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

func newIAConfigurationReclaimCommand(identity CommandIdentity, request iaConfigurationReclaimRequest) (*iaConfigurationReclaimCommand, error) {
	if identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) || identity.Sequence < 2 {
		return nil, invalid("ia_configuration_reclaim_identity")
	}
	if err := validateIAConfigurationReclaimRequest(request); err != nil {
		return nil, err
	}
	if identity.Sequence-request.latestSequence != 1 {
		return nil, invalid("ia_configuration_reclaim_sequence")
	}
	digest, err := canonicalRequestDigest(IAConfigureReclaim, iaConfigurationReclaimRequestVersion, identity, request)
	if err != nil {
		return nil, err
	}
	return &iaConfigurationReclaimCommand{identity: identity, request: request, digest: digest}, nil
}

// One governed configuration ACK/reclaim turn or its original replay. Current
// owner authority is established BEFORE the retained owner/slot read; the
// complete saved slot is decoded before any fresh check.
func reclaimIAConfiguration(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, request iaConfigurationReclaimRequest) (out iaConfigurationReclaimOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAConfigureReclaim {
		return out, ErrInvalid
	}
	command, err := newIAConfigurationReclaimCommand(reference.Identity, request)
	if err != nil {
		return out, err
	}
	// The canonical request digest is recomputed by this writer. A caller's
	// mismatched digest is a conflicting original, never a fresh admission.
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, err := loadIAConfigurationReclaimOwner(ctx, turn, command)
	if err != nil {
		return out, err
	}
	saved, present, err := decodeIAReclaimSlot(owner.slot, iaConfigurationReclaimKind, owner.incarnation,
		owner.last, owner.acknowledged, owner.reclaimed)
	if err != nil {
		return out, err
	}
	sequence := command.identity.Sequence
	// The fixed slot is the only retained reclaim result. It is answered first,
	// so a lost/unknown COMMIT is resolved by the original exact command.
	if present && saved.sequence == sequence {
		if saved.operation != command.identity.OperationID || saved.request != command.digest {
			return out, ErrConflict
		}
		return iaConfigurationReclaimOutcome{decision: receiptReplay, result: saved.result, digest: saved.digest}, nil
	}
	// At or below the ACK floor the cohort is Retired even while a later slot
	// still physically exists; its exact result is no longer retained.
	if sequence <= owner.acknowledged {
		return iaConfigurationReclaimOutcome{decision: receiptRetired}, nil
	}
	if sequence > owner.last+1 {
		return out, ErrConflict
	}
	if request.latestSequence != owner.last || sequence != owner.last+1 {
		return out, ErrResolveRequired
	}
	return applyIAConfigurationReclaim(ctx, turn, authority, command, owner)
}

func loadIAConfigurationReclaimOwner(ctx context.Context, turn *commandTurn, command *iaConfigurationReclaimCommand) (out iaConfigurationReclaimOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if command == nil {
		return out, ErrInvalid
	}
	q := command.request
	var incarnation []byte
	var sourceID sql.NullString
	row, err := wc008DraftQuery(ctx, turn, iaConfigurationReclaimOwnerPoint, q.originalOperation[:])
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.originalRequest, &out.kind, &out.libraryID, &out.libraryKey, &sourceID, &incarnation,
		&out.last, &out.acknowledged, &out.reclaimed, &out.retainedCount, &out.retainedBytes,
		&out.due, &out.ordinal,
		&out.slot.sequence, &out.slot.operation, &out.slot.request, &out.slot.kind, &out.slot.version,
		&out.slot.captured, &out.slot.oldSequence, &out.slot.removedSequence, &out.slot.removedKind,
		&out.slot.removedCount, &out.slot.removedBytes, &out.slot.resultSequence, &out.slot.noNext,
		&out.slot.rows, &out.slot.bytes, &out.slot.appliedMS, &out.slot.resultCount, &out.slot.digest,
		&out.slot.oldOrdinal, &out.slot.removedOrdinal, &out.slot.resultOrdinal)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(out.originalRequest) != 32 || len(incarnation) != 16 ||
		(out.kind != "configure_library" && out.kind != "configure_source") ||
		sourceID.Valid != (out.kind == "configure_source") ||
		(sourceID.Valid && !iaReclaimText(sourceID.String, 128)) ||
		out.libraryID != q.libraryID || out.libraryKey != q.libraryKey ||
		(q.sourceID == "") != (out.kind == "configure_library") || sourceID.String != q.sourceID ||
		out.last < 1 || out.acknowledged < 0 || out.acknowledged > out.last || out.last-out.acknowledged > 1 ||
		out.reclaimed < 0 || out.reclaimed > 1 || out.reclaimed > out.acknowledged ||
		out.retainedCount < 0 || out.retainedBytes < 0 || (out.retainedCount == 0) != (out.retainedBytes == 0) ||
		out.ordinal < -1 || out.reclaimed == 1 && out.ordinal != -1 ||
		out.due.Valid && out.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	out.exists = true
	out.sourceID = sourceID.String
	return out, nil
}

// The configuration owner ledger is bounded (at most 64 source children), so
// this turn can prove its exact remaining rows and bytes before deleting one
// unit rather than trusting a stored total.
func iaConfigurationReclaimProof(ctx context.Context, turn *commandTurn, command *iaConfigurationReclaimCommand, owner iaConfigurationReclaimOwner) (out iaConfigurationReclaimLedger, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if command == nil || !owner.exists {
		return out, ErrInvalid
	}
	q := command.request
	row, err := wc008DraftQuery(ctx, turn, iaConfigurationReclaimHeaderPoint,
		q.originalOperation[:], owner.incarnation[:], int64(1), owner.kind)
	if err != nil {
		return out, err
	}
	var name, kind sql.NullString
	err = row.Scan(&out.headerBytes, &out.headerCount, &name, &kind)
	switch {
	case err == nil:
		if out.headerBytes <= 0 || out.headerCount < 1 || out.headerCount > iaConfigureLibraryMaxSources ||
			name.Valid != (owner.kind == "configure_library") || kind.Valid != name.Valid {
			return out, ErrResolveRequired
		}
		out.header = true
	case errors.Is(err, sql.ErrNoRows):
	default:
		return out, err
	}
	step, ok := turn.steps[iaConfigurationReclaimSourcesPoint]
	if !ok {
		return out, invalid("ia_configuration_reclaim_sources")
	}
	if out.header {
		// The header's result_count is the ORIGINAL cohort length. The remaining
		// children are always the contiguous ordinal tail after the reclaim
		// watermark, so gaps and skipped children can never be served.
		out.base = owner.ordinal + 1
		if out.base < 0 || out.base > out.headerCount {
			return out, ErrResolveRequired
		}
	}
	rows, err := turn.query(ctx, step.statementStep,
		q.originalOperation[:], owner.incarnation[:], int64(1), owner.kind)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ordinal, bytes int64
		if err = rows.Scan(&ordinal, &bytes); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		if !out.header || ordinal != out.base+int64(len(out.sources)) || bytes <= 0 ||
			ordinal < 0 || ordinal >= out.headerCount || ordinal >= iaConfigureLibraryMaxSources {
			return out, ErrResolveRequired
		}
		out.sources = append(out.sources, bytes)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	wantRows := int64(0)
	wantBytes := int64(0)
	if out.header {
		last := out.base - 1 + int64(len(out.sources))
		if out.base+int64(len(out.sources)) != out.headerCount || last != out.headerCount-1 {
			return out, ErrResolveRequired
		}
		wantRows = 1 + int64(len(out.sources))
		wantBytes = out.headerBytes
		for _, bytes := range out.sources {
			if wantBytes > math.MaxInt64-bytes {
				return out, ErrResolveRequired
			}
			wantBytes += bytes
		}
	} else if len(out.sources) != 0 {
		return out, ErrResolveRequired
	}
	if wantRows != owner.retainedCount || wantBytes != owner.retainedBytes {
		return out, ErrResolveRequired
	}
	return out, nil
}

type iaConfigurationReclaimLedger struct {
	header      bool
	headerBytes int64
	headerCount int64
	// base is the first remaining child ordinal; sources[i] is ordinal base+i.
	base    int64
	sources []int64
}

// Fresh ACK/reclaim turn: ACK every previously written receipt, delete exactly
// one measured unit (or none after the header is gone), and rewrite the fixed
// slot with the remaining debt. Every effect rolls back together.
func applyIAConfigurationReclaim(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaConfigurationReclaimCommand, owner iaConfigurationReclaimOwner) (out iaConfigurationReclaimOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if authority == nil || command == nil || !owner.exists || turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	cohort, err := iaConfigurationReclaimProof(ctx, turn, command, owner)
	if err != nil {
		return out, err
	}
	appliedMS, err := iaReclaimTime(ctx, turn)
	if err != nil {
		return out, err
	}
	value := iaReclaimResult{kind: iaConfigurationReclaimKind, version: 1, captured: owner.last, oldSequence: owner.reclaimed,
		removedKind: "none", resultSequence: owner.reclaimed, resultOrdinal: sql.NullInt64{Int64: -1, Valid: true},
		appliedMS: appliedMS, resultCount: 1}
	rows, bytes := owner.retainedCount, owner.retainedBytes
	nextSequence := owner.reclaimed
	nextOrdinal := int64(-1)
	switch {
	case !cohort.header:
		// The header is already gone: nothing left to measure or refund.
		if rows != 0 || bytes != 0 {
			return out, ErrResolveRequired
		}
		value.noNext, value.rows, value.bytes = true, 0, 0
	case len(cohort.sources) > 0:
		next := cohort.base
		if next < 0 {
			return out, ErrResolveRequired
		}
		removed, err := iaConfigurationReclaimDropSource(ctx, turn, command, owner, next)
		if err != nil {
			return out, err
		}
		if removed != cohort.sources[0] {
			return out, ErrResolveRequired
		}
		if rows < 1 || bytes < removed {
			return out, ErrResolveRequired
		}
		value.removedSequence = sql.NullInt64{Int64: 1, Valid: true}
		value.removedKind, value.removedCount, value.removedBytes = "source", 1, removed
		value.resultSequence = 0
		value.removedOrdinal = sql.NullInt64{Int64: next, Valid: true}
		value.resultOrdinal = value.removedOrdinal
		value.noNext, value.rows, value.bytes = false, rows-1, bytes-removed
		nextOrdinal = next
	default:
		removed, err := iaConfigurationReclaimDropHeader(ctx, turn, command, owner)
		if err != nil {
			return out, err
		}
		if removed != cohort.headerBytes || removed != bytes || rows != 1 {
			return out, ErrResolveRequired
		}
		value.removedSequence = sql.NullInt64{Int64: 1, Valid: true}
		value.removedKind, value.removedCount, value.removedBytes = "header", 1, removed
		value.resultSequence, nextSequence = 1, 1
		value.noNext, value.rows, value.bytes = true, 0, 0
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
	if err = iaConfigurationReclaimWriteSlot(ctx, turn, command, owner, value, digest, nextSequence, nextOrdinal); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaConfigurationReclaimOutcome{decision: receiptFresh, result: value, digest: digest}, nil
}

func iaConfigurationReclaimDropSource(ctx context.Context, turn *commandTurn, command *iaConfigurationReclaimCommand, owner iaConfigurationReclaimOwner, ordinal int64) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(turn), bytes, err) }()
	if ordinal < 0 {
		return 0, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, iaConfigurationReclaimSourceDelete,
		command.request.originalOperation[:], owner.incarnation[:], int64(1), owner.kind, ordinal)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, basicStageError(err)
	}
	return bytes, nil
}

func iaConfigurationReclaimDropHeader(ctx context.Context, turn *commandTurn, command *iaConfigurationReclaimCommand, owner iaConfigurationReclaimOwner) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(turn), bytes, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaConfigurationReclaimHeaderDelete,
		command.request.originalOperation[:], owner.incarnation[:], int64(1), owner.kind)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, basicStageError(err)
	}
	return bytes, nil
}

// One CAS writes the ACK floor, the measured debt, the due edge and the fixed
// slot together. The WHERE carries every owner field this turn read, including
// the whole previous slot, so a concurrent turn can never be overwritten.
func iaConfigurationReclaimWriteSlot(ctx context.Context, turn *commandTurn, command *iaConfigurationReclaimCommand, owner iaConfigurationReclaimOwner, value iaReclaimResult, digest ResultDigest, nextSequence, nextOrdinal int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	id := iaConfigurationReclaimAdvanceFirst
	if owner.slot.sequence.Valid {
		id = iaConfigurationReclaimAdvance
	}
	var due any
	if value.rows > 0 {
		due = value.appliedMS
	}
	args := []any{
		command.identity.Sequence, command.identity.Sequence, nextSequence, value.rows, value.bytes, due, nextOrdinal,
		command.identity.Sequence, command.identity.OperationID[:], command.digest[:], iaConfigurationReclaimKind, int64(1),
		value.captured, value.oldSequence, iaReclaimNullable(value.removedSequence), value.removedKind, value.removedCount,
		value.removedBytes, value.resultSequence, flag64(value.noNext), value.rows, value.bytes, value.appliedMS, int64(1),
		digest[:], owner.ordinal, iaReclaimNullable(value.removedOrdinal), iaReclaimNullable(value.resultOrdinal),
		command.request.originalOperation[:], owner.incarnation[:], owner.last, owner.acknowledged, owner.reclaimed,
		owner.retainedCount, owner.retainedBytes, owner.ordinal,
	}
	if id == iaConfigurationReclaimAdvance {
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

func iaReclaimNullable(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func iaConfigurationReclaimDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaConfigurationReclaimStatements()...)
}
