package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

// A lock retains the accepted effective value while it stops automatic
// application; the automatic source itself keeps advancing beneath the lock.
// A restore substitutes the retained automatic source for the effective value
// and clears the lock. Both are single-field typed commands under the shared IP
// governed entry, so current authority precedes original receipt disclosure and
// an original saved result replays without fresh source or lease checks.
// Neither issues a public Applied capability nor any provider-worker authority.
type ipTextLockRequest struct {
	Kind   CommandID
	Source ipTextEditSource
	Field  fieldmeta.TextField
	Before fieldmeta.TextState
}

type ipTextLockCommand struct {
	identity CommandIdentity
	request  ipTextLockRequest
	digest   RequestDigest
	after    fieldmeta.TextChange
	valid    bool
}

type ipTextLockValue struct {
	OwnerID, EntityID, PersonID, Revision, SubjectRevision, AppliedMS int64
	History                                                           EvidenceInteger
	Field                                                             fieldmeta.TextField
	Change                                                            fieldmeta.TextChange
}

type ipTextLockSavedResult struct {
	Kind  CommandID
	Value ipTextLockValue
}

type ipTextLockCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipTextLockValue
	Digest  ResultDigest
}

type ipTextLockApplied struct {
	value  ipTextLockValue
	digest ResultDigest
}

func (c *ipTextLockCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipTextLockCommand")
}

func (ipTextLockCommandResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipTextLockCommandResult")
}

