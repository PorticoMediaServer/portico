package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// This is a concurrency model for the coordinator boundary, not a production
// repository or evidence that root's pending SQLite transactions are correct.
type modelStore struct {
	mu             sync.Mutex
	value          Intent
	cancellation   Cancellation
	installed      int
	cancelWriteErr error
}

func cloneIntent(v Intent) Intent { v.PublicKey = append([]byte(nil), v.PublicKey...); return v }
func (s *modelStore) Load(ctx context.Context, _ string) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneIntent(s.value), ctx.Err()
}
func (s *modelStore) check(v Intent) error {
	if s.value.Revision != v.Revision || !sameBinding(s.value.Binding, v.Binding) || s.value.Stage == CancelPending || s.value.Stage == Cancelled {
		return ErrStale
	}
	return nil
}
func (s *modelStore) Current(ctx context.Context, v Intent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	return s.check(v)
}
func (s *modelStore) BeginFinalizing(ctx context.Context, v Intent) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(v); e != nil {
		return Intent{}, e
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	s.value.Stage = Finalizing
	s.value.Revision++
	return cloneIntent(s.value), nil
}
func (s *modelStore) Committed(ctx context.Context, v Intent, r Commit) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(v); e != nil {
		return Intent{}, e
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	s.value.Stage = Retrieving
	s.value.ClaimGeneration = r.ClaimGeneration
	s.value.CredentialGeneration = r.CredentialGeneration
	s.value.Revision++
	return cloneIntent(s.value), nil
}
func (s *modelStore) Install(ctx context.Context, v Intent, r Result) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(v); e != nil {
		return Intent{}, e
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	if e := r.Credential.Use(func(b []byte) error {
		if len(b) < 32 {
			return ErrInvalid
		}
		return nil
	}); e != nil {
		return Intent{}, e
	}
	s.installed++
	s.value.Stage = Installed
	s.value.Revision++
	return cloneIntent(s.value), nil
}
func (s *modelStore) Acknowledged(ctx context.Context, v Intent) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(v); e != nil {
		return Intent{}, e
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	s.value.InstallationAcknowledged = true
	s.value.Revision++
	return cloneIntent(s.value), nil
}
func (s *modelStore) BeginCancel(ctx context.Context, b Binding, item Cancellation) (Intent, Cancellation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelWriteErr != nil {
		return Intent{}, Cancellation{}, s.cancelWriteErr
	}
	if !sameBinding(s.value.Binding, b) {
		return Intent{}, Cancellation{}, ErrStale
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, Cancellation{}, e
	}
	if s.value.Stage != CancelPending && s.value.Stage != Cancelled {
		s.value.Stage = CancelPending
		s.value.Revision++
		s.cancellation = item
	}
	return cloneIntent(s.value), s.cancellation, nil
}
func (s *modelStore) PendingCancellation(ctx context.Context, v Intent) (Cancellation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.Stage != CancelPending || s.value.Revision != v.Revision {
		return Cancellation{}, ErrStale
	}
	return s.cancellation, ctx.Err()
}
func (s *modelStore) CancelAcknowledged(ctx context.Context, v Intent, id string) (Intent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.Stage != CancelPending || s.value.Revision != v.Revision || s.cancellation.RequestID != id {
		return Intent{}, ErrStale
	}
	if e := ctx.Err(); e != nil {
		return Intent{}, e
	}
	s.value.Stage = Cancelled
	s.value.Revision++
	return cloneIntent(s.value), nil
}

type modelSigner struct{ key ed25519.PrivateKey }

func (s modelSigner) Sign(ctx context.Context, b Binding, raw []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !ed25519.PublicKey(s.key.Public().(ed25519.PublicKey)).Equal(ed25519.PublicKey(b.PublicKey)) {
		return nil, ErrStale
	}
	return ed25519.Sign(s.key, raw), nil
}

type modelTransport struct {
	now           time.Time
	challenges    []string
	finalizes     int
	retrieves     int
	cancels       int
	resultToken   *Secret
	challengeHook func(*Challenge)
	finalizeHook  func() error
	retrieveHook  func() error
	cancelErr     error
}

