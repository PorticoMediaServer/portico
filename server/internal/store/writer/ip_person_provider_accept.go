package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// IP person provider accept 234 publishes ONE sealed IP-14/IP-15 person
// evidence build as the current accepted provider source for one catalogue
// person. It is the accepting command for the person provider path: it
// composes current authority, the original receipt, the exact live person
// owner, the sealed source proof, the complete alias/external cohort, the
// typed person facts, the person/provider source slot, the PB People search
// intent and the saved result in one transaction.
//
// It copies IP-09's accept shape (authority first, original receipt before any
// fresh probe, complete native proof before the first write, typed replay from
// the saved receipt only). Person custody, receipt and proof rows are a
// separate family from the entity provider path; entity receipts never satisfy
// this path even when numeric person/entity IDs, owner incarnations and
// sequences coincide. No catalogue person row is invented: the claimed person
// token/revision is the only identity this command accepts.
const (
	ipPersonAcceptFactMax = 5
)

// ipPersonAcceptField is one typed accepted person fact. A fact is present only
// when the sealed observation carried it; a present empty value is an explicit
// empty observation, never absence.
type ipPersonAcceptField struct {
	Name, Value string
	Present     bool
}

// The closed, ordered person fact family. The order is part of the request
// digest and the saved result mapping, so no caller can relabel or reorder it.
var ipPersonAcceptFactNames = [...]string{"name", "biography", "birthDate", "deathDate", "birthPlace"}

// ipPersonObservation is the complete sealed person observation plus the typed
// cohort the adapter selected from it. The owner tuple is IP-14's exact live
// operation; the source tuple is IP-15's sealed evidence; the selection tuple
// is the PB person/provider source slot; the typed cohort is the accepted facts
// and the exact alias/external lists.
type ipPersonObservation struct {
	operationID, provider, providerID, lease string
	personID, personRevision                 int64
	generation, revision                     int64
	leaseGeneration, leaseUntilMS            int64
	evidenceID, buildID, publicationID       string
	buildGeneration, contentRevision         int64
	searchJobID                              int64
	observedAt                               string
	fields                                   []ipPersonAcceptField
	aliases                                  []ipPersonEvidenceAlias
	external                                 []ipPersonEvidenceExternal
}

// ipPersonAcceptRequest is the original acceptance input. A fresh accept births
// the ONE source slot (sourceRevision 0 and no incarnation) or moves an existing
// EMPTY slot whose exact revision/incarnation the caller names.
type ipPersonAcceptRequest struct {
	observation       ipPersonObservation
	sourceRevision    int64
	sourceIncarnation [16]byte
}

type ipPersonAcceptCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  ipPersonAcceptRequest
	valid    bool
}

type ipPersonAcceptCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipPersonAcceptResult
	Digest  ResultDigest
}

// ipPersonAcceptLookup is the original-receipt decision plus the exact person
// owner snapshot. Receipt resolution always precedes any fresh source probe.
type ipPersonAcceptedLookup[T any] struct {
	owner      ipPersonOwner
	receipt    ipPersonReceipt
	hasReceipt bool
	decision   receiptDecision
	value      T
	digest     ResultDigest
}

// ipPersonSealedFact is the exact immutable sealed person source: the build
// header, its generic evidence body, the original person seal receipt and the
// complete alias/external cohort. Every field is read by an exact key.
type ipPersonSealedFact struct {
	subjectID                                                     int64
	ownerIncarnation                                              [16]byte
	sealIncarnation                                               [16]byte
	sealCommitment                                                [32]byte
	expectedCommitment                                            [32]byte
	ownerGeneration, generation                                   int64
	contentRevision, cursor, expectedRows, expectedBytes          int64
	stagedRows, stagedBytes, sealSequence, sealAcceptedRevision   int64
	sealRows, sealBytes, sealPins                                 int64
	buildEvidence, expectedAlgorithm, sealAlgorithm               string
	provider, subjectKind, providerID, parser                     string
	language, region, requestShape, observedAt, sourceURL, digest string
	title, biography, birthDate, deathDate, birthPlace            string
}

