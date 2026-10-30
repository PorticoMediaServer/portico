package ownedcapture

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PlayCaptureTicket only fixes one eventual capture ID to this service, original
// request and lifetime. Its private shared state makes value copies consume the
// same single attempt. An ID alone cannot reconstruct or consume a ticket.
type PlayCaptureTicket struct{ state *playCaptureTicketState }

type playCaptureTicketState struct {
	service  *Service
	request  Request
	id       string
	life     context.Context
	deadline time.Time
	consumed atomic.Bool
	mu       sync.Mutex
	source   PlaybackCaptureSource
	resolved bool
	terminal error
}

// ID is a private identity reference, not a source, capture receipt or grant.
func (t *PlayCaptureTicket) ID() string {
	if t == nil || t.state == nil {
		return ""
	}
	return t.state.id
}

// NewPlayCaptureTicket issues no authority, reservation, row or byte work.
// Issue it outside SQL from the original finite operation before its preparation
// row. The original request contains only scalar identity and asset values.
func (s *Service) NewPlayCaptureTicket(ctx context.Context, request Request) (*PlayCaptureTicket, error) {
	if s == nil || ctx == nil || dbwork.Snapshot(ctx) != nil || !opaque.MatchString(request.AssetID) {
		return nil, ErrInput
	}
	deadline, finite := ctx.Deadline()
	if !finite || deadline.After(time.Now().Add(120*time.Second)) || !request.Principal.ExpiresAt.IsZero() && deadline.After(request.Principal.ExpiresAt) {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.life.Err(); err != nil {
		return nil, err
	}
	return &PlayCaptureTicket{state: &playCaptureTicketState{service: s, request: request, id: uniqueID(), life: ctx, deadline: deadline}}, nil
}

func (s *Service) playTicketCurrent(ctx context.Context, t *PlayCaptureTicket) (*playCaptureTicketState, error) {
	if s == nil || ctx == nil || t == nil || t.state == nil || t.state.service != s || !captureID.MatchString(t.state.id) {
		return nil, ErrInput
	}
	state := t.state
	for _, life := range []context.Context{ctx, state.life, s.life} {
		if err := life.Err(); err != nil {
			return nil, err
		}
	}
	if !state.deadline.After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	state.mu.Lock()
	err := state.terminal
	state.mu.Unlock()
	return state, err
}

func (t *playCaptureTicketState) refuse(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal == nil {
		t.terminal = err
	}
	return t.terminal
}

// ResolvePlayCaptureTx pins the first actual source-issued preflight value.
// Every subsequent read recomputes that original selection and refuses drift;
// restoring a source later cannot revive a ticket that already failed a check.
func (s *Service) ResolvePlayCaptureTx(ctx context.Context, tx *sql.Tx, ticket *PlayCaptureTicket) (PlaybackCaptureSource, error) {
	if tx == nil {
		return PlaybackCaptureSource{}, ErrInput
	}
	state, err := s.playTicketCurrent(ctx, ticket)
	if err != nil {
		return PlaybackCaptureSource{}, err
	}
	resolver, ok := s.producer.(PlaybackCaptureResolver)
	if !ok {
		return PlaybackCaptureSource{}, state.refuse(ErrCurrent)
	}
	source, err := resolver.ResolvePlaybackCaptureTx(ctx, tx, state.id, state.request)
	if err != nil {
		return PlaybackCaptureSource{}, state.refuse(err)
	}
	if !validPlaybackCaptureSource(source) || source.AssetID != state.request.AssetID || source.ReadAuthorityWitness != state.id {
		return PlaybackCaptureSource{}, state.refuse(ErrCurrent)
	}
	if _, err = s.playTicketCurrent(ctx, ticket); err != nil {
		return PlaybackCaptureSource{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.terminal != nil {
		return PlaybackCaptureSource{}, state.terminal
	}
	if state.resolved && state.source != source {
		state.terminal = ErrCurrent
		return PlaybackCaptureSource{}, state.terminal
	}
	state.source, state.resolved = source, true
	return source, nil
}

// CapturePlayTicket consumes exactly one preflighted ticket under the original
// finite operation check. Failed admission/copy never makes it reusable. The
// normal producer still owns actual acquisition/reservation identities and all
// source/cache/quota/floor/pacing/egress permission.
func (s *Service) CapturePlayTicket(ctx context.Context, ticket *PlayCaptureTicket, original PlayCaptureCheck) (Receipt, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || original == nil {
		return Receipt{}, ErrInput
	}
	state, err := s.playTicketCurrent(ctx, ticket)
	if err != nil {
		return Receipt{}, err
	}
	deadline, finite := ctx.Deadline()
	if !finite || deadline.After(state.deadline) {
		return Receipt{}, ErrInput
	}
	state.mu.Lock()
	resolved := state.resolved
	state.mu.Unlock()
	if !resolved {
		return Receipt{}, ErrCurrent
	}
	if !state.consumed.CompareAndSwap(false, true) {
		return Receipt{}, ErrCurrent
	}
	run, release := bindLife(ctx, state.life)
	defer release()
	check := func(check context.Context, tx *sql.Tx) error {
		if err := original(check, tx); err != nil {
			return err
		}
		_, err := s.ResolvePlayCaptureTx(check, tx, ticket)
		return err
	}
	return s.capture(run, state.request, check, ticket)
}

func (t *PlayCaptureTicket) matches(binding Binding, size int64) bool {
	state := t.state
	state.mu.Lock()
	defer state.mu.Unlock()
	source := state.source
	return state.resolved && state.terminal == nil && binding.SourceID == source.RemoteSourceID && binding.ConfigGeneration == source.EffectiveConfiguration &&
		binding.ObjectID == source.InventoryObjectID && binding.ObjectRevision == source.InventoryRevision && binding.AssetID == source.AssetID &&
		binding.PolicyWitness == source.ReadPolicyWitness && binding.AuthorityWitness == state.id && size == source.Size
}

func validPlaybackCaptureSource(s PlaybackCaptureSource) bool {
	for _, value := range []string{s.LibraryID, s.LibrarySourceID, s.AssetID, s.InventoryObjectID, s.InventoryRevision, s.Incarnation, s.RemoteSourceID, s.EffectiveConfiguration, s.ReadPolicyWitness, s.ReadAuthorityWitness} {
		if !opaque.MatchString(value) {
			return false
		}
	}
	if len(s.RootIdentity) == 0 || len(s.RootIdentity) > 4096 || !utf8.ValidString(s.RootIdentity) {
		return false
	}
	for _, r := range s.RootIdentity {
		if r < 32 || r >= 127 && r <= 159 {
			return false
		}
	}
	return s.LibraryGeneration > 0 && s.RawConfigurationGeneration > 0 && s.ProfileRevision >= 0 && s.Size > 0 && s.ModifiedNS >= 0
}
