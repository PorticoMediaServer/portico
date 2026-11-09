package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA-09 advances one bounded retirement unit per command: exactly one indexed
// catalogue child is removed from the exact durable cursor, or one truthful
// empty window is recorded. The driver, its lease and the immutable step
// receipt are the only durable facts; no caller-selected victim, cascade,
// whole-library listing, ACK or public facade is admitted.
const iaRetirementStepRequestVersion uint16 = 1

// IA-09 phase cursor. Phase 1 walks the child link family; phase 2 walks the
// parent entity family only after phase 1 is provably empty; phase 5 is the
// terminal state the C candidate requires for a complete retirement.
const (
	iaRetirementStepMembers  int64 = 1
	iaRetirementStepEntities int64 = 2
	iaRetirementStepComplete int64 = 5
	iaRetirementStepWindow         = 8
	iaRetirementStepMaxBytes int64 = 64 << 10
)

// Phase-2 entity_kind is the id-pass marker, not a row view. A pass walks the
// exact (library,id) keyset child-first: parents are deferred while any
// in-library child still names them. A pass that removed something restarts
// once at id 0 so the parents it unblocked are revisited; a pass that removed
// nothing is the truthful blocked-entity hold.
const (
	iaRetirementStepPassOpen    int64 = 0 // the current id pass has removed nothing yet
	iaRetirementStepPassRemoved int64 = 1 // the current id pass already removed an entity
)

// IA-09 cannot claim physical custody exit. The MP-15 release proof and the F
// media custody schema are not merged, so a remaining asset, object or pin is a
// named fail-closed hold, never a silently complete retirement. The pin seam
// name is shared with IA-08's MP-10 contract.
var iaRetirementVictimCustodyUnresolved = errors.New("ia retirement: MP-15 victim custody unresolved")

// A catalogue entity that a NO ACTION/RESTRICT referencer still holds after a
// complete id pass removed nothing is a named hold, never a raw
// SQLITE_CONSTRAINT_FOREIGNKEY and never a fabricated terminal exit.
var iaRetirementEntityReferenced = errors.New("ia retirement: catalogue entity still referenced")

// The caller freezes the live driver it observed: the exact retired target, the
// job revision and lease, and the durable cursor. A fresh step may only move the
// cursor the driver already reports; it cannot skip ahead.
type iaRetirementStepScope struct {
	kind, target, libraryKey, resolvedRoot           string
	libraryID, revision, leaseOwner, leaseGeneration int64
	leaseExpiresMS                                   int64
	sourceIncarnation, rootIdentity                  string
	sourceGeneration                                 int64
	phase, entityKind, entityCursor                  int64
	objectCursor                                     string
	walkRemoved, walkKept                            bool
}

type iaRetirementStepRequest struct {
	scope iaRetirementStepScope
	// sourcePinsResolved is the prepared MP-10 seam. A source victim needs an MP
	// release proof, so a folder target always fails closed in this task.
	sourcePinsResolved bool
}

type iaRetirementStepCommand struct {
	identity CommandIdentity
	request  iaRetirementStepRequest
	digest   RequestDigest
	valid    bool
}

// Private same-turn protocol values, not public ingress results. Fresh carries
// the exact typed result and digest this command wrote; replay carries the
// decoded original; retired carries no result.
type iaRetirementStepOutcome struct {
	decision receiptDecision
	result   iaRetirementResult
	digest   ResultDigest
}
type iaRetirementStepHistory struct {
	decision receiptDecision
	result   iaRetirementResult
	digest   ResultDigest
}

// The durable driver as loaded for one exact kind/target. Cursor and lease are
// immutable comparisons; last/acknowledged are the receipt floor.
type iaRetirementStepOwner struct {
	exists                                            bool
	original                                          iaRetirementOriginal
	incarnation                                       [16]byte
	last, acknowledged, reclaimed                     int64
	retainedCount, retainedBytes                      int64
	state, objectCursor                               string
	phase, entityKind, entityCursor                   int64
	walkRemoved, walkKept                             bool
	revision, leaseOwner, leaseGeneration, leaseExpMS int64
}

