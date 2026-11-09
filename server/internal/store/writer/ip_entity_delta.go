package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// IP nonmovie entity provider typed delta replace 217 publishes a NEW sealed
// IP-06/IP-07 observation (and, for series/season, its complete IP-08 set) as
// the current accepted provider source for one catalogue entity, replacing the
// EXACT old accepted head the caller names. It is the replacement command for
// the IP domain: it requires the old publication, its selection revision and
// its upstream source proof keys, and it refuses a head that is no longer the
// current accepted one.
//
// The old source stays charged. The old publication, its evidence and its seal
// receipt are never mutated, and the delta proves the old seal receipt is still
// terminal with a live proof pin before its first write. Retention ends only
// through the bounded downstream retirement of IP-17; this command does not
// reclaim it.
//
// It copies IP-09's accept shape (authority first, original receipt before any
// fresh probe, complete native proof before the first write, typed replay from
// the saved receipt only) and consumes the NEW sealed observation through the
// same small helpers IP-09 uses. It never widens the EN FIRST movie purpose or
// copies credits per child.

// ipEntityDeltaRequest is the complete original replacement input. The
// observation tuple is IP-09's exact acceptance input for the NEW sealed
// source; the previous tuple names the exact old accepted head and its retained
// upstream proof keys.
type ipEntityDeltaRequest struct {
	observation           ipEntityAcceptRequest
	previousPublicationID string
	previousEvidenceID    string
	previousSetID         string
}

type ipEntityDeltaCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  ipEntityDeltaRequest
	valid    bool
}

type ipEntityDeltaCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEntityDeltaResult
	Digest  ResultDigest
}

type ipEntityDeltaReceipt struct {
	incarnation                []byte
	operation, request, result []byte
	sequence, acceptedRevision int64
	appliedMS, resultCount     int64
	resultBytes, terminal      int64
	subjectID                  int64
	kind, phase                string
	evidence, set              string
	parser, build, publication string
}

type ipEntityDeltaLookup struct {
	owner    ipEntityJobOwner
	receipt  ipEntityDeltaReceipt
	decision receiptDecision
	value    ipEntityDeltaResult
	digest   ResultDigest
}

// ipEntityDeltaPriorHead is the exact old accepted publication head. It is read
// by PK and then bound to the current selected provider slot.
type ipEntityDeltaPriorHead struct {
	provider, subjectKind, providerID, operation, decision string
	evidence, set                                          sql.NullString
	selectionRevision                                      int64
}

// ipEntityDeltaPriorEvidence is the retained upstream source header. Its digest
// is the exact observation content identity that decides an exact no-op.
type ipEntityDeltaPriorEvidence struct {
	subjectID                                 int64
	provider, subjectKind, providerID, parser string
	language, region, requestShape, digest    string
	sealed                                    int64
	sealOwner                                 sql.NullString
	sealIncarnation                           []byte
	sealSequence                              sql.NullInt64
}

type ipEntityDeltaPriorSeal struct {
	incarnation []byte
	sequence    int64
	terminal    int64
	rows, bytes int64
	pins        int64
}

type ipEntityDeltaPriorSet struct {
	evidence           string
	complete, coverage int64
}

type ipEntityDeltaPriorSetSeal struct {
	incarnation []byte
	sequence    int64
	terminal    int64
	pins        int64
}

func (c *ipEntityDeltaCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEntityDeltaCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntityDeltaCommand")
}

func ipEntityDeltaShape(q ipEntityDeltaRequest) error {
	if ipEntityAcceptShape(q.observation) != nil {
		return ErrInvalid
	}
	if !ipEntityAcceptText(q.previousPublicationID, ipEntityAcceptIDMax) ||
		len(q.previousPublicationID) == 0 ||
		!ipEvidenceText(q.previousEvidenceID, ipEvidenceIDMax) ||
		len(q.previousEvidenceID) == 0 ||
		!ipEntityAcceptOptionalID(q.previousSetID) {
		return ErrInvalid
	}
	// The replacement names a NEW observation and a NEW publication head. A
	// caller that re-submits the already accepted source is not a replacement.
	if q.previousEvidenceID == q.observation.evidenceID ||
		q.previousPublicationID == q.observation.publicationID {
		return ErrInvalid
	}
	// A retained set proof belongs to the series/season set family only.
	if q.previousSetID != "" && q.observation.subjectKind != "series" &&
		q.observation.subjectKind != "season" {
		return ErrInvalid
	}
	return nil
}

