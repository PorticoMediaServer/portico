package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// IP person evidence seal 233 closes ONE complete IP-15 begin/append build. It
// is the only command that marks a person build ready or a person evidence body
// sealed, and it writes the one typed person proof that later acceptance reads.
// Person proof lives in metadata_person_receipts/metadata_person_builds; it
// never widens an EN or entity seal purpose, namespace or proof FK, and it never
// accepts a source or publishes person data.
const (
	ipPersonSealAlgorithm = "portico-ip-person-seal.sha256.v1"
	ipPersonSealVersion   = int64(1)
	// The begin manifest writes one count word per closed family outside the
	// header and child row bytes. A complete append set stages everything else.
	ipPersonSealCountBytes = int64(2 * 8)
)

// ipPersonSealRequest is the complete sealed person observation. The owner
// tuple is IP-14's exact live operation; the header, exact dates/place and the
// closed alias/external lists are the whole normalized source in exact order.
type ipPersonSealRequest struct {
	operationID, provider, providerID, lease string
	personID, generation, revision           int64
	leaseGeneration, leaseUntilMS            int64
	evidenceID, buildID                      string
	buildGeneration, contentRevision         int64
	header                                   EvidenceHeaderInput
	birthDate, deathDate, birthPlace         EvidenceText
	aliases                                  []ipPersonEvidenceAlias
	external                                 []ipPersonEvidenceExternal
}

// ipPersonSealManifest is derived only from the request, so typed replay never
// reads mutable build, evidence, lease or policy state.
type ipPersonSealManifest struct {
	commitment  [32]byte
	headerBytes int64
	rows, bytes int64
}

type ipPersonSealCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	header   EvidenceHeader
	manifest ipPersonSealManifest
	valid    bool
	request  ipPersonSealRequest
}

type ipPersonSealCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipPersonResult
	Digest  ResultDigest
}

type ipPersonSealEvidence struct {
	subjectID                                                        int64
	parser, provider, subjectKind, providerID                        string
	language, region, requestShape, observedAt, digest               string
	title, biography, biographySource, biographySourceURL, sourceURL string
	etag, lastModified, birthDate, deathDate, birthPlace             string
	sealed                                                           int64
}

func (c *ipPersonSealCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipPersonSealCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPersonSealCommand")
}

func (q ipPersonSealRequest) begin() ipPersonEvidenceBeginRequest {
	return ipPersonEvidenceBeginRequest{
		operationID: q.operationID, provider: q.provider, providerID: q.providerID,
		language: q.header.Language, region: q.header.Region,
		requestShape: q.header.RequestShape, lease: q.lease, personID: q.personID,
		subjectID:  q.header.SubjectID,
		generation: q.generation, revision: q.revision, leaseGeneration: q.leaseGeneration,
		leaseUntilMS: q.leaseUntilMS, buildID: q.buildID, buildGeneration: q.buildGeneration,
		header: q.header, birthDate: q.birthDate, deathDate: q.deathDate,
		birthPlace: q.birthPlace, aliases: q.aliases, external: q.external,
	}
}

func ipPersonEvidenceSealShape(q ipPersonSealRequest) (EvidenceHeader, ipPersonSealManifest, error) {
	var zeroHeader EvidenceHeader
	var zero ipPersonSealManifest
	if len(q.operationID) != 32 || !ipPolicyProviderCode(q.provider) ||
		!ipPersonText(q.providerID, ipPersonProviderIDMax, true) ||
		!ipPersonText(q.lease, ipPersonPointerMax, true) || q.personID <= 0 ||
		q.generation <= 0 || q.generation == math.MaxInt64 ||
		q.revision <= 0 || q.revision == math.MaxInt64 ||
		q.leaseGeneration <= 0 || q.leaseGeneration == math.MaxInt64 ||
		q.leaseUntilMS <= 0 ||
		!ipPersonText(q.evidenceID, ipPersonPointerMax, true) ||
		!ipPersonText(q.buildID, ipPersonPointerMax, true) ||
		q.buildGeneration <= 0 || q.buildGeneration == math.MaxInt64 ||
		q.buildGeneration != q.generation ||
		q.contentRevision <= 0 || q.contentRevision == math.MaxInt64 ||
		q.header.ID != q.evidenceID ||
		!ipPersonEvidenceDate(q.birthDate) || !ipPersonEvidenceDate(q.deathDate) ||
		!ipPersonEvidenceOptionalText(q.birthPlace) ||
		!ipPersonEvidenceHeaderAllowed(q.header, q.begin()) ||
		!ipPersonEvidenceChildren(q.begin()) {
		return zeroHeader, zero, ErrInvalid
	}
	header, err := NewEvidenceHeader(q.header)
	if err != nil {
		return zeroHeader, zero, err
	}
	commitment, total, headerBytes, err := ipPersonEvidenceManifest(q.begin(), header)
	if err != nil {
		return zeroHeader, zero, err
	}
	rows := 1 + int64(len(q.aliases)+len(q.external))
	if rows > 1+ipPersonMaxChildren || total <= 0 || total > ipPersonMaxBytes ||
		headerBytes <= 0 || headerBytes >= total || commitment == [32]byte{} {
		return zeroHeader, zero, ErrInvalid
	}
	return header, ipPersonSealManifest{commitment: commitment, headerBytes: headerBytes,
		rows: rows, bytes: total}, nil
}

