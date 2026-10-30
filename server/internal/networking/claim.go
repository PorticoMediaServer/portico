// Package networking owns the local outbound claim coordinator. It is not
// registered with HTTP. Durable repositories and transports are root-owned
// integration dependencies; neither has an in-memory production fallback.
package networking

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var (
	ErrInvalid          = errors.New("invalid claim input")
	ErrBusy             = errors.New("claim operation already running")
	ErrStale            = errors.New("claim intent changed")
	ErrApprovalRequired = errors.New("claim approval required")
	ErrCancelled        = errors.New("claim cancelled")
	ErrUnavailable      = errors.New("claim service unavailable")
)

type Stage string

const (
	Prepared      Stage = "prepared"
	Approved      Stage = "approved"
	Finalizing    Stage = "finalizing"
	Retrieving    Stage = "retrieving"
	Installed     Stage = "installed"
	CancelPending Stage = "cancel_pending"
	Cancelled     Stage = "cancelled"
)

// UnboundAccount marks a claim prepared without a Portico Account. The web
// approval binds the approver's account (the server adopts it from the signed
// approval); until then nothing account-scoped is sent to Hosted.
const UnboundAccount = "account-unbound"

type Binding struct {
	OperationID     string
	ServerID        string
	AccountID       string
	PublicKey       []byte
	LocalGeneration int64
}
type ClaimPurpose struct {
	Purpose               string `json:"purpose"`
	CurrentOwnerAccountID string `json:"currentOwnerAccountId"`
	PreviousOwnerRole     string `json:"previousOwnerRole"`
	ReturnRegistrationID  string `json:"returnRegistrationId"`
}

func (p ClaimPurpose) Valid() bool {
	if p.ReturnRegistrationID != "" && !validID(p.ReturnRegistrationID) {
		return false
	}
	switch p.Purpose {
	case "setup":
		return p.CurrentOwnerAccountID == "" && p.PreviousOwnerRole == ""
	case "switch":
		return (p.CurrentOwnerAccountID == "" || validID(p.CurrentOwnerAccountID)) && p.PreviousOwnerRole == ""
	case "change-owner":
		return validID(p.CurrentOwnerAccountID) && (p.PreviousOwnerRole == "admin" || p.PreviousOwnerRole == "member" || p.PreviousOwnerRole == "remove")
	}
	return false
}

type Intent struct {
	ClaimPurpose
	Binding
	Revision                 int64
	Stage                    Stage
	ReservationUsername      string
	ApprovalRevision         int64
	ApprovalExpiresAt        time.Time
	ClaimGeneration          string
	CredentialGeneration     string
	InstallationAcknowledged bool
}
type Commit struct {
	OperationID          string
	ServerID             string
	ClaimGeneration      string
	CredentialGeneration string
}

// Secret prevents incidental formatting/JSON serialization of a result token.
// Root Install must persist a protected copy synchronously; the coordinator
// clears the temporary result before returning. No token enters an Intent.
type Secret struct{ value []byte }

func NewSecret(value []byte) (*Secret, error) {
	if len(value) < 32 || len(value) > 2048 {
		return nil, ErrInvalid
	}
	for _, b := range value {
		if b < 33 || b > 126 {
			return nil, ErrInvalid
		}
	}
	return &Secret{value: append([]byte(nil), value...)}, nil
}
func (s *Secret) String() string               { return "[redacted claim credential]" }
func (s *Secret) GoString() string             { return s.String() }
func (s *Secret) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

// Use makes the token available only for the synchronous protected-store write.
// The callback must not retain bytes or log them.
func (s *Secret) Use(fn func([]byte) error) error {
	if s == nil || len(s.value) == 0 || fn == nil {
		return ErrInvalid
	}
	return fn(s.value)
}
func (s *Secret) Clear() {
	if s != nil {
		clear(s.value)
		s.value = nil
	}
}

type Result struct {
	Commit
	Credential *Secret
}
type Cancellation struct {
	Binding
	RequestID       string
	ClaimGeneration string
	Proof           SignedProof
}

