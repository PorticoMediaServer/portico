package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// IP person evidence BEGIN 209 (with the 206/207/208 lifecycle) stages one
// bounded, normalized, immutable person observation under an already admitted
// person operation owner. BEGIN materializes the generic evidence header and the
// single person build; it never appends children, seals, accepts or publishes.
// IP-15 owns person child append/seal.
const (
	ipPersonManifest    = "portico-ip-person-source-sha256-v1"
	ipPersonMaxChildren = 32
	ipPersonMaxBytes    = 64 << 10
	ipPersonTextMax     = 4096
	ipPersonDateMax     = 10
)

type ipPersonEvidenceAlias struct {
	RequestedID, CanonicalID, Alias, ObservedAt string
}

type ipPersonEvidenceExternal struct {
	Provider, SubjectKind, ProviderID string
}

type ipPersonEvidenceBeginRequest struct {
	operationID, provider, providerID, language, region, requestShape, lease string
	personID, subjectID, generation, revision, leaseGeneration, leaseUntilMS int64
	buildID                                                                  string
	buildGeneration                                                          int64
	header                                                                   EvidenceHeaderInput
	birthDate, deathDate, birthPlace                                         EvidenceText
	aliases                                                                  []ipPersonEvidenceAlias
	external                                                                 []ipPersonEvidenceExternal
	expectedRows, expectedBytes                                              int64
	expectedCommitment                                                       [32]byte
}

func (q ipPersonEvidenceBeginRequest) children() int {
	return len(q.aliases) + len(q.external)
}

func ipPersonEvidenceHeaderAllowed(h EvidenceHeaderInput, q ipPersonEvidenceBeginRequest) bool {
	if h.SubjectKind != "person" || !ipPolicyProviderCode(h.Provider) ||
		h.SubjectID != q.subjectID || h.Provider != q.provider || h.ProviderID != q.providerID ||
		h.Language != q.language || h.Region != q.region || h.RequestShape != q.requestShape ||
		!ipPersonText(h.ID, ipPersonPointerMax, true) || !ipPersonText(h.ParserVersion, ipPersonTextMax, true) ||
		!ipPersonText(h.ObservedAt, 64, true) || !ipPersonText(h.SourceURL, ipPersonTextMax, true) ||
		h.ObservationDigest == [32]byte{} || h.Year != 0 || h.LengthMillis.Present ||
		h.BeginYear.Present || h.EndYear.Present || h.ArtistType.Present ||
		h.OriginalTitle != "" || h.Overview != "" || h.Tagline != "" || h.ContentRating != "" ||
		h.Studio != "" || h.Network != "" || h.Country != "" || h.ReleaseDate != "" ||
		h.Artist != "" || h.WikidataID != "" || h.Disambiguation != "" ||
		h.ReleaseGroupID != "" || h.GroupTitle != "" || h.PrimaryType != "" || h.Barcode != "" {
		return false
	}
	prefix := h.Provider + "."
	return strings.HasPrefix(h.ParserVersion, prefix) && strings.Contains(h.ParserVersion, ".person.") &&
		strings.HasPrefix(h.RequestShape, prefix) && strings.Contains(h.RequestShape, ".person.") &&
		h.ParserVersion != h.RequestShape &&
		ipPersonText(h.ETag, ipPersonTextMax, false) && ipPersonText(h.LastModified, 64, false) &&
		ipPersonText(h.BiographySource, ipPersonPointerMax, false) &&
		ipPersonText(h.BiographySourceURL, ipPersonTextMax, false)
}

func ipPersonEvidenceDate(value EvidenceText) bool {
	if !value.Present {
		return value.Value == ""
	}
	if value.Value == "" {
		return true
	}
	if len(value.Value) != ipPersonDateMax {
		return false
	}
	day, err := time.Parse("2006-01-02", value.Value)
	return err == nil && day.Format("2006-01-02") == value.Value
}

func ipPersonEvidenceOptionalText(value EvidenceText) bool {
	return (value.Present || value.Value == "") && ipPersonText(value.Value, ipPersonTextMax, false)
}