func newIPEntityDeltaCommand(identity CommandIdentity, in ipEntityDeltaRequest) (*ipEntityDeltaCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipEntityDeltaShape(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPEntityDelta, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityDeltaCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipEntityDeltaLookupReceipt resolves the ORIGINAL receipt before any fresh
// source/lease probe. Current authority is checked by the caller first.
func ipEntityDeltaLookupReceipt(ctx context.Context, turn *commandTurn, ref ReceiptReference,
	q ipEntityDeltaRequest) (out ipEntityDeltaLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntityDelta {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobOwnerStmt, q.observation.operationID, q.observation.provider)
	if err != nil {
		return out, err
	}
	var incarnation, entityIncarnation, policyIncarnation []byte
	err = row.Scan(&incarnation, &out.owner.lastSequence, &out.owner.acknowledgedSequence,
		&out.owner.generation, &out.owner.status, &out.owner.lease, &out.owner.leaseUntil,
		&out.owner.entityID, &entityIncarnation, &out.owner.sourceRevision, &out.owner.identityRevision,
		&out.owner.metadataRevision, &policyIncarnation, &out.owner.policyRevision,
		&out.owner.consentRevision, &out.owner.selectedID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if _, ok := ipEntityJobTextTime(out.owner.leaseUntil); !ok {
		return out, ErrResolveRequired
	}
	if len(incarnation) != 16 || len(entityIncarnation) != 16 || len(policyIncarnation) != 16 ||
		out.owner.entityID != q.observation.entityID || out.owner.generation <= 0 ||
		out.owner.sourceRevision <= 0 || out.owner.identityRevision <= 0 ||
		out.owner.metadataRevision <= 0 || out.owner.policyRevision <= 0 ||
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipEntityDeltaSavedStmt, q.observation.operationID,
		ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest []byte
	var set sql.NullString
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest,
		&out.receipt.appliedMS, &resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision,
		&out.receipt.phase, &out.receipt.terminal, &out.receipt.evidence, &out.receipt.subjectID,
		&out.receipt.parser, &out.receipt.build, &set, &out.receipt.publication,
		&out.receipt.resultCount, &out.receipt.resultBytes)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.kind != "delta" || out.receipt.phase != "accepted" ||
			out.receipt.terminal != 1 || out.receipt.acceptedRevision <= 0 ||
			len(out.receipt.publication) == 0 || out.receipt.resultCount <= 0 ||
			out.receipt.resultBytes < 0 {
			return out, ErrResolveRequired
		}
		out.receipt.incarnation = receiptIncarnation
		out.receipt.operation = operation
		out.receipt.request = requestDigest
		out.receipt.result = resultDigest
		if set.Valid {
			out.receipt.set = set.String
		}
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], receiptIncarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPEntityDelta,
			resultCount: out.receipt.resultCount}
		copy(found.resultDigest[:], resultDigest)
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPEntityDelta,
		receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
			lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.acknowledgedSequence}, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		value, e := ipEntityDeltaSaved(q, ref.Identity, out.receipt)
		if e != nil {
			return out, errors.Join(ErrResolveRequired, e)
		}
		computed, e := ipEntityDeltaResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != found.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

