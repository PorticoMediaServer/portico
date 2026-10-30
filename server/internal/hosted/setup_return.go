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

func (s *Service) setupCompletionCaller(ctx context.Context, bearer, origin string) (identity.PendingAccountSetup, string, string, error) {
	var none identity.PendingAccountSetup
	if s == nil || s.identity == nil || s.current == nil || !s.Configured() {
		return none, "", "", networking.ErrUnavailable
	}
	pending, proof, err := s.identity.SetupClaimAuthority(ctx, bearer)
	if err != nil {
		return none, "", "", err
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return none, "", "", err
	}
	defer g.Rollback()
	operation, err := s.identity.SetupClaimOperationTx(ctx, g.Tx(), pending, proof)
	if err != nil {
		return none, "", "", err
	}
	if err = identity.CheckSetupReturnOriginTx(ctx, g.Tx(), operation, origin, s.origin); err != nil {
		return none, "", "", err
	}
	return pending, proof, operation, nil
}

// PrepareSetupCompletion is an existing operation's post-install step: once the claim has
// installed, the setup can finish over its signed original address (O-163). No HTTPS
// return registration is resolved or required; that attaches later for ordinary sign-ins.
func (s *Service) PrepareSetupCompletion(ctx context.Context, h *networking.ClaimHandler, bearer, sourceOrigin string) error {
	pending, proof, operation, err := s.setupCompletionCaller(ctx, bearer, sourceOrigin)
	if err != nil {
		return err
	}
	return s.prepareSetupCompletion(ctx, h, pending, proof, operation, sourceOrigin)
}

// PrepareCapturedSetupCompletion resumes the exact server-private capture after
// installation, including across restart. It marks the setup installed only; no browser
// verifier, setup token or owner credential is recovered or issued.
func (s *Service) PrepareCapturedSetupCompletion(ctx context.Context, h *networking.ClaimHandler, pending identity.PendingAccountSetup, proof, operation string) error {
	if s == nil || s.identity == nil || s.current == nil || !s.Configured() {
		return networking.ErrUnavailable
	}
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	sourceOrigin, err := s.identity.CapturedSetupReturnOriginTx(ctx, g.Tx(), pending, proof, operation, s.origin)
	g.Rollback()
	if err != nil {
		return err
	}
	return s.prepareSetupCompletion(ctx, h, pending, proof, operation, sourceOrigin)
}

func (s *Service) prepareSetupCompletion(ctx context.Context, h *networking.ClaimHandler, pending identity.PendingAccountSetup, proof, operation, sourceOrigin string) error {
	guard := func(ctx context.Context, tx *sql.Tx) error {
		if err := s.identity.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, ""); err != nil {
			return err
		}
		return identity.CheckSetupReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
	}
	return h.InstallSetupReturn(ctx, guard, func(ctx context.Context, tx *sql.Tx) error {
		return s.identity.MarkSetupInstalledTx(ctx, tx, pending, proof, operation)
	})
}

// ExchangeSetupCompletion consumes exactly one captured operation attempt.
// The normal registered-return method above remains code-based and sends no
// Authorization. Only this separate private path uses the installed credential.
func (s *Service) ExchangeSetupCompletion(ctx context.Context, h *networking.ClaimHandler, bearer, sourceOrigin string, q serverreturn.SetupExchange) (identity.DirectSignIn, error) {
	var out identity.DirectSignIn
	pending, proof, operation, err := s.setupCompletionCaller(ctx, bearer, sourceOrigin)
	if err != nil {
		return out, err
	}
	var attempt, nonce string
	var who identity.PorticoIdentity
	err = h.RunSetupReturn(ctx, q, networking.SetupReturnActions{
		Guard: func(ctx context.Context, tx *sql.Tx) error {
			if err := s.identity.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, ""); err != nil {
				return err
			}
			return identity.CheckSetupReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
		},
		BeginTx: func(ctx context.Context, tx *sql.Tx) error {
			actual, started, err := s.identity.BeginSetupReturnTx(ctx, tx, pending, proof, q)
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
			if err := s.identity.CheckSetupReturnAttemptTx(ctx, tx, pending, proof, operation, attempt, q); err != nil {
				return err
			}
			return identity.CheckSetupReturnOriginTx(ctx, tx, operation, sourceOrigin, s.origin)
		},
		Verify: func(ctx context.Context, raw json.RawMessage) error {
			var err error
			who, nonce, err = s.VerifySetupReturn(ctx, raw, q)
			return err
		},
		CompleteTx: func(ctx context.Context, tx *sql.Tx) error {
			var err error
			out, err = s.identity.CompleteSetupReturnTx(ctx, tx, pending, proof, operation, attempt, q, who, nonce)
			return err
		},
		AbandonTx: func(ctx context.Context, tx *sql.Tx) error {
			return s.identity.AbandonSetupReturnTx(ctx, tx, operation, attempt)
		},
	})
	if err != nil {
		return identity.DirectSignIn{}, err
	}
	return out, nil
}
