package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

// IP owner edit is one typed descriptive bundle under the shared IP governed
// entry. Current authority precedes historical receipt disclosure; an original
// saved result replays without fresh source/lease checks. Admission, complete
// result replay and COMMIT proof remain this body's job; it issues no public
// Applied capability and no provider-worker authority.
type ipTextEditCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipTextEditResult
	Digest  ResultDigest
}

func (ipTextEditCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipTextEditCommand")
}

func (ipTextEditCommandResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipTextEditCommandResult")
}

func (c *ipTextEditCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipTextEditEntryRequest(c *ipTextEditCommand) ipEntryRequest {
	s := c.request.Source
	kind, library, key := s.TargetKind, s.LibraryID, ""
	if s.PersonID > 0 {
		kind, library = 0, 0
	} else {
		key = s.LibraryKey
	}
	return ipEntryRequest{purpose: IPFieldEdit, action: ipEntryApply, reference: c.reference(),
		targetID: ipTextEditTarget(s), targetKind: kind, libraryID: library, libraryKey: key}
}

// applyIPTextEdit is the closed command body. A fresh bundle runs the atomic
// owner/field/history/receipt cohort; a saved receipt returns its exact original
// result even after the source, projection or authority-adjacent lease moved.
func applyIPTextEdit(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget, c *ipTextEditCommand) (out ipTextEditCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipTextEditCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPFieldEdit {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPFieldEdit, 1, c.identity, c.request)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	entry, err := enterIPCommand(ctx, turn, ipTextEditEntryRequest(c), p, lifetime, b)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch entry.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		value, digest, e := ipTextEditReadSaved(ctx, turn, entry.ownerID, c)
		if e != nil {
			return zero, e
		}
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = value, digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	s := c.request.Source
	if s.PersonID > 0 {
		if err = entry.checkFreshSubject(ctx, turn, [16]byte{}, 0, 0, s.SubjectRevision, s.PersonToken); err != nil {
			return zero, err
		}
	} else if err = entry.checkFreshSubject(ctx, turn, s.MetadataIncarnation, s.SourceRevision, s.IdentityRevision, s.MetadataRevision, ""); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	app, err := ipTextEditWriteFresh(ctx, turn, c, entry, appliedMS)
	if err != nil {
		return zero, err
	}
	out.Value, out.Digest, out.Outcome.State = app.value, app.digest, Applied
	return out, nil
}

// ipTextEditReadSaved reconstructs the COMPLETE original result from the saved
// receipt and its ordered field children, then proves its canonical digest.
func ipTextEditReadSaved(ctx context.Context, turn *commandTurn, ownerID int64, c *ipTextEditCommand) (value ipTextEditResult, digest ResultDigest, err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	s := c.request.Source
	ref := c.reference()
	if ownerID <= 0 {
		return value, digest, ErrResolveRequired
	}
	rows, err := ipTextRows(ctx, turn, ipTextEditReceipt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return value, digest, err
	}
	var operation, request, result []byte
	var kind, field, headerValue, headerAutomatic string
	var count, accepted, subject, applied, changed, effective, valuePresent, automaticPresent, locked int64
	var history sql.NullInt64
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &kind, &count, &accepted, &subject, &applied,
			&changed, &effective, &history, &field, &headerValue, &valuePresent, &headerAutomatic, &automaticPresent, &locked)
	}); err != nil {
		return value, digest, errors.Join(ErrResolveRequired, err)
	}
	changedBool, ok1 := ipTextFlag(changed)
	effectiveBool, ok2 := ipTextFlag(effective)
	var savedRequest RequestDigest
	var savedOperation [16]byte
	copy(savedRequest[:], request)
	copy(savedOperation[:], operation)
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 || kind != "field_edit" ||
		savedRequest != ref.RequestDigest || savedOperation != ref.Identity.OperationID ||
		count <= 0 || count > ipTextMaxFields || accepted <= 0 || subject <= 0 || applied < 0 ||
		!ok1 || !ok2 || field != "" || headerValue != "" || valuePresent != 0 || headerAutomatic != "" ||
		automaticPresent != 0 || locked != 0 || history.Valid != changedBool {
		return value, digest, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipTextEditReceiptField, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return value, digest, err
	}
	defer rows.Close()
	var fields []ipTextEditFieldResult
	var anyEffective, anyChanged bool
	for rows.Next() {
		var ordinal, valuePresent, automaticPresent, locked, changed, effective int64
		var name, fieldValue, automatic string
		if err = rows.Scan(&ordinal, &name, &fieldValue, &valuePresent, &automatic, &automaticPresent, &locked, &changed, &effective); err != nil {
			return value, digest, err
		}
		vp, ok1 := ipTextFlag(valuePresent)
		ap, ok2 := ipTextFlag(automaticPresent)
		lk, ok3 := ipTextFlag(locked)
		ch, ok4 := ipTextFlag(changed)
		ef, ok5 := ipTextFlag(effective)
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || ordinal != int64(len(fields)) || int64(len(fields)) >= count {
			return value, digest, ErrResolveRequired
		}
		fields = append(fields, ipTextEditFieldResult{Field: fieldmeta.TextField(name),
			Change: fieldmeta.TextChange{State: fieldmeta.TextState{
				Effective: fieldmeta.Text{Value: fieldValue, Present: vp},
				Automatic: fieldmeta.Text{Value: automatic, Present: ap}, Locked: lk},
				Changed: ch, EffectiveChanged: ef}})
		anyChanged = anyChanged || ch
		anyEffective = anyEffective || ef
	}
	if err = rows.Err(); err != nil {
		return value, digest, err
	}
	if int64(len(fields)) != count || len(fields) != len(c.request.Fields) || anyChanged != changedBool || anyEffective != effectiveBool {
		return value, digest, ErrResolveRequired
	}
	for j, f := range fields {
		if f.Field != c.request.Fields[j].Field {
			return value, digest, ErrResolveRequired
		}
		if f.Change != c.after[j] {
			return value, digest, ErrResolveRequired
		}
	}
	if err = ipTextEditExpectNoNext(ctx, turn, ownerID, ref, count); err != nil {
		return value, digest, ErrResolveRequired
	}
	value = ipTextEditResult{OwnerID: ownerID, EntityID: s.EntityID, PersonID: s.PersonID,
		Revision: accepted, SubjectRevision: subject, AppliedMS: applied,
		History: EvidenceInteger{Value: history.Int64, Present: history.Valid}, Fields: fields}
	digest, err = ipTextEditResultDigest(ref.Identity, ref.RequestDigest, value)
	if err != nil {
		return ipTextEditResult{}, ResultDigest{}, errors.Join(ErrResolveRequired, err)
	}
	var saved ResultDigest
	copy(saved[:], result)
	if saved != digest {
		return ipTextEditResult{}, ResultDigest{}, ErrResolveRequired
	}
	return value, digest, nil
}