// One private pair keeps the shared finish guard around both decoded values.
type iaRetirementStepLoaded struct {
	owner   iaRetirementStepOwner
	receipt iaRetirementStepReceipt
}

type iaRetirementStepReceipt struct {
	exists               bool
	identity             receiptIdentity
	version, resultCount int64
	result               iaRetirementResult
	digest               ResultDigest
	retainedBytes        int64
}

func iaRetirementStepText(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 && strings.IndexFunc(s, unicode.IsControl) < 0
}

// The cursor is canonical: phase 1 carries the last (entity,asset) link in
// entity_cursor/object_cursor and no pass marker; phase 2 carries the last
// examined entity id in entity_cursor, no object cursor, and the id-pass marker
// in entity_kind.
func iaRetirementStepCursorOK(phase, kind, entity int64, object string) bool {
	if entity < 0 {
		return false
	}
	switch phase {
	case iaRetirementStepMembers:
		if kind != iaRetirementStepPassOpen {
			return false
		}
		if len(object) == 0 || len(object) > 20 || object[0] == '-' {
			return false
		}
		for _, c := range object {
			if c < '0' || c > '9' {
				return false
			}
		}
		n, err := strconv.ParseInt(object, 10, 64)
		return err == nil && n >= 0 && strconv.FormatInt(n, 10) == object
	case iaRetirementStepEntities:
		return (kind == iaRetirementStepPassOpen || kind == iaRetirementStepPassRemoved) && object == ""
	default:
		return false
	}
}

func validateIARetirementStepRequest(q iaRetirementStepRequest) error {
	s := q.scope
	if (s.kind != "library" && s.kind != "folder") || !iaRetirementStepText(s.target, 128) ||
		!iaRetirementStepText(s.libraryKey, 128) || !iaRetirementNativeRoot(s.resolvedRoot) ||
		s.libraryID <= 0 || s.revision <= 0 || s.revision == math.MaxInt64 ||
		s.leaseOwner <= 0 || s.leaseGeneration <= 0 || s.leaseExpiresMS <= 0 ||
		!iaRetirementStepCursorOK(s.phase, s.entityKind, s.entityCursor, s.objectCursor) {
		return ErrInvalid
	}
	if s.kind == "library" {
		if s.target != s.libraryKey || s.sourceIncarnation != "" || s.rootIdentity != "" || s.sourceGeneration != 0 {
			return ErrInvalid
		}
	} else {
		if !iaRetirementStepText(s.sourceIncarnation, 128) || !iaRetirementStepText(s.rootIdentity, 256) ||
			s.sourceGeneration <= 0 || s.sourceGeneration == math.MaxInt64 {
			return ErrInvalid
		}
	}
	total := len(s.kind) + len(s.target) + len(s.libraryKey) + len(s.resolvedRoot) + len(s.sourceIncarnation) +
		len(s.rootIdentity) + len(s.objectCursor)
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

func newIARetirementStepCommand(identity CommandIdentity, request iaRetirementStepRequest) (*iaRetirementStepCommand, error) {
	if identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) || identity.Sequence < 2 {
		return nil, invalid("ia_retirement_step_identity")
	}
	if err := validateIARetirementStepRequest(request); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IARetireStep, iaRetirementStepRequestVersion, identity, request)
	if err != nil {
		return nil, err
	}
	return &iaRetirementStepCommand{identity: identity, request: request, digest: digest, valid: true}, nil
}

func (c *iaRetirementStepCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_retirement_step_command")
	}
	checked, err := newIARetirementStepCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// One governed step or original replay. Current owner authority is established