// ipEntityDeltaSaved rebuilds the COMPLETE saved result from the original
// request and the saved receipt witnesses. It never reads the mutable accepted
// publication, subject slot, entity projection or search job.
func ipEntityDeltaSaved(q ipEntityDeltaRequest, identity CommandIdentity, r ipEntityDeltaReceipt) (ipEntityDeltaResult, error) {
	var zero ipEntityDeltaResult
	noOp := r.publication == q.previousPublicationID
	wantPublication := q.observation.publicationID
	if noOp {
		wantPublication = q.previousPublicationID
	}
	if r.kind != "delta" || r.phase != "accepted" || r.terminal != 1 ||
		!ipEntityAcceptEqualBlob(r.operation, identity.OperationID[:]) ||
		r.evidence != q.observation.evidenceID || r.subjectID != q.observation.subjectID ||
		r.parser != q.observation.parserVersion || r.build != q.observation.buildID ||
		r.set != q.observation.setID || r.publication != wantPublication ||
		r.acceptedRevision <= 0 || r.resultCount != int64(len(q.observation.fields)) ||
		r.resultBytes != ipEntityAcceptFieldBytes(q.observation.fields) {
		return zero, ErrResolveRequired
	}
	out := ipEntityDeltaResult{
		OperationID: q.observation.operationID, PublicationID: r.publication,
		PreviousPublicationID: q.previousPublicationID, EvidenceID: q.observation.evidenceID,
		PreviousEvidenceID: q.previousEvidenceID, SetID: q.observation.setID,
		PreviousSetID: q.previousSetID, Provider: q.observation.provider,
		SubjectKind: q.observation.subjectKind, ProviderID: q.observation.providerID,
		ParserVersion: q.observation.parserVersion, ObservedAt: q.observation.observedAt,
		Lease: q.observation.lease, EntityID: q.observation.entityID,
		OwnerGeneration: q.observation.generation, ContentRevision: q.observation.contentRevision,
		AcceptedRevision: r.acceptedRevision, SelectionRevision: q.observation.selectionRevision,
		SearchJobID: q.observation.searchJobID, FieldCount: int64(len(q.observation.fields)),
		ExternalCount: int64(len(q.observation.external)), TermCount: int64(len(q.observation.terms)),
		CreditCount: int64(len(q.observation.credits)), LeaseUntilMS: q.observation.leaseUntilMS,
		AppliedMS: r.appliedMS, NoOp: noOp,
	}
	if err := preflightIPEntityDeltaResult(out); err != nil {
		return zero, err
	}
	return out, nil
}

// ipEntityDeltaPriorHeadRead proves the exact old accepted publication head. The
// stored selection_revision is the revision this head was accepted from, so the
// caller's exact selection revision must be that plus one.
func ipEntityDeltaPriorHeadRead(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest) (out ipEntityDeltaPriorHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaPriorHeadStmt, q.previousPublicationID, q.observation.entityID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.provider, &out.subjectKind, &out.providerID, &out.evidence, &out.set,
		&out.decision, &out.operation, &out.selectionRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.provider != q.observation.provider || out.subjectKind != q.observation.subjectKind ||
		out.providerID != q.observation.providerID || out.decision != "accepted" ||
		len(out.operation) == 0 || !out.evidence.Valid ||
		out.evidence.String != q.previousEvidenceID ||
		out.set.Valid != (q.previousSetID != "") ||
		(out.set.Valid && out.set.String != q.previousSetID) ||
		q.observation.selectionRevision == math.MaxInt64 ||
		out.selectionRevision != q.observation.selectionRevision-1 {
		return out, ErrConflict
	}
	return out, nil
}

// ipEntityDeltaPriorEvidenceRead proves the retained upstream source header and
// its exact IP seal tuple still belong to the old head.
func ipEntityDeltaPriorEvidenceRead(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest, prior ipEntityDeltaPriorHead) (out ipEntityDeltaPriorEvidence, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaPriorEvidenceStmt, q.previousEvidenceID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.subjectID, &out.provider, &out.subjectKind, &out.providerID, &out.parser,
		&out.language, &out.region, &out.requestShape, &out.digest, &out.sealed, &out.sealOwner,
		&out.sealIncarnation, &out.sealSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.subjectID != q.observation.subjectID || out.provider != q.observation.provider ||
		out.subjectKind != q.observation.subjectKind || out.providerID != q.observation.providerID ||
		out.parser != q.observation.parserVersion || out.language != q.observation.language ||
		out.region != q.observation.region || out.requestShape != q.observation.requestShape ||
		out.sealed != 1 || len(out.digest) != 64 || !out.sealOwner.Valid ||
		out.sealOwner.String != prior.operation || len(out.sealIncarnation) != 16 ||
		!out.sealSequence.Valid || out.sealSequence.Int64 <= 0 {
		return out, ErrConflict
	}
	return out, nil
}

