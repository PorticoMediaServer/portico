package writer

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Writer is the sole typed writer facade. Its zero value grants no authority.
// Opening and trusted capability issuance are private composition operations.
type Writer struct{ effective *effectiveRuntime }

// Commands are sealed preparation values implemented inside writer's domain
// files. Constructors must freeze the COMPLETE pre-allocation request and use
// W's canonical request encoder. A digest, interface marker or embedded command
// is not authority: dispatch must accept only its compiled concrete types.
// No marker is an execution callback or runtime registration hook.
type InventoryAcceptCommand interface{ isInventoryAcceptCommand() }
type SearchImageStageCommand interface{ isSearchImageStageCommand() }
type EffectiveStageBeginCommand interface{ isEffectiveStageBeginCommand() }

func (*effectiveStageBeginCommand) isEffectiveStageBeginCommand() {}

// Disclosure capabilities have no public constructor or caller-selected role.
// A later W composition mints and checks their issuer, scope and live lifetime.
// Nil, zero, foreign or revoked values never authorize receipt access. FIRST
// scan ingress is internal ScanDriver only, not session/credential ingress.
type ScanDisclosure struct {
	issuer *Writer
	scope  *scanScope
}
type PublisherDisclosure struct {
	issuer *Writer
	scope  *publisherScope
}
type EnrichmentDisclosure struct {
	issuer *Writer
	scope  *enrichmentScope
}

// ReceiptReference identifies the ORIGINAL command, never a new retry identity.
// RequestDigest authorizes nothing. Resolve and ACK check CURRENT disclosure,
// then owner/incarnation/identity/kind/digest/floor BEFORE fresh eligibility.
type ReceiptReference struct {
	Identity      CommandIdentity
	RequestDigest RequestDigest
}

// Historical scope is separate from current source/lease/revision/profile CAS.
// Publisher FIRST is an entity publication; People/Reading are not admitted.
type ScanReceiptRequest struct {
	RunID      string
	LibraryID  int64
	LibraryKey string
	Receipt    ReceiptReference
}
type PublisherReceiptRequest struct {
	JobID, LibraryID, EntityID int64
	Receipt                    ReceiptReference
}
type EnrichmentReceiptRequest struct {
	ProviderOperationID string
	LibraryID, EntityID int64
	Receipt             ReceiptReference
}

// ReceiptOutcome is protocol status, not a COMMIT acknowledgment. Applied needs
// the exact complete immutable result; ResultRetired serves no drained cohort.
// Missing above-floor proof/uncertain COMMIT requires resolution of this SAME
// identity, never blind reexecution. Replayed describes this call, not a stored
// result field. EVERY error returns a completely zero value, including evidence.
// Zero output never proves rollback: resolve this SAME original identity after
// cancellation, overrun or any uncertain/late terminal outcome.
type ReceiptOutcome struct {
	State     ResultState
	Reference ReceiptReference
	Replayed  bool
	Evidence  *ExecutionEvidence
}

// AckResult confirms only the persisted owner floor. ACK refunds no bytes and
// does not certify physical receipt/protection reclamation. No Applied alias.
type AckResult struct {
	AcknowledgedThrough int64
	Evidence            *ExecutionEvidence
}

var (
	ErrNotImplemented   = errors.New("writer facade not implemented")
	ErrDisclosureDenied = errors.New("writer current disclosure denied")
	ErrRefused          = errors.New("writer resource admission refused")
)

// Refusal is resource scheduling, never a poison-file diagnostic. No refusal
// advances a cursor or commits an accepted prefix. RetryAt is absent unless an
// actual scheduling decision supplies it. Missing qualification is fail closed.
type RefusalCode uint8

const (
	RefusalCapacity RefusalCode = iota + 1
	RefusalWAL
	RefusalQuantum
	RefusalDeadline
	RefusalUnqualified
)

type Refusal struct {
	Code    RefusalCode
	RetryAt *time.Time
}

func (r *Refusal) Error() string { return ErrRefused.Error() }
func (r *Refusal) Unwrap() error { return ErrRefused }

// Existing sentinels remain meaningful: ErrInvalid/ErrCap are preparation
// refusals; ErrConflict includes wrong identity/digest and stale fresh CAS;
// ErrBusy/ErrClosed are frame availability; ErrResolveRequired (possibly joined
// with ErrOwnerUnavailable) forbids a new turn/identity until durable resolution.
// ErrOverrun and context cancellation/deadlines alone do not prove rollback;
// the private frame preserves terminal truth; resolve the ORIGINAL identity.

// CommitObservation is physical evidence, never the domain receipt outcome.
// Zero means unobserved; Unknown means a COMMIT was attempted but not proved.
type CommitObservation uint8

const (
	CommitNotAttempted CommitObservation = iota + 1
	CommitRolledBack
	CommitSucceeded
	CommitUnknown
)

type WALObservation struct {
	Token      uint64
	ObservedAt time.Time
	State      governor.WALState
}

