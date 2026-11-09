package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

// IP person provider custody 206/207/208 owns one exact person observation
// operation (metadata_person_operations). The command mints the person owner
// only from the exact catalogue person token/revision, the exact immutable
// provider subject and the current provider policy/consent; it never selects a
// latest person, subject or policy revision. Claim/renew admit ownership only:
// neither promises provider success, acceptance or a build.
const (
	ipPersonLeaseMS       = int64(5 * 60 * 1000)
	ipPersonRetryMS       = int64(5 * 60 * 1000)
	ipPersonProviderIDMax = 256
	ipPersonTokenMax      = 256
	ipPersonPointerMax    = 128
)

type ipPersonClaimRequest struct {
	personID, personRevision              int64
	personToken                           string
	libraryID                             int64
	libraryKey, provider, providerID      string
	language, region, requestShape        string
	subjectID, subjectRevision            int64
	subjectIncarnation, policyIncarnation [16]byte
	policyRevision, consentRevision       int64
}

type ipPersonRenewRequest struct {
	operationID, provider, providerID, personToken, lease string
	personID, generation, revision, leaseGeneration       int64
	leaseUntilMS                                          int64
}

type ipPersonFinishRequest struct {
	operationID, provider, providerID, personToken, lease string
	personID, generation, revision, leaseGeneration       int64
	leaseUntilMS                                          int64
	outcome                                               string
}

type ipPersonCommand struct {
	kind     string
	identity CommandIdentity
	digest   RequestDigest
	valid    bool
	claim    ipPersonClaimRequest
	renew    ipPersonRenewRequest
	finish   ipPersonFinishRequest
	begin    ipPersonEvidenceBeginRequest
	append   ipPersonEvidenceAppendRequest
}

type ipPersonCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipPersonResult
	Digest  ResultDigest
}

type ipPersonOwner struct {
	incarnation     [16]byte
	subjectIncarn   [16]byte
	policyIncarn    [16]byte
	lastSequence    int64
	ackSequence     int64
	generation      int64
	revision        int64
	leaseGeneration int64
	leaseUntilMS    int64
	nextAttempt     sql.NullInt64
	personID        int64
	personToken     string
	personRevision  int64
	subjectID       int64
	subjectRevision int64
	policyRevision  int64
	consentRevision int64
	libraryID       int64
	libraryKey      string
	libraryRetired  int64
	provider        string
	providerID      string
	language        string
	region          string
	requestShape    string
	phase           string
	lease           string
	exists          bool
}

type ipPersonReceipt struct {
	kind             string
	incarnation      [16]byte
	sequence         int64
	acceptedRevision int64
	resultCount      int64
	appliedMS        int64
	operationID      [16]byte
	requestDigest    [32]byte
	resultDigest     [32]byte
	evidenceID       string
	buildID          string
	publicationID    string
	sealAlgorithm    string
	sealVersion      int64
	sealRows         int64
	sealBytes        int64
	sealCommitment   [32]byte
}

type ipPersonLookup struct {
	owner      ipPersonOwner
	receipt    ipPersonReceipt
	hasReceipt bool
	decision   receiptDecision
	create     bool
	value      ipPersonResult
	digest     ResultDigest
}

type ipPersonDecode func(ipPersonReceipt) (ipPersonResult, error)

func (c *ipPersonCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipPersonCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPersonCommand")
}

func ipPersonText(value string, max int, required bool) bool {
	return (!required || value != "") && len(value) <= max && utf8.ValidString(value) &&
		!containsNUL(value)
}

func containsNUL(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 0 {
			return true
		}
	}
	return false
}

func ipPersonNamespaceShape(provider, providerID, language, region, requestShape string) error {
	if !ipPolicyProviderCode(provider) || !ipPersonText(providerID, ipPersonProviderIDMax, true) ||
		!ipPersonText(language, ipPersonPointerMax, true) || !ipPolicyRegion(region) ||
		!ipPersonText(requestShape, ipPersonPointerMax, true) {
		return ErrInvalid
	}
	return nil
}