func ipPersonEvidenceChildren(q ipPersonEvidenceBeginRequest) bool {
	if q.children() > ipPersonMaxChildren {
		return false
	}
	for _, alias := range q.aliases {
		if !ipPersonText(alias.RequestedID, ipPersonProviderIDMax, true) ||
			alias.CanonicalID != q.providerID ||
			!ipPersonText(alias.Alias, ipPersonTextMax, false) ||
			!ipPersonText(alias.ObservedAt, 64, true) {
			return false
		}
		if _, ok := ipPersonTextTime(alias.ObservedAt); !ok {
			return false
		}
	}
	for i, external := range q.external {
		if !ipPersonText(external.Provider, ipPersonPointerMax, true) ||
			external.SubjectKind != "person" ||
			!ipPersonText(external.ProviderID, ipPersonProviderIDMax, true) {
			return false
		}
		if i > 0 && !ipPersonExternalLess(q.external[i-1], external) {
			return false
		}
	}
	return true
}

func ipPersonExternalLess(a, b ipPersonEvidenceExternal) bool {
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	if a.SubjectKind != b.SubjectKind {
		return a.SubjectKind < b.SubjectKind
	}
	return a.ProviderID < b.ProviderID
}

// ipPersonEvidenceManifest rebuilds the exact immutable person source wire. It
// returns the whole-evidence commitment, its byte length, and the exact header
// prefix length persisted by BEGIN. Child pages add the remaining bytes.
func ipPersonEvidenceManifest(q ipPersonEvidenceBeginRequest, header EvidenceHeader) ([32]byte, int64, int64, error) {
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.text(ipPersonManifest)
	e.text(header.input.ParserVersion)
	e.bytes(header.input.ObservationDigest[:])
	e.number(uint64(q.personID))
	e.text(q.provider)
	e.text("person")
	e.text(q.providerID)
	e.text(q.language)
	e.text(q.region)
	e.text(q.requestShape)
	e.number(0)
	e.text(header.input.Title)
	e.text(header.input.Biography)
	e.flag(q.birthDate.Present)
	e.text(q.birthDate.Value)
	e.flag(q.deathDate.Present)
	e.text(q.deathDate.Value)
	e.flag(q.birthPlace.Present)
	e.text(q.birthPlace.Value)
	e.text(header.input.ObservedAt)
	e.text(header.input.SourceURL)
	headerBytes := int64(e.written)
	e.number(uint64(len(q.aliases)))
	for i, alias := range q.aliases {
		e.number(1)
		e.number(uint64(i))
		e.text(alias.RequestedID)
		e.text(alias.CanonicalID)
		e.text(alias.Alias)
		e.text(alias.ObservedAt)
	}
	e.number(uint64(len(q.external)))
	for _, external := range q.external {
		e.number(2)
		e.text(external.Provider)
		e.text(external.SubjectKind)
		e.text(external.ProviderID)
	}
	if e.err != nil {
		return [32]byte{}, 0, 0, e.err
	}
	var commitment [32]byte
	copy(commitment[:], e.h.Sum(nil))
	return commitment, int64(e.written), headerBytes, nil
}

func ipPersonEvidenceShapeBegin(q ipPersonEvidenceBeginRequest) (EvidenceHeader, int64, error) {
	var zero EvidenceHeader
	if len(q.operationID) != 32 || !ipPolicyProviderCode(q.provider) ||
		!ipPersonText(q.providerID, ipPersonProviderIDMax, true) ||
		!ipPersonText(q.language, ipPersonPointerMax, true) || !ipPolicyRegion(q.region) ||
		!ipPersonText(q.requestShape, ipPersonPointerMax, true) ||
		!ipPersonText(q.lease, ipPersonPointerMax, true) || q.personID <= 0 || q.subjectID <= 0 || q.generation <= 0 ||
		q.generation == math.MaxInt64 || q.revision <= 0 || q.revision == math.MaxInt64 ||
		q.leaseGeneration <= 0 || q.leaseGeneration == math.MaxInt64 || q.leaseUntilMS <= 0 ||
		!ipPersonText(q.buildID, ipPersonPointerMax, true) || q.buildGeneration <= 0 ||
		q.buildGeneration == math.MaxInt64 || q.buildGeneration != q.generation ||
		q.expectedRows <= 0 || q.expectedBytes <= 0 ||
		q.expectedBytes > ipPersonMaxBytes || q.expectedCommitment == [32]byte{} ||
		!ipPersonEvidenceDate(q.birthDate) || !ipPersonEvidenceDate(q.deathDate) ||
		!ipPersonEvidenceOptionalText(q.birthPlace) {
		return zero, 0, ErrInvalid
	}
	if !ipPersonEvidenceHeaderAllowed(q.header, q) || !ipPersonEvidenceChildren(q) {
		return zero, 0, ErrInvalid
	}
	header, err := NewEvidenceHeader(q.header)
	if err != nil {
		return zero, 0, err
	}
	commitment, total, headerBytes, err := ipPersonEvidenceManifest(q, header)
	if err != nil {
		return zero, 0, err
	}
	if commitment != q.expectedCommitment || total != q.expectedBytes ||
		q.expectedRows != 1+int64(q.children()) || headerBytes <= 0 {
		return zero, 0, ErrInvalid
	}
	return header, headerBytes, nil
}

