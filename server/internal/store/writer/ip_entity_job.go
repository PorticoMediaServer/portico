package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// IP entity acquisition job 203/204/205 owns one exact provider operation and
// its lease over a scheduled metadata_acquisition_jobs subject. The command
// mints the metadata_provider_operations owner (provider namespace) and its
// immutable metadata_operation_subjects observation binding before any external
// IO. Claim/renew admit ownership only: neither promises acquisition success.
// Finish records a truthful terminal outcome without publishing metadata.
const (
	ipEntityJobLeaseMS   = int64(5 * 60 * 1000)
	ipEntityJobRetryMS   = int64(5 * 60 * 1000)
	ipEntityJobRole      = "primary"
	ipEntityJobMaxReason = 512
)

type ipEntityJobClaimRequest struct {
	entityID, libraryID                                                int64
	entityIncarnation                                                  [16]byte
	libraryKey, provider, purpose, role, selectedID                    string
	jobGeneration, jobRevision                                         int64
	entityRevision, sourceRevision, identityRevision, metadataRevision int64
	policyIncarnation                                                  [16]byte
	policyRevision, consentRevision                                    int64
}

type ipEntityJobRenewRequest struct {
	operationID, provider, purpose, selectedID, lease string
	entityID, generation, jobGeneration, jobRevision  int64
	leaseUntilMS                                      int64
}

type ipEntityJobFinishRequest struct {
	operationID, provider, purpose, selectedID, lease string
	entityID, generation, jobGeneration, jobRevision  int64
	leaseUntilMS                                      int64
	outcome, reason                                   string
}

type ipEntityJobCommand struct {
	kind     string
	identity CommandIdentity
	digest   RequestDigest
	valid    bool
	claim    ipEntityJobClaimRequest
	renew    ipEntityJobRenewRequest
	finish   ipEntityJobFinishRequest
}

type ipEntityJobCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEntityJobResult
	Digest  ResultDigest
}

type ipEntityJobOwner struct {
	incarnation                        [16]byte
	lastSequence, acknowledgedSequence int64
	generation                         int64
	status, lease, leaseUntil          string
	entityID                           int64
	entityIncarnation                  [16]byte
	sourceRevision                     int64
	identityRevision                   int64
	metadataRevision                   int64
	policyIncarnation                  [16]byte
	policyRevision                     int64
	consentRevision                    int64
	selectedID                         string
	exists                             bool
}

type ipEntityJobSubjectFacts struct {
	entityIncarnation                [16]byte
	revision                         int64
	sourceRevision, identityRevision int64
	metadataRevision                 int64
}

type ipEntityJobCurrentFacts struct {
	entityIncarnation             [16]byte
	entityRevision                int64
	sourceRevision                int64
	identityRevision              int64
	metadataRevision              int64
	entityRetired, libraryRetired int64
	policyIncarnation             [16]byte
	policyRevision, enabled       int64
}

type ipEntityJobCandidate struct {
	ipEntityJobCurrentFacts
	status, selectedID, lease, episodeOrder string
	jobGeneration, jobRevision              int64
	leaseUntilMS, nextAttemptMS, attempts   int64
	manual, reviewSearch                    int64
	libraryID                               int64
	libraryKey                              string
	algorithm, refreshMode                  string
}

type ipEntityJobReceipt struct {
	kind             string
	sequence         int64
	acceptedRevision int64
	appliedMS        int64
	phase            string
	terminal         bool
	operationID      [16]byte
	requestDigest    [32]byte
	resultDigest     [32]byte
}

type ipEntityJobLookup struct {
	owner      ipEntityJobOwner
	receipt    ipEntityJobReceipt
	hasReceipt bool
	decision   receiptDecision
	create     bool
	value      ipEntityJobResult
	digest     ResultDigest
}

type ipEntityJobDecode func(ipEntityJobReceipt) (ipEntityJobResult, error)

func (c *ipEntityJobCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEntityJobCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntityJobCommand")
}