func (c *ipTextLockCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipTextLockAction(kind CommandID) (fieldmeta.EditAction, bool) {
	switch kind {
	case IPFieldLock:
		return fieldmeta.LockText, true
	case IPFieldRestore:
		return fieldmeta.RestoreAutomatic, true
	}
	return 0, false
}

func ipTextLockEntryRequest(c *ipTextLockCommand) ipEntryRequest {
	s := c.request.Source
	kind, library, key := s.TargetKind, s.LibraryID, ""
	if s.PersonID > 0 {
		kind, library = 0, 0
	} else {
		key = s.LibraryKey
	}
	return ipEntryRequest{purpose: c.request.Kind, action: ipEntryApply, reference: c.reference(),
		targetID: ipTextEditTarget(s), targetKind: kind, libraryID: library, libraryKey: key}
}

// ipTextLockSourceShape mirrors the owner-edit source shape. The accepted
// projection is not an input to a single-field lock or restore: both read the
// current catalogue snapshot under the owner CAS, so a caller-supplied
// projection would be unused digest filler.
func ipTextLockSourceShape(s ipTextEditSource) bool {
	if s.OwnerRevision <= 0 || s.OwnerRevision == math.MaxInt64 || (s.EntityID > 0) == (s.PersonID > 0) ||
		s.EntityID < 0 || s.PersonID < 0 || len(s.Actor) == 0 || len(s.Actor) > ipTextActorMax || !utf8.ValidString(s.Actor) {
		return false
	}
	if s.PersonID > 0 {
		if s.Kind != "person" || s.TargetKind != 0 || s.LibraryID != 0 || s.LibraryKey != "" ||
			s.MetadataRevision != 0 || s.SourceRevision != 0 || s.IdentityRevision != 0 ||
			s.MetadataIncarnation != [16]byte{} || s.Projection != (ipTextProjection{}) ||
			s.SubjectRevision <= 0 || s.SubjectRevision == math.MaxInt64 ||
			len(s.PersonToken) == 0 || len(s.PersonToken) > 128 || !utf8.ValidString(s.PersonToken) {
			return false
		}
		return true
	}
	return ipTextEditEntityKind(s.Kind) && s.TargetKind > 0 && ipEntryOwnerKind(s.TargetKind) == s.Kind &&
		s.LibraryID > 0 && len(s.LibraryKey) > 0 && len(s.LibraryKey) <= 128 && utf8.ValidString(s.LibraryKey) &&
		s.PersonToken == "" && s.Projection == (ipTextProjection{}) &&
		s.SubjectRevision > 0 && s.SubjectRevision < math.MaxInt64 &&
		s.MetadataRevision > 0 && s.MetadataRevision < math.MaxInt64 &&
		s.SourceRevision > 0 && s.SourceRevision < math.MaxInt64 &&
		s.IdentityRevision > 0 && s.IdentityRevision < math.MaxInt64 &&
		s.MetadataIncarnation != [16]byte{}
}

// newIPTextFieldLockCommand freezes the complete original request. It runs
// under W preparation custody before any hold and performs no SQL. The author
// pair (value plus reading-identity key) belongs to the multi-field edit body
// until a lock/restore variant can move both facts atomically.
func newIPTextFieldLockCommand(identity CommandIdentity, in ipTextLockRequest) (*ipTextLockCommand, error) {
	action, ok := ipTextLockAction(in.Kind)
	if !ok || !ipTextEditIdentityValid(identity) || !ipTextLockSourceShape(in.Source) ||
		!ipTextEditFieldAllowed(in.Source.Kind, in.Field) || in.Field == ipTextAuthorField ||
		!ipTextEditValidSource(in.Before.Effective) || !ipTextEditValidSource(in.Before.Automatic) ||
		(in.Before.Locked && !in.Before.Effective.Present) {
		return nil, ErrInvalid
	}
	change, err := fieldmeta.PrepareTextEdit(in.Field, in.Before, fieldmeta.TextEdit{Action: action})
	if err != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(in.Kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	s := in.Source
	s.Kind = strings.Clone(s.Kind)
	s.Actor = strings.Clone(s.Actor)
	s.PersonToken = strings.Clone(s.PersonToken)
	s.LibraryKey = strings.Clone(s.LibraryKey)
	in.Source = s
	in.Field = fieldmeta.TextField(strings.Clone(string(in.Field)))
	in.Before.Effective.Value = strings.Clone(in.Before.Effective.Value)
	in.Before.Automatic.Value = strings.Clone(in.Before.Automatic.Value)
	return &ipTextLockCommand{identity: identity, request: in, digest: digest, after: change, valid: true}, nil
}

// applyIPTextFieldLock is the closed command body. A fresh request runs the one
// owner/field/history/receipt cohort; a saved receipt returns its exact original
// result even after the source, projection or authority-adjacent lease moved.
func applyIPTextFieldLock(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget, c *ipTextLockCommand) (out ipTextLockCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipTextLockCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != c.request.Kind {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(c.request.Kind, 1, c.identity, c.request)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	entry, err := enterIPCommand(ctx, turn, ipTextLockEntryRequest(c), p, lifetime, b)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch entry.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		value, digest, e := ipTextLockReadSaved(ctx, turn, entry.ownerID, c)
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
	app, err := ipTextFieldLockWriteFresh(ctx, turn, c, entry, appliedMS)
	if err != nil {
		return zero, err
	}
	out.Value, out.Digest, out.Outcome.State = app.value, app.digest, Applied
	return out, nil
}

// ipTextLockReadSaved reconstructs the COMPLETE original single-field result
// from the saved receipt and proves its canonical digest against the original
// request's expected change.
func ipTextLockReadSaved(ctx context.Context, turn *commandTurn, ownerID int64, c *ipTextLockCommand) (value ipTextLockValue, digest ResultDigest, err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ownerID <= 0 || c == nil || !c.valid {
		return value, digest, ErrResolveRequired
	}
	ids := ipTextFieldMutationStatements(c.request.Kind)
	ref := c.reference()
	rows, err := ipTextRows(ctx, turn, ids.Receipt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return value, digest, err
	}
	var operation, request, result []byte
	var kind, field, effectiveValue, automaticValue string
	var count, accepted, subject, applied, valuePresent, automaticPresent, locked, changed, effective int64
	var history sql.NullInt64
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &kind, &count, &accepted, &subject, &applied,
			&field, &effectiveValue, &valuePresent, &automaticValue, &automaticPresent, &locked, &changed, &effective, &history)
	}); err != nil {
		return value, digest, errors.Join(ErrResolveRequired, err)
	}
	var savedRequest RequestDigest
	var savedOperation [16]byte
	copy(savedRequest[:], request)
	copy(savedOperation[:], operation)
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 || kind != ipTextEditCommandName(c.request.Kind) ||
		savedRequest != ref.RequestDigest || savedOperation != ref.Identity.OperationID ||
		count != 1 || accepted <= 0 || subject <= 0 || applied < 0 || field != string(c.request.Field) {
		return value, digest, ErrResolveRequired
	}
	vp, ok1 := ipTextFlag(valuePresent)
	ap, ok2 := ipTextFlag(automaticPresent)
	lk, ok3 := ipTextFlag(locked)
	ch, ok4 := ipTextFlag(changed)
	ef, ok5 := ipTextFlag(effective)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || history.Valid != ch {
		return value, digest, ErrResolveRequired
	}
	change := fieldmeta.TextChange{
		State: fieldmeta.TextState{Effective: fieldmeta.Text{Value: effectiveValue, Present: vp},
			Automatic: fieldmeta.Text{Value: automaticValue, Present: ap}, Locked: lk},
		Changed: ch, EffectiveChanged: ef}
	if c.request.Kind == IPFieldLock && !lk || c.request.Kind == IPFieldRestore && lk || change != c.after {
		return value, digest, ErrResolveRequired
	}
	s := c.request.Source
	value = ipTextLockValue{OwnerID: ownerID, EntityID: s.EntityID, PersonID: s.PersonID,
		Revision: accepted, SubjectRevision: subject, AppliedMS: applied,
		History: EvidenceInteger{Value: history.Int64, Present: history.Valid},
		Field:   c.request.Field, Change: change}
	digest, err = ipTextLockResultDigest(ref.Identity, ref.RequestDigest, c.request.Kind, value)
	if err != nil {
		return ipTextLockValue{}, ResultDigest{}, errors.Join(ErrResolveRequired, err)
	}
	var stored ResultDigest
	copy(stored[:], result)
	if stored != digest {
		return ipTextLockValue{}, ResultDigest{}, ErrResolveRequired
	}
	return value, digest, nil
}