func ipTextEditExpectNoNext(ctx context.Context, turn *commandTurn, ownerID int64, ref ReceiptReference, count int64) error {
	rows, err := ipTextRows(ctx, turn, ipTextEditNoNext, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence, count)
	if err != nil {
		return err
	}
	var n int64
	err = ipTextOne(rows, func() error { return rows.Scan(&n) })
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrResolveRequired
}

func ipTextEditWriteFresh(ctx context.Context, turn *commandTurn, c *ipTextEditCommand, entry *ipCommandEntry, appliedMS int64) (out ipTextEditApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid || entry == nil || entry.decision != receiptFresh || appliedMS < 0 {
		return out, ErrInvalid
	}
	s := c.request.Source
	entity, person := ipTextEditNamespaces(s)
	target := ipTextEditTarget(s)
	revision := int64(0)
	ownerID := entry.ownerID
	if ownerID == 0 {
		if s.OwnerRevision != 1 || entry.owner.lastSequence != 0 || entry.owner.acknowledgedSequence != 0 {
			return out, ErrConflict
		}
		rows, e := ipTextRows(ctx, turn, ipTextEditOwnerInsert, entity, person, s.Kind, c.identity.Incarnation[:])
		if e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if e = ipTextOne(rows, func() error { return rows.Scan(&ownerID) }); e != nil || ownerID <= 0 {
			return out, errors.Join(ErrResolveRequired, e)
		}
		revision = 1
	} else {
		rows, e := ipTextRows(ctx, turn, ipTextEditOwner, ownerID, s.Kind, entity, person)
		if e != nil {
			return out, e
		}
		if e = ipTextOne(rows, func() error { return rows.Scan(&revision) }); e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if revision != s.OwnerRevision {
			return out, ErrConflict
		}
	}
	for j, f := range c.request.Fields {
		state, found, e := ipTextEditFieldState(ctx, turn, s, f.Field)
		if e != nil {
			return out, e
		}
		catalog, e := ipTextEditCatalogState(ctx, turn, s, f.Field)
		if e != nil {
			return out, e
		}
		if !ipTextEditBeforeMatches(f.Before, state, found, catalog) {
			return out, ErrConflict
		}
		if !c.after[j].Changed {
			continue
		}
		if found {
			if e = ipTextExpectOne(ctx, turn, ipTextEditFieldUpdate,
				c.after[j].State.Effective.Value, c.after[j].State.Automatic.Value,
				ipTextBool(c.after[j].State.Effective.Present), ipTextBool(c.after[j].State.Automatic.Present),
				ipTextBool(c.after[j].State.Locked), s.Actor, ipTextEditStamp(appliedMS),
				s.Kind, target, string(f.Field),
				f.Before.Effective.Value, f.Before.Automatic.Value,
				ipTextBool(f.Before.Effective.Present), ipTextBool(f.Before.Automatic.Present), ipTextBool(f.Before.Locked)); e != nil {
				return out, e
			}
		} else if e = ipTextExecOne(ctx, turn, ipTextEditFieldInsert,
			s.Kind, target, string(f.Field), c.after[j].State.Effective.Value, c.after[j].State.Automatic.Value,
			ipTextBool(c.after[j].State.Effective.Present), ipTextBool(c.after[j].State.Automatic.Present),
			ipTextBool(c.after[j].State.Locked), s.Actor, ipTextEditStamp(appliedMS)); e != nil {
			return out, e
		}
		if !c.after[j].EffectiveChanged {
			continue
		}
		value := c.after[j].State.Effective.Value
		switch {
		case s.PersonID > 0:
			e = ipTextExpectOne(ctx, turn, ipTextEditPersonUpdate, value, s.PersonID, f.Before.Effective.Value)
		case f.Field == ipTextAuthorField:
			e = ipTextEditApplyAuthor(ctx, turn, s.EntityID, value, f.Before.Effective.Value)
		case ipTextEditKeyField(f.Field):
			// Applied once after the cohort so title/sort/language land together.
		default:
			e = ipTextExpectOne(ctx, turn, ipTextEditScalarUpdate,
				string(f.Field), value, string(f.Field), value, string(f.Field), value,
				string(f.Field), value, string(f.Field), value, string(f.Field), value,
				s.EntityID, string(f.Field), f.Before.Effective.Value)
		}
		if e != nil {
			return out, e
		}
	}
	if c.projectionChanged {
		p := c.projection
		if err = ipTextExpectOne(ctx, turn, ipTextEditProjectionUpdate,
			p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles, s.EntityID); err != nil {
			return out, err
		}
	}
	history := EvidenceInteger{}
	if c.changed {
		if s.PersonID > 0 {
			err = ipTextExpectOne(ctx, turn, ipTextEditPersonAdvance, s.PersonID, s.SubjectRevision)
		} else {
			err = ipTextExpectOne(ctx, turn, ipTextEditSubjectAdvance, s.EntityID, s.LibraryID, s.MetadataIncarnation[:], s.SubjectRevision, s.MetadataRevision)
		}
		if err != nil {
			return out, err
		}
		history, err = ipTextEditWriteHistory(ctx, turn, c, target, appliedMS)
		if err != nil {
			return out, err
		}
	}
	nextOwner, nextSubject := s.OwnerRevision, s.SubjectRevision
	if c.changed {
		nextOwner, nextSubject = nextOwner+1, nextSubject+1
	}
	value := c.resultValue(ownerID, nextOwner, nextSubject, appliedMS, history)
	digest, err := ipTextEditResultDigest(c.identity, c.digest, value)
	if err != nil {
		return out, err
	}
	savedHistory := any(nil)
	if history.Present {
		savedHistory = history.Value
	}
	if err = ipTextExpectOne(ctx, turn, ipTextEditOwnerAdvance, nextOwner, c.identity.Sequence, ownerID,
		s.Kind, entity, person, c.identity.Incarnation[:], revision, entry.owner.lastSequence, entry.owner.acknowledgedSequence); err != nil {
		return out, err
	}
	if err = ipTextExecOne(ctx, turn, ipTextEditReceiptInsert, ownerID, c.identity.Incarnation[:], c.identity.Sequence,
		c.identity.OperationID[:], c.digest[:], appliedMS, digest[:], int64(len(value.Fields)),
		nextOwner, nextSubject, ipTextBool(c.changed), ipTextBool(c.effective), savedHistory); err != nil {
		return out, err
	}
	for j, f := range value.Fields {
		if err = ipTextExecOne(ctx, turn, ipTextEditReceiptFieldWrite, ownerID, c.identity.Incarnation[:], c.identity.Sequence,
			int64(j), string(f.Field), f.Change.State.Effective.Value, ipTextBool(f.Change.State.Effective.Present),
			f.Change.State.Automatic.Value, ipTextBool(f.Change.State.Automatic.Present),
			ipTextBool(f.Change.State.Locked), ipTextBool(f.Change.Changed), ipTextBool(f.Change.EffectiveChanged)); err != nil {
			return out, err
		}
	}
	read, readDigest, err := ipTextEditReadSaved(ctx, turn, ownerID, c)
	if err != nil || readDigest != digest || !ipTextEditResultEqual(read, value) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.value, out.digest = read, readDigest
	return out, nil
}