// Store methods are atomic, context-bounded persistence operations. Root must
// implement these using typed rows in the local database before registration.
// Current checks revision, immutable binding, positive stage, live local owner
// intent and key/reset generation. Install additionally writes the protected
// credential and read-verifies it before its transaction publishes Installed.
// BeginCancel does NOT require the earlier stage/revision: it matches immutable
// Binding, fences any positive phase and persists the exact deny-only assertion
// before key destruction. It is idempotent and returns the original outbox item.
// No method may publish success when commit/durable acknowledgement is unknown.
type Store interface {
	Load(context.Context, string) (Intent, error)
	Current(context.Context, Intent) error
	BeginFinalizing(context.Context, Intent) (Intent, error)
	Committed(context.Context, Intent, Commit) (Intent, error)
	Install(context.Context, Intent, Result) (Intent, error)
	Acknowledged(context.Context, Intent) (Intent, error)
	BeginCancel(context.Context, Binding, Cancellation) (Intent, Cancellation, error)
	PendingCancellation(context.Context, Intent) (Cancellation, error)
	CancelAcknowledged(context.Context, Intent, string) (Intent, error)
}

// Transport talks only to the configured Hosted origin with verified TLS (or
// explicitly allocated loopback fixture transport), no redirects/ambient proxy.
// Challenge and proof bodies are bounded to 16 KiB. Never expose these methods
// or proofs to account broker/UI code. Hosted rechecks strict current-family
// approval at finalization and retrieval, including generation and mode.
// Acknowledge uses the exact protected installed SERVER credential internally.
// Cancel returns only after an exact operation/request cancellation receipt.
type Transport interface {
	Challenge(context.Context, Intent, Purpose, SignedProof) (Challenge, error)
	Finalize(context.Context, Intent, SignedProof) (Commit, error)
	Retrieve(context.Context, Intent, SignedProof) (Result, error)
	Acknowledge(context.Context, Intent) error
	Cancel(context.Context, Cancellation) error
}

// Signer is an internal key-store seam, not an arbitrary-payload public API.
// It checks the supplied immutable key/reset binding. Cancellation signing
// occurs before BeginCancel commits and before root destroys the old key.
type Signer interface {
	Sign(context.Context, Binding, []byte) ([]byte, error)
}

type Coordinator struct {
	operation string
	audience  string
	store     Store
	transport Transport
	signer    Signer
	now       func() time.Time
	busy      atomic.Bool
	timeout   time.Duration
}

func NewCoordinator(operation, audience string, store Store, transport Transport, signer Signer) (*Coordinator, error) {
	if !validID(operation) || !validAudience(audience) || store == nil || transport == nil || signer == nil {
		return nil, ErrInvalid
	}
	return &Coordinator{operation: operation, audience: audience, store: store, transport: transport, signer: fencedClaimSigner{inner: signer}, now: time.Now, timeout: 8 * time.Second}, nil
}

// Step performs one bounded durable phase. An ambiguous remote response leaves
// its phase intact; a later explicit Step obtains a FRESH nonce for the same
// operation. This method never refreshes/reapproves an expired approval.
func (c *Coordinator) Step(parent context.Context) (Intent, error) {
	if _, e := claimAuthority(parent); e != nil {
		return Intent{}, e
	}
	if !c.busy.CompareAndSwap(false, true) {
		return Intent{}, ErrBusy
	}
	defer c.busy.Store(false)
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	v, e := c.load(ctx)
	if e != nil {
		return Intent{}, e
	}
	switch v.Stage {
	case Cancelled:
		return v, ErrCancelled
	case CancelPending:
		return c.deliverCancellation(ctx, v)
	case Prepared:
		return v, ErrApprovalRequired
	case Installed:
		if v.InstallationAcknowledged {
			return v, nil
		}
		if e = c.store.Current(ctx, v); e != nil {
			return v, e
		}
		if e = c.transport.Acknowledge(ctx, v); e != nil {
			return v, safeRemote(e)
		}
		if e = ctx.Err(); e != nil {
			return v, e
		}
		return c.store.Acknowledged(ctx, v)
	case Approved:
		if e = c.approved(v); e != nil {
			return v, e
		}

		v, e = c.store.BeginFinalizing(ctx, v)
		if e != nil {
			return Intent{}, e
		}
		if e = validateIntent(v, c.operation); e != nil {
			return Intent{}, e
		}
		if v.Stage != Finalizing {
			return Intent{}, ErrStale
		}
		fallthrough
	case Finalizing:
		if v.Purpose == "switch" && v.CurrentOwnerAccountID == "" {
			if armer, ok := c.store.(interface {
				ArmPreinstallCancellation(context.Context, Intent, Cancellation) (Cancellation, error)
			}); ok {
				item, err := c.cancellation(ctx, v)
				if err != nil {
					return v, err
				}
				if _, err = armer.ArmPreinstallCancellation(ctx, v, item); err != nil {
					return v, err
				}
			}
		}
		proof, e := c.proof(ctx, v, FinalizePurpose)
		if e != nil {
			return v, e
		}
		result, e := c.transport.Finalize(ctx, v, proof)
		if e != nil {
			return v, safeRemote(e)
		}
		if e = ctx.Err(); e != nil {
			return v, e
		}
		if e = validateCommit(v, result); e != nil {
			return v, e
		}
		return c.store.Committed(ctx, v, result)
	case Retrieving:
		proof, e := c.proof(ctx, v, ResultPurpose)
		if e != nil {
			return v, e
		}
		result, e := c.transport.Retrieve(ctx, v, proof)
		if result.Credential != nil {
			defer result.Credential.Clear()
		}
		if e != nil {
			return v, safeRemote(e)
		}
		if e = ctx.Err(); e != nil {
			return v, e
		}
		if e = validateCommit(v, result.Commit); e != nil {
			return v, e
		}
		if result.Credential == nil || len(result.Credential.value) == 0 {
			return v, ErrInvalid
		}
		// Store.Install MUST recheck all binding/generation/revision/cancellation
		// facts atomically; a network completion is not local install authority.
		return c.store.Install(ctx, v, result)
	default:
		return Intent{}, ErrInvalid
	}
}