func (t *modelTransport) Challenge(_ context.Context, v Intent, p Purpose, request SignedProof) (Challenge, error) {
	var w claimWire
	if json.Unmarshal(request.Payload, &w) != nil || w.Kind != "portico.claim.nonce-request" || w.Purpose != p || !ed25519.Verify(ed25519.PublicKey(v.PublicKey), request.Payload, request.Signature) {
		return Challenge{}, ErrInvalid
	}
	t.challenges = append(t.challenges, w.RequestID)
	d := sha256.Sum256(request.Payload)
	expiry := t.now.Add(30 * time.Second)
	if v.ApprovalExpiresAt.Before(expiry) {
		expiry = v.ApprovalExpiresAt
	}
	c := Challenge{RequestDigest: base64.RawURLEncoding.EncodeToString(d[:]), NonceID: fmt.Sprintf("nonce_%d", len(t.challenges)), Nonce: make([]byte, 32), IssuedAt: t.now, ExpiresAt: expiry}
	if t.challengeHook != nil {
		t.challengeHook(&c)
	}
	return c, nil
}
func (t *modelTransport) Finalize(_ context.Context, v Intent, p SignedProof) (Commit, error) {
	t.finalizes++
	if e := checkModelProof(v, p, FinalizePurpose); e != nil {
		return Commit{}, e
	}
	if t.finalizeHook != nil {
		if e := t.finalizeHook(); e != nil {
			return Commit{}, e
		}
	}
	return Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, nil
}
func (t *modelTransport) Retrieve(_ context.Context, v Intent, p SignedProof) (Result, error) {
	t.retrieves++
	if e := checkModelProof(v, p, ResultPurpose); e != nil {
		return Result{}, e
	}
	secret, _ := NewSecret([]byte(strings.Repeat("x", 43)))
	t.resultToken = secret
	if t.retrieveHook != nil {
		if e := t.retrieveHook(); e != nil {
			return Result{Credential: secret}, e
		}
	}
	return Result{Commit: Commit{OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration}, Credential: secret}, nil
}
func (t *modelTransport) Acknowledge(context.Context, Intent) error { return nil }
func (t *modelTransport) Cancel(_ context.Context, item Cancellation) error {
	t.cancels++
	if e := validateCancellation("https://hosted.example", item); e != nil {
		return e
	}
	return t.cancelErr
}
func checkModelProof(v Intent, p SignedProof, purpose Purpose) error {
	var w claimWire
	if json.Unmarshal(p.Payload, &w) != nil || w.Kind != "portico.claim.proof" || w.Purpose != purpose || w.NonceID == "" || !ed25519.Verify(ed25519.PublicKey(v.PublicKey), p.Payload, p.Signature) {
		return ErrInvalid
	}
	return nil
}
func fixture(t *testing.T) (*Coordinator, *modelStore, *modelTransport) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := &modelStore{value: Intent{Binding: Binding{OperationID: "operation_1", ServerID: "server_1", AccountID: "account_1", PublicKey: pub, LocalGeneration: 0}, Revision: 1, Stage: Approved, ApprovalRevision: 1, ApprovalExpiresAt: now.Add(5 * time.Minute)}}
	transport := &modelTransport{now: now}
	c, e := NewCoordinator("operation_1", "https://hosted.example", store, transport, modelSigner{key})
	if e != nil {
		t.Fatal(e)
	}
	c.now = func() time.Time { return now }
	return c, store, transport
}

func TestClaimCoordinatorCompleteAndRedacted(t *testing.T) {
	c, s, remote := fixture(t)
	v, e := c.Step(fixtureClaimContext(t))
	if e != nil || v.Stage != Retrieving {
		t.Fatal("finalization did not advance", e)
	}
	v, e = c.Step(fixtureClaimContext(t))
	if e != nil || v.Stage != Installed || s.installed != 1 {
		t.Fatal("installation failed", e)
	}
	if len(remote.resultToken.value) != 0 {
		t.Fatal("temporary credential retained")
	}
	v, e = c.Step(fixtureClaimContext(t))
	if e != nil || !v.InstallationAcknowledged {
		t.Fatal("acknowledgment failed", e)
	}
	secret, _ := NewSecret([]byte(strings.Repeat("private-token", 4)))
	defer secret.Clear()
	if strings.Contains(fmt.Sprintf("%v %#v", secret, secret), "private-token") {
		t.Fatal("credential formatting leaked")
	}
	if _, e = json.Marshal(secret); e == nil {
		t.Fatal("credential serialized")
	}
}