// ipEntityDeltaPriorSealRead proves the ORIGINAL old seal receipt is still
// terminal and charged. A drained or unknown old proof refuses the replacement:
// the new head must never outlive its retained upstream keys.
func ipEntityDeltaPriorSealRead(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest, prior ipEntityDeltaPriorHead,
	evidence ipEntityDeltaPriorEvidence) (out ipEntityDeltaPriorSeal, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaPriorSealStmt, prior.operation, q.previousEvidenceID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.incarnation, &out.sequence, &out.terminal, &out.rows, &out.bytes, &out.pins)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(out.incarnation) != 16 || out.sequence <= 0 || out.terminal != 1 ||
		out.rows <= 0 || out.bytes <= 0 || out.pins < 1 ||
		!ipEntityAcceptEqualBlob(out.incarnation, evidence.sealIncarnation) ||
		out.sequence != evidence.sealSequence.Int64 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityDeltaPriorSetRead(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest) (out ipEntityDeltaPriorSet, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaPriorSetStmt, q.previousSetID, q.observation.entityID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.evidence, &out.complete, &out.coverage)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.evidence != q.previousEvidenceID || out.complete != 1 || out.coverage != 1 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityDeltaPriorSetSealRead(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest, prior ipEntityDeltaPriorHead) (out ipEntityDeltaPriorSetSeal, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaPriorSetSealStmt, prior.operation, q.previousSetID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.incarnation, &out.sequence, &out.terminal, &out.pins)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(out.incarnation) != 16 || out.sequence <= 0 || out.terminal != 1 || out.pins < 1 {
		return out, ErrConflict
	}
	return out, nil
}

func ipEntityDeltaProjectionValue(p ipEntityAcceptProjectionFacts, name string) (string, bool) {
	switch name {
	case "title":
		return p.title, true
	case "sortTitle":
		return p.sortTitle, true
	case "language":
		return p.language, true
	case "overview":
		return p.overview, true
	case "originalTitle":
		return p.originalTitle, true
	case "tagline":
		return p.tagline, true
	case "releaseDate":
		return p.releaseDate, true
	case "contentRating":
		return p.contentRating, true
	case "studio":
		return p.studio, true
	case "network":
		return p.network, true
	case "country":
		return p.country, true
	case "year":
		return strconv.FormatInt(p.year, 10), true
	}
	return "", false
}