func ipTextFieldLockFieldState(ctx context.Context, turn *commandTurn, ids ipTextFieldMutationSet, s ipTextEditSource, field fieldmeta.TextField) (state fieldmeta.TextState, found bool, err error) {
	rows, err := ipTextRows(ctx, turn, ids.Field, s.Kind, ipTextEditTarget(s), string(field))
	if err != nil {
		return state, false, err
	}
	var value, automatic string
	var valuePresent, automaticPresent, locked int64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&value, &automatic, &valuePresent, &automaticPresent, &locked)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	vp, ok1 := ipTextFlag(valuePresent)
	ap, ok2 := ipTextFlag(automaticPresent)
	lk, ok3 := ipTextFlag(locked)
	if !ok1 || !ok2 || !ok3 {
		return state, false, ErrResolveRequired
	}
	state = fieldmeta.TextState{Effective: fieldmeta.Text{Value: value, Present: vp},
		Automatic: fieldmeta.Text{Value: automatic, Present: ap}, Locked: lk}
	return state, true, nil
}

type ipTextLockCatalog struct {
	Value      fieldmeta.Text
	Projection ipTextProjection
}

// ipTextFieldLockCatalogState reads the accepted effective column and the exact
// sort projection for the one target. Presence cannot be recovered from a
// NOT NULL text column: a non-empty value is present.
func ipTextFieldLockCatalogState(ctx context.Context, turn *commandTurn, ids ipTextFieldMutationSet, s ipTextEditSource, field fieldmeta.TextField) (out ipTextLockCatalog, err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if s.PersonID > 0 {
		rows, err := ipTextRows(ctx, turn, ids.PersonCatalog, s.PersonID)
		if err != nil {
			return out, err
		}
		var biography string
		if err = ipTextOne(rows, func() error { return rows.Scan(&biography) }); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		out.Value = fieldmeta.Text{Value: biography, Present: biography != ""}
		return out, nil
	}
	rows, err := ipTextRows(ctx, turn, ids.Catalog, s.EntityID, s.TargetKind, s.LibraryID)
	if err != nil {
		return out, err
	}
	var title, sortTitle, language, overview, originalTitle, tagline, studio, network, country string
	var key, head, keepKey, keepHead string
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&title, &sortTitle, &language, &overview, &originalTitle, &tagline,
			&studio, &network, &country, &key, &head, &keepKey, &keepHead)
	}); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	out.Projection = ipTextProjection{title, sortTitle, language, key, head, keepKey, keepHead}
	switch field {
	case fieldmeta.Title:
		out.Value = fieldmeta.Text{Value: title, Present: title != ""}
	case fieldmeta.SortTitle:
		out.Value = fieldmeta.Text{Value: sortTitle, Present: sortTitle != ""}
	case fieldmeta.MetadataLanguage:
		out.Value = fieldmeta.Text{Value: language, Present: language != ""}
	case fieldmeta.Description:
		out.Value = fieldmeta.Text{Value: overview, Present: overview != ""}
	case fieldmeta.OriginalTitle:
		out.Value = fieldmeta.Text{Value: originalTitle, Present: originalTitle != ""}
	case fieldmeta.Tagline:
		out.Value = fieldmeta.Text{Value: tagline, Present: tagline != ""}
	case fieldmeta.Studio:
		out.Value = fieldmeta.Text{Value: studio, Present: studio != ""}
	case fieldmeta.Network:
		out.Value = fieldmeta.Text{Value: network, Present: network != ""}
	case fieldmeta.Country:
		out.Value = fieldmeta.Text{Value: country, Present: country != ""}
	default:
		return out, ErrInvalid
	}
	return out, nil
}