func TestClaimCoordinatorAmbiguousRetryUsesFreshNonce(t *testing.T) {
	c, _, remote := fixture(t)
	remote.finalizeHook = func() error {
		if remote.finalizes == 1 {
			return errors.New("private upstream body")
		}
		return nil
	}
	if _, e := c.Step(fixtureClaimContext(t)); !errors.Is(e, ErrUnavailable) || strings.Contains(e.Error(), "private") {
		t.Fatal("unsafe remote error")
	}
	v, e := c.Step(fixtureClaimContext(t))
	if e != nil || v.ClaimGeneration != "1" || len(remote.challenges) != 2 || remote.challenges[0] == remote.challenges[1] {
		t.Fatal("retry did not retain intent with fresh nonce", e)
	}
}

func TestClaimCoordinatorApprovalDoesNotAutoRenew(t *testing.T) {
	c, s, remote := fixture(t)
	s.value.ApprovalExpiresAt = remote.now
	if _, e := c.Step(fixtureClaimContext(t)); !errors.Is(e, ErrApprovalRequired) {
		t.Fatal("expired approval accepted")
	}
	if len(remote.challenges) != 0 {
		t.Fatal("expired approval sent proof")
	}
	s.value.ApprovalExpiresAt = remote.now.Add(time.Minute)
	remote.finalizeHook = func() error { return ErrApprovalRequired }
	if _, e := c.Step(fixtureClaimContext(t)); !errors.Is(e, ErrApprovalRequired) {
		t.Fatal("server generation denial not preserved")
	}
	if s.value.ApprovalRevision != 1 {
		t.Fatal("approval silently revised")
	}
}

func TestClaimCoordinatorRejectsWrongNonceAndLateIntent(t *testing.T) {
	for _, kind := range []string{"digest", "oversize_nonce", "expired", "lifetime", "intent_change"} {
		t.Run(kind, func(t *testing.T) {
			c, s, r := fixture(t)
			r.challengeHook = func(ch *Challenge) {
				switch kind {
				case "digest":
					ch.RequestDigest = "wrong"
				case "oversize_nonce":
					ch.Nonce = make([]byte, 33)
				case "expired":
					ch.ExpiresAt = r.now
				case "lifetime":
					ch.ExpiresAt = r.now.Add(time.Minute)
				case "intent_change":
					s.mu.Lock()
					s.value.LocalGeneration++
					s.value.Revision++
					s.mu.Unlock()
				}
			}
			if _, e := c.Step(fixtureClaimContext(t)); e == nil {
				t.Fatal("invalid challenge or stale intent accepted")
			}
			if r.finalizes != 0 {
				t.Fatal("invalid proof sent to finalize")
			}
		})
	}
}

func TestClaimCoordinatorResetDuringFinalization(t *testing.T) {
	c, s, r := fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWait := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWait)
	r.finalizeHook = func() error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { _, e := c.Step(fixtureClaimContext(t)); done <- e }()
	awaitSignal(t, entered)
	if _, e := c.Step(fixtureClaimContext(t)); !errors.Is(e, ErrBusy) {
		t.Fatal("parallel positive operation admitted")
	}
	v, e := c.Cancel(fixtureClaimContext(t))
	if e != nil || v.Stage != Cancelled {
		t.Fatal("cancel failed", e)
	}
	var wire cancelWire
	if json.Unmarshal(s.cancellation.Proof.Payload, &wire) != nil || wire.Mode != "intent" || wire.ClaimGeneration != "" {
		t.Fatal("precommit cancel misclassified")
	}
	releaseWait()
	if e = awaitResult(t, done); !errors.Is(e, ErrStale) {
		t.Fatal("late finalization installed after reset", e)
	}
	if s.value.Stage != Cancelled || s.installed != 0 {
		t.Fatal("reset overwritten")
	}
}

func TestClaimCoordinatorResetDuringResult(t *testing.T) {
	c, s, r := fixture(t)
	if _, e := c.Step(fixtureClaimContext(t)); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWait := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseWait)
	r.retrieveHook = func() error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { _, e := c.Step(fixtureClaimContext(t)); done <- e }()
	awaitSignal(t, entered)
	if _, e := c.Cancel(fixtureClaimContext(t)); e != nil {
		t.Fatal(e)
	}
	releaseWait()
	if e := awaitResult(t, done); !errors.Is(e, ErrStale) {
		t.Fatal("late result installed", e)
	}
	if s.installed != 0 || len(r.resultToken.value) != 0 {
		t.Fatal("cancelled result retained")
	}
	var w cancelWire
	_ = json.Unmarshal(s.cancellation.Proof.Payload, &w)
	if w.Mode != "committed" || w.ClaimGeneration != "1" {
		t.Fatal("committed cancellation lost generation")
	}
}