func newIPPersonEvidenceBeginCommand(identity CommandIdentity, in ipPersonEvidenceBeginRequest) (*ipPersonCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if _, _, err := ipPersonEvidenceShapeBegin(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPPersonEvidenceBegin, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonCommand{kind: "begin", identity: identity, begin: in, digest: digest, valid: true}, nil
}

func ipPersonEvidenceBeginSaved(q ipPersonEvidenceBeginRequest, header EvidenceHeader, headerBytes int64,
	identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	if r.kind != "begin" || r.evidenceID != header.ID() || r.buildID != q.buildID ||
		r.acceptedRevision != 1 || r.resultCount != 1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "begin", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: q.revision + 1, LeaseGeneration: q.leaseGeneration, LeaseUntilMS: q.leaseUntilMS,
		AppliedMS: r.appliedMS, EvidenceID: header.ID(), BuildID: q.buildID, SubjectKind: "person",
		ParserVersion: header.input.ParserVersion, BuildGeneration: q.buildGeneration,
		MaterializedRows: 1, ResultBytes: headerBytes, ResultCount: 1,
		Commitment: q.expectedCommitment}, nil
}

type ipPersonBuild struct {
	evidenceID, owner, state, algorithm, provider, subjectKind, providerID string
	ownerIncarnation                                                       [16]byte
	commitment                                                             [32]byte
	ownerGeneration, generation                                            int64
	cursor, expectedRows, expectedBytes                                    int64
	stagedRows, stagedBytes, charge, contentRevision                       int64
}

func ipPersonBuildRead(ctx context.Context, turn *commandTurn, buildID string) (out ipPersonBuild, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonBuildStmt, buildID)
	if err != nil {
		return out, err
	}
	var incarnation, commitment []byte
	err = row.Scan(&out.evidenceID, &out.owner, &incarnation, &out.ownerGeneration, &out.generation,
		&out.state, &out.cursor, &out.expectedRows, &out.expectedBytes, &out.algorithm,
		&commitment, &out.stagedRows, &out.stagedBytes, &out.charge, &out.contentRevision,
		&out.provider, &out.subjectKind, &out.providerID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sql.ErrNoRows
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || len(commitment) != 32 || out.ownerGeneration <= 0 ||
		out.generation <= 0 || out.cursor < 0 || out.expectedRows <= 0 || out.expectedBytes <= 0 ||
		out.stagedRows < 0 || out.stagedBytes < 0 || out.charge < 0 || out.contentRevision <= 0 ||
		!ipPersonText(out.evidenceID, ipPersonPointerMax, true) ||
		!ipPersonText(out.owner, ipPersonPointerMax, true) ||
		!ipPersonText(out.algorithm, ipPersonTextMax, true) ||
		out.subjectKind != "person" || !ipPolicyProviderCode(out.provider) ||
		!ipPersonText(out.providerID, ipPersonProviderIDMax, true) {
		return out, ErrResolveRequired
	}
	switch out.state {
	case "building", "ready", "retiring", "done":
	default:
		return out, ErrResolveRequired
	}
	copy(out.ownerIncarnation[:], incarnation)
	copy(out.commitment[:], commitment)
	return out, nil
}