func ipPersonShapeClaim(q ipPersonClaimRequest) error {
	if q.personID <= 0 || !ipPersonText(q.personToken, ipPersonTokenMax, true) ||
		q.personRevision <= 0 || q.personRevision == math.MaxInt64 || q.libraryID <= 0 ||
		!ipPersonText(q.libraryKey, ipPersonPointerMax, true) ||
		q.subjectID <= 0 || q.subjectRevision <= 0 || q.subjectRevision == math.MaxInt64 ||
		q.subjectIncarnation == [16]byte{} || q.policyIncarnation == [16]byte{} ||
		q.policyRevision <= 0 || q.policyRevision == math.MaxInt64 || q.consentRevision <= 0 ||
		q.consentRevision == math.MaxInt64 {
		return ErrInvalid
	}
	return ipPersonNamespaceShape(q.provider, q.providerID, q.language, q.region, q.requestShape)
}

func ipPersonShapeOwner(q ipPersonRenewRequest) error {
	if len(q.operationID) != 32 || q.personID <= 0 || !ipPersonText(q.personToken, ipPersonTokenMax, true) ||
		!ipPersonText(q.lease, ipPersonPointerMax, true) || q.generation <= 0 || q.generation == math.MaxInt64 ||
		q.revision <= 0 || q.revision == math.MaxInt64 || q.leaseGeneration <= 0 ||
		q.leaseGeneration == math.MaxInt64 || q.leaseUntilMS <= 0 ||
		!ipPolicyProviderCode(q.provider) || !ipPersonText(q.providerID, ipPersonProviderIDMax, true) {
		return ErrInvalid
	}
	return nil
}

func ipPersonShapeRenew(q ipPersonRenewRequest) error {
	if err := ipPersonShapeOwner(q); err != nil {
		return err
	}
	return nil
}

func ipPersonShapeFinish(q ipPersonFinishRequest) error {
	if err := ipPersonShapeOwner(ipPersonRenewRequest{operationID: q.operationID, provider: q.provider,
		providerID: q.providerID, personToken: q.personToken, lease: q.lease, personID: q.personID,
		generation: q.generation, revision: q.revision, leaseGeneration: q.leaseGeneration,
		leaseUntilMS: q.leaseUntilMS}); err != nil {
		return err
	}
	if !ipPersonFinishOutcome(q.outcome) {
		return ErrInvalid
	}
	return nil
}