func ipEntityJobShapeProvider(provider, purpose string) error {
	if !ipPolicyProviderCode(provider) || purpose != "metadata" {
		return ErrInvalid
	}
	return nil
}

func ipEntityJobShapeClaim(in ipEntityJobClaimRequest) error {
	if in.entityID <= 0 || in.libraryID <= 0 || in.entityIncarnation == [16]byte{} ||
		len(in.libraryKey) == 0 || len(in.libraryKey) > 128 || !utf8.ValidString(in.libraryKey) ||
		len(in.selectedID) == 0 || len(in.selectedID) > 128 || !utf8.ValidString(in.selectedID) ||
		in.role != ipEntityJobRole || in.jobGeneration <= 0 || in.jobRevision <= 0 ||
		in.jobGeneration == math.MaxInt64 || in.jobRevision == math.MaxInt64 ||
		in.entityRevision <= 0 || in.sourceRevision <= 0 || in.identityRevision <= 0 || in.metadataRevision <= 0 ||
		in.policyIncarnation == [16]byte{} || in.policyRevision <= 0 || in.consentRevision <= 0 {
		return ErrInvalid
	}
	return ipEntityJobShapeProvider(in.provider, in.purpose)
}

func ipEntityJobShapeOwner(operationID, provider, purpose, selectedID, lease string, entityID, generation, jobGeneration, jobRevision, leaseUntilMS int64) error {
	if len(operationID) != 32 || len(selectedID) == 0 || len(selectedID) > 128 || !utf8.ValidString(selectedID) ||
		len(lease) == 0 || len(lease) > 128 || !utf8.ValidString(lease) ||
		entityID <= 0 || generation <= 0 || jobGeneration <= 0 || jobRevision <= 0 ||
		jobGeneration == math.MaxInt64 || jobRevision == math.MaxInt64 || leaseUntilMS <= 0 {
		return ErrInvalid
	}
	return ipEntityJobShapeProvider(provider, purpose)
}

func ipEntityJobShapeRenew(in ipEntityJobRenewRequest) error {
	return ipEntityJobShapeOwner(in.operationID, in.provider, in.purpose, in.selectedID, in.lease,
		in.entityID, in.generation, in.jobGeneration, in.jobRevision, in.leaseUntilMS)
}

func ipEntityJobShapeFinish(in ipEntityJobFinishRequest) error {
	if err := ipEntityJobShapeOwner(in.operationID, in.provider, in.purpose, in.selectedID, in.lease,
		in.entityID, in.generation, in.jobGeneration, in.jobRevision, in.leaseUntilMS); err != nil {
		return err
	}
	if !ipEntityJobOutcome(in.outcome) || len(in.reason) > ipEntityJobMaxReason || !utf8.ValidString(in.reason) {
		return ErrInvalid
	}
	if in.outcome == "done" && in.reason != "" {
		return ErrInvalid
	}
	return nil
}