// ipTextFieldLockCatalogUpdate applies the one effective-value change to the
// accepted catalogue column. A lock never reaches this path: it changes only the
// lock bit. Key fields move the whole filename sort projection atomically.
func ipTextFieldLockCatalogUpdate(ctx context.Context, turn *commandTurn, ids ipTextFieldMutationSet, s ipTextEditSource, catalog ipTextLockCatalog, field fieldmeta.TextField, value string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if s.PersonID > 0 {
		return ipTextExpectOne(ctx, turn, ids.PersonUpdate, value, s.PersonID, catalog.Value.Value)
	}
	if ipTextEditKeyField(field) {
		p := catalog.Projection
		switch field {
		case fieldmeta.Title:
			p.Title = value
		case fieldmeta.SortTitle:
			p.SortTitle = value
		case fieldmeta.MetadataLanguage:
			p.Language = value
		}
		next, e := ProjectFilenameMetadata(p.Title, p.SortTitle, p.Language)
		if e != nil {
			return ErrInvalid
		}
		return ipTextExpectOne(ctx, turn, ids.ProjectionUpdate,
			next.Title, next.SortTitle, next.MetadataLanguage, next.SortKey, next.SortHead,
			next.SortKeyKeepArticles, next.SortHeadKeepArticles, s.EntityID,
			catalog.Projection.Title, catalog.Projection.SortTitle, catalog.Projection.Language,
			catalog.Projection.SortKey, catalog.Projection.SortHead,
			catalog.Projection.SortKeyKeepArticles, catalog.Projection.SortHeadKeepArticles)
	}
	return ipTextExpectOne(ctx, turn, ids.ScalarUpdate,
		string(field), value, string(field), value, string(field), value,
		string(field), value, string(field), value, string(field), value,
		s.EntityID, string(field), catalog.Value.Value)
}