func newIPPersonClaimCommand(identity CommandIdentity, in ipPersonClaimRequest) (*ipPersonCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipPersonShapeClaim(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPPersonClaim, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonCommand{kind: "claim", identity: identity, claim: in, digest: digest, valid: true}, nil
}

func newIPPersonRenewCommand(identity CommandIdentity, in ipPersonRenewRequest) (*ipPersonCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipPersonShapeRenew(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPPersonRenew, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonCommand{kind: "renew", identity: identity, renew: in, digest: digest, valid: true}, nil
}

func newIPPersonFinishCommand(identity CommandIdentity, in ipPersonFinishRequest) (*ipPersonCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipPersonShapeFinish(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPPersonFinish, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPersonCommand{kind: "finish", identity: identity, finish: in, digest: digest, valid: true}, nil
}

func ipPersonLeaseUntil(appliedMS int64) (int64, error) {
	if appliedMS < 0 || appliedMS > math.MaxInt64-ipPersonLeaseMS {
		return 0, ErrInvalid
	}
	return appliedMS + ipPersonLeaseMS, nil
}

func ipPersonRetryAt(appliedMS int64) (int64, error) {
	if appliedMS < 0 || appliedMS > math.MaxInt64-ipPersonRetryMS {
		return 0, ErrInvalid
	}
	return appliedMS + ipPersonRetryMS, nil
}

func ipPersonFlag(v int64) bool { return v == 0 || v == 1 }

func ipPersonTextTime(text string) (int64, bool) {
	return ipEntityJobTextTime(text)
}

func ipPersonTimeText(ms int64) string {
	return ipEntityJobTimeText(ms)
}

// The person receipt family has no claim/finish command kinds: a claim is the
// BEGIN-kind receipt with no evidence, and finish is the retry-kind receipt.
// The mapping is derived from the stored pair, never from a caller field. The
// persisted spellings are the pinned person DDL's closed CHECK list: 209's
// evidence begin is stored as 'begin' with an evidence ID, 232's evidence
// append as 'append', 233's seal as 'evidence_seal', 234 accept as 'accept' and
// 235 delta as 'delta'. Registry descriptor names (ip_person_evidence_begin,
// ip_person_accept, ip_person_delta, ...) are never stored.
func ipPersonReceiptKind(kind, evidenceID string) (CommandID, bool) {
	switch kind {
	case "begin":
		if evidenceID == "" {
			return IPPersonClaim, true
		}
		return IPPersonEvidenceBegin, true
	case "renew":
		return IPPersonRenew, true
	case "retry":
		return IPPersonFinish, true
	case "append":
		if evidenceID == "" {
			return 0, false
		}
		return IPPersonEvidenceAppend, true
	case "evidence_seal":
		if evidenceID == "" {
			return 0, false
		}
		return IPPersonSeal, true
	case "accept":
		if evidenceID == "" {
			return 0, false
		}
		return IPPersonAccept, true
	case "delta":
		if evidenceID == "" {
			return 0, false
		}
		return IPPersonDelta, true
	}
	return 0, false
}

// ipPersonReceiptRead reads and validates the ONE original receipt row for the
// exact owner incarnation and command sequence. It never classifies a purpose
// and never reads mutable owner state; the caller binds the stored kind to its
// own purpose. Entity-purpose receipt families live in their own tables, so
// equal numeric witnesses can never satisfy this read.
func ipPersonReceiptRead(ctx context.Context, turn *commandTurn, ownerID string,
	ref ReceiptReference) (out ipPersonReceipt, kind CommandID, found bool, err error) {
	row, err := wc008DraftQuery(ctx, turn, ipPersonReceiptStmt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, 0, false, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest []byte
	var evidenceID, buildID, publicationID, sealAlgorithm sql.NullString
	var sealVersion, sealRows, sealBytes sql.NullInt64
	var sealCommitment []byte
	err = row.Scan(&receiptIncarnation, &out.sequence, &operation, &requestDigest,
		&out.appliedMS, &resultDigest, &out.kind, &out.acceptedRevision,
		&out.resultCount, &evidenceID, &buildID, &publicationID, &sealAlgorithm, &sealVersion,
		&sealCommitment, &sealRows, &sealBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return out, 0, false, nil
	}
	if err != nil {
		return out, 0, false, err
	}
	out.evidenceID = evidenceID.String
	out.buildID = buildID.String
	out.publicationID = publicationID.String
	out.sealAlgorithm = sealAlgorithm.String
	out.sealVersion = sealVersion.Int64
	out.sealRows = sealRows.Int64
	out.sealBytes = sealBytes.Int64
	kind, known := ipPersonReceiptKind(out.kind, out.evidenceID)
	sealFields := sealAlgorithm.Valid || sealVersion.Valid || sealCommitment != nil ||
		sealRows.Valid || sealBytes.Valid
	if sealFields != (kind == IPPersonSeal) {
		return out, 0, false, ErrResolveRequired
	}
	if kind == IPPersonSeal && (out.sealAlgorithm != ipPersonSealAlgorithm ||
		out.sealVersion != ipPersonSealVersion || len(sealCommitment) != 32 ||
		out.sealRows <= 0 || out.sealBytes <= 0) {
		return out, 0, false, ErrResolveRequired
	} else if kind == IPPersonSeal {
		copy(out.sealCommitment[:], sealCommitment)
	}
	// A retained accept/delta result names its accepted publication; no other
	// person kind may carry one. This is the closed receipt/result mapping, not
	// a mutable current-head probe.
	accepting := kind == IPPersonAccept || kind == IPPersonDelta
	if accepting != (out.publicationID != "") {
		return out, 0, false, ErrResolveRequired
	}
	if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
		len(resultDigest) != 32 || out.sequence <= 0 || out.appliedMS < 0 ||
		out.acceptedRevision <= 0 || out.resultCount < 0 || !known ||
		!ipPersonText(out.evidenceID, ipPersonPointerMax, false) ||
		!ipPersonText(out.buildID, ipPersonPointerMax, false) ||
		!ipPersonText(out.publicationID, ipPersonPointerMax, false) {
		return out, 0, false, ErrResolveRequired
	}
	copy(out.incarnation[:], receiptIncarnation)
	copy(out.operationID[:], operation)
	copy(out.requestDigest[:], requestDigest)
	copy(out.resultDigest[:], resultDigest)
	return out, kind, true, nil
}

// ipPersonOwnerLookupReceipt resolves the ORIGINAL receipt before any fresh
// person, subject, policy or lease probe. Current authority is checked by the
// caller first. Claim may create its owner only as the first command of a new
// incarnation; renew/finish/begin require the existing exact person operation.
func ipPersonOwnerLookupReceipt(ctx context.Context, turn *commandTurn, purpose CommandID, ref ReceiptReference,
	ownerID, provider, providerID string, personID int64, createOK bool, decode ipPersonDecode) (out ipPersonLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipPersonOwnerStmt, ownerID, provider, providerID, personID)
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
		if !createOK || ref.Identity.Sequence != 1 {
			return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
		}
		out.create = true
		out.decision = receiptFresh
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = ipPersonScanOwner(&out.owner, incarnation, subjectIncarnation, policyIncarnation, nextAttempt,
		provider, providerID, personID); err != nil {
		return out, err
	}
	out.owner.exists = true

	var kind CommandID
	var hasReceipt bool
	out.receipt, kind, hasReceipt, err = ipPersonReceiptRead(ctx, turn, ownerID, ref)
	if err != nil {
		return out, err
	}
	out.hasReceipt = hasReceipt
	if out.hasReceipt {
		actual := receiptIdentity{command: ref.Identity}
		actual.command.Incarnation = out.receipt.incarnation
		copy(actual.command.OperationID[:], out.receipt.operationID[:])
		actual.digest = out.receipt.requestDigest
		found := receiptFacts{exists: true, identity: actual, kind: kind, resultCount: out.receipt.resultCount}
		copy(found.resultDigest[:], out.receipt.resultDigest[:])
		out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, purpose,
			receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
				lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.ackSequence}, found)
		if err != nil {
			return out, err
		}
	}
	if !out.hasReceipt {
		out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, purpose,
			receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
				lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.ackSequence},
			receiptFacts{})
		if err != nil {
			return out, err
		}
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

func ipPersonScanOwner(o *ipPersonOwner, incarnation, subjectIncarnation, policyIncarnation []byte,
	nextAttempt sql.NullInt64, provider, providerID string, personID int64) error {
	if len(incarnation) != 16 || len(subjectIncarnation) != 16 || len(policyIncarnation) != 16 ||
		o.personID != personID || o.provider != provider || o.providerID != providerID ||
		o.generation <= 0 || o.revision <= 0 || o.leaseGeneration <= 0 || o.lastSequence < 0 ||
		o.ackSequence < 0 || o.ackSequence > o.lastSequence || o.leaseUntilMS < 0 ||
		!ipPersonText(o.lease, ipPersonPointerMax, true) || !ipPersonText(o.personToken, ipPersonTokenMax, true) ||
		o.personRevision <= 0 || o.subjectID <= 0 || o.subjectRevision <= 0 || o.policyRevision <= 0 ||
		o.consentRevision <= 0 || o.libraryID <= 0 || !ipPersonText(o.libraryKey, ipPersonPointerMax, true) ||
		!ipPersonFlag(o.libraryRetired) || !ipPersonText(o.language, ipPersonPointerMax, true) ||
		!ipPolicyRegion(o.region) || !ipPersonText(o.requestShape, ipPersonPointerMax, true) ||
		!ipPersonPhase(o.phase) || nextAttempt.Valid && nextAttempt.Int64 < 0 {
		return ErrResolveRequired
	}
	copy(o.incarnation[:], incarnation)
	copy(o.subjectIncarn[:], subjectIncarnation)
	copy(o.policyIncarn[:], policyIncarnation)
	o.nextAttempt = nextAttempt
	return nil
}

func ipPersonPhase(value string) bool {
	switch value {
	case "claimed", "building", "sealed", "accepted", "stale", "retiring", "done":
		return true
	}
	return false
}

// ipPersonOwnerFresh proves the exact live owner: lease generation and expiry,
// captured person token/revision, immutable provider subject and current policy
// revision. A new observation, policy revision, consent revision or lease loss
// is stale. No latest/MAX person, subject or policy lookup exists. Every caller
// that continues work (renew, begin, append, seal, accept, delta) also requires
// the current GLOBAL provider enablement; only FINISH passes false.
func ipPersonOwnerFresh(ctx context.Context, turn *commandTurn, owner ipPersonOwner, provider, providerID, lease string,
	personID, generation, revision, leaseGeneration, leaseUntilMS, appliedMS int64) error {
	return ipPersonOwnerFreshGated(ctx, turn, owner, provider, providerID, lease,
		personID, generation, revision, leaseGeneration, leaseUntilMS, appliedMS, true)
}

// ipPersonOwnerFreshGated is ipPersonOwnerFresh with an explicit global-provider
// gate. FINISH uses requireProviderEnabled=false because exact-owner custody
// closure must still settle after a disable; it still proves the lease, person,
// subject, policy and consent witnesses and performs no acquisition side effect.
func ipPersonOwnerFreshGated(ctx context.Context, turn *commandTurn, owner ipPersonOwner, provider, providerID, lease string,
	personID, generation, revision, leaseGeneration, leaseUntilMS, appliedMS int64, requireProviderEnabled bool) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !owner.exists || owner.lease != lease || owner.generation != generation ||
		owner.revision != revision || owner.leaseGeneration != leaseGeneration ||
		owner.personID != personID || leaseUntilMS <= appliedMS {
		return ErrConflict
	}
	if owner.leaseUntilMS != leaseUntilMS {
		return ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, ipPersonPersonStmt, personID, owner.personToken)
	if err != nil {
		return err
	}
	var personRevision int64
	if err = row.Scan(&personRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}
	if personRevision != owner.personRevision {
		return ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, ipPersonSubjectStmt, owner.subjectID, provider, providerID,
		owner.language, owner.region, owner.requestShape)
	if err != nil {
		return err
	}
	var subjectIncarnation []byte
	var subjectRevision int64
	if err = row.Scan(&subjectIncarnation, &subjectRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}
	if len(subjectIncarnation) != 16 || subjectRevision != owner.subjectRevision ||
		[16]byte(subjectIncarnation) != owner.subjectIncarn {
		return ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, ipPersonPolicyStmt, owner.libraryID, provider)
	if err != nil {
		return err
	}
	var policyIncarnation []byte
	var policyRevision, enabled int64
	var policyRegion string
	if err = row.Scan(&policyIncarnation, &policyRevision, &enabled, &policyRegion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if len(policyIncarnation) != 16 || [16]byte(policyIncarnation) != owner.policyIncarn ||
		policyRevision != owner.policyRevision || enabled != 1 || policyRegion != owner.region {
		return ErrConflict
	}
	if owner.libraryRetired != 0 {
		return ErrDisclosureDenied
	}
	if requireProviderEnabled {
		if err = ipPolicyProviderEnabled(ctx, turn, provider); err != nil {
			return err
		}
	}
	return ipPolicyProviderConsent(ctx, turn, provider, owner.consentRevision)
}

func ipPersonClaimSaved(q ipPersonClaimRequest, identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	until, err := ipPersonLeaseUntil(r.appliedMS)
	if err != nil || r.kind != "begin" || r.evidenceID != "" || r.buildID != "" ||
		r.acceptedRevision != 1 || r.resultCount != 1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "claim", OperationID: ipPersonHex(identity.OperationID),
		PersonID: q.personID, Provider: q.provider, ProviderID: q.providerID,
		Lease: ipPersonHex(identity.Incarnation), Generation: 1, Revision: 1, LeaseGeneration: 1,
		LeaseUntilMS: until, AppliedMS: r.appliedMS, ResultCount: 1}, nil
}

