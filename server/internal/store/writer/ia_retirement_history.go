package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This is a retained original driver reference, never a live source, lease or
// mutable library witness. Saved history resolves only the exact original
// operation/incarnation; a later rename, disable, source move or live-row
// deletion cannot edit it.
type iaRetirementBeginReference struct {
	kind, target      string
	libraryID         int64
	libraryKey        string
	originalOperation [16]byte
	identity          CommandIdentity
	request           RequestDigest
	purpose           CommandID
}

type iaRetirementOwner struct {
	exists                        bool
	original                      iaRetirementOriginal
	incarnation                   [16]byte
	last, acknowledged, reclaimed int64
	retainedCount, retainedBytes  int64
}

type iaRetirementReceipt struct {
	exists               bool
	identity             receiptIdentity
	purpose              CommandID
	version, resultCount int64
	result               iaRetirementResult
	digest               ResultDigest
	retainedBytes        int64
}

// One private pair keeps the shared finish guard around both decoded values.
type iaRetirementOwnerReceipt struct {
	owner   iaRetirementOwner
	receipt iaRetirementReceipt
}

// Private same-turn history value. The result and digest are present only for
// an Applied replay; ACKed/retired carries neither.
type iaRetirementBeginHistory struct {
	decision receiptDecision
	result   iaRetirementResult
	digest   ResultDigest
}

func iaRetirementTargetKind(kind CommandID) (string, error) {
	switch kind {
	case IARetireLibrary:
		return "library", nil
	case IARetireSource:
		return "folder", nil
	default:
		return "", invalid("ia_retirement_target_kind")
	}
}

func iaRetirementBeginReferenceFor(purpose CommandID, scope iaRetirementBeginScope, reference ReceiptReference) (iaRetirementBeginReference, error) {
	kind, err := iaRetirementTargetKind(purpose)
	if err != nil {
		return iaRetirementBeginReference{}, err
	}
	target := scope.libraryKey
	if purpose == IARetireSource {
		target = scope.sourceID
	}
	return iaRetirementBeginReference{
		kind: kind, target: target, libraryID: scope.libraryID, libraryKey: scope.libraryKey,
		originalOperation: reference.Identity.OperationID, identity: reference.Identity,
		request: reference.RequestDigest, purpose: purpose,
	}, nil
}

func validateIARetirementBeginReference(q iaRetirementBeginReference) error {
	if _, err := iaRetirementKindName(q.purpose); err != nil {
		return err
	}
	if (q.kind != "library" && q.kind != "folder") || q.target == "" || q.libraryID <= 0 || q.libraryKey == "" ||
		q.originalOperation == ([16]byte{}) || q.identity.Incarnation == ([16]byte{}) ||
		q.identity.OperationID == ([16]byte{}) || q.identity.Sequence != 1 || q.request == (RequestDigest{}) ||
		q.kind == "library" && q.target != q.libraryKey ||
		q.purpose == IARetireLibrary && q.kind != "library" ||
		q.purpose == IARetireSource && q.kind != "folder" {
		return invalid("ia_retirement_begin_reference")
	}
	remaining := maxCanonicalRequestBytes
	for _, text := range []string{q.kind, q.target, q.libraryKey} {
		if len(text) > remaining {
			return invalid("ia_retirement_begin_reference_bytes")
		}
		remaining -= len(text)
	}
	for _, text := range []string{q.kind, q.target, q.libraryKey} {
		if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return invalid("ia_retirement_begin_reference_text")
		}
	}
	return nil
}