// BEFORE any retained driver/receipt read; live lease and target witnesses are
// compared only after the receipt is Fresh. A saved result is decoded from the
// immutable receipt, never a live catalogue row.
func stepIARetirement(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, request iaRetirementStepRequest) (out iaRetirementStepOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IARetireStep {
		return out, ErrInvalid
	}
	command, err := newIARetirementStepCommand(reference.Identity, request)
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
	loaded, err := loadIARetirementStep(ctx, turn, command)
	if err != nil {
		return out, err
	}
	owner := loaded.owner
	ownerFacts := receiptOwnerFacts{exists: owner.exists, incarnation: owner.incarnation,
		lastSequence: owner.last, acknowledgedSequence: owner.acknowledged}
	decision, err := classifyReceipt(receiptIdentity{command.identity, command.digest}, IARetireStep, ownerFacts,
		receiptFacts{exists: loaded.receipt.exists, identity: loaded.receipt.identity, kind: IARetireStep,
			resultCount: loaded.receipt.resultCount, resultDigest: loaded.receipt.digest})
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaRetirementStepOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIARetirementStep(ctx, turn, authority, command, loaded)
		if err != nil {
			return out, err
		}
		return iaRetirementStepOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIARetirementStep(ctx, turn, authority, command, loaded.owner)
	default:
		return out, ErrResolveRequired
	}
}

// Owner, receipt header and body are read from the immutable original keys. The
// driver row may describe a target whose live rows are already gone; only the
// fresh path consults those rows.
func loadIARetirementStep(ctx context.Context, turn *commandTurn, command *iaRetirementStepCommand) (out iaRetirementStepLoaded, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if command == nil || !command.valid {
		return out, ErrInvalid
	}
	s := command.request.scope
	owner := out.owner
	var original, request, incarnation []byte
	var sourceIncarnation, rootIdentity sql.NullString
	var sourceGeneration sql.NullInt64
	var walkRemoved, walkKept int64
	row, err := wc008DraftQuery(ctx, turn, iaRetireStepOwner, s.kind, s.target)
	if err != nil {
		return out, err
	}
	err = row.Scan(&original, &request, &incarnation,
		&owner.original.libraryID, &owner.original.libraryKey, &owner.original.resolvedRoot,
		&sourceIncarnation, &sourceGeneration, &rootIdentity,
		&owner.last, &owner.acknowledged, &owner.reclaimed, &owner.retainedCount, &owner.retainedBytes,
		&owner.state, &owner.phase, &owner.entityKind, &owner.entityCursor, &owner.objectCursor, &walkRemoved, &walkKept,
		&owner.revision, &owner.leaseOwner, &owner.leaseGeneration, &owner.leaseExpMS)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(original) != 16 || len(request) != 32 || len(incarnation) != 16 ||
		sourceIncarnation.Valid != (s.kind == "folder") || sourceGeneration.Valid != sourceIncarnation.Valid ||
		rootIdentity.Valid != sourceIncarnation.Valid || (walkRemoved != 0 && walkRemoved != 1) || (walkKept != 0 && walkKept != 1) {
		return out, ErrResolveRequired
	}
	owner.exists = true
	owner.original.kind, owner.original.target = s.kind, s.target
	copy(owner.original.operationID[:], original)
	copy(owner.original.requestDigest[:], request)
	copy(owner.incarnation[:], incarnation)
	owner.original.sourceIncarnation, owner.original.sourceGeneration, owner.original.rootIdentity = sourceIncarnation.String, sourceGeneration.Int64, rootIdentity.String
	owner.walkRemoved, owner.walkKept = walkRemoved == 1, walkKept == 1
	if owner.original.libraryID != s.libraryID || owner.original.libraryKey != s.libraryKey ||
		owner.original.resolvedRoot != s.resolvedRoot || owner.original.sourceIncarnation != s.sourceIncarnation ||
		owner.original.sourceGeneration != s.sourceGeneration || owner.original.rootIdentity != s.rootIdentity ||
		owner.incarnation != command.identity.Incarnation {
		return out, ErrResolveRequired
	}
	if owner.last < 0 || owner.acknowledged < 0 || owner.reclaimed < 0 || owner.acknowledged > owner.last ||
		owner.reclaimed > owner.acknowledged || owner.last-owner.acknowledged > 1 ||
		owner.retainedCount < 0 || owner.retainedBytes < 0 || (owner.retainedCount == 0) != (owner.retainedBytes == 0) ||
		owner.retainedCount > owner.last-owner.reclaimed || owner.reclaimed != 0 {
		return out, ErrResolveRequired
	}
	if owner.state != "retiring" && owner.state != "complete" || owner.phase < 0 || owner.phase > 5 ||
		owner.revision <= 0 || owner.leaseOwner < 0 || owner.leaseGeneration < 0 || owner.leaseExpMS < 0 {
		return out, ErrResolveRequired
	}
	out.owner = owner
	return iaRetirementStepHeader(ctx, turn, command, owner, out.receipt)
}