func ipPersonBuildOwnerCount(ctx context.Context, turn *commandTurn, owner string, incarnation [16]byte) (n int64, err error) {
	row, err := wc008DraftQuery(ctx, turn, ipPersonBuildOwnerStmt, owner, incarnation[:])
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func ipPersonBeginFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonEvidenceBeginRequest, header EvidenceHeader, headerBytes int64, owner ipPersonOwner,
	appliedMS int64) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if header.input.SubjectID != owner.subjectID {
		return out, ErrConflict
	}
	if _, e := ipPersonBuildRead(ctx, turn, q.buildID); e == nil {
		return out, ErrConflict
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if n, e := ipPersonBuildOwnerCount(ctx, turn, q.operationID, identity.Incarnation); e != nil {
		return out, e
	} else if n != 0 {
		return out, ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, ipPersonReceiptReverseStmt, header.ID())
	if err != nil {
		return out, err
	}
	var existing int64
	if err = row.Scan(&existing); err != nil || existing != 0 {
		return out, errors.Join(ErrConflict, err)
	}
	if err = execIPEntityEvidence(ctx, turn, ipPersonInsertEvidenceStmt, header.input.ID, header.input.SubjectID,
		header.input.ParserVersion, header.input.Provider, header.input.ProviderID, header.input.Language,
		header.input.Region, header.input.RequestShape, fmt.Sprintf("%x", header.input.ObservationDigest),
		header.input.ObservedAt, header.input.SourceURL, header.input.ETag, header.input.LastModified,
		header.input.Title, header.input.Biography, header.input.BiographySource,
		header.input.BiographySourceURL, q.birthDate.Value, q.deathDate.Value, q.birthPlace.Value); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if err = execIPEntityEvidence(ctx, turn, ipPersonInsertBuildStmt, q.buildID, header.ID(), q.provider,
		q.providerID, q.operationID, identity.Incarnation[:], q.generation, q.buildGeneration,
		q.expectedRows, q.expectedBytes, ipPersonManifest, q.expectedCommitment[:],
		headerBytes, headerBytes); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = turn.queryRow(ctx, turn.steps[ipPersonAdvanceOwnerStmt].statementStep,
		identity.Sequence, q.operationID, identity.Incarnation[:], q.generation, q.revision,
		q.lease, q.leaseGeneration, q.leaseUntilMS, owner.lastSequence, owner.ackSequence)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	var revision int64
	if err = row.Scan(&revision); err != nil || revision != q.revision+1 {
		return out, errors.Join(ErrConflict, err)
	}
	return ipPersonResult{Kind: "begin", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: revision, LeaseGeneration: q.leaseGeneration, LeaseUntilMS: q.leaseUntilMS,
		AppliedMS: appliedMS, EvidenceID: header.ID(), BuildID: q.buildID, SubjectKind: "person",
		ParserVersion: header.input.ParserVersion, BuildGeneration: q.buildGeneration,
		MaterializedRows: 1, ResultBytes: headerBytes, ResultCount: 1,
		Commitment: q.expectedCommitment}, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved BEGIN with zero new effects, before any fresh lease,
// person or build probe. One build ever exists per operation/incarnation.
func applyIPPersonEvidenceBegin(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonCommand) (out ipPersonCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonCommandResult
	if c == nil || !c.valid || c.kind != "begin" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonEvidenceBegin {
		return zero, ErrInvalid
	}
	q := c.begin
	header, headerBytes, err := ipPersonEvidenceShapeBegin(q)
	if err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPersonEvidenceBegin, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonEvidenceBeginSaved(q, header, headerBytes, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonEvidenceBegin, c.reference(),
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
	if lookup.owner.phase != "claimed" {
		return zero, ErrConflict
	}
	if err = ipPersonOwnerFresh(ctx, turn, lookup.owner, q.provider, q.providerID, q.lease,
		q.personID, q.generation, q.revision, q.leaseGeneration, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipPersonBeginFresh(ctx, turn, c.identity, q, header, headerBytes, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipPersonRecordReceipt(ctx, turn, q.operationID, c.identity, c.digest, value, "begin",
		1, header.ID(), q.buildID)
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonEvidenceBegin, c.reference(),
		q.operationID, q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