func newIPEntityClaimCommand(identity CommandIdentity, in ipEntityJobClaimRequest) (*ipEntityJobCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipEntityJobShapeClaim(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPEntityClaim, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityJobCommand{kind: "claim", identity: identity, claim: in, digest: digest, valid: true}, nil
}

func newIPEntityRenewCommand(identity CommandIdentity, in ipEntityJobRenewRequest) (*ipEntityJobCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipEntityJobShapeRenew(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPEntityRenew, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityJobCommand{kind: "renew", identity: identity, renew: in, digest: digest, valid: true}, nil
}

func newIPEntityFinishCommand(identity CommandIdentity, in ipEntityJobFinishRequest) (*ipEntityJobCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) || ipEntityJobShapeFinish(in) != nil {
		return nil, ErrInvalid
	}
	digest, err := canonicalRequestDigest(IPEntityFinish, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipEntityJobCommand{kind: "finish", identity: identity, finish: in, digest: digest, valid: true}, nil
}

func ipEntityJobLeaseUntil(appliedMS int64) (int64, error) {
	if appliedMS < 0 || appliedMS > math.MaxInt64-ipEntityJobLeaseMS {
		return 0, ErrInvalid
	}
	return appliedMS + ipEntityJobLeaseMS, nil
}

func ipEntityJobRetryAt(appliedMS int64) (int64, error) {
	if appliedMS < 0 || appliedMS > math.MaxInt64-ipEntityJobRetryMS {
		return 0, ErrInvalid
	}
	return appliedMS + ipEntityJobRetryMS, nil
}

func ipEntityJobTimeText(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

func ipEntityJobTextTime(text string) (int64, bool) {
	if len(text) == 0 || len(text) > 64 {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || t.UnixMilli() < 0 {
		return 0, false
	}
	return t.UnixMilli(), true
}

func ipEntityJobFlag(v int64) bool {
	return v == 0 || v == 1
}

func ipEntityJobReceiptKind(kind string) (CommandID, bool) {
	switch kind {
	case "claim":
		return IPEntityClaim, true
	case "renew":
		return IPEntityRenew, true
	case "finish":
		return IPEntityFinish, true
	}
	return 0, false
}

// ipEntityJobLookupReceipt resolves the ORIGINAL receipt before any fresh
// source, policy or lease probe. Current authority is checked by the caller
// first. Claim may create its owner only as the first command of a new
// incarnation; renew/finish require the existing exact provider operation.
func ipEntityJobLookupReceipt(ctx context.Context, turn *commandTurn, purpose CommandID, ref ReceiptReference,
	ownerID, provider string, entityID int64, createOK bool, decode ipEntityJobDecode) (out ipEntityJobLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobOwnerStmt, ownerID, provider)
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
	if _, ok := ipEntityJobTextTime(out.owner.leaseUntil); !ok {
		return out, ErrResolveRequired
	}
	if len(incarnation) != 16 || len(entityIncarnation) != 16 || len(policyIncarnation) != 16 ||
		out.owner.entityID != entityID || out.owner.generation <= 0 || out.owner.sourceRevision <= 0 ||
		out.owner.identityRevision <= 0 || out.owner.metadataRevision <= 0 || out.owner.policyRevision <= 0 ||
		out.owner.consentRevision <= 0 || len(out.owner.selectedID) == 0 || len(out.owner.selectedID) > 128 ||
		out.owner.lastSequence < 0 || out.owner.acknowledgedSequence < 0 {
		return out, ErrResolveRequired
	}
	copy(out.owner.incarnation[:], incarnation)
	copy(out.owner.entityIncarnation[:], entityIncarnation)
	copy(out.owner.policyIncarnation[:], policyIncarnation)
	out.owner.exists = true

	row, err = wc008DraftQuery(ctx, turn, ipEntityJobReceiptStmt, ownerID, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var receiptIncarnation, operation, requestDigest, resultDigest []byte
	var terminal int64
	var found receiptFacts
	err = row.Scan(&receiptIncarnation, &out.receipt.sequence, &operation, &requestDigest, &out.receipt.appliedMS,
		&resultDigest, &out.receipt.kind, &out.receipt.acceptedRevision, &out.receipt.phase, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		term := terminal == 1
		kind, known := ipEntityJobReceiptKind(out.receipt.kind)
		if len(receiptIncarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 ||
			len(resultDigest) != 32 || out.receipt.sequence <= 0 || out.receipt.appliedMS < 0 ||
			out.receipt.acceptedRevision <= 0 || !ipEntityJobFlag(terminal) || !known ||
			!(out.receipt.phase == "claimed" || out.receipt.phase == "done" || out.receipt.phase == "retry") ||
			(out.receipt.kind != "finish") == term {
			return out, ErrResolveRequired
		}
		out.receipt.terminal = term
		copy(out.receipt.operationID[:], operation)
		copy(out.receipt.requestDigest[:], requestDigest)
		copy(out.receipt.resultDigest[:], resultDigest)
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], receiptIncarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: kind, resultCount: 1}
		copy(found.resultDigest[:], resultDigest)
		out.hasReceipt = true
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, purpose,
		receiptOwnerFacts{exists: true, incarnation: out.owner.incarnation,
			lastSequence: out.owner.lastSequence, acknowledgedSequence: out.owner.acknowledgedSequence}, found)
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

func ipEntityJobClaimSaved(in ipEntityJobClaimRequest, identity CommandIdentity, r ipEntityJobReceipt) (ipEntityJobResult, error) {
	var zero ipEntityJobResult
	until, err := ipEntityJobLeaseUntil(r.appliedMS)
	if err != nil || r.kind != "claim" || r.phase != "claimed" || r.terminal ||
		r.acceptedRevision != in.jobRevision+1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipEntityJobResult{Kind: "claim", OperationID: ipEntityJobHex(identity.OperationID),
		EntityID: in.entityID, Provider: in.provider, Purpose: in.purpose, SelectedID: in.selectedID,
		Lease: ipEntityJobHex(identity.Incarnation), Generation: in.jobGeneration + 1,
		Revision: in.jobRevision + 1, LeaseUntilMS: until, AppliedMS: r.appliedMS, ResultCount: 1}, nil
}

func ipEntityJobRenewSaved(in ipEntityJobRenewRequest, identity CommandIdentity, r ipEntityJobReceipt) (ipEntityJobResult, error) {
	var zero ipEntityJobResult
	until, err := ipEntityJobLeaseUntil(r.appliedMS)
	if err != nil || r.kind != "renew" || r.phase != "claimed" || r.terminal ||
		r.acceptedRevision != in.jobRevision+1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipEntityJobResult{Kind: "renew", OperationID: in.operationID,
		EntityID: in.entityID, Provider: in.provider, Purpose: in.purpose, SelectedID: in.selectedID,
		Lease: in.lease, Generation: in.jobGeneration,
		Revision: in.jobRevision + 1, LeaseUntilMS: until, AppliedMS: r.appliedMS, ResultCount: 1}, nil
}

func ipEntityJobFinishSaved(in ipEntityJobFinishRequest, identity CommandIdentity, r ipEntityJobReceipt) (ipEntityJobResult, error) {
	var zero ipEntityJobResult
	if r.kind != "finish" || r.phase != in.outcome || !r.terminal ||
		r.acceptedRevision != in.jobRevision+1 || r.operationID != identity.OperationID {
		return zero, ErrResolveRequired
	}
	return ipEntityJobResult{Kind: "finish", OperationID: in.operationID,
		EntityID: in.entityID, Provider: in.provider, Purpose: in.purpose, SelectedID: in.selectedID,
		Generation: in.jobGeneration, Revision: in.jobRevision + 1, LeaseUntilMS: r.appliedMS,
		AppliedMS: r.appliedMS, Outcome: in.outcome, ResultCount: 1}, nil
}

func ipEntityJobSubjectFactsRead(ctx context.Context, turn *commandTurn, ownerID, provider string, entityID int64) (out ipEntityJobSubjectFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobSubjectStmt, ownerID, provider, entityID)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	err = row.Scan(&out.revision, &incarnation, &out.sourceRevision, &out.identityRevision, &out.metadataRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrResolveRequired
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || out.revision <= 0 || out.sourceRevision <= 0 ||
		out.identityRevision <= 0 || out.metadataRevision <= 0 {
		return out, ErrResolveRequired
	}
	copy(out.entityIncarnation[:], incarnation)
	return out, nil
}

func ipEntityJobCurrentFactsRead(ctx context.Context, turn *commandTurn, provider string, entityID int64) (out ipEntityJobCurrentFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobCurrentStmt, provider, entityID)
	if err != nil {
		return out, err
	}
	var incarnation, policyIncarnation []byte
	err = row.Scan(&out.entityRevision, &incarnation, &out.sourceRevision, &out.identityRevision,
		&out.metadataRevision, &out.entityRetired, &out.libraryRetired, &policyIncarnation,
		&out.policyRevision, &out.enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || len(policyIncarnation) != 16 || out.entityRevision <= 0 ||
		out.sourceRevision <= 0 || out.identityRevision <= 0 || out.metadataRevision <= 0 ||
		out.policyRevision <= 0 || !ipEntityJobFlag(out.entityRetired) ||
		!ipEntityJobFlag(out.libraryRetired) || !ipEntityJobFlag(out.enabled) {
		return out, ErrResolveRequired
	}
	copy(out.entityIncarnation[:], incarnation)
	copy(out.policyIncarnation[:], policyIncarnation)
	return out, nil
}

// ipEntityJobCandidateRead reads the one exact scheduled subject, its current
// entity/library source and the provider policy that authorizes it.
func ipEntityJobCandidateRead(ctx context.Context, turn *commandTurn, entityID int64, provider, purpose string) (out ipEntityJobCandidate, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobCandidateStmt, entityID, provider, purpose)
	if err != nil {
		return out, err
	}
	var incarnation, policyIncarnation []byte
	err = row.Scan(&out.status, &out.selectedID, &out.jobGeneration, &out.jobRevision, &out.lease,
		&out.leaseUntilMS, &out.nextAttemptMS, &out.attempts, &out.manual, &out.reviewSearch,
		&out.episodeOrder, &out.entityRevision, &incarnation, &out.sourceRevision, &out.identityRevision,
		&out.metadataRevision, &out.entityRetired, &out.libraryID, &out.libraryKey, &out.libraryRetired,
		&policyIncarnation, &out.policyRevision, &out.enabled, &out.algorithm, &out.refreshMode)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || len(policyIncarnation) != 16 || out.jobGeneration <= 0 || out.jobRevision <= 0 ||
		out.leaseUntilMS < 0 || out.nextAttemptMS < 0 || out.attempts < 0 || len(out.selectedID) == 0 ||
		len(out.selectedID) > 128 || out.entityRevision <= 0 || out.sourceRevision <= 0 ||
		out.identityRevision <= 0 || out.metadataRevision <= 0 || out.policyRevision <= 0 ||
		len(out.algorithm) == 0 || len(out.algorithm) > 64 || !ipRefreshMode(out.refreshMode) ||
		!ipEntityJobFlag(out.entityRetired) || !ipEntityJobFlag(out.libraryRetired) ||
		!ipEntityJobFlag(out.enabled) || !ipEntityJobFlag(out.manual) || !ipEntityJobFlag(out.reviewSearch) {
		return out, ErrResolveRequired
	}
	copy(out.entityIncarnation[:], incarnation)
	copy(out.policyIncarnation[:], policyIncarnation)
	return out, nil
}

// Exact current owner and operation are admitted before external IO. A live
// lease is another worker's owner; an expired lease still consumes exactly one
// new generation so the old owner cannot renew or finish.
func ipEntityJobClaimFresh(ctx context.Context, turn *commandTurn, identity CommandIdentity, q ipEntityJobClaimRequest, appliedMS int64) (out ipEntityJobResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	c, err := ipEntityJobCandidateRead(ctx, turn, q.entityID, q.provider, q.purpose)
	if err != nil {
		return out, err
	}
	if c.entityIncarnation != q.entityIncarnation || c.entityRevision != q.entityRevision ||
		c.sourceRevision != q.sourceRevision || c.identityRevision != q.identityRevision ||
		c.metadataRevision != q.metadataRevision || c.jobGeneration != q.jobGeneration ||
		c.jobRevision != q.jobRevision || c.selectedID != q.selectedID {
		return out, ErrConflict
	}
	if c.libraryID != q.libraryID || c.libraryKey != q.libraryKey {
		return out, ErrDisclosureDenied
	}
	if c.entityRetired != 0 || c.libraryRetired != 0 || c.enabled != 1 {
		return out, ErrDisclosureDenied
	}
	if c.policyIncarnation != q.policyIncarnation || c.policyRevision != q.policyRevision {
		return out, ErrConflict
	}
	if c.status == "claimed" {
		if c.lease == "" || c.leaseUntilMS > appliedMS {
			return out, ErrConflict
		}
	} else if c.status != "pending" {
		return out, ErrConflict
	}
	if c.nextAttemptMS > appliedMS {
		return out, ErrConflict
	}
	if c.lease != "" && c.leaseUntilMS > appliedMS {
		return out, ErrConflict
	}
	until, err := ipEntityJobLeaseUntil(appliedMS)
	if err != nil {
		return out, err
	}
	owner := ipEntityJobHex(identity.OperationID)
	lease := ipEntityJobHex(identity.Incarnation)
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobInsertOwnerStmt, owner, q.entityID, q.provider, lease,
		ipEntityJobTimeText(until), q.entityIncarnation[:], q.sourceRevision, q.identityRevision,
		q.metadataRevision, q.policyIncarnation[:], q.policyRevision, q.consentRevision, c.algorithm,
		c.refreshMode, q.selectedID, c.manual, c.reviewSearch, ipEntityJobTimeText(appliedMS),
		identity.Incarnation[:])
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	var generation, sequence int64
	if err = row.Scan(&generation, &sequence); err != nil || generation != 1 || sequence != 1 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = wc008DraftChanged(ctx, turn, ipEntityJobInsertSubjStmt, q.provider, owner, q.entityID,
		q.entityIncarnation[:], q.entityRevision, q.sourceRevision, q.identityRevision, q.metadataRevision); err != nil {
		return out, err
	}
	row, err = wc008DraftQuery(ctx, turn, ipEntityJobClaimStmt, lease, until, appliedMS, q.entityID, q.provider,
		q.purpose, q.jobGeneration, q.jobRevision, q.selectedID, c.lease, c.leaseUntilMS, c.nextAttemptMS)
	if err != nil {
		return out, ErrConflict
	}
	var newGeneration, newRevision int64
	if err = row.Scan(&newGeneration, &newRevision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if newGeneration != q.jobGeneration+1 || newRevision != q.jobRevision+1 {
		return out, ErrConflict
	}
	return ipEntityJobResult{Kind: "claim", OperationID: owner, EntityID: q.entityID, Provider: q.provider,
		Purpose: q.purpose, SelectedID: q.selectedID, Lease: lease, Generation: newGeneration,
		Revision: newRevision, LeaseUntilMS: until, AppliedMS: appliedMS, ResultCount: 1}, nil
}

// ipEntityJobOwnerFresh proves the claimed operation is still the exact owner:
// live lease, unchanged observation, current policy and provider consent. A new
// observation, policy revision, consent revision or lease loss is stale.
func ipEntityJobOwnerFresh(ctx context.Context, turn *commandTurn, ownerID string, owner ipEntityJobOwner, provider, selectedID, lease string,
	generation, leaseUntilMS, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if owner.status != "claimed" || owner.lease != lease || owner.generation != generation ||
		owner.selectedID != selectedID || leaseUntilMS <= appliedMS {
		return ErrConflict
	}
	claimed, ok := ipEntityJobTextTime(owner.leaseUntil)
	if !ok || claimed != leaseUntilMS {
		return ErrConflict
	}
	subject, err := ipEntityJobSubjectFactsRead(ctx, turn, ownerID, provider, owner.entityID)
	if err != nil {
		return err
	}
	current, err := ipEntityJobCurrentFactsRead(ctx, turn, provider, owner.entityID)
	if err != nil {
		return err
	}
	if subject.entityIncarnation != owner.entityIncarnation ||
		subject.sourceRevision != owner.sourceRevision ||
		subject.identityRevision != owner.identityRevision ||
		subject.metadataRevision != owner.metadataRevision ||
		current.entityIncarnation != subject.entityIncarnation ||
		current.entityRevision != subject.revision ||
		current.sourceRevision != subject.sourceRevision ||
		current.identityRevision != subject.identityRevision ||
		current.metadataRevision != subject.metadataRevision ||
		current.policyIncarnation != owner.policyIncarnation ||
		current.policyRevision != owner.policyRevision {
		return ErrConflict
	}
	if current.entityRetired != 0 || current.libraryRetired != 0 || current.enabled != 1 {
		return ErrDisclosureDenied
	}
	return ipPolicyProviderConsent(ctx, turn, provider, owner.consentRevision)
}

func ipEntityJobRecordReceipt(ctx context.Context, turn *commandTurn, owner string, id CommandIdentity, request RequestDigest,
	value ipEntityJobResult, terminal int64, kind string, phase string) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	digest, err := ipEntityJobResultDigest(id, request, value)
	if err != nil {
		return out, err
	}
	written, err := turn.exec(ctx, turn.steps[ipEntityJobRecordStmt].statementStep, owner,
		id.Incarnation[:], id.Sequence, id.OperationID[:], request[:], kind, value.Revision,
		value.AppliedMS, digest[:], phase, terminal, value.EntityID, value.Provider)
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
func applyIPEntityClaim(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityJobCommand) (out ipEntityJobCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityJobCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntityClaim {
		return zero, ErrInvalid
	}
	q := c.claim
	if err = ipEntityJobShapeClaim(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPEntityClaim, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	ownerID := ipEntityJobHex(c.identity.OperationID)
	decode := func(r ipEntityJobReceipt) (ipEntityJobResult, error) {
		return ipEntityJobClaimSaved(q, c.identity, r)
	}
	lookup, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityClaim, c.reference(), ownerID, q.provider, q.entityID, true, decode)
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
	value, err := ipEntityJobClaimFresh(ctx, turn, c.identity, q, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipEntityJobRecordReceipt(ctx, turn, ownerID, c.identity, c.digest, value, 0, "claim", "claimed")
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityClaim, c.reference(), ownerID, q.provider, q.entityID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

// Renew extends only the exact live owner's lease. A fresh renew grants
// continued work, so it requires the current GLOBAL provider enablement exactly
// as claim does (finish does not inherit that gate). The lease generation, job
// generation/revision and observation witnesses must still match before IO.
func applyIPEntityRenew(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityJobCommand) (out ipEntityJobCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityJobCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntityRenew {
		return zero, ErrInvalid
	}
	q := c.renew
	if err = ipEntityJobShapeRenew(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPEntityRenew, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipEntityJobReceipt) (ipEntityJobResult, error) {
		return ipEntityJobRenewSaved(q, c.identity, r)
	}
	lookup, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityRenew, c.reference(), q.operationID, q.provider, q.entityID, false, decode)
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
	// Fresh renew is acquisition work, not custody closure: a globally
	// disabled provider must refuse it before any lease or receipt effect.
	if err = ipPolicyProviderEnabled(ctx, turn, q.provider); err != nil {
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
	if err = ipEntityJobOwnerFresh(ctx, turn, q.operationID, lookup.owner, q.provider, q.selectedID, q.lease,
		q.generation, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	until, err := ipEntityJobLeaseUntil(appliedMS)
	if err != nil {
		return zero, err
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobRenewJobStmt, until, q.entityID, q.provider, q.purpose,
		q.jobGeneration, q.jobRevision, q.lease, q.leaseUntilMS)
	if err != nil {
		return zero, ErrConflict
	}
	var revision int64
	if err = row.Scan(&revision); err != nil || revision != q.jobRevision+1 {
		return zero, errors.Join(ErrConflict, err)
	}
	if err = wc008DraftChanged(ctx, turn, ipEntityJobRenewOwnerStmt, ipEntityJobTimeText(until),
		c.identity.Sequence, q.operationID, q.provider, c.identity.Incarnation[:], q.generation,
		q.lease, ipEntityJobTimeText(q.leaseUntilMS), lookup.owner.lastSequence,
		lookup.owner.acknowledgedSequence); err != nil {
		return zero, err
	}
	value := ipEntityJobResult{Kind: "renew", OperationID: q.operationID, EntityID: q.entityID,
		Provider: q.provider, Purpose: q.purpose, SelectedID: q.selectedID, Lease: q.lease,
		Generation: q.jobGeneration, Revision: revision, LeaseUntilMS: until, AppliedMS: appliedMS,
		ResultCount: 1}
	digest, err := ipEntityJobRecordReceipt(ctx, turn, q.operationID, c.identity, c.digest, value, 0, "renew", "claimed")
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityRenew, c.reference(), q.operationID, q.provider, q.entityID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

// Finish records the truthful terminal outcome of the exact live owner. It is
// custody closure, so it deliberately does NOT require the global provider
// enablement that claim and renew do: exact-owner bookkeeping must still settle
// after a disable. Done does not publish, accept or lease anything and starts no
// IO; retry only records a passive reschedule that claim refuses while disabled.
func applyIPEntityFinish(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context,
	b ipPolicyBudget, c *ipEntityJobCommand) (out ipEntityJobCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEntityJobCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEntityFinish {
		return zero, ErrInvalid
	}
	q := c.finish
	if err = ipEntityJobShapeFinish(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPEntityFinish, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	decode := func(r ipEntityJobReceipt) (ipEntityJobResult, error) {
		return ipEntityJobFinishSaved(q, c.identity, r)
	}
	lookup, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityFinish, c.reference(), q.operationID, q.provider, q.entityID, false, decode)
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
	if err = ipEntityJobOwnerFresh(ctx, turn, q.operationID, lookup.owner, q.provider, q.selectedID, q.lease,
		q.generation, q.leaseUntilMS, appliedMS); err != nil {
		return zero, err
	}
	jobStatus, ownerStatus, nextAttempt := "done", "staged", appliedMS
	if q.outcome == "retry" {
		jobStatus, ownerStatus = "pending", "failed"
		if nextAttempt, err = ipEntityJobRetryAt(appliedMS); err != nil {
			return zero, err
		}
	}
	row, err := wc008DraftQuery(ctx, turn, ipEntityJobFinishJobStmt, jobStatus, appliedMS, nextAttempt, q.reason,
		q.entityID, q.provider, q.purpose, q.jobGeneration, q.jobRevision, q.lease, q.leaseUntilMS)
	if err != nil {
		return zero, ErrConflict
	}
	var revision int64
	if err = row.Scan(&revision); err != nil || revision != q.jobRevision+1 {
		return zero, errors.Join(ErrConflict, err)
	}
	if err = wc008DraftChanged(ctx, turn, ipEntityJobFinishOwnerStmt, ownerStatus, ipEntityJobTimeText(appliedMS),
		ipEntityJobTimeText(appliedMS), q.reason, c.identity.Sequence, q.operationID, q.provider,
		c.identity.Incarnation[:], q.generation, q.lease, ipEntityJobTimeText(q.leaseUntilMS),
		lookup.owner.lastSequence, lookup.owner.acknowledgedSequence); err != nil {
		return zero, err
	}
	value := ipEntityJobResult{Kind: "finish", OperationID: q.operationID, EntityID: q.entityID,
		Provider: q.provider, Purpose: q.purpose, SelectedID: q.selectedID, Generation: q.jobGeneration,
		Revision: revision, LeaseUntilMS: appliedMS, AppliedMS: appliedMS, Outcome: q.outcome,
		ResultCount: 1}
	digest, err := ipEntityJobRecordReceipt(ctx, turn, q.operationID, c.identity, c.digest, value, 1, "finish", q.outcome)
	if err != nil {
		return zero, err
	}
	saved, err := ipEntityJobLookupReceipt(ctx, turn, IPEntityFinish, c.reference(), q.operationID, q.provider, q.entityID, false, decode)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