type replacementWithdrawer interface {
	WithdrawReplacement(context.Context, Intent) (Intent, bool, error)
}

// Cancel is deliberately independent of Step's busy guard. A reset must fence
// local installation while finalization/retrieval is in flight. Network failure
// leaves a durable CancelPending outbox, never an invented global success.
func (c *Coordinator) Cancel(parent context.Context) (Intent, error) {
	if _, e := claimAuthority(parent); e != nil {
		return Intent{}, e
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	v, e := c.load(ctx)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage == Cancelled {
		return v, nil
	}
	if v.Stage == CancelPending {
		return c.deliverCancellation(ctx, v)
	}
	if store, ok := c.store.(replacementWithdrawer); ok {
		withdrawn, handled, e := store.WithdrawReplacement(ctx, v)
		if e != nil {
			return v, e
		}
		if handled {
			return withdrawn, nil
		}
	}
	assertion, e := c.cancellation(ctx, v)
	if e != nil {
		return v, e
	}
	v, _, e = c.store.BeginCancel(ctx, v.Binding, assertion)
	if e != nil {
		return v, e
	}
	if e = validateIntent(v, c.operation); e != nil {
		return Intent{}, e
	}
	if v.Stage == Cancelled {
		return v, nil
	}
	if v.Stage != CancelPending {
		return Intent{}, ErrStale
	}
	return c.deliverCancellation(ctx, v)
}
func (c *Coordinator) deliverCancellation(ctx context.Context, v Intent) (Intent, error) {
	item, e := c.store.PendingCancellation(ctx, v)
	if e != nil {
		return v, e
	}
	if !sameBinding(v.Binding, item.Binding) || !validID(item.RequestID) {
		return v, ErrInvalid
	}
	if e = validateCancellation(c.audience, item); e != nil {
		return v, e
	}
	if e = c.transport.Cancel(ctx, item); e != nil {
		// Hosted has no claim operation for this id, so there is no credential
		// or grant to revoke; the server fences any later approval because
		// BeginCancel already advanced reset_generation.
		if errors.Is(e, ErrApprovalRequired) && item.ClaimGeneration == "" {
			return c.store.CancelAcknowledged(ctx, v, item.RequestID)
		}
		return v, safeRemote(e)
	}
	if e = ctx.Err(); e != nil {
		return v, e
	}
	return c.store.CancelAcknowledged(ctx, v, item.RequestID)
}
func (c *Coordinator) load(ctx context.Context) (Intent, error) {
	v, e := c.store.Load(ctx, c.operation)
	if e != nil {
		return Intent{}, e
	}
	if e = validateIntent(v, c.operation); e != nil {
		return Intent{}, e
	}
	return v, nil
}
func (c *Coordinator) approved(v Intent) error {
	if v.ApprovalRevision < 1 || !c.now().Before(v.ApprovalExpiresAt) {
		return ErrApprovalRequired
	}
	return nil
}
func safeRemote(e error) error {
	switch {
	case errors.Is(e, context.Canceled):
		return context.Canceled
	case errors.Is(e, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(e, ErrApprovalRequired):
		return ErrApprovalRequired
	case errors.Is(e, ErrCancelled):
		return ErrCancelled
	case errors.Is(e, ErrStale):
		return ErrStale
	default:
		return ErrUnavailable
	}
}
