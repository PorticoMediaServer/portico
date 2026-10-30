package hosted

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

func (s *Service) ownerCompletionCaller(ctx context.Context, bearer, origin string) (identity.Principal, string, error) {
	if s == nil || s.identity == nil || s.current == nil || !s.Configured() {
		return identity.Principal{}, "", networking.ErrUnavailable
	}
	p, err := s.identity.OwnerClaimAuthority(ctx, bearer)
	if err != nil {
		return identity.Principal{}, "", err
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return identity.Principal{}, "", err
	}
	defer g.Rollback()
	operation, err := s.identity.OwnerClaimOperationTx(ctx, g.Tx(), p)
	if err != nil {
		return identity.Principal{}, "", err
	}
	if err = identity.CheckOwnerReturnOriginTx(ctx, g.Tx(), operation, origin, s.origin); err != nil {
		return identity.Principal{}, "", err
	}
	return p, operation, nil
}

// PrepareOwnerCompletion is an existing operation's post-install step. Its
// registration is resolved using the actual installed credential and serving
// HTTPS route. It never prepares an ordinary sign-in or invents an origin/ID.
func (s *Service) PrepareOwnerCompletion(ctx context.Context, h *networking.ClaimHandler, bearer, sourceOrigin string) (serverreturn.Registration, error) {
	p, operation, err := s.ownerCompletionCaller(ctx, bearer, sourceOrigin)
	if err != nil {
		return serverreturn.Registration{}, err
	}
	guard := func(ctx context.Context, tx *sql.Tx) error {
		if err := s.identity.CheckOwnerClaimTx(ctx, tx, p, operation); err != nil {
			return err
		}
		return identity.CheckOwnerReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
	}
	registration, _, err := h.BindOwnerReturn(ctx, guard, func(ctx context.Context, tx *sql.Tx, r serverreturn.Registration, generation int64) (bool, error) {
		return s.identity.BindOwnerReturnRegistrationTx(ctx, tx, p, operation, identity.OwnerReturnRoute{Registration: r, Generation: generation})
	})
	return registration, err
}

// ExchangeOwnerCompletion consumes exactly one captured operation attempt.
// The normal registered-return method above remains code-based and sends no
// Authorization. Only this separate private path uses the installed credential.
func (s *Service) ExchangeOwnerCompletion(ctx context.Context, h *networking.ClaimHandler, bearer, sourceOrigin string, q serverreturn.OwnerExchange) (identity.DirectSignIn, error) {
	var out identity.DirectSignIn
	p, operation, err := s.ownerCompletionCaller(ctx, bearer, sourceOrigin)
	if err != nil {
		return out, err
	}
	var attempt, nonce string
	var route identity.OwnerReturnRoute
	var who identity.PorticoIdentity
	err = h.RunOwnerReturn(ctx, q, networking.OwnerReturnActions{
		Guard: func(ctx context.Context, tx *sql.Tx) error {
			if err := s.identity.CheckOwnerClaimTx(ctx, tx, p, operation); err != nil {
				return err
			}
			return identity.CheckOwnerReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
		},
		BeginTx: func(ctx context.Context, tx *sql.Tx, r serverreturn.Registration, generation int64) error {
			route = identity.OwnerReturnRoute{Registration: r, Generation: generation}
			actual, started, err := s.identity.BeginOwnerReturnTx(ctx, tx, p, q, route)
			if err != nil {
				return err
			}
			if actual != operation {
				return identity.ErrUnauthorized
			}
			attempt = started
			return nil
		},
		CheckAttemptTx: func(ctx context.Context, tx *sql.Tx) error {
			if err := s.identity.CheckOwnerReturnAttemptTx(ctx, tx, p, operation, attempt, q, route); err != nil {
				return err
			}
			return identity.CheckOwnerReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
		},
		Verify: func(ctx context.Context, raw json.RawMessage) error {
			var err error
			who, nonce, err = s.VerifyOwnerReturn(ctx, raw, q)
			return err
		},
		CompleteTx: func(ctx context.Context, tx *sql.Tx) error {
			var err error
			out, err = s.identity.CompleteOwnerReturnTx(ctx, tx, p, operation, attempt, q, route, who, nonce)
			return err
		},
		AbandonTx: func(ctx context.Context, tx *sql.Tx) error {
			return s.identity.AbandonOwnerReturnTx(ctx, tx, operation, attempt)
		},
	})
	if err != nil {
		return identity.DirectSignIn{}, err
	}
	return out, nil
}
