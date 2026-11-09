package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// IP person evidence append 232 materializes one bounded page of one declared
// person child family under the exact live person operation that admitted
// BEGIN 209. Person has its own closed family set, alias then external IDs; it
// is deliberately not the entity family of 211, so person and entity receipts
// remain distinct even when numeric IDs, incarnations and sequences coincide.
// Append never marks the build ready, seals the evidence, accepts a source or
// publishes person data.
const (
	ipPersonFamilyAlias    = "alias"
	ipPersonFamilyExternal = "external"
)

type ipPersonFamilySpec struct {
	name    string
	maxPage int
}

// The order is part of the person begin manifest and the page family index
// carried by append, so a seal accounts for exactly these two families.
var ipPersonFamilySpecs = [...]ipPersonFamilySpec{
	{name: ipPersonFamilyAlias, maxPage: ipPersonMaxChildren},
	{name: ipPersonFamilyExternal, maxPage: ipPersonMaxChildren},
}

func ipPersonFamilyIndex(name string) int {
	for i := range ipPersonFamilySpecs {
		if ipPersonFamilySpecs[i].name == name {
			return i
		}
	}
	return -1
}

// buildRevision is the person BUILD's content revision, not the owner revision:
// BEGIN opens the build at 1 and every append returns the next value, so pages
// chain from 1 without re-reading mutable state. revision stays the captured
// owner revision for the lease/authority CAS. The child page carries no
// language/region/request shape of its own: those stay the exact captured
// namespace on the person operation and are re-proved by ipPersonOwnerFresh, so
// a page cannot retarget another person, provider or locale.
type ipPersonEvidenceAppendRequest struct {
	operationID, provider, providerID, lease string
	personID, generation, revision           int64
	leaseGeneration, leaseUntilMS            int64
	evidenceID, buildID                      string
	buildGeneration, buildRevision           int64
	family                                   string
	familyIndex, familyStart, familyRows     int64
	afterOrdinal, materializedBefore         int64
	aliases                                  []ipPersonEvidenceAlias
	external                                 []ipPersonEvidenceExternal
}

func (q ipPersonEvidenceAppendRequest) counts() [2]int {
	return [2]int{len(q.aliases), len(q.external)}
}

// page returns the one selected family page and its row count. Any row in the
// other family, or an out-of-range index, is refused.
func (q ipPersonEvidenceAppendRequest) page(family int) (any, int) {
	counts := q.counts()
	for i, count := range counts {
		if count > 0 && i != family {
			return nil, -1
		}
	}
	switch family {
	case 0:
		return q.aliases, len(q.aliases)
	case 1:
		return q.external, len(q.external)
	}
	return nil, -1
}

func ipPersonEvidenceAppendChildren(q ipPersonEvidenceAppendRequest) error {
	for _, alias := range q.aliases {
		if !ipPersonText(alias.RequestedID, ipPersonProviderIDMax, true) ||
			alias.CanonicalID != q.providerID ||
			!ipPersonText(alias.Alias, ipPersonTextMax, false) ||
			!ipPersonText(alias.ObservedAt, 64, true) {
			return ErrInvalid
		}
		if _, ok := ipPersonTextTime(alias.ObservedAt); !ok {
			return ErrInvalid
		}
	}
	for i, external := range q.external {
		if !ipPersonText(external.Provider, ipPersonPointerMax, true) ||
			external.SubjectKind != "person" ||
			!ipPersonText(external.ProviderID, ipPersonProviderIDMax, true) {
			return ErrInvalid
		}
		if i > 0 && !ipPersonExternalLess(q.external[i-1], external) {
			return ErrInvalid
		}
	}
	return nil
}

// ipPersonEvidencePageBytes is the exact number of begin-manifest bytes one
// page contributes: each alias row is its tag/ordinal plus four length-prefixed
// texts, each external row is its tag plus three length-prefixed texts. The two
// family count words are accounted once by the seal, so a complete build always
// stages exactly expected_bytes.
func ipPersonEvidencePageBytes(family int, q ipPersonEvidenceAppendRequest, rows int64) (int64, error) {
	if family < 0 || family >= len(ipPersonFamilySpecs) {
		return 0, ErrInvalid
	}
	page, count := q.page(family)
	if page == nil || count <= 0 || int64(count) != rows ||
		rows > int64(ipPersonFamilySpecs[family].maxPage) {
		return 0, ErrInvalid
	}
	var total int64
	add := func(part int64) error {
		if part < 0 || total > ipPersonMaxBytes-part {
			return ErrCap
		}
		total += part
		return nil
	}
	switch family {
	case 0:
		for _, alias := range q.aliases {
			if !ipPersonText(alias.RequestedID, ipPersonProviderIDMax, true) ||
				alias.CanonicalID != q.providerID ||
				!ipPersonText(alias.Alias, ipPersonTextMax, false) ||
				!ipPersonText(alias.ObservedAt, 64, true) {
				return 0, ErrInvalid
			}
			if err := add(16 + 4*8 + int64(len(alias.RequestedID)) + int64(len(alias.CanonicalID)) +
				int64(len(alias.Alias)) + int64(len(alias.ObservedAt))); err != nil {
				return 0, err
			}
		}
	case 1:
		for i, external := range q.external {
			if !ipPersonText(external.Provider, ipPersonPointerMax, true) ||
				external.SubjectKind != "person" ||
				!ipPersonText(external.ProviderID, ipPersonProviderIDMax, true) ||
				(i > 0 && !ipPersonExternalLess(q.external[i-1], external)) {
				return 0, ErrInvalid
			}
			if err := add(8 + 3*8 + int64(len(external.Provider)) +
				int64(len(external.SubjectKind)) + int64(len(external.ProviderID))); err != nil {
				return 0, err
			}
		}
	default:
		return 0, ErrInvalid
	}
	if total <= 0 {
		return 0, ErrInvalid
	}
	return total, nil
}

