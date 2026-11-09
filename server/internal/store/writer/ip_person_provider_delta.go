package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// IP person provider typed delta replace 235 publishes a NEW sealed person
// observation as the current accepted provider source for one catalogue
// person, replacing the EXACT old accepted head the caller names. It requires
// the old publication, its evidence and the exact person/provider source slot
// incarnation/revision, and it refuses a head that is no longer the current
// accepted one.
//
// The old source stays charged. The old publication, its evidence and its
// original seal receipt are never mutated; the delta proves the old seal
// receipt is still retained with a live pin before its first write. Retention
// ends only through IP-17's bounded downstream retirement; this command never
// reclaims it.
//
// It copies IP-10's delta shape (authority first, original receipt before any
// fresh probe, complete native proof before the first write, typed replay from
// the saved receipt only) and consumes the NEW sealed person observation
// through the same small helpers IP-16's accept uses.
type ipPersonDeltaRequest struct {
	observation               ipPersonObservation
	previousPublicationID     string
	previousEvidenceID        string
	previousSourceIncarnation [16]byte
	previousSourceRevision    int64
}

type ipPersonDeltaCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  ipPersonDeltaRequest
	valid    bool
}

type ipPersonDeltaCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipPersonDeltaResult
	Digest  ResultDigest
}

// ipPersonDeltaPriorHead is the exact old accepted publication. It is read by
// PK and then bound to the current person/provider source slot.
type ipPersonDeltaPriorHead struct {
	personID                          int64
	provider, subjectKind, providerID string
	evidence, operation, proofOwner   string
	observedAt, proofKind             string
	proofIncarnation                  []byte
	proofSequence, acceptedRevision   int64
}

// ipPersonDeltaPriorEvidence is the retained upstream observation header. Its
// digest and namespace are the exact content identity used for a no-op.
type ipPersonDeltaPriorEvidence struct {
	provider, subjectKind, providerID, parser string
	language, region, requestShape, digest    string
	sealed                                    int64
}

func (c *ipPersonDeltaCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipPersonDeltaCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPersonDeltaCommand")
}

func ipPersonDeltaShape(q ipPersonDeltaRequest) error {
	if ipPersonObservationShape(q.observation) != nil {
		return ErrInvalid
	}
	if !ipPersonText(q.previousPublicationID, ipPersonPointerMax, true) ||
		!ipPersonText(q.previousEvidenceID, ipPersonPointerMax, true) ||
		q.previousSourceIncarnation == [16]byte{} ||
		q.previousSourceRevision <= 0 || q.previousSourceRevision == math.MaxInt64 {
		return ErrInvalid
	}
	// The replacement names a NEW observation and a NEW publication head. A
	// caller that re-submits the already accepted source is not a replacement.
	if q.previousEvidenceID == q.observation.evidenceID ||
		q.previousPublicationID == q.observation.publicationID {
		return ErrInvalid
	}
	return nil
}