type ipPersonSourceFact struct {
	incarnation [16]byte
	revision    int64
	providerID  string
	publication sql.NullString
	evidence    sql.NullString
}

type ipPersonFacts struct {
	revision                                               int64
	name, sortName, biography, birthPlace, biographySource string
	birthDate, deathDate                                   string
}

func (c *ipPersonAcceptCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipPersonAcceptCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPersonAcceptCommand")
}

func ipPersonAcceptFactsOf(fields []ipPersonAcceptField) (name, biography, birthDate, deathDate, birthPlace string) {
	for _, field := range fields {
		switch field.Name {
		case "name":
			name = field.Value
		case "biography":
			biography = field.Value
		case "birthDate":
			birthDate = field.Value
		case "deathDate":
			deathDate = field.Value
		case "birthPlace":
			birthPlace = field.Value
		}
	}
	return name, biography, birthDate, deathDate, birthPlace
}

func ipPersonAcceptFactsShape(fields []ipPersonAcceptField) error {
	if len(fields) != ipPersonAcceptFactMax {
		return ErrInvalid
	}
	for i, name := range ipPersonAcceptFactNames {
		field := fields[i]
		if field.Name != name || !field.Present ||
			!ipPersonText(field.Value, ipPersonTextMax, false) {
			return ErrInvalid
		}
	}
	if len(fields[0].Value) == 0 {
		return ErrInvalid
	}
	if !ipPersonEvidenceDate(EvidenceText{Present: true, Value: fields[2].Value}) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: fields[3].Value}) ||
		!ipPersonEvidenceOptionalText(EvidenceText{Present: true, Value: fields[4].Value}) {
		return ErrInvalid
	}
	return nil
}

func ipPersonObservationShape(q ipPersonObservation) error {
	if len(q.operationID) != 32 || !ipPolicyProviderCode(q.provider) ||
		!ipPersonText(q.providerID, ipPersonProviderIDMax, true) ||
		!ipPersonText(q.lease, ipPersonPointerMax, true) ||
		q.personID <= 0 || q.personRevision <= 0 || q.personRevision == math.MaxInt64 ||
		q.generation <= 0 || q.generation == math.MaxInt64 ||
		q.revision <= 0 || q.revision == math.MaxInt64 ||
		q.leaseGeneration <= 0 || q.leaseGeneration == math.MaxInt64 ||
		q.leaseUntilMS <= 0 ||
		!ipPersonText(q.evidenceID, ipPersonPointerMax, true) ||
		!ipPersonText(q.buildID, ipPersonPointerMax, true) ||
		!ipPersonText(q.publicationID, ipPersonPointerMax, true) ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.buildGeneration != q.generation ||
		q.contentRevision <= 0 || q.contentRevision == math.MaxInt64 ||
		q.searchJobID <= 0 || !ipPersonText(q.observedAt, 64, true) {
		return ErrInvalid
	}
	if _, ok := ipPersonTextTime(q.observedAt); !ok {
		return ErrInvalid
	}
	if err := ipPersonAcceptFactsShape(q.fields); err != nil {
		return err
	}
	if len(q.aliases) > ipPersonMaxChildren || len(q.external) > ipPersonMaxChildren ||
		len(q.aliases)+len(q.external) > ipPersonMaxChildren {
		return ErrInvalid
	}
	if !ipPersonEvidenceChildren(ipPersonEvidenceBeginRequest{providerID: q.providerID,
		aliases: q.aliases, external: q.external}) {
		return ErrInvalid
	}
	return nil
}

func ipPersonAcceptShape(q ipPersonAcceptRequest) error {
	if ipPersonObservationShape(q.observation) != nil {
		return ErrInvalid
	}
	if q.sourceRevision < 0 || q.sourceRevision == math.MaxInt64 ||
		(q.sourceRevision == 0) != (q.sourceIncarnation == [16]byte{}) {
		return ErrInvalid
	}
	return nil
}