func newIPPersonSealCommand(identity CommandIdentity, in ipPersonSealRequest) (*ipPersonSealCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	header, manifest, err := ipPersonEvidenceSealShape(in)
	if err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPPersonSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonSealCommand{identity: identity, request: in, header: header,
		manifest: manifest, digest: digest, valid: true}, nil
}

// ipPersonSealSaved rebuilds the COMPLETE saved seal from the exact request
// manifest and the retained receipt proof. It never reads current evidence,
// build, lease or policy state.
func ipPersonSealSaved(q ipPersonSealRequest, m ipPersonSealManifest, identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	if r.kind != "evidence_seal" || r.acceptedRevision != q.contentRevision+1 ||
		r.operationID != identity.OperationID || r.evidenceID != q.evidenceID ||
		r.buildID != q.buildID || r.resultCount != 1 ||
		r.sealAlgorithm != ipPersonSealAlgorithm || r.sealVersion != ipPersonSealVersion ||
		r.sealRows != m.rows || r.sealBytes != m.bytes || r.sealCommitment != m.commitment {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "seal", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: q.revision + 1, LeaseGeneration: q.leaseGeneration,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS, EvidenceID: q.evidenceID,
		BuildID: q.buildID, SubjectKind: "person", ParserVersion: q.header.ParserVersion,
		BuildGeneration: q.buildGeneration, MaterializedRows: m.rows, ResultBytes: m.bytes,
		ResultCount: 1, Commitment: m.commitment}, nil
}

func ipPersonSealEvidenceRead(ctx context.Context, turn *commandTurn, evidenceID string) (out ipPersonSealEvidence, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonSealEvidenceStmt, evidenceID)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.subjectID, &out.parser, &out.provider, &out.subjectKind, &out.providerID,
		&out.language, &out.region, &out.requestShape, &out.observedAt, &out.digest, &out.sealed,
		&out.title, &out.biography, &out.biographySource, &out.biographySourceURL, &out.sourceURL,
		&out.etag, &out.lastModified, &out.birthDate, &out.deathDate, &out.birthPlace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if out.subjectID <= 0 || out.subjectKind != "person" || !ipPolicyProviderCode(out.provider) ||
		!ipPersonText(out.parser, ipPersonTextMax, true) ||
		!ipPersonText(out.providerID, ipPersonProviderIDMax, true) ||
		!ipPersonText(out.language, ipPersonPointerMax, true) || !ipPolicyRegion(out.region) ||
		!ipPersonText(out.requestShape, ipPersonPointerMax, true) ||
		!ipPersonText(out.observedAt, 64, true) || len(out.digest) != 64 ||
		(out.sealed != 0 && out.sealed != 1) {
		return out, ErrResolveRequired
	}
	return out, nil
}

