package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

func ipBundleNamespaces(s ipTextBundleSource) (entity, person any) {
	if s.EntityID > 0 {
		entity = s.EntityID
	} else {
		person = s.PersonID
	}
	return
}
func ipBundleResultFields(c ipTextBundleCommand) []ipTextBundleFieldResult {
	out := make([]ipTextBundleFieldResult, len(c.after))
	for j, a := range c.after {
		out[j] = ipTextBundleFieldResult{c.request.Fields[j].Field, a}
	}
	return out
}

// Atomic finite field bundle. Same-turn native field/source CAS, normalized
// history, owner revision and all exact saved field results succeed together.
// This private component does not issue current authority/resource grants,
// mutate source provenance into owner provenance, publish events/search or claim
// COMMIT. The outer closed driver must compose those required effects first.
func applyIPTextBundleBody(ctx context.Context, t *commandTurn, c ipTextBundleCommand, appliedMS int64) (out ipTextBundleNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	q := c.request
	s := q.Source
	i := q.Identity
	if t == nil || t.transaction == nil || appliedMS < 0 {
		return out, ErrInvalid
	}
	checked, e := newIPTextBundleCommand(q)
	if e != nil || checked.digest != c.digest || checked.projection != c.projection || checked.changed != c.changed || checked.effective != c.effective || len(checked.after) != len(c.after) {
		return out, ErrInvalid
	}
	for j := range checked.after {
		if checked.after[j] != c.after[j] {
			return out, ErrInvalid
		}
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	entity, person := ipBundleNamespaces(s)
	var owner receiptOwnerFacts
	var revision int64
	var incarnation []byte
	rows, e := ipTextRows(ctx, t, ipBundleOwner, s.OwnerID, entity, person, s.Kind)
	if e != nil {
		return out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&revision, &incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrOwnerUnavailable
	}
	if e != nil {
		return out, e
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)
	var found receiptFacts
	var operation, request, result []byte
	var kind, accepted, subject, at, changed, effective, count, bundle int64
	var history sql.NullInt64
	rows, e = ipTextRows(ctx, t, ipBundleReceipt, s.OwnerID, i.Incarnation[:], i.Sequence)
	if e != nil {
		return out, e
	}
	e = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &kind, &accepted, &subject, &at, &changed, &effective, &history, &count, &bundle)
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
		found.kind = CommandID(kind)
		found.resultCount = count
		copy(found.resultDigest[:], result)
	}
	decision, e := classifyReceipt(receiptIdentity{i, c.digest}, q.Kind, owner, found)
	if e != nil {
		return out, e
	}
	out.decision = decision
	if decision == receiptRetired {
		return out, nil
	}
	target := ipTextBundleTarget(s)
	nextOwner, nextSubject := s.OwnerRevision, s.SubjectRevision
	if c.changed {
		nextOwner++
		nextSubject++
	}
	if decision == receiptReplay {
		if bundle != 1 || count != int64(len(q.Fields)) || changed != ipTextBool(c.changed) || effective != ipTextBool(c.effective) || history.Valid != c.changed || accepted != nextOwner || subject != nextSubject || at < 0 {
			return ipTextBundleNative{}, ErrResolveRequired
		}
		out.value = ipTextBundleResult{s.OwnerID, s.EntityID, s.PersonID, accepted, subject, at, EvidenceInteger{history.Int64, history.Valid}, ipBundleResultFields(c)}
		for j, f := range out.value.Fields {
			if err = ipTextExpectOne(ctx, t, ipBundleFieldReceipt, ipBundleFieldArgs(s.OwnerID, i, int64(j), f)...); err != nil {
				return ipTextBundleNative{}, ErrResolveRequired
			}
		}
		rows, e = ipTextRows(ctx, t, ipBundleNoNext, s.OwnerID, i.Incarnation[:], i.Sequence, int64(len(q.Fields)))
		if e != nil {
			return ipTextBundleNative{}, e
		}
		var n int64
		e = ipTextOne(rows, func() error { return rows.Scan(&n) })
		if !errors.Is(e, sql.ErrNoRows) {
			return ipTextBundleNative{}, errors.Join(ErrResolveRequired, e)
		}
		out.digest, e = canonicalResultDigest(i, c.digest, ipTextBundleSavedResult{q.Kind, out.value})
		if e != nil || [32]byte(out.digest) != found.resultDigest {
			return ipTextBundleNative{}, ErrResolveRequired
		}
		return out, nil
	}
	if decision != receiptFresh || revision != s.OwnerRevision {
		return ipTextBundleNative{}, ErrConflict
	}
	if s.EntityID > 0 {
		err = ipTextExpectOne(ctx, t, ipTextSource, s.EntityID, s.LibraryID, s.MetadataIncarnation[:], s.SubjectRevision, s.MetadataRevision)
	} else {
		err = ipTextExpectOne(ctx, t, ipBundlePersonSource, s.PersonID, s.SubjectRevision)
	}
	if err != nil {
		return ipTextBundleNative{}, err
	}
	// Verify the complete before-cohort before making any requested field write.
	for j, f := range q.Fields {
		b := f.Before
		if err = ipTextExpectOne(ctx, t, ipTextField, s.Kind, target, string(f.Field), b.Effective.Value, b.Automatic.Value, ipTextBool(b.Effective.Present), ipTextBool(b.Automatic.Present), ipTextBool(b.Locked)); err != nil {
			return ipTextBundleNative{}, err
		}
		if !c.after[j].EffectiveChanged {
			continue
		}
		if s.PersonID > 0 {
			err = ipTextExpectOne(ctx, t, ipBundlePersonCurrent, s.PersonID, b.Effective.Value)
		} else if !ipTextKeyField(f.Field) {
			err = ipTextExpectOne(ctx, t, ipTextScalarCurrent, s.EntityID, string(f.Field), b.Effective.Value)
		}
		if err != nil {
			return ipTextBundleNative{}, err
		}
	}
	if c.projection != (ipTextProjection{}) {
		p := s.Projection
		if err = ipTextExpectOne(ctx, t, ipTextProjectionCurrent, s.EntityID, p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles); err != nil {
			return ipTextBundleNative{}, err
		}
	}
	var historyID EvidenceInteger
	if c.changed {
		stamp := time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
		for j, f := range q.Fields {
			a := c.after[j]
			if !a.Changed {
				continue
			}
			if err = ipTextExpectOne(ctx, t, ipTextFieldUpdate, a.State.Effective.Value, ipTextBool(a.State.Effective.Present), ipTextBool(a.State.Locked), s.Actor, stamp, s.Kind, target, string(f.Field), ipTextBool(f.Before.Locked)); err != nil {
				return ipTextBundleNative{}, err
			}
			if !a.EffectiveChanged {
				continue
			}
			if s.PersonID > 0 {
				err = ipTextExpectOne(ctx, t, ipBundlePersonUpdate, a.State.Effective.Value, s.PersonID)
			} else if !ipTextKeyField(f.Field) {
				err = ipTextApplyProjection(ctx, t, ipTextLockRequest{EntityID: s.EntityID, Field: f.Field}, ipTextProjection{}, a.State.Effective.Value)
			}
			if err != nil {
				return ipTextBundleNative{}, err
			}
		}
		if c.projection != (ipTextProjection{}) {
			p := c.projection
			if err = ipTextExpectOne(ctx, t, ipTextProjectionUpdate, p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles, s.EntityID); err != nil {
				return ipTextBundleNative{}, err
			}
		}
		if s.EntityID > 0 {
			err = ipTextExpectOne(ctx, t, ipTextSubjectAdvance, s.EntityID, s.LibraryID, s.MetadataIncarnation[:], s.SubjectRevision, s.MetadataRevision)
		} else {
			err = ipTextExpectOne(ctx, t, ipBundlePersonAdvance, s.PersonID, s.SubjectRevision)
		}
		if err != nil {
			return ipTextBundleNative{}, err
		}
		// The audit names the requested fields; all exact before/after values and
		// locks are retained once in normalized history children, never JSON state.
		names := make([]string, len(q.Fields))
		for j, f := range q.Fields {
			names[j] = string(f.Field)
		}
		audit, e := json.Marshal(struct{ Fields []string }{names})
		if e != nil {
			return ipTextBundleNative{}, e
		}
		var id int64
		rows, e = ipTextRows(ctx, t, ipTextHistoryInsert, s.Kind, target, strconv.FormatInt(s.OwnerRevision, 10), strconv.FormatInt(nextOwner, 10), ipTextCommandName(q.Kind), s.Actor, stamp, string(audit), string(audit))
		if e != nil {
			return ipTextBundleNative{}, e
		}
		e = ipTextOne(rows, func() error { return rows.Scan(&id) })
		if e != nil || id <= 0 {
			return ipTextBundleNative{}, errors.Join(ErrResolveRequired, e)
		}
		historyID = EvidenceInteger{id, true}
		for j, f := range q.Fields {
			b, a := f.Before, c.after[j].State
			if err = ipTextExecOne(ctx, t, ipTextHistoryField, id, string(f.Field), b.Effective.Value, ipTextBool(b.Effective.Present), b.Automatic.Value, ipTextBool(b.Automatic.Present), ipTextBool(b.Locked), a.Effective.Value, ipTextBool(a.Effective.Present), a.Automatic.Value, ipTextBool(a.Automatic.Present), ipTextBool(a.Locked)); err != nil {
				return ipTextBundleNative{}, err
			}
		}
	}
	out.value = ipTextBundleResult{s.OwnerID, s.EntityID, s.PersonID, nextOwner, nextSubject, appliedMS, historyID, ipBundleResultFields(c)}
	out.digest, err = canonicalResultDigest(i, c.digest, ipTextBundleSavedResult{q.Kind, out.value})
	if err != nil {
		return ipTextBundleNative{}, err
	}
	if err = ipTextExpectOne(ctx, t, ipBundleOwnerAdvance, nextOwner, i.Sequence, s.OwnerID, entity, person, s.Kind, i.Incarnation[:], revision, owner.lastSequence, owner.acknowledgedSequence); err != nil {
		return ipTextBundleNative{}, err
	}
	var h any
	if historyID.Present {
		h = historyID.Value
	}
	if err = ipTextExecOne(ctx, t, ipBundleSave, s.OwnerID, i.Incarnation[:], i.Sequence, i.OperationID[:], c.digest[:], appliedMS, out.digest[:], ipTextCommandName(q.Kind), int64(len(q.Fields)), nextOwner, nextSubject, ipTextBool(c.changed), ipTextBool(c.effective), h); err != nil {
		return ipTextBundleNative{}, err
	}
	for j, f := range out.value.Fields {
		if err = ipTextExecOne(ctx, t, ipBundleSaveField, ipBundleFieldArgs(s.OwnerID, i, int64(j), f)...); err != nil {
			return ipTextBundleNative{}, err
		}
	}
	return out, t.checkLifetimes(ctx)
}
func ipBundleFieldArgs(owner int64, i CommandIdentity, ordinal int64, f ipTextBundleFieldResult) []any {
	c := f.Change
	s := c.State
	return []any{owner, i.Incarnation[:], i.Sequence, ordinal, string(f.Field), s.Effective.Value, s.Automatic.Value, ipTextBool(s.Effective.Present), ipTextBool(s.Automatic.Present), ipTextBool(s.Locked), ipTextBool(c.Changed), ipTextBool(c.EffectiveChanged)}
}

func applyIPTextBundle(ctx context.Context, t *commandTurn, c ipTextBundleCommand, appliedMS int64) (out ipTextBundleNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != c.request.Kind {
		return out, ErrInvalid
	}
	return applyIPTextBundleBody(ctx, t, c, appliedMS)
}