func newIPPersonAcceptCommand(identity CommandIdentity, in ipPersonAcceptRequest) (*ipPersonAcceptCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipPersonAcceptShape(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPPersonAccept, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonAcceptCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipPersonAcceptedLookup resolves the ORIGINAL receipt before any fresh person,
// subject, policy or lease probe. Current authority is checked by the caller
// first. The stored receipt kind must be the exact purpose; an entity-purpose
// or person-child receipt kind is a conflict, never a replay.
func ipPersonAcceptedLookupReceipt[T any](ctx context.Context, turn *commandTurn, purpose CommandID,
	ref ReceiptReference, q ipPersonObservation, decode func(ipPersonReceipt) (T, error)) (out ipPersonAcceptedLookup[T], err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipPersonOwnerStmt, q.operationID, q.provider, q.providerID, q.personID)
	if err != nil {
		return out, err
	}
	var incarnation, subjectIncarnation, policyIncarnation []byte
	var nextAttempt sql.NullInt64
	err = row.Scan(&incarnation, &out.owner.lastSequence, &out.owner.ackSequence,
		&out.owner.generation, &out.owner.revision, &out.owner.phase, &out.owner.lease,
		&out.owner.leaseGeneration, &out.owner.leaseUntilMS, &nextAttempt, &out.owner.personID,
		&out.owner.personToken, &out.owner.personRevision, &out.owner.subjectID, &subjectIncarnation,
		&out.owner.subjectRevision, &policyIncarnation, &out.owner.policyRevision,
		&out.owner.consentRevision, &out.owner.provider, &out.owner.providerID, &out.owner.language,
		&out.owner.region, &out.owner.requestShape, &out.owner.libraryID, &out.owner.libraryKey,
		&out.owner.libraryRetired)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if err = ipPersonScanOwner(&out.owner, incarnation, subjectIncarnation, policyIncarnation, nextAttempt,
		q.provider, q.providerID, q.personID); err != nil {
		return out, err
	}
	out.owner.exists = true

	var kind CommandID
	var hasReceipt bool
	out.receipt, kind, hasReceipt, err = ipPersonReceiptRead(ctx, turn, q.operationID, ref)
	if err != nil {
		return out, err
	}
	out.hasReceipt = hasReceipt
	ownerFacts := receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
		lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.ackSequence}
	if out.hasReceipt {
		actual := receiptIdentity{command: ref.Identity}
		actual.command.Incarnation = out.receipt.incarnation
		copy(actual.command.OperationID[:], out.receipt.operationID[:])
		actual.digest = out.receipt.requestDigest
		found := receiptFacts{exists: true, identity: actual, kind: kind,
			resultCount: out.receipt.resultCount}
		copy(found.resultDigest[:], out.receipt.resultDigest[:])
		out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest},
			purpose, ownerFacts, found)
	} else {
		out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest},
			purpose, ownerFacts, receiptFacts{})
	}
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		value, e := decode(out.receipt)
		if e != nil {
			return out, errors.Join(ErrResolveRequired, e)
		}
		computed, e := canonicalResultDigest(ref.Identity, ref.RequestDigest, value)
		if e != nil || computed != out.receipt.resultDigest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.value, out.digest = value, computed
	}
	return out, nil
}