// ipPersonSealAliases proves the exact persisted alias rows: every ordinal from
// zero to the declared count exists once and carries exactly the sealed values.
func ipPersonSealAliases(ctx context.Context, turn *commandTurn, evidenceID string, want []ipPersonEvidenceAlias) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[ipPersonSealAliasesStmt]
	if !ok {
		return ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, evidenceID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	type aliasRow struct {
		requested, canonical, alias, observed string
	}
	stored := make(map[int64]aliasRow)
	for rows.Next() {
		var ordinal int64
		var value aliasRow
		if err = rows.Scan(&ordinal, &value.requested, &value.canonical, &value.alias, &value.observed); err != nil {
			return err
		}
		if ordinal < 0 || int64(len(stored)) >= ipPersonMaxChildren {
			return ErrConflict
		}
		if _, duplicate := stored[ordinal]; duplicate {
			return ErrConflict
		}
		stored[ordinal] = value
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(stored) != len(want) {
		return ErrConflict
	}
	for i, expected := range want {
		value, ok := stored[int64(i)]
		if !ok || value.requested != expected.RequestedID || value.canonical != expected.CanonicalID ||
			value.alias != expected.Alias || value.observed != expected.ObservedAt {
			return ErrConflict
		}
	}
	return nil
}

// ipPersonSealExternal proves the exact persisted external-ID set: the same
// count, the same person-kind tuples and no extra row.
func ipPersonSealExternal(ctx context.Context, turn *commandTurn, evidenceID string, want []ipPersonEvidenceExternal) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	step, ok := turn.steps[ipPersonSealExternalStmt]
	if !ok {
		return ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, evidenceID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	type externalRow struct{ provider, kind, id string }
	stored := make(map[externalRow]bool)
	for rows.Next() {
		var value externalRow
		if err = rows.Scan(&value.provider, &value.kind, &value.id); err != nil {
			return err
		}
		if value.kind != "person" || len(stored) >= ipPersonMaxChildren || stored[value] {
			return ErrConflict
		}
		stored[value] = true
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(stored) != len(want) {
		return ErrConflict
	}
	for _, expected := range want {
		if !stored[externalRow{provider: expected.Provider, kind: expected.SubjectKind, id: expected.ProviderID}] {
			return ErrConflict
		}
	}
	return nil
}

func ipPersonSealFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipPersonSealRequest,
	m ipPersonSealManifest, request RequestDigest, owner ipPersonOwner, appliedMS int64) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	build, err := ipPersonBuildRead(ctx, turn, q.buildID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	// Complete means every declared row and row byte is staged and the only
	// remaining begin-manifest bytes are the two closed family count words.
	if build.evidenceID != q.evidenceID || build.owner != q.operationID ||
		build.ownerIncarnation != identity.Incarnation || build.ownerGeneration != q.generation ||
		build.generation != q.buildGeneration || build.provider != q.provider ||
		build.subjectKind != "person" || build.providerID != q.providerID ||
		build.state != "building" || build.cursor != build.stagedRows ||
		build.cursor != m.rows || build.expectedRows != m.rows ||
		build.contentRevision != q.contentRevision || build.algorithm != ipPersonManifest ||
		build.commitment != m.commitment || build.expectedBytes != m.bytes ||
		build.stagedBytes+ipPersonSealCountBytes != build.expectedBytes {
		return out, ErrConflict
	}
	evidence, err := ipPersonSealEvidenceRead(ctx, turn, q.evidenceID)
	if err != nil {
		return out, err
	}
	if evidence.subjectID != q.header.SubjectID || evidence.parser != q.header.ParserVersion ||
		evidence.provider != q.provider || evidence.providerID != q.providerID ||
		evidence.language != q.header.Language || evidence.region != q.header.Region ||
		evidence.requestShape != q.header.RequestShape || evidence.observedAt != q.header.ObservedAt ||
		evidence.digest != fmt.Sprintf("%x", q.header.ObservationDigest[:]) ||
		evidence.title != q.header.Title || evidence.biography != q.header.Biography ||
		evidence.biographySource != q.header.BiographySource ||
		evidence.biographySourceURL != q.header.BiographySourceURL ||
		evidence.sourceURL != q.header.SourceURL || evidence.etag != q.header.ETag ||
		evidence.lastModified != q.header.LastModified ||
		evidence.birthDate != q.birthDate.Value || evidence.deathDate != q.deathDate.Value ||
		evidence.birthPlace != q.birthPlace.Value || evidence.sealed != 0 {
		return out, ErrConflict
	}
	if err = ipPersonSealAliases(ctx, turn, q.evidenceID, q.aliases); err != nil {
		return out, err
	}
	if err = ipPersonSealExternal(ctx, turn, q.evidenceID, q.external); err != nil {
		return out, err
	}
	if build.charge > math.MaxInt64-ipPersonSealCountBytes {
		return out, ErrCap
	}
	revision := q.contentRevision + 1
	bytes := build.stagedBytes + ipPersonSealCountBytes
	value := ipPersonResult{Kind: "seal", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: q.revision + 1, LeaseGeneration: q.leaseGeneration,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: appliedMS, EvidenceID: q.evidenceID,
		BuildID: q.buildID, SubjectKind: "person", ParserVersion: q.header.ParserVersion,
		BuildGeneration: q.buildGeneration, MaterializedRows: m.rows, ResultBytes: m.bytes,
		ResultCount: 1, Commitment: m.commitment}
	digest, err := ipPersonResultDigest(identity, request, value)
	if err != nil {
		return out, err
	}
	// The build proof FK requires the retained seal receipt first.
	if err = execIPEntityEvidence(ctx, turn, ipPersonSealRecordStmt, q.operationID,
		identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:], appliedMS,
		digest[:], revision, q.evidenceID, q.buildID, ipPersonSealAlgorithm, ipPersonSealVersion,
		m.commitment[:], m.rows, m.bytes); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = execIPEntityEvidence(ctx, turn, ipPersonSealEvidenceUpd, q.evidenceID,
		q.header.SubjectID, q.header.ParserVersion, q.provider, q.providerID, q.header.Language,
		q.header.Region, q.header.RequestShape, fmt.Sprintf("%x", q.header.ObservationDigest[:]),
		q.header.ObservedAt); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := turn.queryRow(ctx, turn.steps[ipPersonSealBuildStmt].statementStep,
		q.operationID, identity.Incarnation[:], identity.Sequence, m.rows, bytes, ipPersonSealCountBytes,
		q.buildID, q.evidenceID, q.provider, q.providerID, q.operationID, identity.Incarnation[:],
		q.generation, q.buildGeneration, build.cursor, build.stagedRows, build.stagedBytes,
		build.charge, build.contentRevision)
	if err != nil {
		return out, ErrConflict
	}
	var contentRevision, stagedRows, stagedBytes, charge int64
	if err = row.Scan(&contentRevision, &stagedRows, &stagedBytes, &charge); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if contentRevision != revision || stagedRows != m.rows || stagedBytes != bytes ||
		charge != build.charge+ipPersonSealCountBytes {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[ipPersonSealOwnerStmt].statementStep,
		identity.Sequence, q.operationID, identity.Incarnation[:], q.generation, q.revision,
		q.lease, q.leaseGeneration, q.leaseUntilMS, owner.lastSequence, owner.ackSequence)
	if err != nil {
		return out, ErrConflict
	}
	var ownerRevision int64
	if err = row.Scan(&ownerRevision); err != nil || ownerRevision != q.revision+1 {
		return out, errors.Join(ErrConflict, err)
	}
	return value, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved seal with zero new effects, before any fresh build, lease,
// evidence or source probe. Seal is the only person command that finalizes the
// build proof and the owner phase; it accepts and publishes nothing.
func applyIPPersonSeal(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonSealCommand) (out ipPersonSealCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonSealCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonSeal {
		return zero, ErrInvalid
	}
	q := c.request
	header, manifest, err := ipPersonEvidenceSealShape(q)
	if err != nil || header.input != c.header.input || manifest != c.manifest {
		return zero, ErrInvalid
	}
	actual, err := canonicalRequestDigest(IPPersonSeal, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonSealSaved(q, manifest, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonSeal, c.reference(),
		q.operationID, q.provider, q.providerID, q.personID, false, decode)
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
	if lookup.owner.phase != "building" {
		return zero, ErrConflict
	}
	if err = ipPersonOwnerFresh(ctx, turn, lookup.owner, q.provider, q.providerID, q.lease,
		q.personID, q.generation, q.revision, q.leaseGeneration, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipPersonSealFresh(ctx, turn, c.identity, q, manifest, c.digest, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonSeal, c.reference(),
		q.operationID, q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest == (ResultDigest{}) || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