func ipPersonRenewSaved(q ipPersonRenewRequest, identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	until, err := ipPersonLeaseUntil(r.appliedMS)
	if err != nil || r.kind != "renew" || r.evidenceID != "" || r.buildID != "" ||
		r.acceptedRevision != q.revision+1 || r.resultCount != 1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "renew", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: q.revision + 1, LeaseGeneration: q.leaseGeneration + 1, LeaseUntilMS: until,
		AppliedMS: r.appliedMS, ResultCount: 1}, nil
}

func ipPersonFinishSaved(q ipPersonFinishRequest, identity CommandIdentity, r ipPersonReceipt) (ipPersonResult, error) {
	var zero ipPersonResult
	if r.kind != "retry" || r.evidenceID != "" || r.buildID != "" ||
		r.acceptedRevision != q.revision+1 || r.resultCount != 1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipPersonResult{Kind: "finish", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Generation: q.generation,
		Revision: q.revision + 1, LeaseGeneration: q.leaseGeneration, LeaseUntilMS: r.appliedMS,
		AppliedMS: r.appliedMS, Outcome: q.outcome, ResultCount: 1}, nil
}

func ipPersonClaimFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipPersonClaimRequest, appliedMS int64) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPersonPersonStmt, q.personID, q.personToken)
	if err != nil {
		return out, err
	}
	var personRevision int64
	if err = row.Scan(&personRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrDisclosureDenied
		}
		return out, err
	}
	if personRevision != q.personRevision {
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, ipPersonSubjectStmt, q.subjectID, q.provider, q.providerID,
		q.language, q.region, q.requestShape)
	if err != nil {
		return out, err
	}
	var subjectIncarnation []byte
	var subjectRevision int64
	if err = row.Scan(&subjectIncarnation, &subjectRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrDisclosureDenied
		}
		return out, err
	}
	if len(subjectIncarnation) != 16 || [16]byte(subjectIncarnation) != q.subjectIncarnation ||
		subjectRevision != q.subjectRevision {
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, ipPersonPolicyStmt, q.libraryID, q.provider)
	if err != nil {
		return out, err
	}
	var policyIncarnation []byte
	var policyRevision, enabled int64
	var policyRegion string
	if err = row.Scan(&policyIncarnation, &policyRevision, &enabled, &policyRegion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrDisclosureDenied
		}
		return out, err
	}
	if len(policyIncarnation) != 16 || [16]byte(policyIncarnation) != q.policyIncarnation ||
		policyRevision != q.policyRevision || enabled != 1 || policyRegion != q.region {
		return out, ErrConflict
	}
	until, err := ipPersonLeaseUntil(appliedMS)
	if err != nil {
		return out, err
	}
	owner := ipPersonHex(identity.OperationID)
	lease := ipPersonHex(identity.Incarnation)
	written, err := turn.exec(ctx, turn.steps[ipPersonInsertOwnerStmt].statementStep, owner, q.personID,
		q.provider, q.libraryID, q.subjectID, q.providerID, q.language, q.region, q.requestShape,
		q.personToken, q.personRevision, q.subjectIncarnation[:], q.subjectRevision,
		q.policyIncarnation[:], q.policyRevision, q.consentRevision, lease, until, identity.Incarnation[:])
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return out, errors.Join(ErrConflict, err)
	}
	return ipPersonResult{Kind: "claim", OperationID: owner, PersonID: q.personID, Provider: q.provider,
		ProviderID: q.providerID, Lease: lease, Generation: 1, Revision: 1, LeaseGeneration: 1,
		LeaseUntilMS: until, AppliedMS: appliedMS, ResultCount: 1}, nil
}