// ipPersonAcceptSaved rebuilds the COMPLETE saved acceptance from the original
// request and the saved receipt witnesses. It never reads the mutable person,
// source slot, publication, origin or search state.
func ipPersonAcceptSaved(q ipPersonAcceptRequest, identity CommandIdentity,
	r ipPersonReceipt) (ipPersonAcceptResult, error) {
	var zero ipPersonAcceptResult
	o := q.observation
	if r.kind != "accept" || r.operationID != identity.OperationID ||
		r.evidenceID != o.evidenceID || r.buildID != o.buildID ||
		r.publicationID != o.publicationID ||
		r.acceptedRevision != o.personRevision+1 ||
		r.resultCount != int64(len(o.fields)) {
		return zero, ErrResolveRequired
	}
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(o.fields)
	out := ipPersonAcceptResult{
		OperationID: o.operationID, PublicationID: o.publicationID, EvidenceID: o.evidenceID,
		Provider: o.provider, ProviderID: o.providerID, ObservedAt: o.observedAt,
		Lease: o.lease, Name: name, Biography: biography, BirthDate: birthDate,
		DeathDate: deathDate, BirthPlace: birthPlace, PersonID: o.personID,
		PersonRevision: o.personRevision + 1, AcceptedRevision: o.personRevision + 1,
		SourceRevision: q.sourceRevision + 1, OwnerGeneration: o.generation,
		ContentRevision: o.contentRevision, SearchJobID: o.searchJobID,
		AliasCount: int64(len(o.aliases)), ExternalCount: int64(len(o.external)),
		LeaseUntilMS: o.leaseUntilMS, AppliedMS: r.appliedMS,
	}
	if preflightIPPersonAcceptResult(out) != nil {
		return zero, ErrResolveRequired
	}
	return out, nil
}

// ipPersonSealedObservationRead proves the exact sealed source tuple: the ready
// build, the immutable evidence header, the ORIGINAL person seal receipt and
// the build's own seal proof FK. Missing, foreign or tampered parts refuse
// before the first write.
func ipPersonSealedObservationRead(ctx context.Context, turn *commandTurn, q ipPersonObservation,
	identity CommandIdentity) (out ipPersonSealedFact, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonAcceptBuildStmt, q.buildID)
	if err != nil {
		return out, err
	}
	var evidenceID, owner, state, expectedAlgorithm, provider, subjectKind, providerID string
	var ownerIncarnation, expectedCommitment, sealIncarnation []byte
	var ownerGeneration, generation, cursor, expectedRows, expectedBytes, stagedRows, stagedBytes, contentRevision int64
	var sealOwner sql.NullString
	var sealSequence sql.NullInt64
	var sealKind sql.NullString
	err = row.Scan(&evidenceID, &owner, &ownerIncarnation, &ownerGeneration, &generation, &state,
		&cursor, &expectedRows, &expectedBytes, &expectedAlgorithm, &expectedCommitment,
		&stagedRows, &stagedBytes, &contentRevision, &provider, &subjectKind, &providerID,
		&sealOwner, &sealIncarnation, &sealSequence, &sealKind)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if evidenceID != q.evidenceID || owner != q.operationID || len(ownerIncarnation) != 16 ||
		[16]byte(ownerIncarnation) != identity.Incarnation ||
		ownerGeneration != q.generation || generation != q.buildGeneration ||
		state != "ready" || cursor <= 0 || cursor != expectedRows || cursor != stagedRows ||
		stagedBytes != expectedBytes || contentRevision != q.contentRevision ||
		expectedAlgorithm != ipPersonManifest || len(expectedCommitment) != 32 ||
		provider != q.provider || subjectKind != "person" || providerID != q.providerID ||
		!sealOwner.Valid || sealOwner.String != q.operationID || len(sealIncarnation) != 16 ||
		!sealSequence.Valid || sealSequence.Int64 <= 0 || !sealKind.Valid ||
		sealKind.String != "evidence_seal" {
		return out, ErrConflict
	}
	out.buildEvidence, out.expectedAlgorithm = evidenceID, expectedAlgorithm
	out.ownerIncarnation = identity.Incarnation
	copy(out.expectedCommitment[:], expectedCommitment)
	out.ownerGeneration, out.generation = ownerGeneration, generation
	out.contentRevision, out.cursor = contentRevision, cursor
	out.expectedRows, out.expectedBytes = expectedRows, expectedBytes
	out.stagedRows, out.stagedBytes = stagedRows, stagedBytes
	out.provider, out.subjectKind, out.providerID = provider, subjectKind, providerID

	row, err = wc008DraftQuery(ctx, turn, ipPersonAcceptEvidenceStmt, q.evidenceID)
	if err != nil {
		return out, err
	}
	var sealed int64
	err = row.Scan(&out.subjectID, &out.provider, &out.subjectKind, &out.providerID, &out.parser,
		&out.language, &out.region, &out.requestShape, &out.digest, &out.observedAt,
		&out.sourceURL, &sealed, &out.title, &out.biography, &out.birthDate, &out.deathDate,
		&out.birthPlace)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.subjectID <= 0 || out.provider != q.provider || out.subjectKind != "person" ||
		out.providerID != q.providerID || len(out.parser) == 0 || len(out.language) == 0 ||
		len(out.region) == 0 || len(out.requestShape) == 0 || len(out.digest) != 64 ||
		len(out.sourceURL) == 0 || sealed != 1 {
		return out, ErrConflict
	}
	if _, ok := ipPersonTextTime(out.observedAt); !ok {
		return out, ErrConflict
	}
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(q.fields)
	if out.observedAt != q.observedAt || out.title != name || out.biography != biography ||
		out.birthDate != birthDate || out.deathDate != deathDate || out.birthPlace != birthPlace {
		return out, ErrConflict
	}

	row, err = wc008DraftQuery(ctx, turn, ipPersonAcceptSealStmt, q.operationID, q.evidenceID)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, commitment, resultDigest []byte
	var sealAcceptedRevision, appliedMS int64
	var receiptEvidence, receiptBuild, algorithm string
	var version, rows, bytes, pins int64
	err = row.Scan(&receiptIncarnation, &out.sealSequence, &resultDigest, &sealAcceptedRevision,
		&appliedMS, &receiptEvidence, &receiptBuild, &algorithm, &version, &commitment,
		&rows, &bytes, &pins)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(receiptIncarnation) != 16 || out.sealSequence <= 0 || len(resultDigest) != 32 ||
		sealAcceptedRevision != q.contentRevision || appliedMS < 0 ||
		receiptEvidence != q.evidenceID || receiptBuild != q.buildID ||
		algorithm != ipPersonManifest || version != ipPersonSealVersion ||
		len(commitment) != 32 || rows != expectedRows || bytes != expectedBytes || pins < 1 ||
		!ipEntityAcceptEqualBlob(commitment, expectedCommitment) ||
		!ipEntityAcceptEqualBlob(receiptIncarnation, sealIncarnation) ||
		out.sealSequence != sealSequence.Int64 {
		return out, ErrConflict
	}
	out.sealAcceptedRevision, out.sealRows, out.sealBytes, out.sealPins = sealAcceptedRevision, rows, bytes, pins
	out.sealAlgorithm = algorithm
	copy(out.sealIncarnation[:], receiptIncarnation)
	copy(out.sealCommitment[:], commitment)
	return out, nil
}