// Private storage adapters against the group C candidate. The owning command
// must establish current owner authority and admit the complete native row
// bytes before entry. These helpers cannot mint ingress, a lease, a public
// result or commit truth.
func iaRetirementPoint(ctx context.Context, turn *commandTurn, id stmt.ID, args []any, dest ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("ia_retirement_statement")
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	if !rows.Next() {
		err = rows.Err()
		if err == nil {
			err = sql.ErrNoRows
		}
	} else {
		err = rows.Scan(dest...)
		if err == nil && rows.Next() {
			err = ErrResolveRequired
		}
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func iaRetirementOneEffect(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("ia_retirement_effect")
	}
	changed, err := turn.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	if changed == nil {
		return ErrResolveRequired
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// Owner, target and header are all scoped to the exact original key. A target
// already retired under another operation/incarnation is a conflict before any
// write, and one operation id can never name a second kind/target.
func loadIARetirementOwner(ctx context.Context, turn *commandTurn, q iaRetirementBeginReference) (out iaRetirementOwnerReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = validateIARetirementBeginReference(q); err != nil {
		return out, err
	}
	var operation, incarnation []byte
	err = iaRetirementPoint(ctx, turn, iaRetirementTargetPoint, []any{q.kind, q.target}, &operation, &incarnation)
	if err == nil {
		if len(operation) != 16 || len(incarnation) != 16 || !equalBytes(operation, q.originalOperation[:]) || !equalBytes(incarnation, q.identity.Incarnation[:]) {
			return out, ErrConflict
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	var savedKind, savedTarget string
	err = iaRetirementPoint(ctx, turn, iaRetirementOperationPoint, []any{q.originalOperation[:]}, &savedKind, &savedTarget, &incarnation)
	if err == nil {
		if savedKind != q.kind || savedTarget != q.target || len(incarnation) != 16 || !equalBytes(incarnation, q.identity.Incarnation[:]) {
			return out, ErrConflict
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	owner, receipt := out.owner, out.receipt
	owner.original.kind, owner.original.target = q.kind, q.target
	var original, request, storedInc []byte
	var sourceIncarnation, rootIdentity sql.NullString
	var sourceGeneration sql.NullInt64
	err = iaRetirementPoint(ctx, turn, iaRetirementOwnerPoint, []any{q.kind, q.target, q.originalOperation[:], q.identity.Incarnation[:]},
		&original, &request, &storedInc, &owner.original.libraryID, &owner.original.libraryKey, &owner.original.resolvedRoot,
		&sourceIncarnation, &sourceGeneration, &rootIdentity, &owner.last, &owner.acknowledged, &owner.reclaimed,
		&owner.retainedCount, &owner.retainedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRetirementBeginHeader(ctx, turn, q, owner, receipt)
	}
	if err != nil {
		return out, err
	}
	if len(original) != 16 || len(request) != 32 || len(storedInc) != 16 ||
		sourceIncarnation.Valid != (q.kind == "folder") || sourceGeneration.Valid != sourceIncarnation.Valid || rootIdentity.Valid != sourceIncarnation.Valid ||
		owner.original.libraryID != q.libraryID || owner.original.libraryKey != q.libraryKey ||
		owner.original.kind != q.kind || owner.original.target != q.target ||
		!equalBytes(original, q.originalOperation[:]) || !equalBytes(request, q.request[:]) ||
		!equalBytes(storedInc, q.identity.Incarnation[:]) {
		return iaRetirementOwnerReceipt{}, ErrConflict
	}
	copy(owner.original.operationID[:], original)
	copy(owner.original.requestDigest[:], request)
	copy(owner.incarnation[:], storedInc)
	owner.original.sourceIncarnation, owner.original.sourceGeneration, owner.original.rootIdentity = sourceIncarnation.String, sourceGeneration.Int64, rootIdentity.String
	if owner.last < 0 || owner.acknowledged < 0 || owner.reclaimed < 0 || owner.acknowledged > owner.last || owner.reclaimed > owner.acknowledged ||
		owner.last-owner.acknowledged > 1 || owner.retainedCount < 0 || owner.retainedBytes < 0 ||
		(owner.retainedCount == 0) != (owner.retainedBytes == 0) || owner.retainedCount > owner.last-owner.reclaimed {
		return iaRetirementOwnerReceipt{}, ErrResolveRequired
	}
	owner.exists = true
	return iaRetirementBeginHeader(ctx, turn, q, owner, receipt)
}

func iaRetirementBeginHeader(ctx context.Context, turn *commandTurn, q iaRetirementBeginReference, owner iaRetirementOwner, receipt iaRetirementReceipt) (out iaRetirementOwnerReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var operation, request, digest []byte
	var kind string
	err = iaRetirementPoint(ctx, turn, iaRetirementReceiptPoint,
		[]any{q.kind, q.target, q.originalOperation[:], q.identity.Incarnation[:], q.identity.Sequence},
		&operation, &request, &kind, &receipt.version, &receipt.resultCount, &digest, &receipt.retainedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRetirementOwnerReceipt{owner, receipt}, nil
	}
	if err != nil {
		return out, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
		return out, ErrResolveRequired
	}
	receipt.exists = true
	receipt.identity.command = q.identity
	copy(receipt.identity.command.OperationID[:], operation)
	receipt.identity.digest = q.request
	copy(receipt.digest[:], digest)
	switch kind {
	case "retire_library":
		receipt.purpose = IARetireLibrary
	case "retire_source":
		receipt.purpose = IARetireSource
	default:
		return out, ErrResolveRequired
	}
	return iaRetirementOwnerReceipt{owner, receipt}, nil
}

func lookupIARetirementBegin(ctx context.Context, turn *commandTurn, q iaRetirementBeginReference) (out iaRetirementBeginHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	loaded, err := loadIARetirementOwner(ctx, turn, q)
	if err != nil {
		return out, err
	}
	owner, receipt := loaded.owner, loaded.receipt
	ownerFacts := receiptOwnerFacts{exists: owner.exists, incarnation: owner.incarnation, lastSequence: owner.last, acknowledgedSequence: owner.acknowledged}
	if !owner.exists {
		ownerFacts = receiptOwnerFacts{exists: true, incarnation: q.identity.Incarnation}
	}
	decision, err := classifyReceipt(receiptIdentity{q.identity, q.request}, q.purpose, ownerFacts,
		receiptFacts{exists: receipt.exists, identity: receipt.identity, kind: receipt.purpose, resultCount: receipt.resultCount, resultDigest: receipt.digest})
	if err != nil {
		return out, err
	}
	if decision == receiptFresh && owner.exists {
		return out, ErrResolveRequired
	}
	if decision != receiptReplay {
		return iaRetirementBeginHistory{decision: decision}, nil
	}
	if receipt.version != 1 || receipt.resultCount != 0 || !owner.exists || owner.retainedCount < 1 {
		return out, ErrResolveRequired
	}
	var removed, kept int64
	err = iaRetirementPoint(ctx, turn, iaRetirementBodyPoint,
		[]any{q.kind, q.target, q.originalOperation[:], q.identity.Incarnation[:], q.identity.Sequence},
		&receipt.result.acceptedRevision, &receipt.result.appliedMS, &receipt.result.phase,
		&receipt.result.entityKind, &receipt.result.entityCursor, &receipt.result.objectCursor, &removed, &kept, &receipt.result.state)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if (removed != 0 && removed != 1) || (kept != 0 && kept != 1) {
		return out, ErrResolveRequired
	}
	receipt.result.walkRemoved, receipt.result.walkKept = removed == 1, kept == 1
	receipt.result.original = owner.original
	if receipt.result.original.kind != q.kind || receipt.result.original.target != q.target ||
		receipt.result.original.libraryID != q.libraryID || receipt.result.original.libraryKey != q.libraryKey ||
		receipt.result.original.operationID != q.originalOperation || receipt.result.original.requestDigest != q.request ||
		receipt.result.acceptedRevision <= 0 || receipt.result.appliedMS < 0 ||
		receipt.result.phase != 0 || receipt.result.entityKind != 0 || receipt.result.entityCursor != 0 ||
		receipt.result.objectCursor != "" || receipt.result.walkRemoved || receipt.result.walkKept || receipt.result.state != "retiring" {
		return out, ErrResolveRequired
	}
	var value any
	switch q.purpose {
	case IARetireLibrary:
		value = iaRetireLibraryResult{receipt.result}
	case IARetireSource:
		value = iaRetireSourceResult{receipt.result}
	default:
		return out, ErrResolveRequired
	}
	digest, err := canonicalResultDigest(q.identity, q.request, value)
	if err != nil || !equalBytes(digest[:], receipt.digest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	retained, err := iaRetirementRetainedBytes(q.purpose, receipt.result)
	if err != nil || retained != receipt.retainedBytes || retained > owner.retainedBytes {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return iaRetirementBeginHistory{decision: receiptReplay, result: receipt.result, digest: digest}, nil
}