func ipPersonEvidenceShapeAppend(q ipPersonEvidenceAppendRequest) error {
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
		q.buildRevision <= 0 || q.buildRevision == math.MaxInt64 ||
		q.familyIndex < 0 || q.familyIndex >= int64(len(ipPersonFamilySpecs)) ||
		q.family != ipPersonFamilySpecs[q.familyIndex].name ||
		q.familyStart < 1 || q.familyRows < 0 || q.afterOrdinal < 0 || q.materializedBefore < 1 {
		return ErrInvalid
	}
	if q.familyStart > math.MaxInt64-q.afterOrdinal ||
		q.familyStart+q.afterOrdinal != q.materializedBefore ||
		q.afterOrdinal > math.MaxInt64-q.familyRows {
		return ErrInvalid
	}
	page, rows := q.page(int(q.familyIndex))
	if page == nil || rows <= 0 || rows > ipPersonFamilySpecs[q.familyIndex].maxPage ||
		q.afterOrdinal+int64(rows) > q.familyRows ||
		len(q.aliases)+len(q.external) > ipPersonMaxChildren {
		return ErrInvalid
	}
	if err := ipPersonEvidenceAppendChildren(q); err != nil {
		return err
	}
	_, err := ipPersonEvidencePageBytes(int(q.familyIndex), q, int64(rows))
	return err
}