// ipPersonCohortRead reads the exact persisted alias/external cohort of one
// evidence body in ordinal order. It never selects by provider or person.
func ipPersonCohortRead(ctx context.Context, turn *commandTurn, evidenceID string) (
	aliases []ipPersonEvidenceAlias, external []ipPersonEvidenceExternal, err error) {
	step, ok := turn.steps[ipPersonAcceptAliasesStmt]
	if !ok {
		return nil, nil, ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, evidenceID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var ordinal int64
		var alias ipPersonEvidenceAlias
		if err = rows.Scan(&ordinal, &alias.RequestedID, &alias.CanonicalID, &alias.Alias,
			&alias.ObservedAt); err != nil {
			return nil, nil, errors.Join(err, rows.Close())
		}
		if ordinal != int64(len(aliases)) || len(aliases) >= ipPersonMaxChildren {
			return nil, nil, errors.Join(ErrConflict, rows.Close())
		}
		aliases = append(aliases, alias)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	step, ok = turn.steps[ipPersonAcceptExternalStmt]
	if !ok {
		return nil, nil, ErrInvalid
	}
	rows, err = turn.query(ctx, step.statementStep, evidenceID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var value ipPersonEvidenceExternal
		if err = rows.Scan(&value.Provider, &value.SubjectKind, &value.ProviderID); err != nil {
			return nil, nil, errors.Join(err, rows.Close())
		}
		if len(external) >= ipPersonMaxChildren {
			return nil, nil, errors.Join(ErrConflict, rows.Close())
		}
		external = append(external, value)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	return aliases, external, nil
}

// ipPersonCohortSame proves the typed cohort is the COMPLETE persisted cohort:
// exact order, exact count and every value.
func ipPersonCohortSame(aliases []ipPersonEvidenceAlias, external []ipPersonEvidenceExternal,
	wantAliases []ipPersonEvidenceAlias, wantExternal []ipPersonEvidenceExternal) error {
	if len(aliases) != len(wantAliases) || len(external) != len(wantExternal) {
		return ErrConflict
	}
	for i := range aliases {
		if aliases[i] != wantAliases[i] {
			return ErrConflict
		}
	}
	for i := range external {
		if external[i] != wantExternal[i] {
			return ErrConflict
		}
	}
	return nil
}

func ipPersonSourceRead(ctx context.Context, turn *commandTurn, personID int64,
	provider string) (out ipPersonSourceFact, exists bool, err error) {
	row, err := wc008DraftQuery(ctx, turn, ipPersonAcceptSourceStmt, personID, provider)
	if err != nil {
		return out, false, err
	}
	var incarnation []byte
	err = row.Scan(&incarnation, &out.revision, &out.providerID, &out.publication, &out.evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(incarnation) != 16 || out.revision <= 0 || len(out.providerID) == 0 ||
		out.publication.Valid != out.evidence.Valid {
		return out, false, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	return out, true, nil
}

func ipPersonFactsRead(ctx context.Context, turn *commandTurn, personID int64,
	token string) (out ipPersonFacts, err error) {
	row, err := wc008DraftQuery(ctx, turn, ipPersonAcceptPersonStmt, personID, token)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.revision, &out.name, &out.sortName, &out.biography, &out.birthPlace,
		&out.biographySource, &out.birthDate, &out.deathDate)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	}
	if err != nil {
		return out, err
	}
	if out.revision <= 0 || !ipPersonText(out.name, ipPersonTextMax, true) ||
		!ipPersonText(out.sortName, ipPersonTextMax, false) ||
		!ipPersonText(out.biography, ipPersonTextMax, false) ||
		!ipPersonText(out.birthPlace, ipPersonTextMax, false) ||
		!ipPersonText(out.biographySource, ipPersonPointerMax, false) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: out.birthDate}) ||
		!ipPersonEvidenceDate(EvidenceText{Present: true, Value: out.deathDate}) {
		return out, ErrResolveRequired
	}
	return out, nil
}