func newIPPersonDeltaCommand(identity CommandIdentity, in ipPersonDeltaRequest) (*ipPersonDeltaCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipPersonDeltaShape(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPPersonDelta, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonDeltaCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipPersonDeltaSaved rebuilds the COMPLETE saved replacement from the original
// request and the saved receipt witnesses. It never reads the mutable person,
// source, publication or search state.
func ipPersonDeltaSaved(q ipPersonDeltaRequest, identity CommandIdentity,
	r ipPersonReceipt) (ipPersonDeltaResult, error) {
	var zero ipPersonDeltaResult
	o := q.observation
	noOp := r.publicationID == q.previousPublicationID
	wantPublication := o.publicationID
	personRevision := o.personRevision + 1
	sourceRevision := q.previousSourceRevision + 1
	if noOp {
		wantPublication = q.previousPublicationID
		personRevision = o.personRevision
		sourceRevision = q.previousSourceRevision
	}
	if r.kind != "delta" || r.operationID != identity.OperationID ||
		r.evidenceID != o.evidenceID || r.buildID != o.buildID ||
		r.publicationID != wantPublication || r.acceptedRevision != personRevision ||
		r.resultCount != int64(len(o.fields)) {
		return zero, ErrResolveRequired
	}
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(o.fields)
	out := ipPersonDeltaResult{
		OperationID: o.operationID, PublicationID: wantPublication,
		PreviousPublicationID: q.previousPublicationID, EvidenceID: o.evidenceID,
		PreviousEvidenceID: q.previousEvidenceID, Provider: o.provider, ProviderID: o.providerID,
		ObservedAt: o.observedAt, Lease: o.lease, Name: name, Biography: biography,
		BirthDate: birthDate, DeathDate: deathDate, BirthPlace: birthPlace, PersonID: o.personID,
		PersonRevision: personRevision, AcceptedRevision: personRevision,
		PreviousSourceRevision: q.previousSourceRevision, SourceRevision: sourceRevision,
		OwnerGeneration: o.generation, ContentRevision: o.contentRevision,
		SearchJobID: o.searchJobID, AliasCount: int64(len(o.aliases)),
		ExternalCount: int64(len(o.external)), LeaseUntilMS: o.leaseUntilMS,
		AppliedMS: r.appliedMS, NoOp: noOp,
	}
	if preflightIPPersonDeltaResult(out) != nil {
		return zero, ErrResolveRequired
	}
	return out, nil
}

func ipPersonDeltaPriorHeadRead(ctx context.Context, turn *commandTurn,
	q ipPersonDeltaRequest) (out ipPersonDeltaPriorHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonDeltaPriorPublicationStmt, q.previousPublicationID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.personID, &out.provider, &out.subjectKind, &out.providerID, &out.evidence,
		&out.operation, &out.acceptedRevision, &out.observedAt, &out.proofOwner,
		&out.proofIncarnation, &out.proofSequence, &out.proofKind)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.personID != q.observation.personID || out.provider != q.observation.provider ||
		out.subjectKind != "person" || out.providerID != q.observation.providerID ||
		out.evidence != q.previousEvidenceID || len(out.operation) != 32 ||
		out.acceptedRevision != q.observation.personRevision ||
		len(out.proofOwner) != 32 || len(out.proofIncarnation) != 16 ||
		out.proofSequence <= 0 || out.proofKind != "evidence_seal" {
		return out, ErrConflict
	}
	if _, ok := ipPersonTextTime(out.observedAt); !ok {
		return out, ErrConflict
	}
	return out, nil
}

func ipPersonDeltaPriorEvidenceRead(ctx context.Context, turn *commandTurn,
	q ipPersonDeltaRequest) (out ipPersonDeltaPriorEvidence, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonDeltaPriorEvidenceStmt, q.previousEvidenceID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.provider, &out.subjectKind, &out.providerID, &out.parser, &out.language,
		&out.region, &out.requestShape, &out.digest, &out.sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.provider != q.observation.provider || out.subjectKind != "person" ||
		out.providerID != q.observation.providerID || len(out.parser) == 0 ||
		len(out.language) == 0 || len(out.region) == 0 || len(out.requestShape) == 0 ||
		len(out.digest) != 64 || out.sealed != 1 {
		return out, ErrConflict
	}
	return out, nil
}

func ipPersonDeltaPriorSealRead(ctx context.Context, turn *commandTurn,
	prior ipPersonDeltaPriorHead) (pins int64, err error) {
	defer func() { pins, err = finishCommand(ctx, commandLifetime(turn), pins, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonDeltaPriorSealStmt, prior.proofOwner,
		prior.proofIncarnation, prior.proofSequence, prior.evidence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&pins); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrConflict
		}
		return 0, err
	}
	if pins < 1 {
		return 0, ErrConflict
	}
	return pins, nil
}