func ipPersonRecordReceipt(ctx context.Context, turn *commandTurn, owner string, id CommandIdentity,
	request RequestDigest, value ipPersonResult, kind string, acceptedRevision int64,
	evidenceID, buildID string) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	digest, err := ipPersonResultDigest(id, request, value)
	if err != nil {
		return out, err
	}
	var evidence, build any
	if evidenceID != "" {
		evidence = evidenceID
	}
	if buildID != "" {
		build = buildID
	}
	written, err := turn.exec(ctx, turn.steps[ipPersonRecordStmt].statementStep, owner,
		id.Incarnation[:], id.Sequence, id.OperationID[:], request[:], value.AppliedMS, digest[:],
		kind, acceptedRevision, value.ResultCount, evidence, build)
	if err != nil {
		return out, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return digest, nil
}

// Current permission precedes historical receipt disclosure. A replay returns
// the original saved claim with zero new effects, before any fresh lease probe.
func applyIPPersonClaim(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonCommand) (out ipPersonCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonCommandResult
	if c == nil || !c.valid || c.kind != "claim" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonClaim {
		return zero, ErrInvalid
	}
	q := c.claim
	if err = ipPersonShapeClaim(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPersonClaim, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	ownerID := ipPersonHex(c.identity.OperationID)
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonClaimSaved(q, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonClaim, c.reference(), ownerID,
		q.provider, q.providerID, q.personID, true, decode)
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
	if err = ipPolicyLibraryScope(ctx, turn, q.libraryID, q.libraryKey); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderEnabled(ctx, turn, q.provider); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderConsent(ctx, turn, q.provider, q.consentRevision); err != nil {
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
	value, err := ipPersonClaimFresh(ctx, turn, c.identity, q, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipPersonRecordReceipt(ctx, turn, ownerID, c.identity, c.digest, value, "begin",
		value.Revision, "", "")
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonClaim, c.reference(), ownerID,
		q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

// Renew extends only the exact live person owner's lease. A fresh renew grants
// continued work, so it requires the current GLOBAL provider enablement as well
// as the lease generation, operation generation/revision, captured person
// revision and subject/policy/consent witnesses before the owner floor advances.
// FINISH is the only lifecycle command that omits the global gate.
func applyIPPersonRenew(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonCommand) (out ipPersonCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonCommandResult
	if c == nil || !c.valid || c.kind != "renew" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonRenew {
		return zero, ErrInvalid
	}
	q := c.renew
	if err = ipPersonShapeRenew(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPersonRenew, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonRenewSaved(q, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonRenew, c.reference(), q.operationID,
		q.provider, q.providerID, q.personID, false, decode)
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
	// The request presents the captured person custody token; a different token
	// is a foreign owner witness and never reaches a fresh write.
	if lookup.owner.personToken != q.personToken {
		return zero, ErrConflict
	}
	if err = ipPersonOwnerFresh(ctx, turn, lookup.owner, q.provider, q.providerID, q.lease,
		q.personID, q.generation, q.revision, q.leaseGeneration, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	until, err := ipPersonLeaseUntil(appliedMS)
	if err != nil {
		return zero, err
	}
	row, err := turn.queryRow(ctx, turn.steps[ipPersonRenewStmt].statementStep, until,
		c.identity.Sequence, q.operationID, q.provider, q.providerID, q.personID,
		c.identity.Incarnation[:], q.generation, q.revision, q.lease, q.leaseGeneration,
		q.leaseUntilMS, lookup.owner.lastSequence, lookup.owner.ackSequence)
	if err != nil {
		return zero, ErrConflict
	}
	var revision, leaseGeneration int64
	if err = row.Scan(&revision, &leaseGeneration); err != nil || revision != q.revision+1 ||
		leaseGeneration != q.leaseGeneration+1 {
		return zero, errors.Join(ErrConflict, err)
	}
	value := ipPersonResult{Kind: "renew", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Lease: q.lease, Generation: q.generation,
		Revision: revision, LeaseGeneration: leaseGeneration, LeaseUntilMS: until,
		AppliedMS: appliedMS, ResultCount: 1}
	digest, err := ipPersonRecordReceipt(ctx, turn, q.operationID, c.identity, c.digest, value,
		"renew", revision, "", "")
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonRenew, c.reference(), q.operationID,
		q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

// Finish records the truthful terminal outcome of the exact live person owner.
// Done closes the operation and forbids any later build; retry keeps the phase
// but schedules the next attempt. It is custody closure: it deliberately does
// NOT require the global provider enablement that claim and renew do, so an
// exact owner can still settle after a disable. Neither outcome accepts,
// publishes, leases or starts IO, and a retried owner still cannot begin work
// while the provider is disabled.
func applyIPPersonFinish(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipPersonCommand) (out ipPersonCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPersonCommandResult
	if c == nil || !c.valid || c.kind != "finish" || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPPersonFinish {
		return zero, ErrInvalid
	}
	q := c.finish
	if err = ipPersonShapeFinish(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPersonFinish, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipPersonReceipt) (ipPersonResult, error) {
		return ipPersonFinishSaved(q, c.identity, r)
	}
	lookup, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonFinish, c.reference(), q.operationID,
		q.provider, q.providerID, q.personID, false, decode)
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
	switch lookup.owner.phase {
	case "claimed", "building", "sealed", "stale":
	default:
		return zero, ErrConflict
	}
	if lookup.owner.personToken != q.personToken {
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
	if err = ipPersonOwnerFreshGated(ctx, turn, lookup.owner, q.provider, q.providerID, q.lease,
		q.personID, q.generation, q.revision, q.leaseGeneration, q.leaseUntilMS, appliedMS, false); err != nil {
		return zero, err
	}
	phase, nextAttempt := lookup.owner.phase, any(nil)
	if q.outcome == "done" {
		phase = "done"
	} else {
		next, e := ipPersonRetryAt(appliedMS)
		if e != nil {
			return zero, e
		}
		nextAttempt = next
	}
	row, err := turn.queryRow(ctx, turn.steps[ipPersonFinishStmt].statementStep, phase,
		appliedMS, nextAttempt, c.identity.Sequence, q.operationID, q.provider, q.providerID,
		q.personID, c.identity.Incarnation[:], q.generation, q.revision, q.lease,
		q.leaseGeneration, q.leaseUntilMS, lookup.owner.lastSequence, lookup.owner.ackSequence)
	if err != nil {
		return zero, ErrConflict
	}
	var revision int64
	if err = row.Scan(&revision); err != nil || revision != q.revision+1 {
		return zero, errors.Join(ErrConflict, err)
	}
	value := ipPersonResult{Kind: "finish", OperationID: q.operationID, PersonID: q.personID,
		Provider: q.provider, ProviderID: q.providerID, Generation: q.generation,
		Revision: revision, LeaseGeneration: q.leaseGeneration, LeaseUntilMS: appliedMS,
		AppliedMS: appliedMS, Outcome: q.outcome, ResultCount: 1}
	digest, err := ipPersonRecordReceipt(ctx, turn, q.operationID, c.identity, c.digest, value,
		"retry", revision, "", "")
	if err != nil {
		return zero, err
	}
	saved, err := ipPersonOwnerLookupReceipt(ctx, turn, IPPersonFinish, c.reference(), q.operationID,
		q.provider, q.providerID, q.personID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