func ipPersonAcceptSearchRead(ctx context.Context, turn *commandTurn,
	q ipPersonObservation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonAcceptSearchStmt, q.searchJobID, q.personID)
	if err != nil {
		return err
	}
	var id, personID, generation int64
	var phase string
	if err = row.Scan(&id, &personID, &generation, &phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
		}
		return err
	}
	if id != q.searchJobID || personID != q.personID || generation <= 0 || len(phase) == 0 {
		return ErrResolveRequired
	}
	return nil
}

func ipPersonAcceptWritePublication(ctx context.Context, turn *commandTurn, q ipPersonObservation,
	source ipPersonSealedFact) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, err := sealStep(turn, ipPersonAcceptPublicationStmt)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, q.publicationID, q.personID, q.provider, q.providerID,
		q.evidenceID, q.operationID, q.operationID, source.sealIncarnation[:], source.sealSequence,
		q.personRevision+1, source.observedAt)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func ipPersonAcceptSourceWrite(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonAcceptRequest, existing ipPersonSourceFact, exists bool) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	o := q.observation
	var step statementStep
	if exists {
		step, err = sealStep(turn, ipPersonAcceptSourceCASStmt)
	} else {
		step, err = sealStep(turn, ipPersonAcceptSourceInsertStmt)
	}
	if err != nil {
		return out, err
	}
	if exists {
		row, e := turn.queryRow(ctx, step, o.publicationID, o.evidenceID, o.personID, o.provider,
			o.providerID, existing.incarnation[:], existing.revision,
			ipPersonNullableText(existing.publication), ipPersonNullableText(existing.evidence))
		if e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if e = row.Scan(&out); e != nil {
			return out, errors.Join(ErrConflict, e)
		}
		if out != existing.revision+1 {
			return out, ErrConflict
		}
		return out, nil
	}
	result, err := turn.exec(ctx, step, o.personID, o.provider, o.providerID,
		identity.OperationID[:], o.publicationID, o.evidenceID)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return out, errors.Join(ErrConflict, err)
	}
	return 1, nil
}