func ipTextFieldLockHistory(ctx context.Context, turn *commandTurn, ids ipTextFieldMutationSet, c *ipTextLockCommand, target, appliedMS int64) (out EvidenceInteger, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	s := c.request.Source
	audit, err := json.Marshal(struct{ Fields []string }{[]string{string(c.request.Field)}})
	if err != nil {
		return out, err
	}
	stamp := ipTextEditStamp(appliedMS)
	rows, err := ipTextRows(ctx, turn, ids.HistoryInsert, s.Kind, target,
		strconv.FormatInt(s.OwnerRevision, 10), strconv.FormatInt(s.OwnerRevision+1, 10),
		ipTextEditCommandName(c.request.Kind), s.Actor, stamp, string(audit), string(audit))
	if err != nil {
		return out, err
	}
	var id int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&id) }); err != nil || id <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	b, a := c.request.Before, c.after.State
	if err = ipTextExecOne(ctx, turn, ids.HistoryField, id, string(c.request.Field),
		b.Effective.Value, ipTextBool(b.Effective.Present), b.Automatic.Value, ipTextBool(b.Automatic.Present), ipTextBool(b.Locked),
		a.Effective.Value, ipTextBool(a.Effective.Present), a.Automatic.Value, ipTextBool(a.Automatic.Present), ipTextBool(a.Locked)); err != nil {
		return out, err
	}
	return EvidenceInteger{Value: id, Present: true}, nil
}

