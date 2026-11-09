package writer

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// iaRetirementBeginRequestVersion is the canonical original request version of
// commands 102/103. A BEGIN freezes exactly the observed live scope, the exact
// configuration revision, and (for a library) the typed name confirmation. No
// actor secret, mapping token, provider id or filesystem fact is retained.
const iaRetirementBeginRequestVersion uint16 = 1

// iaRetirementSourcePinsUnresolved is the named fail-closed seam for the MP-10
// source pin contract, which is not merged. BEGIN of a source cannot prove an
// active pin is retained for the future bound STEP while MP publishes no pin
// release evidence, so a fresh source BEGIN refuses with a typed policy refusal
// rather than inventing MP schema or pin SQL. Library BEGIN only fences access
// and releases no pin; IA-09 owns bounded removal and MP-15 owns pin release.
var iaRetirementSourcePinsUnresolved = errors.New("ia retirement: MP-10 source pins unresolved")

// Live witnesses supplied by the caller. resolvedRoot is the current library
// root (library variant) or the current source root (source variant), already
// observed outside this command; libraryRevision is the live
// libraries.configuration_revision that both the confirmation and the CAS bind.
type iaRetirementBeginScope struct {
	libraryID                               int64
	libraryKey, sourceID, sourceIncarnation string
	sourceGeneration, libraryRevision       int64
	resolvedRoot, rootIdentity              string
}

type iaRetirementBeginRequest struct {
	scope iaRetirementBeginScope
	// Library retirement requires the current name and a typed confirmation that
	// trims to exactly that name. Source retirement carries neither.
	libraryName, confirmation string
	// sourcePinsResolved is the prepared MP-10 seam. False refuses a fresh source
	// BEGIN with a typed policy refusal; it never widens or bypasses the seam.
	sourcePinsResolved bool
}

type iaRetirementBeginCommand struct {
	purpose  CommandID
	identity CommandIdentity
	request  iaRetirementBeginRequest
	digest   RequestDigest
}

// Private same-turn protocol value, not a public ingress result. Fresh carries
// the exact typed result and digest this command wrote; replay carries the
// decoded original; retired carries no result.
type iaRetireBeginOutcome struct {
	decision receiptDecision
	result   iaRetirementResult
	digest   ResultDigest
}

func iaRetirementText(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 && strings.IndexFunc(s, unicode.IsControl) < 0
}

// The resolved root is the live catalog root and the Basic rescan root, so a
// retirement witness is one native absolute, clean path in this host's spelling.
// Opaque remote locations are not admitted by this task.
func iaRetirementNativeRoot(s string) bool {
	if len(s) == 0 || len(s) > iaConfigureLibraryMaxRoot || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return false
	}
	return iaConfigureLibraryLocalRoot(s, runtime.GOOS == "windows")
}

func validateIARetirementBeginRequest(purpose CommandID, q iaRetirementBeginRequest) error {
	if purpose != IARetireLibrary && purpose != IARetireSource {
		return invalid("ia_retirement_begin_purpose")
	}
	s := q.scope
	if s.libraryID <= 0 || !iaRetirementText(s.libraryKey, 128) || s.libraryRevision <= 0 ||
		!iaRetirementNativeRoot(s.resolvedRoot) {
		return ErrInvalid
	}
	if purpose == IARetireLibrary {
		if s.sourceID != "" || s.sourceIncarnation != "" || s.sourceGeneration != 0 || s.rootIdentity != "" ||
			!iaRetirementText(q.libraryName, 100) || strings.TrimSpace(q.libraryName) != q.libraryName ||
			!iaRetirementText(q.confirmation, 128) || strings.TrimSpace(q.confirmation) != q.libraryName {
			return ErrInvalid
		}
	} else {
		if !iaRetirementText(s.sourceID, 128) || !iaRetirementText(s.sourceIncarnation, 128) ||
			s.sourceGeneration <= 0 || s.sourceGeneration == math.MaxInt64 || !iaRetirementText(s.rootIdentity, 256) ||
			q.libraryName != "" || q.confirmation != "" {
			return ErrInvalid
		}
	}
	total := len(s.libraryKey) + len(s.sourceID) + len(s.sourceIncarnation) + len(s.resolvedRoot) + len(s.rootIdentity) +
		len(q.libraryName) + len(q.confirmation)
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

func newIARetirementBeginCommand(purpose CommandID, identity CommandIdentity, request iaRetirementBeginRequest) (*iaRetirementBeginCommand, error) {
	if identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) || identity.Sequence != 1 {
		return nil, invalid("ia_retirement_begin_identity")
	}
	if err := validateIARetirementBeginRequest(purpose, request); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(purpose, iaRetirementBeginRequestVersion, identity, request)
	if err != nil {
		return nil, err
	}
	return &iaRetirementBeginCommand{purpose: purpose, identity: identity, request: request, digest: digest}, nil
}