func ipPersonAcceptPersonWrite(ctx context.Context, turn *commandTurn, q ipPersonObservation,
	token string, before ipPersonFacts) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(q.fields)
	sortName := before.sortName
	if name != before.name {
		sortName = name
	}
	biographySource := ""
	if len(biography) > 0 {
		biographySource = q.provider
	}
	row, err := turn.queryRow(ctx, turn.steps[ipPersonAcceptPersonWriteStmt].statementStep,
		name, sortName, biography, birthDate, deathDate, birthPlace, biographySource,
		q.personID, token, before.revision, before.name, before.sortName, before.biography,
		before.birthDate, before.deathDate, before.birthPlace, before.biographySource)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = row.Scan(&out); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out != before.revision+1 {
		return out, ErrConflict
	}
	return out, nil
}

func ipPersonNullableText(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func ipPersonAcceptFactsWrite(ctx context.Context, turn *commandTurn, q ipPersonObservation,
	source ipPersonSealedFact, sourceRevision int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	for _, alias := range q.aliases {
		if len(alias.Alias) == 0 {
			continue
		}
		key := "name:" + strings.ToLower(strings.TrimSpace(alias.Alias))
		if !ipPersonText(key, ipPersonTextMax+5, true) {
			return ErrInvalid
		}
		step, e := sealStep(turn, ipPersonAcceptAliasStmt)
		if e != nil {
			return e
		}
		if _, e = turn.exec(ctx, step, key, q.personID); e != nil {
			return e
		}
	}
	for _, value := range q.external {
		step, e := sealStep(turn, ipPersonAcceptExternalWriteStmt)
		if e != nil {
			return e
		}
		if _, e = turn.exec(ctx, step, q.personID, value.Provider, value.ProviderID); e != nil {
			return e
		}
	}
	for _, field := range q.fields {
		step, e := sealStep(turn, ipPersonAcceptOriginStmt)
		if e != nil {
			return e
		}
		if _, e = turn.exec(ctx, step, q.personID, field.Name, q.provider, q.providerID,
			q.publicationID, q.evidenceID, source.sourceURL, source.observedAt,
			source.language, source.region, sourceRevision); e != nil {
			return e
		}
	}
	return nil
}

func ipPersonAcceptRetainProof(ctx context.Context, turn *commandTurn, q ipPersonObservation,
	source ipPersonSealedFact) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, err := sealStep(turn, ipPersonAcceptRetainProofStmt)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, q.operationID, source.sealIncarnation[:],
		source.sealSequence, q.evidenceID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func ipPersonAcceptAdvanceOwner(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonObservation, owner ipPersonOwner) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := turn.queryRow(ctx, turn.steps[ipPersonAcceptOwnerStmt].statementStep,
		identity.Sequence, q.operationID, identity.Incarnation[:], q.generation, q.revision,
		q.lease, q.leaseGeneration, q.leaseUntilMS, owner.lastSequence, owner.ackSequence)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = row.Scan(&out); err != nil || out != q.revision+1 {
		return out, errors.Join(ErrConflict, err)
	}
	return out, nil
}

func ipPersonAcceptRecord(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	request RequestDigest, purpose CommandID, value any, operationID, publicationID, evidenceID,
	buildID string, acceptedRevision, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	id := ipPersonAcceptRecordStmt
	if purpose == IPPersonDelta {
		id = ipPersonDeltaRecordStmt
	}
	step, err := sealStep(turn, id)
	if err != nil {
		return err
	}
	digest, err := canonicalResultDigest(identity, request, value)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, operationID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, digest[:], acceptedRevision,
		int64(len(ipPersonAcceptFactNames)), evidenceID, buildID, publicationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.Join(ErrResolveRequired, err)
	}
	return nil
}

