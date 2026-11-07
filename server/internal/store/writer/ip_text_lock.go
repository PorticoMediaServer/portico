package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This private native component consumes a COMPLETE frozen source snapshot.
// The outer closed writer entry must admit/copy its actual octet lengths and
// check current metadata-administrator disclosure first. It cannot be called by
// a product service; it neither opens a writer nor returns public Applied.
// Native reads below copy only fixed-width scalars. All TEXT is supplied by the
// frozen original request, compared in SQL before use, and never re-fetched
// from today's values to reconstruct a historical result.
type ipTextLockRequest struct {
	OwnerID, EntityID, LibraryID                   int64
	Kind, Actor                                    string
	Field                                          fieldmeta.TextField
	ExpectedOwnerRevision, ExpectedSubjectRevision int64
	ExpectedMetadataRevision                       int64
	MetadataIncarnation                            [16]byte
	Before                                         fieldmeta.TextState
	Locked                                         bool
	Edit                                           fieldmeta.TextEdit
	Projection                                     ipTextProjection
}

type ipTextLockCommand struct {
	identity                CommandIdentity
	kind                    CommandID
	request                 ipTextLockRequest
	digest                  RequestDigest
	projection              ipTextProjection
	after                   fieldmeta.TextChange
	beforeAudit, afterAudit string
}

type ipTextLockResult struct {
	OwnerID, EntityID, AcceptedRevision, SubjectRevision, AppliedMS int64
	Kind, Field                                                     string
	Value, Automatic                                                string
	ValuePresent, AutomaticPresent, Locked, Changed                 bool
	HistoryID                                                       EvidenceInteger
}

type ipTextLockNative struct {
	decision receiptDecision
	value    ipTextLockResult
	digest   ResultDigest
}

func newIPTextLockCommand(identity CommandIdentity, q ipTextLockRequest) (ipTextLockCommand, error) {
	edit := fieldmeta.TextEdit{Action: fieldmeta.UnlockText}
	if q.Locked {
		edit.Action = fieldmeta.LockText
	}
	return newIPTextMutationCommand(identity, q, IPFieldLock, edit)
}

func newIPTextMutationCommand(identity CommandIdentity, q ipTextLockRequest, kind CommandID, edit fieldmeta.TextEdit) (ipTextLockCommand, error) {
	if _, err := CheckSealControlStrings([]string{q.Kind, q.Actor, string(q.Field)}); err != nil {
		return ipTextLockCommand{}, err
	}
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 ||
		q.OwnerID <= 0 || q.EntityID <= 0 || q.LibraryID <= 0 || q.MetadataIncarnation == [16]byte{} ||
		q.ExpectedOwnerRevision <= 0 || q.ExpectedSubjectRevision <= 0 || q.ExpectedMetadataRevision <= 0 ||
		q.Kind == "" || q.Actor == "" || !sealNativeText(q.Kind) || !sealNativeText(q.Actor) {
		return ipTextLockCommand{}, ErrInvalid
	}
	if (kind != IPFieldLock && kind != IPFieldEdit && kind != IPFieldRestore) ||
		(kind == IPFieldEdit && edit.Action != fieldmeta.SetText) ||
		(kind == IPFieldRestore && edit.Action != fieldmeta.RestoreAutomatic) ||
		(kind == IPFieldLock && edit.Action != fieldmeta.LockText && edit.Action != fieldmeta.UnlockText) {
		return ipTextLockCommand{}, ErrInvalid
	}
	q.Edit = edit
	after, err := fieldmeta.PrepareTextEdit(q.Field, q.Before, edit)
	if err != nil {
		return ipTextLockCommand{}, errors.Join(ErrInvalid, err)
	}
	q.Kind, q.Actor = strings.Clone(q.Kind), strings.Clone(q.Actor)
	q.Field = fieldmeta.TextField(strings.Clone(string(q.Field)))
	q.Before.Effective.Value = strings.Clone(q.Before.Effective.Value)
	q.Before.Automatic.Value = strings.Clone(q.Before.Automatic.Value)
	digest, err := canonicalRequestDigest(kind, 1, identity, q)
	if err != nil {
		return ipTextLockCommand{}, err
	}
	projection, err := ipTextPrepareProjection(q, after)
	if err != nil {
		return ipTextLockCommand{}, err
	}
	q.Edit.Value = strings.Clone(q.Edit.Value)
	q.Projection = ipTextCloneProjection(q.Projection)
	// Audit format records the field delta. Full field values live once in the
	// normalized history child; they are not duplicated into audit JSON.
	type auditField struct {
		Field  string
		Locked bool
	}
	beforeJSON, err := json.Marshal(auditField{string(q.Field), q.Before.Locked})
	if err != nil {
		return ipTextLockCommand{}, err
	}
	afterJSON, err := json.Marshal(auditField{string(q.Field), after.State.Locked})
	if err != nil {
		return ipTextLockCommand{}, err
	}
	if len(beforeJSON) > maxCanonicalRequestBytes-len(afterJSON) {
		return ipTextLockCommand{}, ErrInvalid
	}
	return ipTextLockCommand{identity: identity, kind: kind, request: q, digest: digest, projection: projection, after: after, beforeAudit: string(beforeJSON), afterAudit: string(afterJSON)}, nil
}