func newIPPersonEvidenceAppendCommand(identity CommandIdentity, in ipPersonEvidenceAppendRequest) (*ipPersonCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipPersonEvidenceShapeAppend(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPPersonEvidenceAppend, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonCommand{kind: "append", identity: identity, append: in, digest: digest, valid: true}, nil
}

func ipPersonEvidenceAppendSaved(q ipPersonEvidenceAppendRequest, identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	_, rows := q.page(int(q.familyIndex))
	pageBytes, err := ipPersonEvidencePageBytes(int(q.familyIndex), q, int64(rows))
	if err != nil {
		return zero, ErrResolveRequired
	}
	if r.kind != "append" || r.acceptedRevision != q.buildRevision+1 ||
		r.resultCount != int64(rows) || r.operationID != identity.OperationID ||
		r.evidenceID != q.evidenceID || r.buildID != q.buildID {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "append", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: q.buildRevision + 1, LeaseGeneration: q.leaseGeneration,
		LeaseUntilMS: q.leaseUntilMS, AppliedMS: r.appliedMS, EvidenceID: q.evidenceID,
		BuildID: q.buildID, SubjectKind: "person", BuildGeneration: q.buildGeneration,
		MaterializedRows: q.materializedBefore + int64(rows), ResultBytes: pageBytes,
		ResultCount: int64(rows)}, nil
}

func ipPersonEvidenceFamilyCount(ctx context.Context, turn *commandTurn, family int, evidenceID string) (n int64, err error) {
	defer func() { n, err = finishCommand(ctx, commandLifetime(turn), n, err) }()
	id := ipPersonAppendAliasCountStmt
	if family == 1 {
		id = ipPersonAppendExternalCountStmt
	}
	return wc008DraftInteger(ctx, turn, id, evidenceID)
}

func ipPersonEvidenceInsertPage(ctx context.Context, turn *commandTurn, family int, q ipPersonEvidenceAppendRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	switch family {
	case 0:
		for i, alias := range q.aliases {
			if err = execIPEntityEvidence(ctx, turn, ipPersonAppendAliasStmt, q.evidenceID,
				q.afterOrdinal+int64(i), alias.RequestedID, alias.CanonicalID, alias.Alias,
				alias.ObservedAt); err != nil {
				return err
			}
		}
	case 1:
		for _, external := range q.external {
			if err = execIPEntityEvidence(ctx, turn, ipPersonAppendExternalStmt, q.evidenceID,
				external.Provider, external.ProviderID); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

func ipPersonEvidenceAppendRecord(ctx context.Context, turn *commandTurn, owner string, id CommandIdentity,
	request RequestDigest, value ipPersonResult) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	digest, err := ipPersonResultDigest(id, request, value)
	if err != nil {
		return out, err
	}
	step, ok := turn.steps[ipPersonAppendRecordStmt]
	if !ok {
		return out, ErrInvalid
	}
	written, err := turn.exec(ctx, step.statementStep, owner, id.Incarnation[:], id.Sequence,
		id.OperationID[:], request[:], value.AppliedMS, digest[:], value.Revision,
		value.MaterializedRows, value.ResultCount, value.EvidenceID, value.BuildID)
	if err != nil {
		return out, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return digest, nil
}

func ipPersonEvidenceAppendFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity,
	q ipPersonEvidenceAppendRequest, owner ipPersonOwner, appliedMS int64) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	build, err := ipPersonBuildRead(ctx, turn, q.buildID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	_, rows := q.page(int(q.familyIndex))
	if rows <= 0 || build.evidenceID != q.evidenceID || build.owner != q.operationID ||
		build.ownerIncarnation != identity.Incarnation || build.ownerGeneration != q.generation ||
		build.generation != q.buildGeneration || build.provider != q.provider ||
		build.subjectKind != "person" || build.providerID != q.providerID ||
		build.state != "building" || build.cursor != build.stagedRows ||
		build.cursor != q.materializedBefore || build.contentRevision != q.buildRevision {
		return out, ErrConflict
	}
	count, err := ipPersonEvidenceFamilyCount(ctx, turn, int(q.familyIndex), q.evidenceID)
	if err != nil {
		return out, err
	}
	// The page must continue its own family at the exact persisted count, so a
	// gapped or interleaved ordinal can never be committed.
	if count != q.afterOrdinal || q.familyStart > build.cursor ||
		build.cursor-q.familyStart != q.afterOrdinal {
		return out, ErrConflict
	}
	if build.cursor > math.MaxInt64-int64(rows) ||
		build.cursor+int64(rows) > build.expectedRows {
		return out, ErrConflict
	}
	pageBytes, err := ipPersonEvidencePageBytes(int(q.familyIndex), q, int64(rows))
	if err != nil {
		return out, err
	}
	if build.stagedBytes > math.MaxInt64-pageBytes || build.charge > math.MaxInt64-pageBytes {
		return out, ErrCap
	}
	if build.stagedBytes+pageBytes > build.expectedBytes {
		return out, ErrConflict
	}
	if err = ipPersonEvidenceInsertPage(ctx, turn, int(q.familyIndex), q); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := turn.queryRow(ctx, turn.steps[ipPersonAppendBuildStmt].statementStep,
		int64(rows), int64(rows), pageBytes, pageBytes, q.buildID, q.evidenceID, q.provider,
		q.providerID, q.operationID, identity.Incarnation[:], q.generation, q.buildGeneration,
		build.cursor, build.stagedRows, build.stagedBytes, build.charge, build.contentRevision)
	if err != nil {
		return out, ErrConflict
	}
	var cursor, stagedRows, stagedBytes, charge, revision int64
	if err = row.Scan(&cursor, &stagedRows, &stagedBytes, &charge, &revision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if cursor != build.cursor+int64(rows) || stagedRows != build.stagedRows+int64(rows) ||
		stagedBytes != build.stagedBytes+pageBytes || charge != build.charge+pageBytes ||
		revision != q.buildRevision+1 {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[ipPersonAppendOwnerStmt].statementStep,
		identity.Sequence, q.operationID, identity.Incarnation[:], q.generation, q.revision,
		q.lease, q.leaseGeneration, q.leaseUntilMS, owner.lastSequence, owner.ackSequence)
	if err != nil {
		return out, ErrConflict
	}
	var ownerRevision int64
	if err = row.Scan(&ownerRevision); err != nil || ownerRevision != q.revision {
		return out, errors.Join(ErrConflict, err)
	}
	return ipPersonResult{Kind: "append", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: revision, LeaseGeneration: q.leaseGeneration, LeaseUntilMS: q.leaseUntilMS,
		AppliedMS: appliedMS, EvidenceID: q.evidenceID, BuildID: q.buildID,
		SubjectKind: "person", BuildGeneration: q.buildGeneration, MaterializedRows: cursor,
		ResultBytes: pageBytes, ResultCount: int64(rows)}, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved page with zero new effects, before any fresh build, lease
// or source probe. Append advances only the exact live person owner's open
// build; the owner revision, generation and lease are witnesses, not outputs.
func applyIPPersonEvidenceAppend(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonCommand) (out ipPersonCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonCommandResult
	if c == nil || !c.valid || c.kind != "append" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonEvidenceAppend {
		return zero, ErrInvalid
	}
	q := c.append
	if err = ipPersonEvidenceShapeAppend(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPersonEvidenceAppend, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonEvidenceAppendSaved(q, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonEvidenceAppend, c.reference(),
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
	value, err := ipPersonEvidenceAppendFresh(ctx, turn, c.identity, q, lookup.owner, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipPersonEvidenceAppendRecord(ctx, turn, q.operationID, c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonEvidenceAppend, c.reference(),
		q.operationID, q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