func iaRetirementStepHeader(ctx context.Context, turn *commandTurn, command *iaRetirementStepCommand, owner iaRetirementStepOwner, receipt iaRetirementStepReceipt) (out iaRetirementStepLoaded, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q := command.request.scope
	var operation, request, digest []byte
	var kind string
	row, err := wc008DraftQuery(ctx, turn, iaRetireStepReceipt,
		q.kind, q.target, owner.original.operationID[:], owner.incarnation[:], command.identity.Sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRetirementStepLoaded{owner, receipt}, nil
	}
	if err != nil {
		return out, err
	}
	err = row.Scan(&operation, &request, &kind, &receipt.version, &receipt.resultCount, &digest, &receipt.retainedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRetirementStepLoaded{owner, receipt}, nil
	}
	if err != nil {
		return out, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || kind != "retire_step" {
		return out, ErrResolveRequired
	}
	receipt.exists = true
	receipt.identity.command = command.identity
	copy(receipt.identity.command.OperationID[:], operation)
	copy(receipt.identity.digest[:], request)
	copy(receipt.digest[:], digest)
	if receipt.identity.digest != command.digest || receipt.identity.command != command.identity {
		return out, ErrConflict
	}
	return iaRetirementStepLoaded{owner, receipt}, nil
}

// Replay decodes only the immutable receipt body. Live catalogue, lease and
// target rows are never consulted, so a later deletion cannot edit history.
func lookupIARetirementStep(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaRetirementStepCommand, loaded iaRetirementStepLoaded) (out iaRetirementStepHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid {
		return out, ErrInvalid
	}
	owner, receipt := loaded.owner, loaded.receipt
	if !owner.exists || !receipt.exists || receipt.version != 1 || receipt.resultCount != 0 ||
		receipt.identity != (receiptIdentity{command.identity, command.digest}) || owner.retainedCount < 1 {
		return out, ErrResolveRequired
	}
	q := command.request.scope
	var removed, kept int64
	row, err := wc008DraftQuery(ctx, turn, iaRetireStepBody,
		q.kind, q.target, owner.original.operationID[:], owner.incarnation[:], command.identity.Sequence)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	err = row.Scan(&receipt.result.acceptedRevision, &receipt.result.appliedMS, &receipt.result.phase,
		&receipt.result.entityKind, &receipt.result.entityCursor, &receipt.result.objectCursor,
		&removed, &kept, &receipt.result.state)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if (removed != 0 && removed != 1) || (kept != 0 && kept != 1) {
		return out, ErrResolveRequired
	}
	receipt.result.walkRemoved, receipt.result.walkKept = removed == 1, kept == 1
	receipt.result.original = owner.original
	if !iaRetirementStepResultOK(q, receipt.result) {
		return out, ErrResolveRequired
	}
	bytes := int64(len(receipt.result.original.libraryKey) + len(receipt.result.original.resolvedRoot) +
		len(receipt.result.original.sourceIncarnation) + len(receipt.result.original.rootIdentity) +
		len(receipt.result.objectCursor) + len(receipt.result.state))
	if err = authority.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	if err = preflightIARetirementResult(IARetireStep, command.identity, command.digest, receipt.result); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaRetireStepResult{receipt.result})
	if err != nil || !equalBytes(digest[:], receipt.digest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	retained, err := iaRetirementRetainedBytes(IARetireStep, receipt.result)
	if err != nil || retained != receipt.retainedBytes || retained > owner.retainedBytes {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaRetirementStepHistory{decision: receiptReplay, result: receipt.result, digest: digest}, nil
}

func iaRetirementStepResultOK(q iaRetirementStepScope, v iaRetirementResult) bool {
	if v.original.kind != q.kind || v.original.target != q.target || v.original.libraryID != q.libraryID ||
		v.original.libraryKey != q.libraryKey || v.original.resolvedRoot != q.resolvedRoot ||
		v.original.sourceIncarnation != q.sourceIncarnation || v.original.sourceGeneration != q.sourceGeneration ||
		v.original.rootIdentity != q.rootIdentity ||
		v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		!iaRetirementStepCursorOK(v.phase, v.entityKind, v.entityCursor, v.objectCursor) &&
			!(v.state == "complete" && v.phase == iaRetirementStepComplete) {
		return false
	}
	if v.state == "complete" {
		return v.phase == iaRetirementStepComplete && v.objectCursor == "" && !v.walkRemoved && !v.walkKept
	}
	return v.state == "retiring" && v.phase != iaRetirementStepComplete
}

// Fresh step: one exact indexed child unit, one driver cursor CAS and one
// immutable receipt. Every failure rolls all three back together.
func applyIARetirementStep(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaRetirementStepCommand, owner iaRetirementStepOwner) (out iaRetirementStepOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if authority == nil || command == nil || !command.valid || !owner.exists ||
		turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	q := command.request.scope
	if owner.last+1 != command.identity.Sequence {
		return out, ErrConflict
	}
	if owner.state != "retiring" || owner.phase != q.phase || owner.entityKind != q.entityKind ||
		owner.entityCursor != q.entityCursor || owner.objectCursor != q.objectCursor ||
		owner.walkRemoved != q.walkRemoved || owner.walkKept != q.walkKept || owner.revision != q.revision ||
		owner.leaseOwner != q.leaseOwner || owner.leaseGeneration != q.leaseGeneration || owner.leaseExpMS != q.leaseExpiresMS {
		return out, ErrConflict
	}
	// A folder retirement's victims are that source's objects and pins, which
	// MP-10/MP-15 own. Without the release proof a source step fails closed.
	if q.kind == "folder" {
		if !command.request.sourcePinsResolved {
			return out, errors.Join(ErrResolveRequired, iaRetirementSourcePinsUnresolved)
		}
		return out, errors.Join(ErrResolveRequired, iaRetirementVictimCustodyUnresolved)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	live, err := iaRetirementStepExists(ctx, turn, iaRetireStepLibrary, q.libraryID, q.libraryKey, q.resolvedRoot)
	if err != nil || !live {
		return out, errors.Join(ErrConflict, err)
	}
	now := turn.transaction.writer.now()
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || !now.Before(deadline) || !now.Before(time.UnixMilli(q.leaseExpiresMS)) || deadline.After(time.UnixMilli(q.leaseExpiresMS)) {
		return out, ErrConflict
	}
	appliedMS, err := iaRetirementStepTime(ctx, turn)
	if err != nil {
		return out, err
	}
	next, err := iaRetirementStepVictim(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if next.bytes > iaRetirementStepMaxBytes {
		return out, ErrCap
	}
	if err = authority.budget.check(ctx, turn, next.bytes); err != nil {
		return out, err
	}
	if owner.revision == math.MaxInt64 {
		return out, ErrCap
	}
	value := iaRetirementResult{
		original: owner.original, acceptedRevision: owner.revision + 1, appliedMS: appliedMS,
		phase: next.phase, entityKind: next.entityKind, entityCursor: next.entityCursor,
		objectCursor: next.objectCursor, walkRemoved: next.removed, walkKept: next.kept, state: next.state,
	}
	if err = preflightIARetirementResult(IARetireStep, command.identity, command.digest, value); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaRetireStepResult{value})
	if err != nil {
		return out, err
	}
	retained, err := iaRetirementRetainedBytes(IARetireStep, value)
	if err != nil {
		return out, err
	}
	if retained > math.MaxInt64-owner.retainedBytes {
		return out, ErrCap
	}
	if err = authority.budget.check(ctx, turn, retained); err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	if err = iaRetirementStepOne(ctx, turn, iaRetireStepAdvance,
		command.identity.Sequence, value.state, value.phase, value.entityKind, value.entityCursor, value.objectCursor,
		flag64(value.walkRemoved), flag64(value.walkKept), owner.revision+1, owner.retainedCount+1, owner.retainedBytes+retained,
		q.kind, q.target, owner.original.operationID[:], owner.incarnation[:], owner.original.requestDigest[:],
		owner.original.libraryID, owner.original.libraryKey, owner.original.resolvedRoot,
		owner.last, owner.acknowledged, owner.reclaimed, owner.retainedCount, owner.retainedBytes,
		owner.phase, owner.entityKind, owner.entityCursor, owner.objectCursor, flag64(owner.walkRemoved), flag64(owner.walkKept),
		owner.revision, owner.leaseOwner, owner.leaseGeneration, owner.leaseExpMS); err != nil {
		return out, err
	}
	if err = iaRetirementStepOne(ctx, turn, iaRetireStepReceiptInsert,
		owner.original.operationID[:], owner.incarnation[:], command.identity.Sequence,
		command.identity.OperationID[:], command.digest[:], "retire_step", int64(1),
		value.acceptedRevision, value.appliedMS, value.original.kind, value.original.target, value.phase,
		value.entityKind, value.entityCursor, value.objectCursor, flag64(value.walkRemoved), flag64(value.walkKept),
		value.state, int64(0), digest[:], retained); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaRetirementStepOutcome{decision: receiptFresh, result: value, digest: digest}, nil
}

func flag64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// The single unit this turn removes, or the truthful empty window. A victim's
// variable bytes are charged before any delete; an over-quota row refuses.
type iaRetirementStepUnit struct {
	phase, entityKind, entityCursor int64
	objectCursor                    string
	removed, kept                   bool
	state                           string
	bytes                           int64
}

func iaRetirementStepVictim(ctx context.Context, turn *commandTurn, q iaRetirementStepScope) (out iaRetirementStepUnit, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = iaRetirementStepUnit{phase: q.phase, entityKind: q.entityKind, entityCursor: q.entityCursor,
		objectCursor: q.objectCursor, state: "retiring"}
	switch q.phase {
	case iaRetirementStepMembers:
		return iaRetirementStepMemberUnit(ctx, turn, q)
	case iaRetirementStepEntities:
		return iaRetirementStepEntityUnit(ctx, turn, q)
	default:
		return out, ErrConflict
	}
}

// Member walk: for the current entity, remove its next link after the last
// asset cursor; when that entity has no link left, advance one entity. A full
// window without a link is a truthful empty unit; exhausting every entity moves
// to the parent family in this same turn.
func iaRetirementStepMemberUnit(ctx context.Context, turn *commandTurn, q iaRetirementStepScope) (out iaRetirementStepUnit, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	entity := q.entityCursor
	asset, err := strconv.ParseInt(q.objectCursor, 10, 64)
	if err != nil {
		return out, ErrInvalid
	}
	for steps := 0; steps < iaRetirementStepWindow; steps++ {
		row, err := wc008DraftQuery(ctx, turn, iaRetireStepNextMember, q.libraryID, entity, asset)
		if err != nil {
			return out, err
		}
		var foundEntity, foundAsset int64
		err = row.Scan(&foundEntity, &foundAsset)
		if err == nil {
			if err = iaRetirementStepOne(ctx, turn, iaRetireStepMemberDelete, q.libraryID, foundEntity, foundAsset); err != nil {
				return out, err
			}
			return iaRetirementStepUnit{phase: iaRetirementStepMembers, entityCursor: foundEntity,
				objectCursor: strconv.FormatInt(foundAsset, 10), removed: true, state: "retiring"}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		next, err := iaRetirementStepNextEntity(ctx, turn, q.libraryID, entity)
		if err != nil {
			return out, err
		}
		if next == 0 {
			// Every entity is past the member cursor: the child family is empty.
			parent := q
			parent.phase, parent.entityKind, parent.entityCursor, parent.objectCursor = iaRetirementStepEntities, 0, 0, ""
			return iaRetirementStepEntityUnit(ctx, turn, parent)
		}
		entity, asset = next, 0
	}
	return iaRetirementStepUnit{phase: iaRetirementStepMembers, entityCursor: entity,
		objectCursor: "0", state: "retiring"}, nil
}

func iaRetirementStepNextEntity(ctx context.Context, turn *commandTurn, libraryID, after int64) (id int64, err error) {
	defer func() { id, err = finishCommand(ctx, commandLifetime(turn), id, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaRetireStepNextEntity, libraryID, after, int64(1))
	if err != nil {
		return 0, err
	}
	var kind int64
	var title string
	err = row.Scan(&id, &kind, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

type iaRetirementStepCandidate struct {
	id, kind, bytes int64
}

func iaRetirementStepEntityPage(ctx context.Context, turn *commandTurn, libraryID, after int64, limit int) (out []iaRetirementStepCandidate, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := turn.query(ctx, turn.steps[iaRetireStepNextEntity].statementStep, libraryID, after, int64(limit))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c iaRetirementStepCandidate
		var title string
		if err = rows.Scan(&c.id, &c.kind, &title); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		c.bytes = int64(len(title))
		out = append(out, c)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// Entity window: at most iaRetirementStepWindow candidates from the exact
// (library,id) keyset, taken child-first. A candidate that any in-library child
// or any other NO ACTION/RESTRICT referencer still names is kept for this
// window, never cascaded and never handed to SQLite as a raw 787. A full page
// of kept rows records a truthful empty unit and advances the keyset; an
// exhausted page either restarts the pass once (the deletions above unblocked
// parents below the cursor) or names the blocked-entity hold.
func iaRetirementStepEntityUnit(ctx context.Context, turn *commandTurn, q iaRetirementStepScope) (out iaRetirementStepUnit, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = iaRetirementStepUnit{phase: iaRetirementStepEntities, entityKind: q.entityKind,
		entityCursor: q.entityCursor, state: "retiring"}
	if q.phase == iaRetirementStepEntities {
		has, err := iaRetirementStepExists(ctx, turn, iaRetireStepMemberExists, q.libraryID)
		if err != nil {
			return out, err
		}
		if has {
			return out, ErrConflict
		}
	}
	window, err := iaRetirementStepEntityPage(ctx, turn, q.libraryID, q.entityCursor, iaRetirementStepWindow)
	if err != nil {
		return out, err
	}
	after := q.entityCursor
	kept := false
	for _, c := range window {
		after = c.id
		held, err := iaRetirementStepExists(ctx, turn, iaRetireStepEntityHold, c.id)
		if err != nil {
			return out, err
		}
		if held {
			kept = true
			continue
		}
		blocked, err := iaRetirementStepReferenced(ctx, turn, c.id)
		if err != nil {
			return out, err
		}
		if blocked {
			kept = true
			continue
		}
		if c.bytes > iaRetirementStepMaxBytes {
			return out, ErrCap
		}
		err = iaRetirementStepOne(ctx, turn, iaRetireStepEntityDelete, q.libraryID, c.id)
		if iaRetirementStepForeignRefused(err) {
			// The one NO ACTION referencer without an entity-leading key
			// (catalog_aggregate_publications) is authoritative here: the row is
			// kept, the statement changed nothing and the pass continues.
			kept = true
			continue
		}
		if err != nil {
			return out, err
		}
		return iaRetirementStepUnit{phase: iaRetirementStepEntities, entityKind: iaRetirementStepPassRemoved,
			entityCursor: c.id, removed: true, kept: kept, state: "retiring", bytes: c.bytes}, nil
	}
	if len(window) == iaRetirementStepWindow {
		return iaRetirementStepUnit{phase: iaRetirementStepEntities, entityKind: q.entityKind,
			entityCursor: after, kept: true, state: "retiring"}, nil
	}
	// Exhausted. Terminal needs actual physical exit: no held entity, no asset
	// and no object custody may remain before the driver may claim complete.
	has, err := iaRetirementStepExists(ctx, turn, iaRetireStepEntityExists, q.libraryID)
	if err != nil {
		return out, err
	}
	if !has {
		asset, err := iaRetirementStepExists(ctx, turn, iaRetireStepAssetExists, q.libraryID)
		if err != nil {
			return out, err
		}
		if asset {
			return out, errors.Join(ErrResolveRequired, iaRetirementVictimCustodyUnresolved)
		}
		object, err := iaRetirementStepExists(ctx, turn, iaRetireStepObjectExists, q.libraryKey)
		if err != nil {
			return out, err
		}
		if object {
			return out, errors.Join(ErrResolveRequired, iaRetirementVictimCustodyUnresolved)
		}
		return iaRetirementStepUnit{phase: iaRetirementStepComplete, state: "complete"}, nil
	}
	if q.entityKind == iaRetirementStepPassRemoved {
		// This pass removed entities, so the pass restart is the bounded
		// child-first guarantee: the parents those removals unblocked are below
		// the cursor and get their exact keyset turn from id 0.
		return iaRetirementStepUnit{phase: iaRetirementStepEntities, entityKind: iaRetirementStepPassOpen,
			entityCursor: 0, kept: true, state: "retiring"}, nil
	}
	return out, errors.Join(ErrResolveRequired, iaRetirementEntityReferenced)
}

// One closed probe per child/hold reverse key. Child probes come first: a
// deferred parent never pays for the hold family, and the in-library child is
// the common case in a TV, music or book hierarchy.
var iaRetirementStepProbes = []struct {
	id   stmt.ID
	args int
}{
	{iaRetireStepChildParent, 1},
	{iaRetireStepChildShow, 1},
	{iaRetireStepChildSeason, 1},
	{iaRetireStepChildArtist, 1},
	{iaRetireStepChildAlbum, 1},
	{iaRetireStepChildBook, 1},
	{iaRetireStepHeldMutation, 10},
	{iaRetireStepHeldSongArtist, 1},
	{iaRetireStepHeldAggregate, 1},
	{iaRetireStepHeldScalar, 1},
	{iaRetireStepHeldContributor, 1},
	{iaRetireStepHeldAsset, 1},
	{iaRetireStepHeldSubject, 1},
	{iaRetireStepHeldProjection, 1},
	{iaRetireStepHeldPublication, 1},
	{iaRetireStepHeldAcquisition, 1},
	{iaRetireStepHeldHint, 1},
	{iaRetireStepHeldReconcile, 1},
	{iaRetireStepHeldExternal, 1},
	{iaRetireStepHeldField, 1},
	{iaRetireStepHeldCredit, 1},
}

func iaRetirementStepReferenced(ctx context.Context, turn *commandTurn, entity int64) (blocked bool, err error) {
	defer func() { blocked, err = finishCommand(ctx, commandLifetime(turn), blocked, err) }()
	for _, probe := range iaRetirementStepProbes {
		args := make([]any, probe.args)
		for at := range args {
			args[at] = entity
		}
		found, err := iaRetirementStepExists(ctx, turn, probe.id, args...)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// SQLITE_CONSTRAINT_FOREIGNKEY from the exact entity DELETE. Exactly one
// NO ACTION referencer into catalog_entities (catalog_aggregate_publications
// .parent_id) has no entity-leading native key, so an unbounded probe scan is
// the only alternative; the native refusal is the closed answer.
func iaRetirementStepForeignRefused(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code() == 787
}

func iaRetirementStepExists(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	step, ok := turn.steps[id]
	if !ok {
		return false, invalid("ia_retirement_step_statement")
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

func iaRetirementStepOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("ia_retirement_step_effect")
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

// The step uses the same owned transaction clock, high-water and protected
// deadline as the BEGIN. It never invents a clock or extends the lease.
func iaRetirementStepTime(ctx context.Context, turn *commandTurn) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.descriptor.ID != IARetireStep || tx.writer == nil || tx.writer.now == nil ||
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