// ExecutionEvidence concerns THIS call only, outside all canonical result
// digests. It never reconstructs original physical timings on historical replay.
// Nil pointers/zero times are unobserved, never invented policy or zero cost.
// HoldStartedAt -> PhysicalReturnedAt is physical custody; ReturnedAt may also
// include post-return WAL sampling. COMMIT times surround the actual attempt.
// Policy is actual asserted native settings. WAL samples retain their actual
// observation time/token, not a fictional transaction-boundary observation.
type ExecutionEvidence struct {
	Purpose                             CommandID
	Class                               governor.Class
	Durability                          Durability
	HoldStartedAt, ReturnedAt           time.Time
	CommitAttemptedAt, CommitReturnedAt *time.Time
	PhysicalReturnedAt                  *time.Time
	Commit                              CommitObservation
	Policy                              *sqliteconn.Settings
	WALBefore, WALAfter                 *WALObservation
}

// AcceptInventory is one whole immutable ordered batch/receipt. FIRST admits
// only a qualified N=1 ordinary-movie recipe; larger wire shapes confer no
// admission. Any member failure rolls back the ENTIRE batch, never a prefix.
func (w *Writer) AcceptInventory(ctx context.Context, disclosure *ScanDisclosure, input InventoryAcceptCommand) (released InventoryResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil || w.effective.inventoryPlan == nil {
		return InventoryResult{}, ErrNotImplemented
	}
	request, err := inventoryAcceptReference(input)
	if err != nil {
		return InventoryResult{}, err
	}
	out, _, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, &inventoryInvocation{disclosure: disclosure, request: request, input: input.(*inventoryAcceptCommand)})
	return out.inventory, err
}

// ResolveScan resolves InventoryAccept only, after current disclosure and before
// fresh source/root/lease/cursor/overflow checks. Floor precedes cohort decode.
func (w *Writer) ResolveScan(ctx context.Context, disclosure *ScanDisclosure, request ScanReceiptRequest) (released InventoryResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil || w.effective.inventoryPlan == nil {
		return InventoryResult{}, ErrNotImplemented
	}
	out, _, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, &inventoryInvocation{disclosure: disclosure, request: request})
	return out.inventory, err
}

func (w *Writer) AckScan(ctx context.Context, disclosure *ScanDisclosure, request ScanReceiptRequest) (released AckResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil || w.effective.inventoryPlan == nil {
		return AckResult{}, ErrNotImplemented
	}
	_, ack, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, true, &inventoryInvocation{disclosure: disclosure, request: request})
	return ack, err
}

// StageSearchImage allocates a hidden image/seal/held-reclaimer in the publisher
// owner's transaction/receipt. It creates no FTS/head/availability publication.
func (w *Writer) StageSearchImage(ctx context.Context, disclosure *PublisherDisclosure, input SearchImageStageCommand) (released SearchImageStageResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return SearchImageStageResult{}, ErrNotImplemented
	}
	invocation, err := preparePublisherInvocation(ctx, w, disclosure, publisherStageImage, PublisherReceiptRequest{}, input)
	if err != nil {
		return SearchImageStageResult{}, err
	}
	out, _, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, nil, commandInvocation{publisher: &invocation})
	return out.publisher, err
}

func (w *Writer) ResolveSearchImage(ctx context.Context, disclosure *PublisherDisclosure, request PublisherReceiptRequest) (released SearchImageStageResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return SearchImageStageResult{}, ErrNotImplemented
	}
	invocation, err := preparePublisherInvocation(ctx, w, disclosure, publisherResolveImage, request, nil)
	if err != nil {
		return SearchImageStageResult{}, err
	}
	out, _, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, false, nil, commandInvocation{publisher: &invocation})
	return out.publisher, err
}

func (w *Writer) AckSearchImage(ctx context.Context, disclosure *PublisherDisclosure, request PublisherReceiptRequest) (released AckResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return AckResult{}, ErrNotImplemented
	}
	invocation, err := preparePublisherInvocation(ctx, w, disclosure, publisherAckImage, request, nil)
	if err != nil {
		return AckResult{}, err
	}
	_, ack, err := w.effective.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, true, nil, commandInvocation{publisher: &invocation})
	return ack, err
}

// StageEffective is ONLY hidden EffectiveStage BEGIN, not EvidenceStage,
// materialized members, sealing, serving-head acceptance or final EnrichAccept.
func (w *Writer) StageEffective(ctx context.Context, disclosure *EnrichmentDisclosure, command EffectiveStageBeginCommand) (released EffectiveStageResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return EffectiveStageResult{}, ErrNotImplemented
	}
	input, ok := command.(*effectiveStageBeginCommand)
	if !ok || input == nil || !input.valid {
		return EffectiveStageResult{}, ErrInvalid
	}
	r := EnrichmentReceiptRequest{ProviderOperationID: input.request.ownerID, LibraryID: input.request.entity.libraryID,
		EntityID: input.request.entity.id, Receipt: ReceiptReference{input.identity, input.digest}}
	return w.effective.execute(ctx, w, disclosure, r, input)
}

func (w *Writer) ResolveEffective(ctx context.Context, disclosure *EnrichmentDisclosure, request EnrichmentReceiptRequest) (released EffectiveStageResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return EffectiveStageResult{}, ErrNotImplemented
	}
	return w.effective.execute(ctx, w, disclosure, request, nil)
}

func (w *Writer) AckEffective(ctx context.Context, disclosure *EnrichmentDisclosure, request EnrichmentReceiptRequest) (released AckResult, releaseErr error) {
	defer func() { released, releaseErr = finishCommand(ctx, ctx, released, releaseErr) }()
	if w == nil || w.effective == nil {
		return AckResult{}, ErrNotImplemented
	}
	return w.effective.ack(ctx, w, disclosure, request)
}