// ipEntityDeltaFieldsSame proves the typed descriptive cohort is the SAME
// accepted observation: the same field names and the same effective values.
// Projection fields compare against the current automatic projection; other
// fields compare against the durable automatic observation when one exists.
func ipEntityDeltaFieldsSame(ctx context.Context, turn *commandTurn,
	q ipEntityDeltaRequest, before ipEntityAcceptProjectionFacts) (same bool, err error) {
	defer func() { same, err = finishCommand(ctx, commandLifetime(turn), same, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityDeltaOriginsStmt, q.observation.entityID,
		q.previousPublicationID)
	if err != nil {
		return false, err
	}
	var count int64
	var joined string
	if err = row.Scan(&count, &joined); err != nil {
		return false, err
	}
	if count != int64(len(q.observation.fields)) {
		return false, nil
	}
	known := make(map[string]bool, int(count))
	if count > 0 {
		for _, field := range strings.Split(joined, "\x1f") {
			known[field] = true
		}
	}
	if len(known) != len(q.observation.fields) {
		return false, nil
	}
	kind := ipEntityAcceptOwnerKind(q.observation.subjectKind)
	for _, field := range q.observation.fields {
		if !known[field.Name] {
			return false, nil
		}
		if value, ok := ipEntityDeltaProjectionValue(before, field.Name); ok {
			if field.Present && field.Value != value {
				return false, nil
			}
			continue
		}
		if !field.Present {
			continue
		}
		row, e := wc008DraftQuery(ctx, turn, ipEntityDeltaOwnerAutomaticStmt, kind,
			q.observation.entityID, field.Name)
		if e != nil {
			return false, e
		}
		var value string
		var present int64
		if e = row.Scan(&value, &present); errors.Is(e, sql.ErrNoRows) {
			continue
		} else if e != nil {
			return false, e
		}
		if present != 1 || value != field.Value {
			return false, nil
		}
	}
	return true, nil
}

// ipEntityDeltaNoOp is the EXACT no-op decision: the new observation carries
// the same provider content digest and the same typed field/child cohort as the
// old accepted head, and it keeps the same set (or no set). Anything else is a
// new observation and publishes a replacement head.
func ipEntityDeltaNoOp(ctx context.Context, turn *commandTurn, q ipEntityDeltaRequest,
	newest ipEntityAcceptEvidenceFacts, prior ipEntityDeltaPriorEvidence,
	before ipEntityAcceptProjectionFacts) (noOp bool, err error) {
	defer func() { noOp, err = finishCommand(ctx, commandLifetime(turn), noOp, err) }()
	if q.observation.setID != q.previousSetID {
		return false, nil
	}
	if newest.digest != prior.digest || newest.provider != prior.provider ||
		newest.subjectKind != prior.subjectKind || newest.providerID != prior.providerID ||
		newest.parser != prior.parser || newest.language != prior.language ||
		newest.region != prior.region || newest.requestShape != prior.requestShape {
		return false, nil
	}
	// The same typed cohort must be the COMPLETE old sealed cohort, not merely a
	// subset of it.
	priorObservation := q.observation
	priorObservation.evidenceID = q.previousEvidenceID
	if err = ipEntityAcceptChildren(ctx, turn, priorObservation); err != nil {
		return false, nil
	}
	return ipEntityDeltaFieldsSame(ctx, turn, q, before)
}

func ipEntityDeltaSelectionAdvance(ctx context.Context, turn *commandTurn, q ipEntityDeltaRequest,
	selection ipEntityAcceptSelectionFacts) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := turn.queryRow(ctx, turn.steps[ipEntityDeltaSelectionCASStmt].statementStep,
		q.observation.evidenceID,
		ipEntityAcceptNullable(sql.NullString{String: q.observation.setID, Valid: q.observation.setID != ""}),
		q.observation.publicationID, q.observation.observedAt, q.observation.entityID,
		q.observation.provider, q.observation.selectionIncarnation[:],
		q.observation.selectionRevision, q.observation.subjectKind, q.observation.providerID,
		ipEntityAcceptNullable(selection.evidence), ipEntityAcceptNullable(selection.set),
		ipEntityAcceptNullable(selection.publication))
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = row.Scan(&out); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out != selection.revision+1 {
		return out, ErrConflict
	}
	return out, nil
}