// ipPersonDeltaNoOp is the EXACT no-op decision: the new observation carries the
// same provider content digest and namespace, the same typed person facts as
// the accepted head and the same complete alias/external cohort. Anything else
// is a new observation and publishes a replacement head.
func ipPersonDeltaNoOp(ctx context.Context, turn *commandTurn, q ipPersonDeltaRequest,
	newest ipPersonSealedFact, prior ipPersonDeltaPriorEvidence,
	before ipPersonFacts) (noOp bool, err error) {
	defer func() { noOp, err = finishCommand(ctx, commandLifetime(turn), noOp, err) }()
	if newest.digest != prior.digest || newest.provider != prior.provider ||
		newest.subjectKind != prior.subjectKind || newest.providerID != prior.providerID ||
		newest.parser != prior.parser || newest.language != prior.language ||
		newest.region != prior.region || newest.requestShape != prior.requestShape {
		return false, nil
	}
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(q.observation.fields)
	if name != before.name || biography != before.biography ||
		birthDate != before.birthDate || deathDate != before.deathDate ||
		birthPlace != before.birthPlace {
		return false, nil
	}
	aliases, external, err := ipPersonCohortRead(ctx, turn, q.previousEvidenceID)
	if err != nil {
		return false, err
	}
	if ipPersonCohortSame(aliases, external, q.observation.aliases, q.observation.external) != nil {
		return false, nil
	}
	return true, nil
}