func TestClaimCoordinatorCancellationPersistsAcrossRestart(t *testing.T) {
	c, s, r := fixture(t)
	r.cancelErr = errors.New("offline")
	v, e := c.Cancel(fixtureClaimContext(t))
	if !errors.Is(e, ErrUnavailable) || v.Stage != CancelPending {
		t.Fatal("offline cancellation reported complete")
	}
	original := s.cancellation.RequestID
	c2, e := NewCoordinator(c.operation, c.audience, s, r, c.signer)
	if e != nil {
		t.Fatal(e)
	}
	c2.now = c.now
	r.cancelErr = nil
	v, e = c2.Step(fixtureClaimContext(t))
	if e != nil || v.Stage != Cancelled || s.cancellation.RequestID != original {
		t.Fatal("restart did not replay original cancellation", e)
	}
}

func TestClaimCoordinatorFailedCancelWriteNeverSends(t *testing.T) {
	c, s, r := fixture(t)
	s.cancelWriteErr = errors.New("storage unavailable")
	if _, e := c.Cancel(fixtureClaimContext(t)); e == nil {
		t.Fatal("failed outbox treated as success")
	}
	if r.cancels != 0 || s.value.Stage != Approved {
		t.Fatal("cancel sent without durable fence")
	}
}

func TestClaimCoordinatorContextAndResultFailureClearSecret(t *testing.T) {
	c, s, r := fixture(t)
	if _, e := c.Step(fixtureClaimContext(t)); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(fixtureClaimContext(t))
	r.retrieveHook = func() error { cancel(); return nil }
	if _, e := c.Step(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled request accepted")
	}
	if s.installed != 0 || len(r.resultToken.value) != 0 {
		t.Fatal("cancelled result retained")
	}
}

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("coordination barrier not reached")
	}
}
func awaitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not return")
		return context.DeadlineExceeded
	}
}

func TestClaimCoordinatorRejectsUnsafeConfigurationAndCommit(t *testing.T) {
	c, s, r := fixture(t)
	for _, origin := range []string{"http://hosted.example", "https://user:password@hosted.example", "https://hosted.example/path", "https://hosted.example?", "https://hosted.example/#x", "https://HOSTED.example"} {
		if _, e := NewCoordinator(c.operation, origin, s, r, c.signer); !errors.Is(e, ErrInvalid) {
			t.Fatal("unsafe audience accepted")
		}
	}
	if _, e := NewCoordinator(c.operation, c.audience, nil, r, c.signer); !errors.Is(e, ErrInvalid) {
		t.Fatal("missing durable store accepted")
	}
	v := s.value
	for _, commit := range []Commit{{OperationID: "other", ServerID: v.ServerID, ClaimGeneration: "1", CredentialGeneration: "1"}, {OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "9223372036854775808", CredentialGeneration: "1"}, {OperationID: v.OperationID, ServerID: v.ServerID, ClaimGeneration: "01", CredentialGeneration: "1"}} {
		if validateCommit(v, commit) == nil {
			t.Fatal("invalid commit identity/generation accepted")
		}
	}
}

func TestClaimCoordinatorCancellationProofIsPurposeAndScopeBound(t *testing.T) {
	c, s, _ := fixture(t)
	original, e := c.cancellation(fixtureClaimContext(t), s.value)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"account", "operation", "key", "generation", "mode", "unknown_field", "signature"} {
		t.Run(kind, func(t *testing.T) {
			item := original
			item.Proof.Payload = append([]byte(nil), original.Proof.Payload...)
			item.Proof.Signature = append([]byte(nil), original.Proof.Signature...)
			switch kind {
			case "account":
				item.AccountID = "other"
			case "operation":
				item.OperationID = "other"
			case "key":
				item.PublicKey = make([]byte, 32)
			case "generation":
				item.LocalGeneration++
			case "mode":
				item.ClaimGeneration = "1"
			case "unknown_field":
				item.Proof.Payload = append([]byte(`{"extra":1,`), item.Proof.Payload[1:]...)
			case "signature":
				item.Proof.Signature[0] ^= 1
			}
			if validateCancellation(c.audience, item) == nil {
				t.Fatal("altered cancellation accepted")
			}
		})
	}
}