// ipPersonAcceptFresh is the ONE accepting write path. Every native proof,
// including the sealed cohort, the captured person revision and the source
// slot CAS, is complete before the first write; any later failure rolls the
// whole acceptance back.
func ipPersonAcceptFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonAcceptRequest, request RequestDigest, owner ipPersonOwner,
	appliedMS int64) (out ipPersonAcceptResult, err error) {
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
	// The captured pre-accept person revision is the exact CAS witness; a newer
	// person revision is a new observation, never read as freshness here.
	if facts.revision != o.personRevision {
		return out, ErrConflict
	}
	existing, exists, err := ipPersonSourceRead(ctx, turn, o.personID, o.provider)
	if err != nil {
		return out, err
	}
	if exists {
		if existing.providerID != o.providerID ||
			existing.publication.Valid || existing.evidence.Valid ||
			q.sourceRevision <= 0 || existing.revision != q.sourceRevision ||
			existing.incarnation != q.sourceIncarnation {
			return out, ErrConflict
		}
	} else if q.sourceRevision != 0 || q.sourceIncarnation != [16]byte{} {
		return out, ErrConflict
	}
	if err = ipPersonAcceptSearchRead(ctx, turn, o); err != nil {
		return out, err
	}
	if err = ipPersonAcceptWritePublication(ctx, turn, o, source); err != nil {
		return out, err
	}
	sourceRevision, err := ipPersonAcceptSourceWrite(ctx, turn, identity, q, existing, exists)
	if err != nil {
		return out, err
	}
	if sourceRevision != q.sourceRevision+1 {
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
	name, biography, birthDate, deathDate, birthPlace := ipPersonAcceptFactsOf(o.fields)
	value := ipPersonAcceptResult{
		OperationID: o.operationID, PublicationID: o.publicationID, EvidenceID: o.evidenceID,
		Provider: o.provider, ProviderID: o.providerID, ObservedAt: source.observedAt,
		Lease: o.lease, Name: name, Biography: biography, BirthDate: birthDate,
		DeathDate: deathDate, BirthPlace: birthPlace, PersonID: o.personID,
		PersonRevision: personRevision, AcceptedRevision: personRevision,
		SourceRevision: sourceRevision, OwnerGeneration: o.generation,
		ContentRevision: o.contentRevision, SearchJobID: o.searchJobID,
		AliasCount: int64(len(o.aliases)), ExternalCount: int64(len(o.external)),
		LeaseUntilMS: o.leaseUntilMS, AppliedMS: appliedMS,
	}
	if err = ipPersonAcceptRecord(ctx, turn, identity, request, IPPersonAccept, value,
		value.OperationID, value.PublicationID, value.EvidenceID, o.buildID,
		value.AcceptedRevision, value.AppliedMS); err != nil {
		return out, err
	}
	return value, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved acceptance with zero new effects, before any fresh person,
// build, lease, source or policy probe.
func applyIPPersonAccept(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof,
	lifetime context.Context, b ipPolicyBudget, c *ipPersonAcceptCommand) (out ipPersonAcceptCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonAcceptCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonAccept {
		return zero, ErrInvalid
	}
	q := c.request
	if ipPersonAcceptShape(q) != nil {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPPersonAccept, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonAcceptResult, error) {
		return ipPersonAcceptSaved(q, c.identity, r)
	}
	lookup, err := ipPersonAcceptedLookupReceipt(ctx, turn, IPPersonAccept, c.reference(),
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
	value, err := ipPersonAcceptFresh(ctx, turn, c.identity, q, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonAcceptedLookupReceipt(ctx, turn, IPPersonAccept, c.reference(),
		o, decode)
	if err != nil || saved.decision != receiptReplay || saved.value != value ||
		saved.digest == (ResultDigest{}) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