// ipPersonDeltaFresh is the ONE write path. Every native proof, including the
// retained old head, the source slot CAS and the old seal pin, is complete
// before the first write; any later failure rolls the whole replacement back.
func ipPersonDeltaFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonDeltaRequest, request RequestDigest, owner ipPersonOwner,
	appliedMS int64) (out ipPersonDeltaResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	o := q.observation
	source, err := ipPersonSealedObservationRead(ctx, turn, o, identity)
	if err != nil {
		return out, err
	}
	aliases, external, err := ipPersonCohortRead(ctx, turn, o.evidenceID)
	if err != nil {
		return out, err
	}
	if err = ipPersonCohortSame(aliases, external, o.aliases, o.external); err != nil {
		return out, err
	}
	if int64(len(aliases)+len(external))+1 != source.expectedRows {
		return out, ErrConflict
	}
	facts, err := ipPersonFactsRead(ctx, turn, o.personID, owner.personToken)
	if err != nil {
		return out, err
	}
	if facts.revision != o.personRevision {
		return out, ErrConflict
	}
	existing, exists, err := ipPersonSourceRead(ctx, turn, o.personID, o.provider)
	if err != nil {
		return out, err
	}
	// The caller must name the EXACT current accepted head: the source slot, the
	// old publication row and the retained upstream proof keys all agree.
	if !exists || existing.providerID != o.providerID ||
		existing.incarnation != q.previousSourceIncarnation ||
		existing.revision != q.previousSourceRevision ||
		!existing.publication.Valid || existing.publication.String != q.previousPublicationID ||
		!existing.evidence.Valid || existing.evidence.String != q.previousEvidenceID {
		return out, ErrConflict
	}
	prior, err := ipPersonDeltaPriorHeadRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	priorEvidence, err := ipPersonDeltaPriorEvidenceRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if _, err = ipPersonDeltaPriorSealRead(ctx, turn, prior); err != nil {
		return out, err
	}
	if err = ipPersonAcceptSearchRead(ctx, turn, o); err != nil {
		return out, err
	}
	noOp, err := ipPersonDeltaNoOp(ctx, turn, q, source, priorEvidence, facts)
	if err != nil {
		return out, err
	}
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(o.fields)
	if noOp {
		value := ipPersonDeltaResult{
			OperationID: o.operationID, PublicationID: q.previousPublicationID,
			PreviousPublicationID: q.previousPublicationID, EvidenceID: o.evidenceID,
			PreviousEvidenceID: q.previousEvidenceID, Provider: o.provider,
			ProviderID: o.providerID, ObservedAt: source.observedAt, Lease: o.lease,
			Name: name, Biography: biography, BirthDate: birthDate, DeathDate: deathDate,
			BirthPlace: birthPlace, PersonID: o.personID,
			PersonRevision: prior.acceptedRevision, AcceptedRevision: prior.acceptedRevision,
			PreviousSourceRevision: q.previousSourceRevision,
			SourceRevision:         q.previousSourceRevision, OwnerGeneration: o.generation,
			ContentRevision: o.contentRevision, SearchJobID: o.searchJobID,
			AliasCount: int64(len(o.aliases)), ExternalCount: int64(len(o.external)),
			LeaseUntilMS: o.leaseUntilMS, AppliedMS: appliedMS, NoOp: true,
		}
		if _, err = ipPersonAcceptAdvanceOwner(ctx, turn, identity, o, owner); err != nil {
			return out, err
		}
		if err = ipPersonAcceptRecord(ctx, turn, identity, request, IPPersonDelta, value,
			value.OperationID, value.PublicationID, value.EvidenceID, o.buildID,
			value.AcceptedRevision, value.AppliedMS); err != nil {
			return out, err
		}
		return value, nil
	}
	if err = ipPersonAcceptWritePublication(ctx, turn, o, source); err != nil {
		return out, err
	}
	sourceRevision, err := ipPersonAcceptSourceWrite(ctx, turn, identity,
		ipPersonAcceptRequest{observation: o, sourceRevision: q.previousSourceRevision,
			sourceIncarnation: q.previousSourceIncarnation}, existing, true)
	if err != nil {
		return out, err
	}
	if sourceRevision != q.previousSourceRevision+1 {
		return out, ErrConflict
	}
	personRevision, err := ipPersonAcceptPersonWrite(ctx, turn, o, owner.personToken, facts)
	if err != nil {
		return out, err
	}
	if personRevision != o.personRevision+1 {
		return out, ErrConflict
	}
	if err = ipPersonAcceptFactsWrite(ctx, turn, o, source, sourceRevision); err != nil {
		return out, err
	}
	if err = ipPersonAcceptRetainProof(ctx, turn, o, source); err != nil {
		return out, err
	}
	if _, err = ipPersonAcceptAdvanceOwner(ctx, turn, identity, o, owner); err != nil {
		return out, err
	}
	value := ipPersonDeltaResult{
		OperationID: o.operationID, PublicationID: o.publicationID,
		PreviousPublicationID: q.previousPublicationID, EvidenceID: o.evidenceID,
		PreviousEvidenceID: q.previousEvidenceID, Provider: o.provider,
		ProviderID: o.providerID, ObservedAt: source.observedAt, Lease: o.lease,
		Name: name, Biography: biography, BirthDate: birthDate, DeathDate: deathDate,
		BirthPlace: birthPlace, PersonID: o.personID, PersonRevision: personRevision,
		AcceptedRevision: personRevision, PreviousSourceRevision: q.previousSourceRevision,
		SourceRevision: sourceRevision, OwnerGeneration: o.generation,
		ContentRevision: o.contentRevision, SearchJobID: o.searchJobID,
		AliasCount: int64(len(o.aliases)), ExternalCount: int64(len(o.external)),
		LeaseUntilMS: o.leaseUntilMS, AppliedMS: appliedMS,
	}
	if err = ipPersonAcceptRecord(ctx, turn, identity, request, IPPersonDelta, value,
		value.OperationID, value.PublicationID, value.EvidenceID, o.buildID,
		value.AcceptedRevision, value.AppliedMS); err != nil {
		return out, err
	}
	return value, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved replacement with zero new effects, before any fresh probe.
func applyIPPersonDelta(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof,
	lifetime context.Context, b ipPolicyBudget, c *ipPersonDeltaCommand) (out ipPersonDeltaCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonDeltaCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonDelta {
		return zero, ErrInvalid
	}
	q := c.request
	if ipPersonDeltaShape(q) != nil {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPPersonDelta, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonDeltaResult, error) {
		return ipPersonDeltaSaved(q, c.identity, r)
	}
	lookup, err := ipPersonAcceptedLookupReceipt(ctx, turn, IPPersonDelta, c.reference(),
		q.observation, decode)
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
	o := q.observation
	if lookup.owner.phase != "sealed" || lookup.owner.personRevision != o.personRevision {
		return zero, ErrConflict
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipPersonOwnerFresh(ctx, turn, lookup.owner, o.provider, o.providerID, o.lease,
		o.personID, o.generation, o.revision, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipPersonDeltaFresh(ctx, turn, c.identity, q, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonAcceptedLookupReceipt(ctx, turn, IPPersonDelta, c.reference(),
		o, decode)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