// One governed IA retirement BEGIN or original replay. Current owner authority
// is established BEFORE any retained owner/header read; fresh live witnesses are
// compared only after the receipt is Fresh. A saved result is decoded from the
// immutable driver/receipt, never a live library or source row.
func beginIARetirement(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, request iaRetirementBeginRequest) (out iaRetireBeginOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	purpose := turn.transaction.descriptor.ID
	if purpose != IARetireLibrary && purpose != IARetireSource {
		return out, ErrInvalid
	}
	command, err := newIARetirementBeginCommand(purpose, reference.Identity, request)
	if err != nil {
		return out, err
	}
	// The canonical request digest is recomputed by this writer. A caller's
	// mismatched digest is a conflicting original, never a fresh admission.
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	q, err := iaRetirementBeginReferenceFor(purpose, request.scope, reference)
	if err != nil {
		return out, err
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	history, err := lookupIARetirementBegin(ctx, turn, q)
	if err != nil {
		return out, err
	}
	switch history.decision {
	case receiptRetired:
		return iaRetireBeginOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		return iaRetireBeginOutcome{decision: receiptReplay, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIARetirementBegin(ctx, turn, authority, command, q)
	default:
		return out, ErrResolveRequired
	}
}

// BEGIN uses the same owned transaction clock, high-water and protected deadline
// as W's unleased InventoryCreate. The existing removal lease is claimed later;
// no new lease, worker or whole-library fan-out is introduced here.
func (turn *commandTurn) iaRetirementBeginTime(ctx context.Context) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if (tx.descriptor.ID != IARetireLibrary && tx.descriptor.ID != IARetireSource) || tx.writer == nil || tx.writer.now == nil ||
		tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
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

// Fresh BEGIN: the immediate deny fence and durable original operation only.
// No target rows, sources, jobs, pins or receipts are deleted or cascaded; IARetire
// Step owns bounded cleanup later. Any later failure rolls back every fence,
// driver and header together.
func applyIARetirementBegin(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaRetirementBeginCommand, q iaRetirementBeginReference) (out iaRetireBeginOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if authority == nil || command == nil || command.purpose != q.purpose || command.identity != q.identity || command.digest != q.request {
		return out, ErrInvalid
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	r, s := command.request, command.request.scope
	var live int64
	if err = iaRetirementPoint(ctx, turn, iaRetirementLiveLibraryPoint, []any{s.libraryID, s.libraryKey}, &live); err != nil || live != 1 {
		return out, errors.Join(ErrConflict, err)
	}
	if command.purpose == IARetireLibrary {
		if strings.TrimSpace(r.confirmation) != r.libraryName {
			return out, ErrConflict
		}
		if err = iaRetirementPoint(ctx, turn, iaRetirementConfirmation,
			[]any{s.libraryKey, r.libraryName, s.resolvedRoot, s.libraryRevision}, &live); err != nil || live != 1 {
			return out, errors.Join(ErrConflict, err)
		}
	}
	// MP-10 is not built. A fresh source BEGIN refuses rather than pretending an
	// unresolved pin release is safe; library BEGIN releases no pin.
	if command.purpose == IARetireSource && !r.sourcePinsResolved {
		return out, errors.Join(ErrResolveRequired, iaRetirementSourcePinsUnresolved)
	}
	appliedMS, err := turn.iaRetirementBeginTime(ctx)
	if err != nil {
		return out, err
	}
	original := iaRetirementOriginal{
		kind: "library", target: s.libraryKey, libraryID: s.libraryID, libraryKey: s.libraryKey, resolvedRoot: s.resolvedRoot,
		operationID: command.identity.OperationID, requestDigest: command.digest,
	}
	var sourceIncarnation, rootIdentity, sourceGeneration any
	if command.purpose == IARetireSource {
		original.kind, original.target = "folder", s.sourceID
		original.sourceIncarnation, original.sourceGeneration, original.rootIdentity = s.sourceIncarnation, s.sourceGeneration, s.rootIdentity
		sourceIncarnation, sourceGeneration, rootIdentity = s.sourceIncarnation, s.sourceGeneration, s.rootIdentity
	}
	value := iaRetirementResult{original: original, acceptedRevision: 1, appliedMS: appliedMS, state: "retiring"}
	var variant any = iaRetireLibraryResult{value}
	if command.purpose == IARetireSource {
		variant = iaRetireSourceResult{value}
	}
	digest, err := canonicalResultDigest(command.identity, q.request, variant)
	if err != nil {
		return out, err
	}
	retained, err := iaRetirementRetainedBytes(command.purpose, value)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	// Revision and original root/generation fences share the same transaction as
	// driver/receipt creation. A later insert/unique/FK failure rolls back every
	// fence; callers must never salvage a partial frame.
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementConfigurationCAS, s.libraryKey, s.libraryRevision); err != nil {
		return out, err
	}
	if command.purpose == IARetireLibrary {
		err = iaRetirementOneEffect(ctx, turn, iaRetirementLibraryFence, s.libraryID, s.libraryKey, s.resolvedRoot)
	} else {
		err = iaRetirementOneEffect(ctx, turn, iaRetirementSourceFence, s.sourceID, s.libraryKey, s.sourceIncarnation, s.sourceGeneration, s.resolvedRoot, s.rootIdentity)
	}
	if err != nil {
		return out, err
	}
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementDriverInsert, original.kind, original.target, original.operationID[:], original.requestDigest[:],
		command.identity.Incarnation[:], original.libraryID, original.libraryKey, original.resolvedRoot, sourceIncarnation, sourceGeneration, rootIdentity,
		int64(1), int64(1), retained, appliedMS); err != nil {
		return out, err
	}
	kindName, err := iaRetirementKindName(command.purpose)
	if err != nil {
		return out, err
	}
	if err = iaRetirementOneEffect(ctx, turn, iaRetirementHeaderInsert, original.operationID[:], command.identity.Incarnation[:], command.identity.Sequence,
		command.identity.OperationID[:], q.request[:], kindName, int64(1), value.acceptedRevision, appliedMS, original.kind, original.target,
		value.phase, value.entityKind, value.entityCursor, value.objectCursor, int64(0), int64(0), value.state, int64(0), digest[:], retained); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return iaRetireBeginOutcome{}, err
	}
	return iaRetireBeginOutcome{decision: receiptFresh, result: value, digest: digest}, nil
}