func ipTextFieldLockWriteFresh(ctx context.Context, turn *commandTurn, c *ipTextLockCommand, entry *ipCommandEntry, appliedMS int64) (out ipTextLockApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid || entry == nil || entry.decision != receiptFresh || appliedMS < 0 {
		return out, ErrInvalid
	}
	s := c.request.Source
	ids := ipTextFieldMutationStatements(c.request.Kind)
	entity, person := ipTextEditNamespaces(s)
	ownerID, revision := entry.ownerID, int64(0)
	if ownerID == 0 {
		if s.OwnerRevision != 1 || entry.owner.lastSequence != 0 || entry.owner.acknowledgedSequence != 0 {
			return out, ErrConflict
		}
		rows, e := ipTextRows(ctx, turn, ids.OwnerInsert, entity, person, s.Kind, c.identity.Incarnation[:])
		if e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if e = ipTextOne(rows, func() error { return rows.Scan(&ownerID) }); e != nil || ownerID <= 0 {
			return out, errors.Join(ErrResolveRequired, e)
		}
		revision = 1
	} else {
		rows, e := ipTextRows(ctx, turn, ids.Owner, ownerID, s.Kind, entity, person)
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
	catalog, err := ipTextFieldLockCatalogState(ctx, turn, ids, s, c.request.Field)
	if err != nil {
		return out, err
	}
	current, found, err := ipTextFieldLockFieldState(ctx, turn, ids, s, c.request.Field)
	if err != nil {
		return out, err
	}
	if found {
		if c.request.Before != current {
			return out, ErrConflict
		}
	} else if !ipTextEditBeforeMatches(c.request.Before, fieldmeta.TextState{}, false, catalog.Value) {
		return out, ErrConflict
	}
	if c.after.Changed {
		if found {
			if err = ipTextExpectOne(ctx, turn, ids.FieldUpdate,
				c.after.State.Effective.Value, c.after.State.Automatic.Value,
				ipTextBool(c.after.State.Effective.Present), ipTextBool(c.after.State.Automatic.Present),
				ipTextBool(c.after.State.Locked), s.Actor, ipTextEditStamp(appliedMS),
				s.Kind, ipTextEditTarget(s), string(c.request.Field),
				c.request.Before.Effective.Value, c.request.Before.Automatic.Value,
				ipTextBool(c.request.Before.Effective.Present), ipTextBool(c.request.Before.Automatic.Present),
				ipTextBool(c.request.Before.Locked)); err != nil {
				return out, err
			}
		} else if err = ipTextExecOne(ctx, turn, ids.FieldInsert,
			s.Kind, ipTextEditTarget(s), string(c.request.Field), c.after.State.Effective.Value,
			c.after.State.Automatic.Value, ipTextBool(c.after.State.Effective.Present),
			ipTextBool(c.after.State.Automatic.Present), ipTextBool(c.after.State.Locked),
			s.Actor, ipTextEditStamp(appliedMS)); err != nil {
			return out, err
		}
	}
	if c.after.EffectiveChanged {
		if err = ipTextFieldLockCatalogUpdate(ctx, turn, ids, s, catalog, c.request.Field, c.after.State.Effective.Value); err != nil {
			return out, err
		}
		if s.PersonID > 0 {
			err = ipTextExpectOne(ctx, turn, ids.PersonAdvance, s.PersonID, s.SubjectRevision)
		} else {
			err = ipTextExpectOne(ctx, turn, ids.SubjectAdvance, s.EntityID, s.LibraryID,
				s.MetadataIncarnation[:], s.SubjectRevision, s.MetadataRevision)
		}
		if err != nil {
			return out, err
		}
	}
	history := EvidenceInteger{}
	if c.after.Changed {
		history, err = ipTextFieldLockHistory(ctx, turn, ids, c, ipTextEditTarget(s), appliedMS)
		if err != nil {
			return out, err
		}
	}
	nextOwner, nextSubject := s.OwnerRevision, s.SubjectRevision
	if c.after.Changed {
		nextOwner++
	}
	if c.after.EffectiveChanged {
		nextSubject++
	}
	value := ipTextLockValue{OwnerID: ownerID, EntityID: s.EntityID, PersonID: s.PersonID,
		Revision: nextOwner, SubjectRevision: nextSubject, AppliedMS: appliedMS,
		History: history, Field: c.request.Field, Change: c.after}
	digest, err := ipTextLockResultDigest(c.identity, c.digest, c.request.Kind, value)
	if err != nil {
		return out, err
	}
	savedHistory := any(nil)
	if history.Present {
		savedHistory = history.Value
	}
	if err = ipTextExpectOne(ctx, turn, ids.OwnerAdvance, nextOwner, c.identity.Sequence, ownerID,
		s.Kind, entity, person, c.identity.Incarnation[:], revision,
		entry.owner.lastSequence, entry.owner.acknowledgedSequence); err != nil {
		return out, err
	}
	if err = ipTextExecOne(ctx, turn, ids.ReceiptInsert, ownerID, c.identity.Incarnation[:], c.identity.Sequence,
		c.identity.OperationID[:], c.digest[:], appliedMS, digest[:], ipTextEditCommandName(c.request.Kind),
		string(c.request.Field), nextOwner, nextSubject, value.Change.State.Effective.Value,
		ipTextBool(value.Change.State.Effective.Present), value.Change.State.Automatic.Value,
		ipTextBool(value.Change.State.Automatic.Present), ipTextBool(value.Change.State.Locked),
		ipTextBool(value.Change.Changed), ipTextBool(value.Change.EffectiveChanged), savedHistory); err != nil {
		return out, err
	}
	read, readDigest, err := ipTextLockReadSaved(ctx, turn, ownerID, c)
	if err != nil || readDigest != digest || !ipTextLockResultEqual(read, value) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.value, out.digest = read, readDigest
	return out, nil
}

func ipTextLockResultEqual(a, b ipTextLockValue) bool {
	return a.OwnerID == b.OwnerID && a.EntityID == b.EntityID && a.PersonID == b.PersonID &&
		a.Revision == b.Revision && a.SubjectRevision == b.SubjectRevision && a.AppliedMS == b.AppliedMS &&
		a.History == b.History && a.Field == b.Field && a.Change == b.Change
}