func applyIPTextLock(ctx context.Context, turn *commandTurn, command ipTextLockCommand, appliedMS int64) (out ipTextLockNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != command.kind || command.digest == (RequestDigest{}) || appliedMS < 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	q, i := command.request, command.identity
	// Recompute the original complete root; possession of the cached digest
	// neither authorizes the command nor hides a later changed implicit input.
	digest, e := canonicalRequestDigest(command.kind, 1, i, q)
	if e != nil || digest != command.digest {
		return out, ErrInvalid
	}
	check, e := fieldmeta.PrepareTextEdit(q.Field, q.Before, q.Edit)
	if e != nil || check != command.after {
		return out, ErrInvalid
	}
	projection, e := ipTextPrepareProjection(q, check)
	if e != nil || projection != command.projection {
		return out, ErrInvalid
	}
	var owner receiptOwnerFacts
	var ownerRevision int64
	var ownerInc []byte
	rows, e := ipTextRows(ctx, turn, ipTextOwner, q.OwnerID, q.EntityID, q.Kind)
	if e != nil {
		return out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&ownerRevision, &ownerInc, &owner.lastSequence, &owner.acknowledgedSequence)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrOwnerUnavailable
	}
	if e != nil {
		return out, e
	}
	if len(ownerInc) != 16 {
		return out, ErrResolveRequired
	}
	copy(owner.incarnation[:], ownerInc)
	owner.exists = true
	var found receiptFacts
	var operation, request, result []byte
	var accepted, subject, at, changed, effective, nativeKind int64
	var history sql.NullInt64
	rows, e = ipTextRows(ctx, turn, ipTextReceiptHeader, q.OwnerID, i.Incarnation[:], i.Sequence)
	if e != nil {
		return out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &accepted, &subject, &at, &changed, &effective, &history, &nativeKind)
	})
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.identity.command = i
		copy(found.identity.command.OperationID[:], operation)
		copy(found.identity.digest[:], request)
		copy(found.resultDigest[:], result)
		found.kind = CommandID(nativeKind)
		found.resultCount = 1
	}
	decision, e := classifyReceipt(receiptIdentity{i, command.digest}, command.kind, owner, found)
	if e != nil {
		return out, e
	}
	out.decision = decision
	if decision == receiptRetired {
		return out, nil
	}
	if decision == receiptReplay {
		if changed < 0 || changed > 1 || effective != ipTextBool(command.after.EffectiveChanged) || accepted <= 0 || subject <= 0 || at < 0 || history.Valid != (changed == 1) {
			return ipTextLockNative{}, ErrResolveRequired
		}
		out.value = ipTextLockResult{q.OwnerID, q.EntityID, accepted, subject, at, q.Kind, string(q.Field), command.after.State.Effective.Value, command.after.State.Automatic.Value, command.after.State.Effective.Present, command.after.State.Automatic.Present, command.after.State.Locked, changed == 1, EvidenceInteger{history.Int64, history.Valid}}
		if err = ipTextReceiptMatches(ctx, turn, command, out.value); err != nil {
			return ipTextLockNative{}, err
		}
		out.digest, err = ipTextMutationResultDigest(command.kind, i, command.digest, out.value, command.after.EffectiveChanged)
		if err != nil || [32]byte(out.digest) != found.resultDigest {
			return ipTextLockNative{}, ErrResolveRequired
		}
		return out, nil
	}
	if decision != receiptFresh || ownerRevision != q.ExpectedOwnerRevision {
		return ipTextLockNative{}, ErrConflict
	}
	if q.ExpectedOwnerRevision == math.MaxInt64 || q.ExpectedSubjectRevision == math.MaxInt64 || q.ExpectedMetadataRevision == math.MaxInt64 {
		return ipTextLockNative{}, ErrConflict
	}
	// Fresh eligibility follows historical resolution. Full current field bytes
	// are CAS predicates, not uncharged SELECT payloads or caller proof flags.
	if err = ipTextExpectOne(ctx, turn, ipTextSource, q.EntityID, q.LibraryID, q.MetadataIncarnation[:], q.ExpectedSubjectRevision, q.ExpectedMetadataRevision); err != nil {
		return ipTextLockNative{}, err
	}
	b := q.Before
	if err = ipTextExpectOne(ctx, turn, ipTextField, q.Kind, q.EntityID, string(q.Field), b.Effective.Value, b.Automatic.Value, ipTextBool(b.Effective.Present), ipTextBool(b.Automatic.Present), ipTextBool(b.Locked)); err != nil {
		return ipTextLockNative{}, err
	}
	if command.after.EffectiveChanged {
		if err = ipTextCheckProjection(ctx, turn, q, command.projection); err != nil {
			return ipTextLockNative{}, err
		}
	}
	ownerNext, subjectNext := ownerRevision, q.ExpectedSubjectRevision
	var historyID EvidenceInteger
	if command.after.Changed {
		ownerNext++
		subjectNext++
		stamp := time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
		if err = ipTextExpectOne(ctx, turn, ipTextFieldUpdate, command.after.State.Effective.Value, ipTextBool(command.after.State.Effective.Present), ipTextBool(command.after.State.Locked), q.Actor, stamp, q.Kind, q.EntityID, string(q.Field), ipTextBool(b.Locked)); err != nil {
			return ipTextLockNative{}, err
		}
		if command.after.EffectiveChanged {
			if err = ipTextApplyProjection(ctx, turn, q, command.projection, command.after.State.Effective.Value); err != nil {
				return ipTextLockNative{}, err
			}
		}
		if err = ipTextExpectOne(ctx, turn, ipTextSubjectAdvance, q.EntityID, q.LibraryID, q.MetadataIncarnation[:], q.ExpectedSubjectRevision, q.ExpectedMetadataRevision); err != nil {
			return ipTextLockNative{}, err
		}
		var id int64
		rows, e = ipTextRows(ctx, turn, ipTextHistoryInsert, q.Kind, q.EntityID, strconv.FormatInt(ownerRevision, 10), strconv.FormatInt(ownerNext, 10), ipTextCommandName(command.kind), q.Actor, stamp, command.beforeAudit, command.afterAudit)
		if e != nil {
			return ipTextLockNative{}, e
		}
		e = ipTextOne(rows, func() error { return rows.Scan(&id) })
		if e != nil || id <= 0 {
			return ipTextLockNative{}, errors.Join(ErrResolveRequired, e)
		}
		historyID = EvidenceInteger{id, true}
		a := command.after.State
		if err = ipTextExecOne(ctx, turn, ipTextHistoryField, id, string(q.Field), b.Effective.Value, ipTextBool(b.Effective.Present), b.Automatic.Value, ipTextBool(b.Automatic.Present), ipTextBool(b.Locked), a.Effective.Value, ipTextBool(a.Effective.Present), a.Automatic.Value, ipTextBool(a.Automatic.Present), ipTextBool(a.Locked)); err != nil {
			return ipTextLockNative{}, err
		}
	}
	out.value = ipTextLockResult{q.OwnerID, q.EntityID, ownerNext, subjectNext, appliedMS, q.Kind, string(q.Field), command.after.State.Effective.Value, command.after.State.Automatic.Value, command.after.State.Effective.Present, command.after.State.Automatic.Present, command.after.State.Locked, command.after.Changed, historyID}
	out.digest, err = ipTextMutationResultDigest(command.kind, i, command.digest, out.value, command.after.EffectiveChanged)
	if err != nil {
		return ipTextLockNative{}, err
	}
	if err = ipTextExpectOne(ctx, turn, ipTextOwnerAdvance, ownerNext, i.Sequence, q.OwnerID, q.EntityID, q.Kind, i.Incarnation[:], ownerRevision, owner.lastSequence, owner.acknowledgedSequence); err != nil {
		return ipTextLockNative{}, err
	}
	var historyValue any
	if historyID.Present {
		historyValue = historyID.Value
	}
	if err = ipTextExecOne(ctx, turn, ipTextReceiptInsert, q.OwnerID, i.Incarnation[:], i.Sequence, i.OperationID[:], command.digest[:], appliedMS, out.digest[:], ipTextCommandName(command.kind), string(q.Field), ownerNext, subjectNext, out.value.Value, ipTextBool(out.value.ValuePresent), out.value.Automatic, ipTextBool(out.value.AutomaticPresent), ipTextBool(out.value.Locked), ipTextBool(out.value.Changed), ipTextBool(command.after.EffectiveChanged), historyValue); err != nil {
		return ipTextLockNative{}, err
	}
	return out, turn.checkLifetimes(ctx)
}

func ipTextBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func ipTextRows(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	step, ok := t.steps[id]
	if !ok {
		return nil, ErrInvalid
	}
	return t.query(ctx, step.statementStep, args...)
}
func ipTextOne(rows *commandRows, scan func() error) (err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if e := rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	if err = scan(); err != nil {
		return err
	}
	if rows.Next() {
		return ErrResolveRequired
	}
	return rows.Err()
}
func ipTextExpectOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	rows, err := ipTextRows(ctx, t, id, args...)
	if err != nil {
		return err
	}
	var n int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&n) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ipTextExecOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	result, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func ipTextReceiptMatches(ctx context.Context, t *commandTurn, c ipTextLockCommand, v ipTextLockResult) error {
	i := c.identity
	var history any
	if v.HistoryID.Present {
		history = v.HistoryID.Value
	}
	return ipTextExpectOne(ctx, t, ipTextReceiptBody, c.request.OwnerID, i.Incarnation[:], i.Sequence, ipTextCommandName(c.kind), string(c.request.Field), v.Value, ipTextBool(v.ValuePresent), v.Automatic, ipTextBool(v.AutomaticPresent), ipTextBool(v.Locked), history)
}