// ipEntityDeltaFresh is the ONE write path. Every native proof, including the
// retained old head and its live upstream pin, is complete before the first
// write; any later failure rolls back the whole replacement.
func ipEntityDeltaFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipEntityDeltaRequest, request RequestDigest, owner ipEntityJobOwner,
	appliedMS int64) (out ipEntityDeltaResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	observation := q.observation
	evidence, err := ipEntityAcceptEvidenceRead(ctx, turn, observation)
	if err != nil {
		return out, err
	}
	seal, err := ipEntityAcceptEvidenceSealRead(ctx, turn, observation)
	if err != nil {
		return out, err
	}
	var setSeal ipEntityAcceptSealFacts
	if observation.setID != "" {
		proofSequence, err := ipEntityAcceptSetRead(ctx, turn, observation)
		if err != nil {
			return out, err
		}
		setSeal, err = ipEntityAcceptSetSealRead(ctx, turn, observation)
		if err != nil {
			return out, err
		}
		if !ipEntityAcceptEqualBlob(setSeal.incarnation, seal.incarnation) ||
			setSeal.evidence != observation.evidenceID || setSeal.sequence != proofSequence {
			return out, ErrConflict
		}
	}
	build, err := ipEntityAcceptBuildRead(ctx, turn, observation)
	if err != nil {
		return out, err
	}
	if !ipEntityAcceptEqualBlob(build.ownerIncarnation, seal.incarnation) ||
		build.ownerGeneration != observation.generation {
		return out, ErrConflict
	}
	selection, err := ipEntityAcceptSelectionRead(ctx, turn, observation)
	if err != nil {
		return out, err
	}
	// The caller must name the EXACT current accepted head: the selection slot,
	// the old publication row and the retained upstream proof keys all agree.
	if !selection.publication.Valid || !selection.evidence.Valid ||
		selection.publication.String != q.previousPublicationID ||
		selection.evidence.String != q.previousEvidenceID ||
		selection.set.Valid != (q.previousSetID != "") ||
		(selection.set.Valid && selection.set.String != q.previousSetID) {
		return out, ErrConflict
	}
	prior, err := ipEntityDeltaPriorHeadRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	priorEvidence, err := ipEntityDeltaPriorEvidenceRead(ctx, turn, q, prior)
	if err != nil {
		return out, err
	}
	if _, err = ipEntityDeltaPriorSealRead(ctx, turn, q, prior, priorEvidence); err != nil {
		return out, err
	}
	if q.previousSetID != "" {
		if _, err = ipEntityDeltaPriorSetRead(ctx, turn, q); err != nil {
			return out, err
		}
		if _, err = ipEntityDeltaPriorSetSealRead(ctx, turn, q, prior); err != nil {
			return out, err
		}
	}
	if err = ipEntityAcceptSearchRead(ctx, turn, observation); err != nil {
		return out, err
	}
	if err = ipEntityAcceptChildren(ctx, turn, observation); err != nil {
		return out, err
	}
	before, err := ipEntityAcceptProjectionRead(ctx, turn, observation, owner)
	if err != nil {
		return out, err
	}
	noOp, err := ipEntityDeltaNoOp(ctx, turn, q, evidence, priorEvidence, before)
	if err != nil {
		return out, err
	}
	if noOp {
		value := ipEntityDeltaResult{
			OperationID: observation.operationID, PublicationID: q.previousPublicationID,
			PreviousPublicationID: q.previousPublicationID, EvidenceID: observation.evidenceID,
			PreviousEvidenceID: q.previousEvidenceID, SetID: observation.setID,
			PreviousSetID: q.previousSetID, Provider: observation.provider,
			SubjectKind: observation.subjectKind, ProviderID: observation.providerID,
			ParserVersion: observation.parserVersion, ObservedAt: observation.observedAt,
			Lease: observation.lease, EntityID: observation.entityID,
			OwnerGeneration: observation.generation, ContentRevision: observation.contentRevision,
			AcceptedRevision: before.revision, SelectionRevision: observation.selectionRevision,
			SearchJobID: observation.searchJobID, FieldCount: int64(len(observation.fields)),
			ExternalCount: int64(len(observation.external)), TermCount: int64(len(observation.terms)),
			CreditCount: int64(len(observation.credits)), LeaseUntilMS: observation.leaseUntilMS,
			AppliedMS: appliedMS, NoOp: true,
		}
		if err = ipEntityDeltaRecord(ctx, turn, identity, request, value, observation.subjectID, observation.buildID, ipEntityAcceptFieldBytes(observation.fields)); err != nil {
			return out, err
		}
		if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, observation.operationID,
			observation.provider, observation.lease, observation.generation,
			observation.leaseUntilMS, owner); err != nil {
			return out, err
		}
		return value, nil
	}
	after, err := ipEntityAcceptProjection(observation, before)
	if err != nil {
		return out, err
	}
	sortKey, sortHead, keepKey, keepHead, err := ipEntityAcceptSortKeys(after)
	if err != nil {
		return out, err
	}
	if err = execIPEntityAccept(ctx, turn, ipEntityDeltaPublicationStmt,
		prior.provider, prior.subjectKind, prior.providerID, observation.publicationID,
		observation.operationID, observation.entityID, observation.provider,
		observation.subjectKind, observation.providerID, observation.evidenceID,
		ipEntityAcceptNullable(sql.NullString{String: observation.setID, Valid: observation.setID != ""}),
		q.previousPublicationID, observation.selectionRevision, observation.generation,
		observation.observedAt); err != nil {
		return out, err
	}
	if _, err = ipEntityDeltaSelectionAdvance(ctx, turn, q, selection); err != nil {
		return out, err
	}
	for _, row := range observation.external {
		if err = execIPEntityAccept(ctx, turn, ipEntityAcceptExternalStmt,
			observation.entityID, row.Provider, row.SubjectKind, row.ProviderID); err != nil {
			return out, err
		}
	}
	for _, row := range observation.terms {
		if err = ipEntityAcceptWriteTerm(ctx, turn, observation, row); err != nil {
			return out, err
		}
	}
	revision, err := ipEntityAcceptProjectionWrite(ctx, turn, observation, before, after,
		sortKey, sortHead, keepKey, keepHead)
	if err != nil {
		return out, err
	}
	if revision <= before.revision {
		return out, ErrConflict
	}
	if err = ipEntityAcceptOwnerFields(ctx, turn, observation); err != nil {
		return out, err
	}
	if err = ipEntityAcceptOrigins(ctx, turn, observation, evidence); err != nil {
		return out, err
	}
	if err = execIPEntityAccept(ctx, turn, ipEntityDeltaRetainProofStmt,
		observation.operationID, seal.incarnation, seal.sequence, observation.evidenceID); err != nil {
		return out, err
	}
	if observation.setID != "" {
		if err = execIPEntityAccept(ctx, turn, ipEntityDeltaRetainSetProofStmt,
			observation.operationID, setSeal.incarnation, setSeal.sequence,
			observation.setID, observation.evidenceID); err != nil {
			return out, err
		}
	}
	value := ipEntityDeltaResult{
		OperationID: observation.operationID, PublicationID: observation.publicationID,
		PreviousPublicationID: q.previousPublicationID, EvidenceID: observation.evidenceID,
		PreviousEvidenceID: q.previousEvidenceID, SetID: observation.setID,
		PreviousSetID: q.previousSetID, Provider: observation.provider,
		SubjectKind: observation.subjectKind, ProviderID: observation.providerID,
		ParserVersion: observation.parserVersion, ObservedAt: observation.observedAt,
		Lease: observation.lease, EntityID: observation.entityID,
		OwnerGeneration: observation.generation, ContentRevision: observation.contentRevision,
		AcceptedRevision: revision, SelectionRevision: observation.selectionRevision,
		SearchJobID: observation.searchJobID, FieldCount: int64(len(observation.fields)),
		ExternalCount: int64(len(observation.external)), TermCount: int64(len(observation.terms)),
		CreditCount: int64(len(observation.credits)), LeaseUntilMS: observation.leaseUntilMS,
		AppliedMS: appliedMS,
	}
	if err = ipEntityDeltaRecord(ctx, turn, identity, request, value, observation.subjectID, observation.buildID, ipEntityAcceptFieldBytes(observation.fields)); err != nil {
		return out, err
	}
	if err = ipEntityEvidenceAdvanceOwner(ctx, turn, identity, observation.operationID,
		observation.provider, observation.lease, observation.generation,
		observation.leaseUntilMS, owner); err != nil {
		return out, err
	}
	return value, nil
}