func ipTextEditStamp(appliedMS int64) string {
	return time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
}

func ipTextEditApplyAuthor(ctx context.Context, turn *commandTurn, entity int64, value, before string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	facts, err := ipTextEditReadAuthor(ctx, turn, entity)
	if err != nil {
		return err
	}
	if facts.Author != before {
		return ErrConflict
	}
	reading, key := any(nil), any(nil)
	if facts.Reading.Valid {
		reading = facts.Reading.String
	}
	if facts.Key.Valid {
		key = facts.Key.String
	}
	return ipTextExpectOne(ctx, turn, ipTextEditAuthorUpdate, value, value, ipTextAuthorKey(value), entity, before, reading, key)
}

func ipTextEditWriteHistory(ctx context.Context, turn *commandTurn, c *ipTextEditCommand, target, appliedMS int64) (out EvidenceInteger, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	s := c.request.Source
	names := make([]string, 0, len(c.request.Fields))
	for j, f := range c.request.Fields {
		if c.after[j].Changed {
			names = append(names, string(f.Field))
		}
	}
	audit, err := json.Marshal(struct{ Fields []string }{names})
	if err != nil {
		return out, err
	}
	stamp := ipTextEditStamp(appliedMS)
	rows, err := ipTextRows(ctx, turn, ipTextEditHistoryInsert, s.Kind, target,
		strconv.FormatInt(s.OwnerRevision, 10), strconv.FormatInt(s.OwnerRevision+1, 10),
		ipTextEditCommandName(IPFieldEdit), s.Actor, stamp, string(audit), string(audit))
	if err != nil {
		return out, err
	}
	var id int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&id) }); err != nil || id <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	for j, f := range c.request.Fields {
		if !c.after[j].Changed {
			continue
		}
		b, a := f.Before, c.after[j].State
		if err = ipTextExecOne(ctx, turn, ipTextEditHistoryField, id, string(f.Field),
			b.Effective.Value, ipTextBool(b.Effective.Present), b.Automatic.Value, ipTextBool(b.Automatic.Present), ipTextBool(b.Locked),
			a.Effective.Value, ipTextBool(a.Effective.Present), a.Automatic.Value, ipTextBool(a.Automatic.Present), ipTextBool(a.Locked)); err != nil {
			return out, err
		}
	}
	return EvidenceInteger{Value: id, Present: true}, nil
}