func ipEntityDeltaRecord(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	request RequestDigest, value ipEntityDeltaResult, subjectID int64, buildID string,
	resultBytes int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	digest, err := ipEntityDeltaResultDigest(identity, request, value)
	if err != nil {
		return err
	}
	return execIPEntityAccept(ctx, turn, ipEntityDeltaRecordStmt,
		value.OperationID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:],
		request[:], value.AcceptedRevision, value.AppliedMS, digest[:], value.FieldCount,
		resultBytes, value.EvidenceID,
		subjectID, value.ParserVersion, buildID,
		ipEntityAcceptNullable(sql.NullString{String: value.SetID, Valid: value.SetID != ""}),
		value.PublicationID)
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved replacement with zero new effects, before any fresh probe.
func applyIPEntityDelta(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityDeltaCommand) (out ipEntityDeltaCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityDeltaCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPEntityDelta {
		return zero, ErrInvalid
	}
	q := c.request
	if ipEntityDeltaShape(q) != nil {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPEntityDelta, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEntityDeltaLookupReceipt(ctx, turn, c.reference(), q)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
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
	if err = ipEntityJobOwnerFresh(ctx, turn, q.observation.operationID, lookup.owner,
		q.observation.provider, q.observation.selectedID, q.observation.lease,
		q.observation.generation, q.observation.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipEntityDeltaFresh(ctx, turn, c.identity, q, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityDeltaLookupReceipt(ctx, turn, c.reference(), q)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
